package cmd

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/filecoin-project/go-address"
	"github.com/filecoin-project/lotus/chain/types"
	"github.com/filecoin-project/lotus/chain/types/ethtypes"
)

type ProjectType string

const (
	ProjectTypeHardhat ProjectType = "hardhat"
	ProjectTypeFoundry ProjectType = "foundry"
)

type ContractProject struct {
	Name         string            `json:"name"`
	GitURL       string            `json:"git_url"`
	GitRef       string            `json:"git_ref,omitempty"`
	ProjectType  ProjectType       `json:"project_type"`
	MainContract string            `json:"main_contract"`
	ContractPath string            `json:"contract_path,omitempty"`
	CloneDir     string            `json:"clone_dir"`
	GenerateAbi  bool              `json:"generate_abi,omitempty"`
	Env          map[string]string `json:"env"`
}

type DeployedContract struct {
	Name               string              `json:"name"`
	Address            ethtypes.EthAddress `json:"address"`
	DeployerAddress    ethtypes.EthAddress `json:"deployer_address"`
	DeployerPrivateKey string              `json:"deployer_private_key"`
	TransactionHash    ethtypes.EthHash    `json:"txhash"`
	AbiPath            string              `json:"abi_path"`
	BindingsPath       string              `json:"bindings_path"`
}

type ContractManager struct {
	workspaceDir    string
	deploymentsFile string
	deployerKey     string
	rpcURL          string
}

func NewContractManager(workspaceDir, rpcURL string) *ContractManager {
	absWorkspaceDir, _ := filepath.Abs(workspaceDir)
	os.MkdirAll(absWorkspaceDir, 0755)
	contractsDir := filepath.Join(absWorkspaceDir, "contracts")
	os.MkdirAll(contractsDir, 0755)

	return &ContractManager{
		workspaceDir:    absWorkspaceDir,
		deploymentsFile: filepath.Join(absWorkspaceDir, "deployments.json"),
		rpcURL:          rpcURL,
	}
}

func (cm *ContractManager) SetDeployerKey(privateKey string) {
	cm.deployerKey = privateKey
}

func (cm *ContractManager) GetDeployerKey() string {
	return cm.deployerKey
}

func (cm *ContractManager) CloneRepository(project *ContractProject) error {
	if err := os.MkdirAll(cm.workspaceDir, 0755); err != nil {
		return fmt.Errorf("failed to create workspace directory: %w", err)
	}

	if project.CloneDir == "" {
		project.CloneDir = filepath.Join(cm.workspaceDir, fmt.Sprintf("project_%d", time.Now().Unix()))
	} else {
		if !filepath.IsAbs(project.CloneDir) {
			project.CloneDir = filepath.Join(cm.workspaceDir, project.CloneDir)
		}
	}

	cmd := exec.Command("git", "clone", project.GitURL, project.CloneDir)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to clone repository: %w, output: %s", err, output)
	}

	if project.GitRef != "" {
		fmt.Printf("Checking out git reference: %s\n", project.GitRef)
		originalDir, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get current directory: %w", err)
		}
		defer os.Chdir(originalDir)

		if err := os.Chdir(project.CloneDir); err != nil {
			return fmt.Errorf("failed to change to project directory: %w", err)
		}

		checkoutCmd := exec.Command("git", "checkout", project.GitRef)
		checkoutOutput, err := checkoutCmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to checkout git reference '%s': %w, output: %s", project.GitRef, err, checkoutOutput)
		}
		fmt.Printf("Successfully checked out: %s\n", project.GitRef)
	}

	return nil
}

func (cm *ContractManager) CompileHardhatProject(project *ContractProject) error {
	originalDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}
	defer os.Chdir(originalDir)

	if err := os.Chdir(project.CloneDir); err != nil {
		return fmt.Errorf("failed to change to project directory: %w", err)
	}

	cmd := exec.Command("yarn", "install")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to install yarn dependencies: %w, output: %s", err, output)
	}

	if project.Env != nil {
		cmd.Env = os.Environ()
		for key, value := range project.Env {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", key, value))
		}
	}

	return nil
}

func (cm *ContractManager) CompileFoundryProject(project *ContractProject) error {
	originalDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}
	defer os.Chdir(originalDir)

	workingDir := project.CloneDir

	if strings.HasPrefix(project.ContractPath, "service_contracts/") {
		parts := strings.Split(project.ContractPath, "/")
		if len(parts) > 1 {
			subDir := filepath.Join(project.CloneDir, parts[0])
			if info, err := os.Stat(subDir); err == nil && info.IsDir() {
				workingDir = subDir
			}
		}
	}

	if err := os.Chdir(workingDir); err != nil {
		return fmt.Errorf("failed to change to project directory %s: %w", workingDir, err)
	}

	cmd := exec.Command("forge", "build")
	if project.Env != nil {
		cmd.Env = os.Environ()
		for key, value := range project.Env {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", key, value))
		}
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to compile with forge build: %w, output: %s", err, output)
	}

	return nil
}

func (cm *ContractManager) CreateDeployerAccount() (string, ethtypes.EthAddress, error) {
	key, ethAddr, filAddr := NewAccount()

	fundAmount := types.FromFil(10)
	_, err := FundWallet(context.Background(), filAddr, fundAmount, true)
	if err != nil {
		return "", ethtypes.EthAddress{}, fmt.Errorf("failed to fund deployer account: %w", err)
	}

	privateKey := fmt.Sprintf("0x%x", key.PrivateKey)
	cm.deployerKey = privateKey

	return privateKey, ethAddr, nil
}

func (cm *ContractManager) DeployContract(project *ContractProject, contractPath string, constructorArgs []string, generateBindings bool, cleanup bool) (*DeployedContract, error) {
	if cm.deployerKey == "" {
		return nil, fmt.Errorf("deployer key not set, create a deployer account first")
	}

	originalDir, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("failed to get current directory: %w", err)
	}
	defer os.Chdir(originalDir)

	workingDir := project.CloneDir
	contractFile := contractPath

	if strings.HasPrefix(contractPath, "service_contracts/") {
		parts := strings.Split(contractPath, "/")
		if len(parts) > 1 {
			subDir := filepath.Join(project.CloneDir, parts[0])
			if info, err := os.Stat(subDir); err == nil && info.IsDir() {
				workingDir = subDir
				contractFile = strings.Join(parts[1:], "/")
			}
		}
	}

	if err := os.Chdir(workingDir); err != nil {
		return nil, fmt.Errorf("failed to change to project directory %s: %w", workingDir, err)
	}

	fmt.Printf("Running forge create from directory: %s\n", workingDir)
	fmt.Printf("Contract path: %s\n", contractFile)

	args := []string{
		"create",
		"--rpc-url", cm.rpcURL,
		"--private-key", cm.deployerKey,
		"--broadcast",
		"--optimizer-runs", "200",
		"--via-ir",
		contractFile,
	}

	if len(constructorArgs) > 0 {
		args = append(args, "--constructor-args")
		args = append(args, constructorArgs...)
	}

	cmd := exec.Command("forge", args...)
	if project.Env != nil {
		cmd.Env = os.Environ()
		for key, value := range project.Env {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", key, value))
		}
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to deploy contract with forge: %w, output: %s", err, output)
	}

	deployedContract, err := cm.parseForgeCreateOutput(string(output), project, contractPath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse forge create output: %w", err)
	}

	if generateBindings {
		if err := cm.extractArtifacts(project, deployedContract, generateBindings); err != nil {
			fmt.Printf("Warning: failed to extract artifacts: %v\n", err)
		}
	}

	if err := cm.saveDeployment(deployedContract); err != nil {
		return nil, fmt.Errorf("failed to save deployment: %w", err)
	}

	if cleanup {
		if err := cm.CleanupProject(project); err != nil {
			fmt.Printf("Warning: Failed to cleanup project directory: %v\n", err)
		}
	}

	return deployedContract, nil
}

func (cm *ContractManager) extractArtifacts(project *ContractProject, contract *DeployedContract, generateBindings bool) error {
	contractsDir := filepath.Join(cm.workspaceDir, "contracts")
	if err := os.MkdirAll(contractsDir, 0755); err != nil {
		return fmt.Errorf("failed to create contracts dir: %w", err)
	}

	abiPath, err := cm.extractABIWithForgeInspect(project, contract.Name)
	if err != nil {
		return fmt.Errorf("failed to extract ABI: %w", err)
	}
	contract.AbiPath = abiPath
	fmt.Printf("Saved ABI for %s to %s\n", contract.Name, abiPath)

	if generateBindings {
		bindingsPath, err := cm.generateBindings(contract.Name, abiPath)
		if err != nil {
			return fmt.Errorf("failed to generate bindings: %w", err)
		}
		contract.BindingsPath = bindingsPath
		fmt.Printf("Generated Go bindings for %s at %s\n", contract.Name, bindingsPath)
	}

	return nil
}

func (cm *ContractManager) extractABIWithForgeInspect(project *ContractProject, contractName string) (string, error) {
	originalDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get current directory: %w", err)
	}
	defer os.Chdir(originalDir)

	workingDir := project.CloneDir
	contractFile := project.ContractPath

	if strings.HasPrefix(project.ContractPath, "service_contracts/") {
		parts := strings.Split(project.ContractPath, "/")
		if len(parts) > 1 {
			subDir := filepath.Join(project.CloneDir, parts[0])
			if info, err := os.Stat(subDir); err == nil && info.IsDir() {
				workingDir = subDir
				contractFile = strings.Join(parts[1:], "/")
			}
		}
	}

	if err := os.Chdir(workingDir); err != nil {
		return "", fmt.Errorf("failed to change to project directory: %w", err)
	}

	// Use forge inspect to extract ABI directly from source
	contractPath := fmt.Sprintf("%s:%s", contractFile, project.MainContract)
	cmd := exec.Command("forge", "inspect", contractPath, "abi", "--json")

	if project.Env != nil {
		cmd.Env = os.Environ()
		for key, value := range project.Env {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", key, value))
		}
	}

	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to extract ABI with forge inspect: %w", err)
	}

	var abiJSON interface{}
	if err := json.Unmarshal(output, &abiJSON); err != nil {
		return "", fmt.Errorf("invalid ABI JSON from forge inspect (output was: %s): %w", string(output), err)
	}

	abiPath := filepath.Join(cm.workspaceDir, "contracts", fmt.Sprintf("%s.abi.json", strings.ToLower(contractName)))
	if err := os.WriteFile(abiPath, output, 0644); err != nil {
		return "", fmt.Errorf("failed to save ABI file: %w", err)
	}

	fmt.Printf("Extracted ABI using forge inspect for %s\n", contractName)
	return abiPath, nil
}

func (cm *ContractManager) generateBindings(contractName, abiPath string) (string, error) {
	contractsDir := filepath.Join(cm.workspaceDir, "contracts")
	bindingsPath := filepath.Join(contractsDir, fmt.Sprintf("%s.go", strings.ToLower(contractName)))

	cmd := exec.Command("abigen",
		"--abi", abiPath,
		"--pkg", "contracts",
		"--type", contractName,
		"--out", bindingsPath)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to generate Go bindings: %w, output: %s", err, string(output))
	}

	return bindingsPath, nil
}

func (cm *ContractManager) parseForgeCreateOutput(output string, project *ContractProject, contractPath string) (*DeployedContract, error) {
	lines := strings.Split(output, "\n")
	var contractAddr string

	for _, line := range lines {
		if strings.Contains(line, "Deployed to:") {
			parts := strings.Split(line, "Deployed to:")
			if len(parts) > 1 {
				contractAddr = strings.TrimSpace(parts[1])
				break
			}
		}
	}

	if contractAddr == "" {
		return nil, fmt.Errorf("failed to extract contract address from forge create output: %s", output)
	}

	ethAddr, err := ethtypes.ParseEthAddress(contractAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse contract address: %w", err)
	}

	cmd := exec.Command("cast", "wallet", "address", "--private-key", cm.deployerKey)
	deployerOutput, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to get deployer address: %w", err)
	}

	deployerAddr, err := ethtypes.ParseEthAddress(strings.TrimSpace(string(deployerOutput)))
	if err != nil {
		return nil, fmt.Errorf("failed to parse deployer address: %w", err)
	}

	return &DeployedContract{
		Name:               project.Name,
		Address:            ethAddr,
		DeployerAddress:    deployerAddr,
		DeployerPrivateKey: cm.deployerKey,
		TransactionHash:    ethtypes.EthHash{},
	}, nil
}

func (cm *ContractManager) RunCustomDeployScript(project *ContractProject, scriptPath string) error {
	originalDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}
	defer os.Chdir(originalDir)

	if err := os.Chdir(project.CloneDir); err != nil {
		return fmt.Errorf("failed to change to project directory: %w", err)
	}

	if err := os.Chmod(scriptPath, 0755); err != nil {
		return fmt.Errorf("failed to make script executable: %w", err)
	}

	cmd := exec.Command("bash", scriptPath)
	cmd.Env = os.Environ()
	if project.Env != nil {
		for key, value := range project.Env {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", key, value))
		}
	}

	if cm.deployerKey != "" {
		cmd.Env = append(cmd.Env, fmt.Sprintf("PRIVATE_KEY=%s", cm.deployerKey))
	}

	if cm.rpcURL != "" {
		cmd.Env = append(cmd.Env, fmt.Sprintf("RPC_URL=%s", cm.rpcURL))
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to run deployment script: %w, output: %s", err, output)
	}

	log.Printf("Deployment script output: %s", string(output))

	if err := cm.CleanupProject(project); err != nil {
		fmt.Printf("Warning: Failed to cleanup project directory: %v\n", err)
	}

	return nil
}

func (cm *ContractManager) RunShellCommands(project *ContractProject, commands string) error {
	originalDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}
	defer os.Chdir(originalDir)

	if err := os.Chdir(project.CloneDir); err != nil {
		return fmt.Errorf("failed to change to project directory: %w", err)
	}

	commandList := strings.Split(commands, ";")
	for i, cmdStr := range commandList {
		cmdStr = strings.TrimSpace(cmdStr)
		if cmdStr == "" {
			continue
		}

		fmt.Printf("Running command %d/%d: %s\n", i+1, len(commandList), cmdStr)

		cmd := exec.Command("sh", "-c", cmdStr)
		cmd.Env = os.Environ()
		if project.Env != nil {
			for key, value := range project.Env {
				cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", key, value))
			}
		}

		if cm.deployerKey != "" {
			cmd.Env = append(cmd.Env, fmt.Sprintf("PRIVATE_KEY=%s", cm.deployerKey))
		}

		if cm.rpcURL != "" {
			cmd.Env = append(cmd.Env, fmt.Sprintf("RPC_URL=%s", cm.rpcURL))
		}

		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to run command '%s': %w, output: %s", cmdStr, err, output)
		}

		log.Printf("Command output: %s", string(output))
	}

	if err := cm.CleanupProject(project); err != nil {
		fmt.Printf("Warning: Failed to cleanup project directory: %v\n", err)
	}

	return nil
}

func (cm *ContractManager) saveDeployment(contract *DeployedContract) error {
	var deployments []*DeployedContract

	if data, err := os.ReadFile(cm.deploymentsFile); err == nil {
		if err := json.Unmarshal(data, &deployments); err != nil {
			return fmt.Errorf("failed to parse existing deployments: %w", err)
		}
	}

	deployments = append(deployments, contract)

	data, err := json.MarshalIndent(deployments, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal deployments: %w", err)
	}

	dir := filepath.Dir(cm.deploymentsFile)
	os.MkdirAll(dir, 0755)

	if err := os.WriteFile(cm.deploymentsFile, data, 0644); err != nil {
		return err
	}

	// Also save deployer account to accounts.json
	if contract.DeployerPrivateKey != "" {
		if err := cm.saveDeployerAccount(contract); err != nil {
			fmt.Printf("Warning: failed to save deployer account: %v\n", err)
		}
	}

	return nil
}

func (cm *ContractManager) saveDeployerAccount(contract *DeployedContract) error {
	accountsPath := filepath.Join(cm.workspaceDir, "accounts.json")

	type AccountInfo struct {
		Address    string `json:"address"`
		EthAddress string `json:"ethAddress"`
		PrivateKey string `json:"privateKey"`
	}

	type AccountsFile struct {
		Accounts map[string]AccountInfo `json:"accounts"`
	}

	accounts := AccountsFile{Accounts: make(map[string]AccountInfo)}

	// Load existing accounts if file exists
	if data, err := os.ReadFile(accountsPath); err == nil {
		if err := json.Unmarshal(data, &accounts); err != nil {
			return fmt.Errorf("failed to parse existing accounts: %w", err)
		}
	}

	// Convert eth address to Filecoin address
	ethAddrStr := contract.DeployerAddress.String()
	ethAddrBytes, err := hex.DecodeString(strings.TrimPrefix(ethAddrStr, "0x"))
	if err != nil {
		return fmt.Errorf("failed to decode eth address: %w", err)
	}

	filAddr, err := address.NewDelegatedAddress(10, ethAddrBytes)
	if err != nil {
		return fmt.Errorf("failed to create delegated address: %w", err)
	}

	// Only add deployer if it doesn't already exist
	if _, exists := accounts.Accounts["deployer"]; !exists {
		accounts.Accounts["deployer"] = AccountInfo{
			Address:    filAddr.String(),
			EthAddress: ethAddrStr,
			PrivateKey: contract.DeployerPrivateKey,
		}

		data, err := json.MarshalIndent(accounts, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal accounts: %w", err)
		}

		if err := os.WriteFile(accountsPath, data, 0644); err != nil {
			return fmt.Errorf("failed to write accounts file: %w", err)
		}

		fmt.Printf("Added deployer account to %s\n", accountsPath)
	}

	return nil
}

func (cm *ContractManager) LoadDeployments() ([]*DeployedContract, error) {
	var deployments []*DeployedContract

	data, err := os.ReadFile(cm.deploymentsFile)
	if err != nil {
		if os.IsNotExist(err) {
			return deployments, nil
		}
		return nil, fmt.Errorf("failed to read deployments file: %w", err)
	}

	if err := json.Unmarshal(data, &deployments); err != nil {
		return nil, fmt.Errorf("failed to parse deployments: %w", err)
	}

	return deployments, nil
}

func (cm *ContractManager) GetDeployment(contractName string) (*DeployedContract, error) {
	deployments, err := cm.LoadDeployments()
	if err != nil {
		return nil, err
	}

	for _, deployment := range deployments {
		if deployment.Name == contractName {
			return deployment, nil
		}
	}

	return nil, fmt.Errorf("deployment not found for contract: %s", contractName)
}

func (cm *ContractManager) CleanupProject(project *ContractProject) error {
	if project.CloneDir == "" {
		return nil
	}

	fmt.Printf("Cleaning up project directory: %s\n", project.CloneDir)
	if err := os.RemoveAll(project.CloneDir); err != nil {
		return fmt.Errorf("failed to remove project directory %s: %w", project.CloneDir, err)
	}

	fmt.Printf("Successfully cleaned up project directory\n")
	return nil
}

func (cm *ContractManager) CleanupWorkspace() error {
	entries, err := os.ReadDir(cm.workspaceDir)
	if err != nil {
		return fmt.Errorf("failed to read workspace directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "project_") {
			projectPath := filepath.Join(cm.workspaceDir, entry.Name())
			if err := os.RemoveAll(projectPath); err != nil {
				return fmt.Errorf("failed to remove project directory %s: %w", projectPath, err)
			}
		}
	}

	return nil
}
