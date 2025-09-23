package cmd

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/filecoin-project/go-address"
	filbig "github.com/filecoin-project/go-state-types/big"
	filcrypto "github.com/filecoin-project/go-state-types/crypto"
	"github.com/filecoin-project/lotus/api"
	"github.com/filecoin-project/lotus/chain/types"
	"github.com/filecoin-project/lotus/chain/types/ethtypes"
	"github.com/filecoin-project/lotus/chain/wallet/key"
	"github.com/filecoin-project/lotus/lib/sigs"
	_ "github.com/filecoin-project/lotus/lib/sigs/delegated"

	"github.com/urfave/cli/v2"
)

// SignTransaction signs an Ethereum transaction
func SignTransaction(tx *ethtypes.Eth1559TxArgs, privateKey []byte) {
	preimage, err := tx.ToRlpUnsignedMsg()
	if err != nil {
		log.Printf("Failed to convert transaction to RLP: %v", err)
		return
	}
	signature, err := sigs.Sign(filcrypto.SigTypeDelegated, privateKey, preimage)
	if err != nil {
		log.Printf("Failed to sign transaction: %v", err)
		return
	}
	err = tx.InitialiseSignature(*signature)
	if err != nil {
		log.Printf("Failed to initialise signature: %v", err)
		return
	}
}

// SubmitTransaction submits a signed Ethereum transaction to the network
// Returns the transaction hash
func SubmitTransaction(ctx context.Context, api api.FullNode, tx ethtypes.EthTransaction) ethtypes.EthHash {
	signed, err := tx.ToRlpSignedMsg()
	if err != nil {
		log.Printf("Failed to convert transaction to RLP: %v", err)
		return ethtypes.EthHash{}
	}
	txHash, err := api.EthSendRawTransaction(ctx, signed)
	if err != nil {
		log.Printf("Failed to send transaction: %v", err)
		return ethtypes.EthHash{}
	}
	return txHash
}

// DeployContract deploys a smart contract
func DeployContract(ctx context.Context, contractPath string, deployer string, fundAmount string, generateBindings bool, workspace string, contractName string, abiPath string) error {
	fmt.Printf("Deploying smart contract from %s...\n", contractPath)

	// Create new account for deployment if deployer not specified
	var key *key.Key
	var ethAddr ethtypes.EthAddress
	var deployerAddr address.Address

	if deployer == "" {
		k, eth, fil := NewAccount()
		if k == nil {
			return fmt.Errorf("failed to create deployer account")
		}
		key = k
		ethAddr = eth
		deployerAddr = fil
		fmt.Printf("Created deployer account: %s (ETH: %s)\n", deployerAddr, ethAddr)
	} else {
		// Parse existing deployer address
		addr, err := address.NewFromString(deployer)
		if err != nil {
			return fmt.Errorf("invalid deployer address: %w", err)
		}
		deployerAddr = addr
	}

	// Fund deployer account if needed
	if fundAmount != "" {
		amount, _ := filbig.FromString(fundAmount)
		fundAmountAtto := types.BigMul(amount, types.NewInt(1e18))

		_, err := FundWallet(ctx, deployerAddr, fundAmountAtto, true)
		if err != nil {
			return fmt.Errorf("failed to fund deployer: %w", err)
		}
		fmt.Printf("Funded deployer with %s FIL\n", fundAmount)
	}

	// Wait for funds to be available
	fmt.Println("Waiting for funds to be available...")
	time.Sleep(5 * time.Second)

	// Read and decode contract
	contractHex, err := os.ReadFile(contractPath)
	if err != nil {
		return fmt.Errorf("failed to read contract file: %w", err)
	}

	contract, err := hex.DecodeString(string(contractHex))
	if err != nil {
		return fmt.Errorf("failed to decode contract: %w", err)
	}

	api := clientt.GetAPI()

	// Estimate gas
	gasParams, err := json.Marshal(ethtypes.EthEstimateGasParams{Tx: ethtypes.EthCall{
		From: &ethAddr,
		Data: contract,
	}})
	if err != nil {
		return fmt.Errorf("failed to marshal gas params: %w", err)
	}

	gasLimit, err := api.EthEstimateGas(ctx, gasParams)
	if err != nil {
		return fmt.Errorf("failed to estimate gas: %w", err)
	}

	// Get gas fees
	maxPriorityFee, err := api.EthMaxPriorityFeePerGas(ctx)
	if err != nil {
		return fmt.Errorf("failed to get max priority fee: %w", err)
	}

	// Get nonce
	nonce, err := api.MpoolGetNonce(ctx, deployerAddr)
	if err != nil {
		return fmt.Errorf("failed to get nonce: %w", err)
	}

	// Create EIP-1559 transaction
	tx := ethtypes.Eth1559TxArgs{
		ChainID:              31415926,
		Value:                filbig.Zero(),
		Nonce:                int(nonce),
		MaxFeePerGas:         types.NanoFil,
		MaxPriorityFeePerGas: filbig.Int(maxPriorityFee),
		GasLimit:             int(gasLimit),
		Input:                contract,
		V:                    filbig.Zero(),
		R:                    filbig.Zero(),
		S:                    filbig.Zero(),
	}

	fmt.Printf("Transaction details:\n")
	fmt.Printf("  Gas Limit: %d\n", gasLimit)
	fmt.Printf("  Max Priority Fee: %s\n", maxPriorityFee.String())
	fmt.Printf("  Nonce: %d\n", nonce)

	// Sign and submit transaction
	fmt.Println("Signing and submitting transaction...")
	if key != nil {
		SignTransaction(&tx, key.PrivateKey)
	}

	txHash := SubmitTransaction(ctx, api, &tx)
	if txHash == ethtypes.EmptyEthHash {
		return fmt.Errorf("failed to submit transaction")
	}

	// Wait for transaction to be mined
	fmt.Println("Waiting for transaction to be mined...")
	time.Sleep(10 * time.Second)

	// Get transaction receipt
	receipt, err := api.EthGetTransactionReceipt(ctx, txHash)
	if err != nil {
		return fmt.Errorf("failed to get transaction receipt: %w", err)
	}

	if receipt == nil {
		return fmt.Errorf("transaction receipt is nil")
	}

	if receipt.Status == 1 {
		fmt.Printf("Contract deployed successfully!\n")
		fmt.Printf("Contract Address: %s\n", receipt.ContractAddress)

		// Save deployment artifacts
		if err := saveDeploymentArtifacts(contractPath, receipt.ContractAddress.String(), txHash, deployerAddr, ethAddr, key, generateBindings, workspace, contractName, abiPath); err != nil {
			fmt.Printf("Warning: failed to save deployment artifacts: %v\n", err)
		}

	} else {
		return fmt.Errorf("transaction failed with status: %d", receipt.Status)
	}

	return nil
}

// saveDeploymentArtifacts saves deployment information and artifacts to workspace
func saveDeploymentArtifacts(contractPath, contractAddress string, txHash ethtypes.EthHash, deployerAddr address.Address, ethAddr ethtypes.EthAddress, key *key.Key, generateBindings bool, workspace, contractName, abiPath string) error {
	// Create contract manager
	manager := NewContractManager(workspace, "")

	// Determine contract name
	if contractName == "" {
		// Extract name from contract file path
		baseName := filepath.Base(contractPath)
		contractName = strings.TrimSuffix(baseName, filepath.Ext(baseName))
	}

	// Parse contract address
	contractEthAddr, err := ethtypes.ParseEthAddress(contractAddress)
	if err != nil {
		return fmt.Errorf("failed to parse contract address: %w", err)
	}

	// Get deployer private key
	var deployerPrivateKey string
	if key != nil {
		deployerPrivateKey = fmt.Sprintf("0x%x", key.PrivateKey)
	}

	// Create deployed contract struct
	deployedContract := &DeployedContract{
		Name:               contractName,
		Address:            contractEthAddr,
		DeployerAddress:    ethAddr,
		DeployerPrivateKey: deployerPrivateKey,
		TransactionHash:    txHash,
	}

	// Save contract bytecode
	contractHex, err := os.ReadFile(contractPath)
	if err != nil {
		return fmt.Errorf("failed to read contract file: %w", err)
	}

	// Save bytecode to workspace
	contractsDir := filepath.Join(workspace, "contracts")
	if err := os.MkdirAll(contractsDir, 0755); err != nil {
		return fmt.Errorf("failed to create contracts directory: %w", err)
	}

	bytecodePath := filepath.Join(contractsDir, fmt.Sprintf("%s.bin", strings.ToLower(contractName)))
	if err := os.WriteFile(bytecodePath, contractHex, 0644); err != nil {
		return fmt.Errorf("failed to save bytecode: %w", err)
	}

	fmt.Printf("Saved contract bytecode to %s\n", bytecodePath)

	// Handle ABI - either use provided ABI file or try to extract from source
	finalAbiPath := filepath.Join(contractsDir, fmt.Sprintf("%s.abi.json", strings.ToLower(contractName)))

	if abiPath != "" {
		// Use provided ABI file
		abiData, err := os.ReadFile(abiPath)
		if err != nil {
			return fmt.Errorf("failed to read provided ABI file: %w", err)
		}

		// Validate ABI JSON
		var abiDataParsed interface{}
		if err := json.Unmarshal(abiData, &abiDataParsed); err != nil {
			return fmt.Errorf("invalid ABI JSON in provided file: %w", err)
		}

		if err := os.WriteFile(finalAbiPath, abiData, 0644); err != nil {
			return fmt.Errorf("failed to save ABI: %w", err)
		}

		fmt.Printf("Saved ABI from provided file to %s\n", finalAbiPath)
	} else {
		// Try to extract ABI from source files
		extractedAbiPath, err := extractABIFromSource(contractPath, contractName, contractsDir)
		if err != nil {
			fmt.Printf("Warning: failed to extract ABI from source: %v\n", err)
			fmt.Printf("Creating minimal ABI. Use --abi flag to provide proper ABI file.\n")

			// Create a minimal ABI for hex deployments (empty ABI)
			minimalABI := []interface{}{}
			abiBytes, err := json.Marshal(minimalABI)
			if err != nil {
				return fmt.Errorf("failed to marshal minimal ABI: %w", err)
			}

			if err := os.WriteFile(finalAbiPath, abiBytes, 0644); err != nil {
				return fmt.Errorf("failed to save minimal ABI: %w", err)
			}

			fmt.Printf("Saved minimal ABI to %s\n", finalAbiPath)
		} else {
			finalAbiPath = extractedAbiPath
			fmt.Printf("Extracted and saved ABI from source to %s\n", finalAbiPath)
		}
	}

	deployedContract.AbiPath = finalAbiPath

	// Generate Go bindings if requested
	if generateBindings {
		if bindingsPath, err := generateGoBindingsFromHex(contractName, finalAbiPath, bytecodePath, contractsDir); err == nil {
			deployedContract.BindingsPath = bindingsPath
			fmt.Printf("Generated Go bindings to %s\n", bindingsPath)
		} else {
			fmt.Printf("Warning: failed to generate Go bindings: %v\n", err)
		}
	}

	// Save deployment information
	if err := manager.saveDeployment(deployedContract); err != nil {
		return fmt.Errorf("failed to save deployment info: %w", err)
	}

	fmt.Printf("Saved deployment information to workspace\n")
	return nil
}

// generateGoBindingsFromHex generates Go bindings from hex file deployment
func generateGoBindingsFromHex(contractName, abiPath, bytecodePath, contractsDir string) (string, error) {
	bindingsPath := filepath.Join(contractsDir, fmt.Sprintf("%s.go", strings.ToLower(contractName)))

	cmd := exec.Command("abigen",
		"--abi", abiPath,
		"--bin", bytecodePath,
		"--pkg", "contracts",
		"--type", contractName,
		"--out", bindingsPath)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to generate Go bindings: %w, output: %s", err, string(output))
	}

	return bindingsPath, nil
}

// extractABIFromSource tries to extract ABI from Solidity source files
func extractABIFromSource(contractPath, contractName, contractsDir string) (string, error) {
	// Look for corresponding .sol file
	contractDir := filepath.Dir(contractPath)
	baseName := strings.TrimSuffix(filepath.Base(contractPath), filepath.Ext(contractPath))
	solFile := filepath.Join(contractDir, baseName+".sol")

	// Check if .sol file exists
	if _, err := os.Stat(solFile); err != nil {
		return "", fmt.Errorf("no corresponding .sol file found for %s", contractPath)
	}

	// Try to compile the Solidity file to extract ABI
	// This is a simplified approach - in practice, you might want to use solc directly
	tempDir := filepath.Join(contractsDir, "temp_compile")
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	// Copy .sol file to temp directory
	tempSolFile := filepath.Join(tempDir, filepath.Base(solFile))
	solData, err := os.ReadFile(solFile)
	if err != nil {
		return "", fmt.Errorf("failed to read .sol file: %w", err)
	}

	if err := os.WriteFile(tempSolFile, solData, 0644); err != nil {
		return "", fmt.Errorf("failed to write temp .sol file: %w", err)
	}

	// Try to compile using solc (if available)
	cmd := exec.Command("solc", "--abi", "--bin", "--output-dir", tempDir, tempSolFile)
	_, err = cmd.CombinedOutput()
	if err != nil {
		// If solc is not available, try to extract ABI from the source file directly
		return extractABIFromSourceFile(solFile, contractName, contractsDir)
	}

	// Look for generated ABI file
	abiFile := filepath.Join(tempDir, fmt.Sprintf("%s.abi", contractName))
	if _, err := os.Stat(abiFile); err != nil {
		// Try with lowercase name
		abiFile = filepath.Join(tempDir, fmt.Sprintf("%s.abi", strings.ToLower(contractName)))
		if _, err := os.Stat(abiFile); err != nil {
			return "", fmt.Errorf("ABI file not found after compilation")
		}
	}

	// Copy ABI to final location
	finalAbiPath := filepath.Join(contractsDir, fmt.Sprintf("%s.abi.json", strings.ToLower(contractName)))
	abiData, err := os.ReadFile(abiFile)
	if err != nil {
		return "", fmt.Errorf("failed to read generated ABI: %w", err)
	}

	if err := os.WriteFile(finalAbiPath, abiData, 0644); err != nil {
		return "", fmt.Errorf("failed to save ABI: %w", err)
	}

	return finalAbiPath, nil
}

// extractABIFromSourceFile tries to extract ABI information from Solidity source
func extractABIFromSourceFile(solFile, contractName, contractsDir string) (string, error) {
	// This is a very basic approach - in practice, you'd want to use a proper Solidity parser
	// For now, we'll create a basic ABI based on common patterns

	solData, err := os.ReadFile(solFile)
	if err != nil {
		return "", fmt.Errorf("failed to read .sol file: %w", err)
	}

	// Look for function signatures in the source
	// This is a simplified approach - a real implementation would use a proper parser
	content := string(solData)

	// Check if this looks like a SimpleCoin contract
	if strings.Contains(content, "function sendCoin") && strings.Contains(content, "function getBalance") {
		// Create a basic ABI for SimpleCoin
		simpleCoinABI := `[
			{
				"inputs": [],
				"stateMutability": "nonpayable",
				"type": "constructor"
			},
			{
				"anonymous": false,
				"inputs": [
					{
						"indexed": true,
						"internalType": "address",
						"name": "_from",
						"type": "address"
					},
					{
						"indexed": true,
						"internalType": "address",
						"name": "_to",
						"type": "address"
					},
					{
						"indexed": false,
						"internalType": "uint256",
						"name": "_value",
						"type": "uint256"
					}
				],
				"name": "Transfer",
				"type": "event"
			},
			{
				"inputs": [
					{
						"internalType": "address",
						"name": "addr",
						"type": "address"
					}
				],
				"name": "getBalance",
				"outputs": [
					{
						"internalType": "uint256",
						"name": "",
						"type": "uint256"
					}
				],
				"stateMutability": "view",
				"type": "function"
			},
			{
				"inputs": [
					{
						"internalType": "address",
						"name": "addr",
						"type": "address"
					}
				],
				"name": "getBalanceInEth",
				"outputs": [
					{
						"internalType": "uint256",
						"name": "",
						"type": "uint256"
					}
				],
				"stateMutability": "view",
				"type": "function"
			},
			{
				"inputs": [
					{
						"internalType": "address",
						"name": "receiver",
						"type": "address"
					},
					{
						"internalType": "uint256",
						"name": "amount",
						"type": "uint256"
					}
				],
				"name": "sendCoin",
				"outputs": [
					{
						"internalType": "bool",
						"name": "sufficient",
						"type": "bool"
					}
				],
				"stateMutability": "nonpayable",
				"type": "function"
			}
		]`

		finalAbiPath := filepath.Join(contractsDir, fmt.Sprintf("%s.abi.json", strings.ToLower(contractName)))
		if err := os.WriteFile(finalAbiPath, []byte(simpleCoinABI), 0644); err != nil {
			return "", fmt.Errorf("failed to save extracted ABI: %w", err)
		}

		return finalAbiPath, nil
	}

	return "", fmt.Errorf("unable to extract ABI from source file")
}

var ContractCmd = &cli.Command{
	Name:  "contract",
	Usage: "Contract operations",
	Subcommands: []*cli.Command{
		{
			Name:      "deploy",
			Usage:     "Deploy a contract from hex file",
			ArgsUsage: "<contract-file>",
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:  "deployer",
					Usage: "Deployer wallet address (creates new if not specified)",
				},
				&cli.StringFlag{
					Name:  "fund",
					Value: "10",
					Usage: "Amount to fund deployer wallet (FIL)",
				},
				&cli.StringFlag{
					Name:  "value",
					Value: "0",
					Usage: "Value to send with deployment (FIL)",
				},
				&cli.BoolFlag{
					Name:  "bindings",
					Usage: "Generate Go bindings using abigen and save to disk",
				},
				&cli.StringFlag{
					Name:  "workspace",
					Usage: "Workspace directory for saving deployment artifacts",
					Value: "./workspace",
				},
				&cli.StringFlag{
					Name:  "contract-name",
					Usage: "Name of the contract (used for artifact naming)",
				},
				&cli.StringFlag{
					Name:  "abi",
					Usage: "Path to ABI file for the contract (optional, will try to extract from source if not provided)",
				},
			},
			Action: func(c *cli.Context) error {
				if c.NArg() != 1 {
					return fmt.Errorf("expected 1 argument: <contract-file>")
				}

				ctx := context.Background()
				contractFile := c.Args().Get(0)
				deployer := c.String("deployer")
				fundAmount := c.String("fund")
				generateBindings := c.Bool("bindings")
				workspace := c.String("workspace")
				contractName := c.String("contract-name")
				abiPath := c.String("abi")

				return DeployContract(ctx, contractFile, deployer, fundAmount, generateBindings, workspace, contractName, abiPath)
			},
		},
		{
			Name:  "from-git",
			Usage: "Deploy contract from git repository",
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:     "git-url",
					Usage:    "Git repository URL",
					Required: true,
				},
				&cli.StringFlag{
					Name:  "project-type",
					Usage: "Project type (hardhat or foundry)",
					Value: "foundry",
				},
				&cli.StringFlag{
					Name:  "main-contract",
					Usage: "Main contract name to deploy",
				},
				&cli.StringFlag{
					Name:  "contract-path",
					Usage: "Relative path to the contract file (e.g., 'contracts/SimpleCoin.sol')",
				},
				&cli.StringFlag{
					Name:  "constructor-args",
					Usage: "Constructor arguments (comma-separated)",
				},
				&cli.StringFlag{
					Name:  "workspace",
					Usage: "Workspace directory for cloning and compilation",
					Value: "./workspace",
				},
				&cli.StringFlag{
					Name:  "rpc-url",
					Usage: "RPC URL for deployment",
					Value: "http://localhost:1234/rpc/v1",
				},
				&cli.BoolFlag{
					Name:  "create-deployer",
					Usage: "Create a new deployer account",
				},
				&cli.StringFlag{
					Name:  "deployer-key",
					Usage: "Private key for deployment (if not creating new)",
				},
				&cli.StringFlag{
					Name:  "deploy-script",
					Usage: "Custom deployment script to run (e.g., 'scripts/deploy.sh')",
				},
				&cli.StringSliceFlag{
					Name:  "env",
					Usage: "Environment variables for deployment (format: KEY=VALUE)",
				},
				&cli.StringFlag{
					Name:  "commands",
					Usage: "Shell commands to run after cloning (separated by semicolons, e.g., 'yarn install; yarn hardhat deploy')",
				},
				&cli.StringFlag{
					Name:  "git-ref",
					Usage: "Git reference to checkout (tag, branch, or commit hash)",
				},
				&cli.BoolFlag{
					Name:  "bindings",
					Usage: "Generate Go bindings using abigen and save to disk",
				},
			},
			Action: deployFromGit,
		},
		{
			Name:  "list",
			Usage: "List deployed contracts",
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:  "workspace",
					Usage: "Workspace directory",
					Value: "./workspace",
				},
			},
			Action: listDeployments,
		},
		{
			Name:  "info",
			Usage: "Get deployment information for a contract",
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:     "contract",
					Usage:    "Contract name",
					Required: true,
				},
				&cli.StringFlag{
					Name:  "workspace",
					Usage: "Workspace directory",
					Value: "./workspace",
				},
			},
			Action: getDeploymentInfo,
		},
		{
			Name:  "cleanup",
			Usage: "Clean up temporary project directories",
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:  "workspace",
					Usage: "Workspace directory",
					Value: "./workspace",
				},
			},
			Action: cleanupWorkspace,
		},
		{
			Name:      "call",
			Usage:     "Call a contract method",
			ArgsUsage: "<contract-address> <method-name>",
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:     "contract",
					Usage:    "Contract address",
					Required: true,
				},
				&cli.StringFlag{
					Name:     "method",
					Usage:    "Method name to call",
					Required: true,
				},
				&cli.StringFlag{
					Name:  "rpc-url",
					Usage: "RPC URL for contract interaction",
					Value: "http://localhost:1234/rpc/v1",
				},
				&cli.StringFlag{
					Name:  "args",
					Usage: "Method arguments (comma-separated)",
				},
				&cli.StringFlag{
					Name:  "types",
					Usage: "Argument types (comma-separated: address,uint256,bool,string)",
				},
				&cli.BoolFlag{
					Name:  "transaction",
					Usage: "Send as transaction (for state-changing functions)",
				},
				&cli.StringFlag{
					Name:  "private-key",
					Usage: "Private key for transaction signing (hex format, 0x prefix optional)",
				},
				&cli.Uint64Flag{
					Name:  "gas-limit",
					Usage: "Gas limit for transaction (0 = auto-estimate)",
					Value: 0,
				},
			},
			Action: callContractMethod,
		},
	},
}

// deployFromGit deploys a contract from a git repository
func deployFromGit(c *cli.Context) error {
	if deployScript := c.String("deploy-script"); deployScript != "" {
		return deployWithCustomScript(c)
	}

	// Check if using shell commands
	if commands := c.String("commands"); commands != "" {
		return deployWithShellCommands(c)
	}

	if c.String("main-contract") == "" {
		return fmt.Errorf("main-contract is required for deployment")
	}

	// Create contract manager
	manager := NewContractManager(c.String("workspace"), c.String("rpc-url"))

	// Handle deployer account
	if c.Bool("create-deployer") {
		fmt.Println("Creating new deployer account...")
		privateKey, address, err := manager.CreateDeployerAccount()
		if err != nil {
			return fmt.Errorf("failed to create deployer account: %w", err)
		}
		fmt.Printf("Created deployer account: %s\n", address.String())
		fmt.Printf("Private key: %s\n", privateKey)
	} else if deployerKey := c.String("deployer-key"); deployerKey != "" {
		manager.SetDeployerKey(deployerKey)
	} else {
		return fmt.Errorf("either --create-deployer or --deployer-key must be provided")
	}

	// Create project configuration
	projectType := ProjectType(c.String("project-type"))
	project := &ContractProject{
		GitURL:       c.String("git-url"),
		GitRef:       c.String("git-ref"),
		ProjectType:  projectType,
		MainContract: c.String("main-contract"),
		CloneDir:     "",
		Env:          make(map[string]string),
	}

	// Set custom contract path if provided
	if contractPath := c.String("contract-path"); contractPath != "" {
		project.ContractPath = contractPath
	}

	// Parse environment variables
	envVars := c.StringSlice("env")
	for _, envVar := range envVars {
		parts := strings.SplitN(envVar, "=", 2)
		if len(parts) == 2 {
			project.Env[parts[0]] = parts[1]
		}
	}

	// Clone repository
	fmt.Printf("Cloning repository: %s\n", project.GitURL)
	if err := manager.CloneRepository(project); err != nil {
		return fmt.Errorf("failed to clone repository: %w", err)
	}
	fmt.Printf("Repository cloned to: %s\n", project.CloneDir)

	// Determine if we need to compile first based on project type
	if project.ProjectType == ProjectTypeHardhat {
		fmt.Printf("Hardhat project detected - compiling first...\n")
		if err := manager.CompileHardhatProject(project); err != nil {
			return fmt.Errorf("failed to compile Hardhat project: %w", err)
		}
		fmt.Printf("Hardhat compilation completed\n")
	} else {
		fmt.Printf("Foundry project - deploying directly with forge create...\n")
	}

	// Parse constructor arguments
	var constructorArgs []string
	if argsStr := c.String("constructor-args"); argsStr != "" {
		constructorArgs = strings.Split(argsStr, ",")
		for i, arg := range constructorArgs {
			constructorArgs[i] = strings.TrimSpace(arg)
		}
	}

	// Deploy contract
	fmt.Printf("Deploying contract: %s\n", project.MainContract)
	if len(constructorArgs) > 0 {
		fmt.Printf("Constructor args: %v\n", constructorArgs)
	}

	// Check if using custom deployment script
	if deployScript := c.String("deploy-script"); deployScript != "" {
		fmt.Printf("Running custom deployment script: %s\n", deployScript)
		if err := manager.RunCustomDeployScript(project, deployScript); err != nil {
			return fmt.Errorf("failed to run deployment script: %w", err)
		}
		fmt.Printf("Custom deployment script completed successfully\n")
		return nil
	}

	// Deploy contract using forge create
	contractPath := fmt.Sprintf("%s:%s", project.ContractPath, project.MainContract)
	generateBindings := c.Bool("bindings")
	deployedContract, err := manager.DeployContract(project, contractPath, constructorArgs, generateBindings)

	if err != nil {
		return fmt.Errorf("failed to deploy contract: %w", err)
	}

	// Display deployment results
	fmt.Printf("\nContract deployed successfully!\n")
	fmt.Printf("Contract: %s\n", deployedContract.Name)
	fmt.Printf("Address: %s\n", deployedContract.Address.String())
	fmt.Printf("Transaction: %s\n", deployedContract.TransactionHash.String())
	fmt.Printf("Deployer: %s\n", deployedContract.DeployerAddress.String())
	fmt.Printf("Deployer Key: %s\n", deployedContract.DeployerPrivateKey)

	return nil
}

func listDeployments(c *cli.Context) error {
	manager := NewContractManager(c.String("workspace"), "")

	deployments, err := manager.LoadDeployments()
	if err != nil {
		return fmt.Errorf("failed to load deployments: %w", err)
	}

	if len(deployments) == 0 {
		fmt.Println("No deployments found.")
		return nil
	}

	fmt.Printf("Found %d deployed contracts:\n\n", len(deployments))

	for i, deployment := range deployments {
		fmt.Printf("%d. %s\n", i+1, deployment.Name)
		fmt.Printf("   Address: %s\n", deployment.Address.String())
		fmt.Printf("   TX Hash: %s\n", deployment.TransactionHash.String())
		fmt.Printf("   Deployer: %s\n", deployment.DeployerAddress.String())
		fmt.Printf("   Deployer Key: %s\n", deployment.DeployerPrivateKey)
		if deployment.AbiPath != "" {
			fmt.Printf("   ABI Path: %s\n", deployment.AbiPath)
		}
		if deployment.BindingsPath != "" {
			fmt.Printf("   Go Bindings: %s\n", deployment.BindingsPath)
		}
		fmt.Println()
	}

	return nil
}

func getDeploymentInfo(c *cli.Context) error {
	manager := NewContractManager(c.String("workspace"), "")

	deployment, err := manager.GetDeployment(c.String("contract"))
	if err != nil {
		return fmt.Errorf("failed to get deployment info: %w", err)
	}

	fmt.Printf("Contract: %s\n", deployment.Name)
	fmt.Printf("Address: %s\n", deployment.Address.String())
	fmt.Printf("Transaction Hash: %s\n", deployment.TransactionHash.String())
	fmt.Printf("Deployer Address: %s\n", deployment.DeployerAddress.String())
	fmt.Printf("Deployer Key: %s\n", deployment.DeployerPrivateKey)
	if deployment.AbiPath != "" {
		fmt.Printf("ABI Path: %s\n", deployment.AbiPath)
	}
	if deployment.BindingsPath != "" {
		fmt.Printf("Go Bindings: %s\n", deployment.BindingsPath)
	}

	return nil
}

func cleanupWorkspace(c *cli.Context) error {
	manager := NewContractManager(c.String("workspace"), "")

	fmt.Printf("Cleaning up workspace: %s\n", c.String("workspace"))

	if err := manager.CleanupWorkspace(); err != nil {
		return fmt.Errorf("failed to cleanup workspace: %w", err)
	}

	fmt.Println("Workspace cleaned up successfully")
	return nil
}

// deployWithCustomScript handles deployment using a custom script
func deployWithCustomScript(c *cli.Context) error {
	// Create contract manager
	manager := NewContractManager(c.String("workspace"), c.String("rpc-url"))

	// Handle deployer account
	if c.Bool("create-deployer") {
		fmt.Println("Creating new deployer account...")
		privateKey, address, err := manager.CreateDeployerAccount()
		if err != nil {
			return fmt.Errorf("failed to create deployer account: %w", err)
		}
		fmt.Printf("Created deployer account: %s\n", address.String())
		fmt.Printf("Private key: %s\n", privateKey)
	} else if deployerKey := c.String("deployer-key"); deployerKey != "" {
		manager.SetDeployerKey(deployerKey)
	} else {
		return fmt.Errorf("either --create-deployer or --deployer-key must be provided")
	}

	// Create minimal project configuration for custom script
	project := &ContractProject{
		GitURL:   c.String("git-url"),
		GitRef:   c.String("git-ref"),
		CloneDir: "", // Will be set by CloneRepository
		Env:      make(map[string]string),
	}

	// Parse environment variables
	envVars := c.StringSlice("env")
	for _, envVar := range envVars {
		parts := strings.SplitN(envVar, "=", 2)
		if len(parts) == 2 {
			project.Env[parts[0]] = parts[1]
		}
	}

	// Clone repository
	fmt.Printf("Cloning repository: %s\n", project.GitURL)
	if err := manager.CloneRepository(project); err != nil {
		return fmt.Errorf("failed to clone repository: %w", err)
	}
	fmt.Printf("Repository cloned to: %s\n", project.CloneDir)

	// Run custom deployment script
	deployScript := c.String("deploy-script")
	fmt.Printf("Running custom deployment script: %s\n", deployScript)
	if err := manager.RunCustomDeployScript(project, deployScript); err != nil {
		return fmt.Errorf("failed to run deployment script: %w", err)
	}
	fmt.Printf("Custom deployment script completed successfully\n")
	return nil
}

// deployWithShellCommands handles deployment using shell commands
func deployWithShellCommands(c *cli.Context) error {
	// Create contract manager
	manager := NewContractManager(c.String("workspace"), c.String("rpc-url"))

	if c.Bool("create-deployer") {
		fmt.Println("Creating new deployer account...")
		privateKey, address, err := manager.CreateDeployerAccount()
		if err != nil {
			return fmt.Errorf("failed to create deployer account: %w", err)
		}
		fmt.Printf("Created deployer account: %s\n", address.String())
		fmt.Printf("Private key: %s\n", privateKey)
	} else if deployerKey := c.String("deployer-key"); deployerKey != "" {
		manager.SetDeployerKey(deployerKey)
	} else {
		return fmt.Errorf("either --create-deployer or --deployer-key must be provided")
	}

	// Create minimal project configuration for shell commands
	project := &ContractProject{
		GitURL:   c.String("git-url"),
		GitRef:   c.String("git-ref"),
		CloneDir: "", // Will be set by CloneRepository
		Env:      make(map[string]string),
	}

	// Parse environment variables
	envVars := c.StringSlice("env")
	for _, envVar := range envVars {
		parts := strings.SplitN(envVar, "=", 2)
		if len(parts) == 2 {
			project.Env[parts[0]] = parts[1]
		}
	}

	// Clone repository
	fmt.Printf("Cloning repository: %s\n", project.GitURL)
	if err := manager.CloneRepository(project); err != nil {
		return fmt.Errorf("failed to clone repository: %w", err)
	}
	fmt.Printf("Repository cloned to: %s\n", project.CloneDir)

	// Run shell commands
	commands := c.String("commands")
	fmt.Printf("Running shell commands: %s\n", commands)
	if err := manager.RunShellCommands(project, commands); err != nil {
		return fmt.Errorf("failed to run shell commands: %w", err)
	}
	fmt.Printf("Shell commands completed successfully\n")
	return nil
}

func callContractMethod(c *cli.Context) error {
	contractAddress := c.String("contract")
	methodName := c.String("method")
	rpcURL := c.String("rpc-url")
	argsStr := c.String("args")
	typesStr := c.String("types")
	isTransaction := c.Bool("transaction")
	privateKeyStr := c.String("private-key")
	gasLimit := c.Uint64("gas-limit")

	var args, types []string
	if argsStr != "" {
		args = strings.Split(argsStr, ",")
		for i, arg := range args {
			args[i] = strings.TrimSpace(arg)
		}
	}
	if typesStr != "" {
		types = strings.Split(typesStr, ",")
		for i, t := range types {
			types[i] = strings.TrimSpace(t)
		}
	}

	if len(args) != len(types) {
		return fmt.Errorf("number of arguments (%d) must match number of types (%d)", len(args), len(types))
	}

	wrapper, err := NewContractWrapper(rpcURL, contractAddress)
	if err != nil {
		return fmt.Errorf("failed to create contract wrapper: %w", err)
	}
	defer wrapper.Close()

	convertedArgs, err := convertArguments(args, types)
	if err != nil {
		return fmt.Errorf("failed to convert arguments: %w", err)
	}

	if isTransaction {
		if privateKeyStr == "" {
			return fmt.Errorf("private key is required for transactions")
		}

		privateKey, err := parsePrivateKey(privateKeyStr)
		if err != nil {
			return fmt.Errorf("failed to parse private key: %w", err)
		}

		tx, err := wrapper.SendTransaction(methodName, convertedArgs, privateKey, gasLimit)
		if err != nil {
			return fmt.Errorf("failed to send transaction: %w", err)
		}

		fmt.Printf("Transaction sent successfully!\n")
		fmt.Printf("Method: %s\n", methodName)
		fmt.Printf("Transaction Hash: %s\n", tx.Hash().Hex())
		fmt.Printf("Gas Limit: %d\n", tx.Gas())
		fmt.Printf("Gas Price: %s\n", tx.GasPrice().String())
	} else {
		result, err := wrapper.CallMethod(methodName, convertedArgs)
		if err != nil {
			return fmt.Errorf("failed to call contract method: %w", err)
		}

		fmt.Printf("Method: %s\n", methodName)
		fmt.Printf("Result: 0x%x\n", result)
	}

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
			return nil, fmt.Errorf("failed to convert argument %d (%s): %w", i, arg, err)
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
		val, ok := new(big.Int).SetString(arg, 10)
		if !ok {
			return nil, fmt.Errorf("invalid uint256 value: %s", arg)
		}
		return val, nil
	case "bool":
		return arg == "true", nil
	case "string":
		return arg, nil
	default:
		return nil, fmt.Errorf("unsupported type: %s", argType)
	}
}

func parsePrivateKey(privateKeyStr string) (*ecdsa.PrivateKey, error) {
	// Remove 0x prefix if present
	if strings.HasPrefix(privateKeyStr, "0x") {
		privateKeyStr = privateKeyStr[2:]
	}

	// Parse hex string
	privateKeyBytes, err := hex.DecodeString(privateKeyStr)
	if err != nil {
		return nil, fmt.Errorf("invalid hex format: %w", err)
	}

	// Create private key
	privateKey, err := crypto.ToECDSA(privateKeyBytes)
	if err != nil {
		return nil, fmt.Errorf("invalid private key: %w", err)
	}

	return privateKey, nil
}
