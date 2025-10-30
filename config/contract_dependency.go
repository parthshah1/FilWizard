package config

import (
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math/big"
	"os"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

type PostDeploymentAction struct {
	Method      string   `json:"method"`
	Args        []string `json:"args"`
	Types       []string `json:"types"`
	Description string   `json:"description,omitempty"`
}

type PostDeployment struct {
	Initialize *PostDeploymentAction  `json:"initialize,omitempty"`
	Actions    []PostDeploymentAction `json:"actions,omitempty"`
}

type ContractConfig struct {
	Name            string            `json:"name"`
	ProjectType     string            `json:"project_type"`
	GitURL          string            `json:"git_url"`
	GitRef          string            `json:"git_ref"`
	MainContract    string            `json:"main_contract"`
	ContractPath    string            `json:"contract_path"`
	ConstructorArgs []string          `json:"constructor_args"`
	Dependencies    []string          `json:"dependencies,omitempty"`
	PostDeployment  *PostDeployment   `json:"post_deployment,omitempty"`
	Environment     map[string]string `json:"environment,omitempty"`
	DeployScript    string            `json:"deploy_script,omitempty"`
	ScriptDir       string            `json:"script_dir,omitempty"`
	CloneCommands   []string          `json:"clone_commands,omitempty"`
	Exports         map[string]string `json:"exports,omitempty"`
}

type ContractsConfig struct {
	Environment map[string]string `json:"environment,omitempty"`
	Contracts   []ContractConfig  `json:"contracts"`
}

type DeploymentRecord struct {
	Name               string `json:"name"`
	Address            string `json:"address"`
	DeployerAddress    string `json:"deployer_address"`
	DeployerPrivateKey string `json:"deployer_private_key"`
	TxHash             string `json:"txhash"`
	ABIPath            string `json:"abi_path"`
	BindingsPath       string `json:"bindings_path"`
}

// LoadContractsConfig reads and parses the contracts configuration file
func LoadContractsConfig(configPath string) (*ContractsConfig, error) {
	data, err := ioutil.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read contracts config: %w", err)
	}

	var config ContractsConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse contracts config: %w", err)
	}

	return &config, nil
}

// LoadDeploymentRecords reads deployment records from deployments.json
func LoadDeploymentRecords(deploymentsPath string) ([]DeploymentRecord, error) {
	if _, err := os.Stat(deploymentsPath); os.IsNotExist(err) {
		return []DeploymentRecord{}, nil
	}

	data, err := ioutil.ReadFile(deploymentsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read deployments: %w", err)
	}

	var deployments []DeploymentRecord
	if err := json.Unmarshal(data, &deployments); err != nil {
		return nil, fmt.Errorf("failed to parse deployments: %w", err)
	}

	return deployments, nil
}

// ResolveDependencies replaces template variables in constructor args with actual contract addresses
func ResolveDependencies(contract ContractConfig, deployments []DeploymentRecord) ([]string, error) {
	resolvedArgs := make([]string, len(contract.ConstructorArgs))

	for i, arg := range contract.ConstructorArgs {
		resolved := arg

		// Handle special encoded init data for proxy contracts
		if arg == "__ENCODED_INIT_DATA__" {
			initData, err := generateInitializeCallData(contract)
			if err != nil {
				return nil, fmt.Errorf("failed to generate init data: %w", err)
			}
			resolved = initData
		} else if arg == "__ENCODED_INIT_DATA_REGISTRY__" {
			// Special case for ServiceProviderRegistry
			contractCopy := contract
			contractCopy.Name = "ServiceProviderRegistry"
			initData, err := generateInitializeCallData(contractCopy)
			if err != nil {
				return nil, fmt.Errorf("failed to generate registry init data: %w", err)
			}
			resolved = initData
		} else if strings.HasPrefix(arg, "${") && strings.HasSuffix(arg, "}") {
			// Handle ${ContractName} format (legacy)
			contractName := arg[2 : len(arg)-1]
			address := findContractAddress(contractName, deployments)
			if address == "" {
				return nil, fmt.Errorf("dependency contract %s not found in deployments", contractName)
			}
			resolved = address
		} else if strings.Contains(arg, "{address:") {
			// Handle {address:ContractName} format (new)
			resolved = resolveAddressPlaceholders(arg, deployments)
			if strings.Contains(resolved, "{address:") {
				// Still contains unresolved placeholders
				return nil, fmt.Errorf("unresolved address placeholder in argument: %s", arg)
			}
		} else if strings.Contains(arg, "{env:") {
			// Handle {env:VARIABLE} format for environment variables
			resolved = resolveEnvPlaceholders(arg)
			if strings.Contains(resolved, "{env:") {
				// Still contains unresolved placeholders
				return nil, fmt.Errorf("unresolved environment placeholder in argument: %s", arg)
			}
		}

		if strings.Contains(resolved, "{deployment:") {
			var err error
			resolved, err = resolveDeploymentPlaceholders(resolved, deployments)
			if err != nil {
				return nil, err
			}

			if strings.Contains(resolved, "{deployment:") {
				return nil, fmt.Errorf("unresolved deployment placeholder in argument: %s", arg)
			}
		}

		resolvedArgs[i] = resolved
	}

	return resolvedArgs, nil
}

// resolveAddressPlaceholders resolves {address:ContractName} placeholders in a string
func resolveAddressPlaceholders(input string, deployments []DeploymentRecord) string {
	result := input

	// Handle multiple placeholders in a single string
	for {
		start := strings.Index(result, "{address:")
		if start == -1 {
			break
		}

		end := strings.Index(result[start:], "}")
		if end == -1 {
			break
		}
		end += start

		placeholder := result[start : end+1]
		contractName := placeholder[9 : len(placeholder)-1] // Extract from {address: to }

		address := findContractAddress(contractName, deployments)
		if address == "" {
			// Leave unresolved for error handling
			break
		}

		result = strings.Replace(result, placeholder, address, 1)
	}

	return result
}

// resolveEnvPlaceholders resolves {env:VARIABLE} placeholders in a string
func resolveEnvPlaceholders(input string) string {
	result := input

	// Handle multiple placeholders in a single string
	for {
		start := strings.Index(result, "{env:")
		if start == -1 {
			break
		}

		end := strings.Index(result[start:], "}")
		if end == -1 {
			break
		}
		end += start

		placeholder := result[start : end+1]
		envVar := placeholder[5 : len(placeholder)-1] // Extract from {env: to }

		value := os.Getenv(envVar)
		if value == "" {
			// Leave unresolved for error handling
			break
		}

		result = strings.Replace(result, placeholder, value, 1)
	}

	return result
}

func resolveDeploymentPlaceholders(input string, deployments []DeploymentRecord) (string, error) {
	result := input

	for {
		start := strings.Index(result, "{deployment:")
		if start == -1 {
			break
		}

		end := strings.Index(result[start:], "}")
		if end == -1 {
			break
		}
		end += start

		placeholder := result[start : end+1]
		content := placeholder[len("{deployment:") : len(placeholder)-1]
		parts := strings.Split(content, ":")
		if len(parts) != 2 {
			return "", fmt.Errorf("invalid deployment placeholder: %s", placeholder)
		}

		contractName := parts[0]
		field := strings.ToLower(parts[1])

		record := findDeploymentRecord(deployments, contractName)
		if record == nil {
			return "", fmt.Errorf("deployment record for %s not found", contractName)
		}

		var value string
		switch field {
		case "deployer_private_key":
			value = record.DeployerPrivateKey
		case "deployer_address":
			value = record.DeployerAddress
		case "address":
			value = record.Address
		default:
			return "", fmt.Errorf("unsupported deployment placeholder field: %s", field)
		}

		if value == "" {
			return "", fmt.Errorf("empty value for placeholder %s", placeholder)
		}

		result = strings.Replace(result, placeholder, value, 1)
	}

	return result, nil
}

// ValidateDependencies checks if all required dependencies are deployed
func ValidateDependencies(contract ContractConfig, deployments []DeploymentRecord) error {
	for _, dep := range contract.Dependencies {
		if findContractAddress(dep, deployments) == "" {
			return fmt.Errorf("required dependency %s is not deployed", dep)
		}
	}
	return nil
}

// GetDeploymentOrder returns contracts sorted by dependency order
func GetDeploymentOrder(contracts []ContractConfig) ([]ContractConfig, error) {
	var ordered []ContractConfig
	deployed := make(map[string]bool)

	for len(ordered) < len(contracts) {
		progress := false

		for _, contract := range contracts {
			if deployed[contract.Name] {
				continue
			}

			canDeploy := true
			for _, dep := range contract.Dependencies {
				if !deployed[dep] {
					canDeploy = false
					break
				}
			}

			if canDeploy {
				ordered = append(ordered, contract)
				deployed[contract.Name] = true
				progress = true
			}
		}

		if !progress {
			return nil, fmt.Errorf("circular dependency detected or missing dependency")
		}
	}

	return ordered, nil
}

func findContractAddress(name string, deployments []DeploymentRecord) string {
	for _, deployment := range deployments {
		if strings.EqualFold(deployment.Name, name) {
			return deployment.Address
		}
	}
	return ""
}

func findDeploymentRecord(deployments []DeploymentRecord, name string) *DeploymentRecord {
	for i := range deployments {
		if strings.EqualFold(deployments[i].Name, name) {
			return &deployments[i]
		}
	}
	return nil
}

func ExecutePostDeployment(contract ContractConfig, contractAddress string, deployments []DeploymentRecord, rpcURL, privateKey string) error {
	if contract.PostDeployment == nil {
		return nil
	}

	if contract.PostDeployment.Initialize != nil {
		if err := executeAction(contract, contractAddress, *contract.PostDeployment.Initialize, deployments, rpcURL, privateKey); err != nil {
			return fmt.Errorf("failed to execute initialize: %w", err)
		}
	}

	for _, action := range contract.PostDeployment.Actions {
		if err := executeAction(contract, contractAddress, action, deployments, rpcURL, privateKey); err != nil {
			return fmt.Errorf("failed to execute action %s: %w", action.Method, err)
		}
	}

	return nil
}

func executeAction(contract ContractConfig, contractAddress string, action PostDeploymentAction, deployments []DeploymentRecord, rpcURL, privateKey string) error {
	resolvedArgs, err := ResolveDependencies(ContractConfig{ConstructorArgs: action.Args}, deployments)
	if err != nil {
		return fmt.Errorf("failed to resolve action args: %w", err)
	}

	fmt.Printf("Calling %s.%s() with args: %v\n", contract.Name, action.Method, resolvedArgs)

	return callContractMethod(contractAddress, action.Method, resolvedArgs, action.Types, rpcURL, privateKey)
}

func callContractMethod(contractAddress, methodName string, args []string, types []string, rpcURL, privateKey string) error {
	convertedArgs, err := convertArguments(args, types)
	if err != nil {
		return fmt.Errorf("failed to convert arguments: %w", err)
	}

	wrapper, err := NewContractWrapper(rpcURL, contractAddress)
	if err != nil {
		return fmt.Errorf("failed to create contract wrapper: %w", err)
	}
	defer wrapper.Close()

	privateKeyECDSA, err := parsePrivateKey(privateKey)
	if err != nil {
		return fmt.Errorf("failed to parse private key: %w", err)
	}

	tx, err := wrapper.SendTransaction(methodName, convertedArgs, privateKeyECDSA, 0)
	if err != nil {
		return fmt.Errorf("failed to send transaction: %w", err)
	}

	fmt.Printf("Post-deployment action completed - TX: %s\n", tx.Hash().Hex())
	return nil
}

func convertArguments(args, types []string) ([]interface{}, error) {
	if len(args) != len(types) {
		return nil, fmt.Errorf("argument count mismatch")
	}

	converted := make([]interface{}, len(args))
	for i, arg := range args {
		argType := types[i]
		convertedArg, err := convertArgument(arg, argType)
		if err != nil {
			return nil, fmt.Errorf("failed to convert arg %d: %w", i, err)
		}
		converted[i] = convertedArg
	}

	return converted, nil
}

func convertArgument(arg, argType string) (interface{}, error) {
	switch strings.ToLower(argType) {
	case "address":
		return common.HexToAddress(arg), nil
	case "address_from_private_key", "privatekey_address", "address-private-key":
		pk, err := parsePrivateKey(arg)
		if err != nil {
			return nil, fmt.Errorf("invalid private key: %w", err)
		}
		addr := crypto.PubkeyToAddress(pk.PublicKey)
		return addr, nil
	case "uint256", "uint":
		value, ok := new(big.Int).SetString(arg, 0)
		if !ok {
			return nil, fmt.Errorf("invalid uint value: %s", arg)
		}
		return value, nil
	case "uint64":
		if strings.HasPrefix(arg, "0x") {
			val, err := strconv.ParseUint(arg[2:], 16, 64)
			if err != nil {
				return nil, fmt.Errorf("failed to parse hex uint64: %w", err)
			}
			return new(big.Int).SetUint64(val), nil
		}
		val, err := strconv.ParseUint(arg, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("failed to parse uint64: %w", err)
		}
		return new(big.Int).SetUint64(val), nil
	case "uint32":
		if strings.HasPrefix(arg, "0x") {
			val, err := strconv.ParseUint(arg[2:], 16, 32)
			if err != nil {
				return nil, fmt.Errorf("failed to parse hex uint32: %w", err)
			}
			return new(big.Int).SetUint64(uint64(val)), nil
		}
		val, err := strconv.ParseUint(arg, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("failed to parse uint32: %w", err)
		}
		return new(big.Int).SetUint64(uint64(val)), nil
	case "bool":
		return strconv.ParseBool(arg)
	case "string":
		return arg, nil
	case "bytes":
		if strings.HasPrefix(arg, "0x") {
			return common.FromHex(arg), nil
		}
		return []byte(arg), nil
	default:
		return nil, fmt.Errorf("unsupported type: %s", argType)
	}
}

func parsePrivateKey(privateKeyStr string) (*ecdsa.PrivateKey, error) {
	privateKeyStr = strings.TrimPrefix(privateKeyStr, "0x")

	privateKeyBytes, err := hex.DecodeString(privateKeyStr)
	if err != nil {
		return nil, fmt.Errorf("invalid hex format: %w", err)
	}

	if len(privateKeyBytes) != 32 {
		return nil, fmt.Errorf("invalid private key length: got %d bytes, want 32 bytes", len(privateKeyBytes))
	}

	privateKey, err := crypto.ToECDSA(privateKeyBytes)
	if err != nil {
		return nil, fmt.Errorf("invalid private key: %w", err)
	}

	return privateKey, nil
}

// SetEnvironmentVariables sets environment variables from the global and contract-specific configurations
func (c *ContractsConfig) SetEnvironmentVariables(contractName string, deployments []DeploymentRecord) {
	// Set global environment variables first
	for key, value := range c.Environment {
		resolvedValue := c.ResolveAddressPlaceholdersWithDeployments(value, deployments)
		os.Setenv(key, resolvedValue)
	}

	// Find contract-specific environment variables and set them
	if contract := c.GetContractByName(contractName); contract != nil {
		for key, value := range contract.Environment {
			// Resolve address placeholders if any
			resolvedValue := c.ResolveAddressPlaceholdersWithDeployments(value, deployments)
			os.Setenv(key, resolvedValue)
		}
		for exportName, target := range contract.Exports {
			if resolvedValue, err := resolveExportValue(target, contractName, deployments); err == nil {
				os.Setenv(exportName, resolvedValue)
			}
		}
	}
}

// ResolveAddressPlaceholders resolves {address:ContractName} placeholders in environment values
func (c *ContractsConfig) ResolveAddressPlaceholders(value string) string {
	// Simple placeholder resolution - replace {address:ContractName} with actual addresses
	// This would need to read from deployments.json or be called after deployment
	if strings.Contains(value, "{address:") {
		// For now, return as-is - this should be called after contracts are deployed
		// and have access to the deployment records
		return value
	}
	return value
}

// ResolveAddressPlaceholdersWithDeployments resolves {address:ContractName} placeholders using deployment records
func (c *ContractsConfig) ResolveAddressPlaceholdersWithDeployments(value string, deployments []DeploymentRecord) string {
	return resolveAddressPlaceholders(value, deployments)
}

// UpdateEnvironmentWithDeployments updates environment variables with resolved contract addresses
func (c *ContractsConfig) UpdateEnvironmentWithDeployments(contractName string, deployments []DeploymentRecord) {
	env := c.GetEnvironmentForContract(contractName)

	for key, value := range env {
		resolvedValue := c.ResolveAddressPlaceholdersWithDeployments(value, deployments)
		if resolvedValue != value {
			fmt.Printf("  Resolved %s: %s -> %s\n", key, value, resolvedValue)
			os.Setenv(key, resolvedValue)
		}
	}

	if contract := c.GetContractByName(contractName); contract != nil && len(contract.Exports) > 0 {
		for exportName, target := range contract.Exports {
			resolvedValue, err := resolveExportValue(target, contractName, deployments)
			if err != nil {
				fmt.Printf("  Warning: failed to resolve export %s for %s: %v\n", exportName, contractName, err)
				continue
			}
			os.Setenv(exportName, resolvedValue)
			fmt.Printf("  Exported %s=%s\n", exportName, resolvedValue)
		}
	}
}

// GetEnvironmentForContract returns the merged environment variables for a specific contract
func (c *ContractsConfig) GetEnvironmentForContract(contractName string) map[string]string {
	env := make(map[string]string)

	// Start with global environment
	for key, value := range c.Environment {
		env[key] = value
	}

	// Override with contract-specific environment
	for _, contract := range c.Contracts {
		if contract.Name == contractName {
			for key, value := range contract.Environment {
				env[key] = value
			}
			break
		}
	}

	return env
}

// GetContractByName returns the contract configuration for a given name if it exists
func (c *ContractsConfig) GetContractByName(contractName string) *ContractConfig {
	for i := range c.Contracts {
		if c.Contracts[i].Name == contractName {
			return &c.Contracts[i]
		}
	}
	return nil
}

func resolveExportValue(target string, currentContract string, deployments []DeploymentRecord) (string, error) {
	trimmed := strings.TrimSpace(target)
	if trimmed == "" {
		return "", fmt.Errorf("empty export target")
	}

	if strings.EqualFold(trimmed, "self") {
		trimmed = currentContract
	}

	if strings.Contains(trimmed, "{address:") {
		resolved := resolveAddressPlaceholders(trimmed, deployments)
		if strings.Contains(resolved, "{address:") {
			return "", fmt.Errorf("unresolved address placeholder: %s", target)
		}
		return resolved, nil
	}

	if common.IsHexAddress(trimmed) {
		return common.HexToAddress(trimmed).Hex(), nil
	}

	address := findContractAddress(trimmed, deployments)
	if address == "" {
		return "", fmt.Errorf("contract %s not found in deployments", trimmed)
	}

	return address, nil
}

// generateInitializeCallData creates the encoded function call data for proxy initialization
func generateInitializeCallData(contract ContractConfig) (string, error) {
	// For ServiceProviderRegistry, we need to generate: initialize()
	if contract.Name == "ServiceProviderRegistry" {
		// ServiceProviderRegistry initialize() takes no parameters
		return "CAST_CALLDATA:initialize()", nil
	}

	// For FilecoinWarmStorageService, we need to generate:
	// initialize(uint64,uint256,address,string,string)
	if contract.Name == "FilecoinWarmStorageService" {
		// Get the values from environment
		maxProvingPeriod := getEnvValue(contract, "MAX_PROVING_PERIOD", "60")
		challengeWindowSize := getEnvValue(contract, "CHALLENGE_WINDOW_SIZE", "30")
		filBeamController := getEnvValue(contract, "FILBEAM_CONTROLLER_ADDRESS", "0x0000000000000000000000000000000000000000")
		serviceName := getEnvValue(contract, "SERVICE_NAME", "DevNet WarmStorage Service")
		serviceDescription := getEnvValue(contract, "SERVICE_DESCRIPTION", "Filecoin WarmStorage service for local devnet testing")

		// Build the function signature and encode the call data
		// For now, we'll return a placeholder that indicates we need special handling
		// This will be processed later during deployment using the actual cast tool
		return fmt.Sprintf("CAST_CALLDATA:initialize(uint64,uint256,address,string,string):%s:%s:%s:\"%s\":\"%s\"",
			maxProvingPeriod, challengeWindowSize, filBeamController, serviceName, serviceDescription), nil
	}

	return "", fmt.Errorf("init data generation not implemented for contract: %s", contract.Name)
} // Helper functions for encoding
func getEnvValue(contract ContractConfig, key, defaultValue string) string {
	if val, exists := contract.Environment[key]; exists {
		return val
	}
	return defaultValue
}
