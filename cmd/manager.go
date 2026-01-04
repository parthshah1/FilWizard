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
	"regexp"
	"strings"

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
	Name             string            `json:"name"`
	GitURL           string            `json:"git_url"`
	GitRef           string            `json:"git_ref,omitempty"`
	ProjectType      ProjectType       `json:"project_type"`
	MainContract     string            `json:"main_contract"`
	ContractPath     string            `json:"contract_path,omitempty"`
	CloneDir         string            `json:"clone_dir"`
	ScriptDir        string            `json:"script_dir,omitempty"`
	GenerateAbi      bool              `json:"generate_abi,omitempty"`
	GenerateBindings bool              `json:"generate_bindings,omitempty"`
	Env              map[string]string `json:"env"`
	CloneCommands    []string          `json:"clone_commands,omitempty"`
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

// AccountInfo holds account details for JSON serialization
type AccountInfo struct {
	Address    string `json:"address"`
	EthAddress string `json:"ethAddress"`
	PrivateKey string `json:"privateKey"`
}

// AccountsFile holds the structure of accounts.json
type AccountsFile struct {
	Accounts map[string]AccountInfo `json:"accounts"`
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
		project.CloneDir = filepath.Join(cm.workspaceDir, project.Name)
	} else {
		if !filepath.IsAbs(project.CloneDir) {
			project.CloneDir = filepath.Join(cm.workspaceDir, project.CloneDir)
		}
	}

	// Determine which ref to checkout - use the one specified in JSON config
	checkoutRef := project.GitRef
	if checkoutRef == "" {
		// If no ref specified, get default branch from remote
		lsRemoteCmd := exec.Command("git", "ls-remote", "--symref", project.GitURL, "HEAD")
		lsRemoteOutput, err := lsRemoteCmd.CombinedOutput()
		if err == nil {
			lines := strings.Split(string(lsRemoteOutput), "\n")
			for _, line := range lines {
				if strings.HasPrefix(line, "ref:") {
					parts := strings.Fields(line)
					if len(parts) >= 2 {
						ref := parts[1]
						if strings.HasPrefix(ref, "refs/heads/") {
							checkoutRef = strings.TrimPrefix(ref, "refs/heads/")
							break
						}
					}
				}
			}
		}
		if checkoutRef == "" {
			checkoutRef = "main" // fallback to main
		}
	}

	originalDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}
	defer os.Chdir(originalDir)

	// Check if directory already exists
	if _, err := os.Stat(project.CloneDir); err == nil {
		// Directory exists, fetch latest and checkout the ref
		fmt.Printf("Directory %s already exists, fetching latest %s...\n", project.CloneDir, checkoutRef)
		if err := os.Chdir(project.CloneDir); err != nil {
			return fmt.Errorf("failed to change to project directory: %w", err)
		}
	} else {
		// Directory doesn't exist, clone fresh
		fmt.Printf("Cloning repository: %s\n", project.GitURL)
		cmd := exec.Command("git", "clone", project.GitURL, project.CloneDir)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to clone repository: %w, output: %s", err, output)
		}

		if err := os.Chdir(project.CloneDir); err != nil {
			return fmt.Errorf("failed to change to project directory: %w", err)
		}
	}

	// Always fetch all refs from origin to get the latest remote state
	fmt.Printf("Fetching all refs from origin...\n")
	fetchAllCmd := exec.Command("git", "fetch", "origin", "--tags", "--force")
	fetchAllOutput, err := fetchAllCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to fetch from origin: %w, output: %s", err, fetchAllOutput)
	}

	// Discard any local changes to ensure clean state
	fmt.Printf("Discarding any local changes...\n")
	resetHardCmd := exec.Command("git", "reset", "--hard", "HEAD")
	if _, resetErr := resetHardCmd.CombinedOutput(); resetErr != nil {
		// Non-fatal, might be on a detached HEAD or no commits yet
		fmt.Printf("Note: Could not reset (might be expected)\n")
	}
	cleanCmd := exec.Command("git", "clean", "-fd")
	if _, cleanErr := cleanCmd.CombinedOutput(); cleanErr != nil {
		// Non-fatal
		fmt.Printf("Note: Could not clean working directory (might be expected)\n")
	}

	// Check if the ref exists as a remote branch
	checkBranchCmd := exec.Command("git", "ls-remote", "--heads", "origin", checkoutRef)
	branchOutput, _ := checkBranchCmd.CombinedOutput()
	remoteBranchExists := strings.TrimSpace(string(branchOutput)) != ""

	// Always checkout the latest version of the specified ref
	fmt.Printf("Checking out latest %s...\n", checkoutRef)
	var checkoutCmd *exec.Cmd
	if remoteBranchExists {
		// For branches, force update local branch to match remote using -B flag
		// This already puts us at origin/<ref>, so no pull needed
		fmt.Printf("Updating local branch %s to match origin/%s...\n", checkoutRef, checkoutRef)
		checkoutCmd = exec.Command("git", "checkout", "-B", checkoutRef, fmt.Sprintf("origin/%s", checkoutRef))
	} else {
		// For tags/commits, just checkout directly
		checkoutCmd = exec.Command("git", "checkout", checkoutRef)
	}

	checkoutOutput, err := checkoutCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to checkout git reference '%s': %w, output: %s", checkoutRef, err, checkoutOutput)
	}

	// For branches, ensure upstream tracking is set
	if remoteBranchExists {
		setUpstreamCmd := exec.Command("git", "branch", "--set-upstream-to", fmt.Sprintf("origin/%s", checkoutRef), checkoutRef)
		if _, upstreamErr := setUpstreamCmd.CombinedOutput(); upstreamErr != nil {
			// Non-fatal, tracking might already be set
			fmt.Printf("Note: Could not set upstream tracking (may already be set)\n")
		}

		// Hard reset to origin/<ref> to ensure we're exactly at the remote HEAD
		// This is more reliable than pull, especially if there are any local modifications
		fmt.Printf("Resetting to origin/%s to ensure clean state...\n", checkoutRef)
		resetToOriginCmd := exec.Command("git", "reset", "--hard", fmt.Sprintf("origin/%s", checkoutRef))
		resetOutput, resetErr := resetToOriginCmd.CombinedOutput()
		if resetErr != nil {
			return fmt.Errorf("failed to reset to origin/%s: %w, output: %s", checkoutRef, resetErr, resetOutput)
		}
	}

	fmt.Printf("Successfully checked out latest %s\n", checkoutRef)

	// Execute clone commands if specified
	if len(project.CloneCommands) > 0 {
		fmt.Printf("Executing %d clone command(s)...\n", len(project.CloneCommands))
		for i, cmdStr := range project.CloneCommands {
			cmdStr = strings.TrimSpace(cmdStr)
			if cmdStr == "" {
				continue
			}

			fmt.Printf("Running clone command %d/%d: %s\n", i+1, len(project.CloneCommands), cmdStr)

			cloneCmd := exec.Command("sh", "-c", cmdStr)
			cloneCmd.Dir = project.CloneDir // Set working directory to the cloned repo
			cloneCmd.Env = os.Environ()
			if project.Env != nil {
				for key, value := range project.Env {
					cloneCmd.Env = append(cloneCmd.Env, fmt.Sprintf("%s=%s", key, value))
				}
			}

			cloneOutput, err := cloneCmd.CombinedOutput()
			if err != nil {
				return fmt.Errorf("failed to run clone command '%s': %w, output: %s", cmdStr, err, cloneOutput)
			}

			fmt.Printf("Clone command completed successfully\n")
		}

		// After clone commands, clean up any untracked files but keep intentional changes
		// Clone commands (like submodule updates) should leave the repo in a clean state
		fmt.Printf("Cleaning untracked files after clone commands...\n")
		cleanAfterCloneCmd := exec.Command("git", "clean", "-fd")
		if _, cleanErr := cleanAfterCloneCmd.CombinedOutput(); cleanErr != nil {
			// Non-fatal
			fmt.Printf("Note: Could not clean after clone commands (might be expected)\n")
		}
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

	// Process constructor args for special cases (like encoded init data)
	processedArgs, err := processConstructorArgs(constructorArgs)
	if err != nil {
		return nil, fmt.Errorf("failed to process constructor args: %w", err)
	}

	if len(processedArgs) > 0 {
		args = append(args, "--constructor-args")
		args = append(args, processedArgs...)
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

func (cm *ContractManager) RunCustomDeployScript(project *ContractProject, scriptPath string) (string, error) {
	originalDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("failed to get current directory: %w", err)
	}
	defer os.Chdir(originalDir)

	workingDir := project.CloneDir
	if project.ScriptDir != "" {
		workingDir = filepath.Join(project.CloneDir, project.ScriptDir)
	}

	if err := os.Chdir(workingDir); err != nil {
		return "", fmt.Errorf("failed to change to script directory: %w", err)
	}

	if err := os.Chmod(scriptPath, 0755); err != nil {
		return "", fmt.Errorf("failed to make script executable: %w", err)
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
		return string(output), fmt.Errorf("failed to run deployment script: %w, output: %s", err, output)
	}

	log.Printf("Deployment script output: %s", string(output))

	return string(output), nil
}

func (cm *ContractManager) RunShellCommands(project *ContractProject, commands string) error {
	originalDir, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}
	defer os.Chdir(originalDir)

	workingDir := project.CloneDir
	if project.ScriptDir != "" {
		workingDir = filepath.Join(project.CloneDir, project.ScriptDir)
	}

	if err := os.Chdir(workingDir); err != nil {
		return fmt.Errorf("failed to change to script directory: %w", err)
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

	// Uses package-level AccountInfo and AccountsFile types

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

func (cm *ContractManager) EnsureCloneCommandsExecuted(project *ContractProject) error {
	if len(project.CloneCommands) == 0 {
		return nil
	}

	// Check if the clone directory exists
	if _, err := os.Stat(project.CloneDir); os.IsNotExist(err) {
		return fmt.Errorf("project directory %s does not exist", project.CloneDir)
	}

	// Check for marker file to see if clone commands were already executed
	markerFile := filepath.Join(project.CloneDir, ".clone_commands_done")
	if _, err := os.Stat(markerFile); err == nil {
		fmt.Printf("Clone commands already executed for %s (found marker file), skipping...\n", project.Name)
		return nil
	}

	fmt.Printf("Ensuring clone commands are executed for %s...\n", project.Name)

	for i, cmdStr := range project.CloneCommands {
		cmdStr = strings.TrimSpace(cmdStr)
		if cmdStr == "" {
			continue
		}

		fmt.Printf("Running clone command %d/%d: %s\n", i+1, len(project.CloneCommands), cmdStr)

		cloneCmd := exec.Command("sh", "-c", cmdStr)
		cloneCmd.Dir = project.CloneDir // Set working directory to the cloned repo
		cloneCmd.Env = os.Environ()
		if project.Env != nil {
			for key, value := range project.Env {
				cloneCmd.Env = append(cloneCmd.Env, fmt.Sprintf("%s=%s", key, value))
			}
		}

		cloneOutput, err := cloneCmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("failed to run clone command '%s': %w, output: %s", cmdStr, err, cloneOutput)
		}

		fmt.Printf("Clone command completed successfully\n")
	}

	// Create marker file to indicate clone commands have been executed
	markerFile = filepath.Join(project.CloneDir, ".clone_commands_done")
	if err := os.WriteFile(markerFile, []byte("done\n"), 0644); err != nil {
		fmt.Printf("Warning: failed to create marker file %s: %v\n", markerFile, err)
	}

	return nil
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

// ImportScriptOutputToDeployments parses arbitrary script output and imports contract addresses
// into the workspace deployments.json. The expected file contains lines with '<Name>: <address>'
// or any line containing a 0x-prefixed address. All contracts found in the output will be
// added/updated.
// contractName and mainContract are optional - if provided, will create an alias entry
// for contractName pointing to mainContract's address if mainContract is found.
func (cm *ContractManager) ImportScriptOutputToDeployments(contractsConfigPath, deploymentsPath, outputPath string, contractName, mainContract string) error {
	// Read script output
	outData, err := os.ReadFile(outputPath)
	if err != nil {
		return fmt.Errorf("failed to read output file: %w", err)
	}

	lines := strings.Split(string(outData), "\n")
	fmt.Printf("DEBUG: Read %d lines from script output\n", len(lines))

	// Get deployer address once if we have the key
	var deployerAddr ethtypes.EthAddress
	if cm.deployerKey != "" {
		cmd := exec.Command("cast", "wallet", "address", "--private-key", cm.deployerKey)
		if deployerOutput, err := cmd.CombinedOutput(); err == nil {
			if addr, err := ethtypes.ParseEthAddress(strings.TrimSpace(string(deployerOutput))); err == nil {
				deployerAddr = addr
			}
		}
	}

	// Load existing deployments if present
	var existing []*DeployedContract
	if data, err := os.ReadFile(deploymentsPath); err == nil {
		_ = json.Unmarshal(data, &existing) // ignore error, we'll overwrite if malformed
	}
	fmt.Printf("DEBUG: Loaded %d existing deployments\n", len(existing))

	// Map by name for easy lookup
	byName := make(map[string]*DeployedContract)
	for _, d := range existing {
		byName[strings.ToLower(d.Name)] = d
	}

	// Parse lines for patterns like 'Name: 0x...' or 'Name 0x...'
	reAddr := regexp.MustCompile(`0x[0-9a-fA-F]{40}`)
	reNameAddr := regexp.MustCompile(`(?i)^\s*([A-Za-z0-9_\-]+)[:\s]+(0x[0-9a-fA-F]{40})`) // captures name and addr

	parsedCount := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// try name: addr
		if m := reNameAddr.FindStringSubmatch(line); len(m) == 3 {
			name := strings.ToLower(m[1])
			addrStr := m[2]
			ethAddr, err := ethtypes.ParseEthAddress(addrStr)
			if err != nil {
				continue
			}
			d := &DeployedContract{
				Name:               name,
				Address:            ethAddr,
				DeployerAddress:    deployerAddr,
				DeployerPrivateKey: cm.deployerKey,
			}
			byName[name] = d
			parsedCount++
			fmt.Printf("DEBUG: Parsed contract %s: %s\n", name, addrStr)
			continue
		}

		// otherwise try to find an address and heuristically match a contract name in the line
		if addr := reAddr.FindString(line); addr != "" {
			// try to find any known contract name appearing in the line
			lower := strings.ToLower(line)
			for allowedName := range byName {
				if strings.Contains(lower, allowedName) {
					ethAddr, err := ethtypes.ParseEthAddress(addr)
					if err != nil {
						continue
					}
					d := &DeployedContract{
						Name:               allowedName,
						Address:            ethAddr,
						DeployerAddress:    deployerAddr,
						DeployerPrivateKey: cm.deployerKey,
					}
					byName[allowedName] = d
					parsedCount++
					fmt.Printf("DEBUG: Parsed contract %s: %s (heuristic)\n", allowedName, addr)
					break
				}
			}
		}
	}
	fmt.Printf("DEBUG: Parsed %d contracts from script output\n", parsedCount)

	// Recreate deployments slice preserving unknown entries
	var out []*DeployedContract
	// keep existing entries that weren't updated
	for _, d := range existing {
		lname := strings.ToLower(d.Name)
		if nd, ok := byName[lname]; ok {
			out = append(out, nd)
			delete(byName, lname)
		} else {
			out = append(out, d)
		}
	}
	// add any new entries parsed
	for _, d := range byName {
		out = append(out, d)
	}

	// Create alias entry if contractName and mainContract are provided
	// This allows exports with "self" to work when the script outputs mainContract name
	if contractName != "" && mainContract != "" {
		contractNameLower := strings.ToLower(contractName)
		mainContractLower := strings.ToLower(mainContract)

		// Check if mainContract exists in deployments
		var mainContractDeployment *DeployedContract
		for _, d := range out {
			if strings.EqualFold(d.Name, mainContractLower) {
				mainContractDeployment = d
				break
			}
		}

		// If mainContract found and contractName doesn't exist, create alias
		if mainContractDeployment != nil {
			contractNameExists := false
			for _, d := range out {
				if strings.EqualFold(d.Name, contractNameLower) {
					contractNameExists = true
					break
				}
			}

			if !contractNameExists {
				aliasDeployment := &DeployedContract{
					Name:               contractNameLower,
					Address:            mainContractDeployment.Address,
					DeployerAddress:    mainContractDeployment.DeployerAddress,
					DeployerPrivateKey: mainContractDeployment.DeployerPrivateKey,
					TransactionHash:    mainContractDeployment.TransactionHash,
					AbiPath:            mainContractDeployment.AbiPath,
					BindingsPath:       mainContractDeployment.BindingsPath,
				}
				out = append(out, aliasDeployment)
				fmt.Printf("DEBUG: Created alias entry %s -> %s (address: %s)\n", contractNameLower, mainContractLower, mainContractDeployment.Address.String())
			}
		}
	}

	fmt.Printf("DEBUG: Final deployments count: %d\n", len(out))

	// write back
	outBytes, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal deployments: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(deploymentsPath), 0755); err != nil {
		return fmt.Errorf("failed to ensure deployments dir: %w", err)
	}

	if err := os.WriteFile(deploymentsPath, outBytes, 0644); err != nil {
		return fmt.Errorf("failed to write deployments file: %w", err)
	}

	fmt.Printf("DEBUG: Successfully wrote %d contracts to %s\n", len(out), deploymentsPath)
	return nil
}

// processConstructorArgs handles special constructor argument formats
func processConstructorArgs(args []string) ([]string, error) {
	processedArgs := make([]string, len(args))

	for i, arg := range args {
		if strings.HasPrefix(arg, "CAST_CALLDATA:") {
			// Parse the CAST_CALLDATA format and convert it to actual call data
			callData, err := processCastCallData(arg)
			if err != nil {
				return nil, fmt.Errorf("failed to process cast calldata: %w", err)
			}
			processedArgs[i] = callData
		} else {
			processedArgs[i] = arg
		}
	}

	return processedArgs, nil
}

// processCastCallData converts CAST_CALLDATA format to actual encoded call data
func processCastCallData(castCallData string) (string, error) {
	// Format: CAST_CALLDATA:initialize(uint64,uint256,address,string,string):60:30:0x0000...:"Service Name":"Service Desc"
	// Or: CAST_CALLDATA:initialize() for no-arg functions
	parts := strings.Split(castCallData, ":")
	if len(parts) < 2 {
		return "", fmt.Errorf("invalid CAST_CALLDATA format: %s", castCallData)
	}

	funcSig := parts[1]
	funcArgs := parts[2:]

	// Use cast to generate the actual call data
	castArgs := []string{"calldata", funcSig}

	// Only add arguments if they exist
	if len(funcArgs) > 0 && funcArgs[0] != "" {
		castArgs = append(castArgs, funcArgs...)
	}

	cmd := exec.Command("cast", castArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to generate call data with cast: %w, output: %s", err, string(output))
	}

	return strings.TrimSpace(string(output)), nil
}
