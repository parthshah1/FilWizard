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
	Initialize *PostDeploymentAction   `json:"initialize,omitempty"`
	Actions    []PostDeploymentAction  `json:"actions,omitempty"`
}

type ContractConfig struct {
	Name           string          `json:"name"`
	ProjectType    string          `json:"project_type"`
	GitURL         string          `json:"git_url"`
	GitRef         string          `json:"git_ref"`
	MainContract   string          `json:"main_contract"`
	ContractPath   string          `json:"contract_path"`
	ConstructorArgs []string       `json:"constructor_args"`
	Dependencies   []string        `json:"dependencies,omitempty"`
	PostDeployment *PostDeployment `json:"post_deployment,omitempty"`
}

type ContractsConfig struct {
	Contracts []ContractConfig `json:"contracts"`
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
		if strings.HasPrefix(arg, "${") && strings.HasSuffix(arg, "}") {
			contractName := arg[2 : len(arg)-1]

			address := findContractAddress(contractName, deployments)
			if address == "" {
				return nil, fmt.Errorf("dependency contract %s not found in deployments", contractName)
			}

			resolvedArgs[i] = address
		} else {
			resolvedArgs[i] = arg
		}
	}

	return resolvedArgs, nil
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
		if deployment.Name == name {
			return deployment.Address
		}
	}
	return ""
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
	switch argType {
	case "address":
		return common.HexToAddress(arg), nil
	case "uint256", "uint":
		value, ok := new(big.Int).SetString(arg, 0)
		if !ok {
			return nil, fmt.Errorf("invalid uint256 value: %s", arg)
		}
		return value, nil
	case "bool":
		return strconv.ParseBool(arg)
	case "string":
		return arg, nil
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
