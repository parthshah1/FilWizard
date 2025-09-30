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
	"github.com/parthshah1/mpool-tx/config"

	"github.com/urfave/cli/v2"
)

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

func DeployContract(ctx context.Context, contractPath string, deployer string, fundAmount string, generateBindings bool, workspace string, contractName string, abiPath string) error {
	fmt.Printf("Deploying smart contract from %s...\n", contractPath)

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
		addr, err := address.NewFromString(deployer)
		if err != nil {
			return fmt.Errorf("invalid deployer address: %w", err)
		}
		deployerAddr = addr
	}

	if fundAmount != "" {
		amount, _ := filbig.FromString(fundAmount)
		fundAmountAtto := types.BigMul(amount, types.NewInt(1e18))

		_, err := FundWallet(ctx, deployerAddr, fundAmountAtto, true)
		if err != nil {
			return fmt.Errorf("failed to fund deployer: %w", err)
		}
		fmt.Printf("Funded deployer with %s FIL\n", fundAmount)
	}

	fmt.Println("Waiting for funds to be available...")
	time.Sleep(5 * time.Second)

	contractHex, err := os.ReadFile(contractPath)
	if err != nil {
		return fmt.Errorf("failed to read contract file: %w", err)
	}

	contract, err := hex.DecodeString(string(contractHex))
	if err != nil {
		return fmt.Errorf("failed to decode contract: %w", err)
	}

	api := clientt.GetAPI()

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

	maxPriorityFee, err := api.EthMaxPriorityFeePerGas(ctx)
	if err != nil {
		return fmt.Errorf("failed to get max priority fee: %w", err)
	}

	nonce, err := api.MpoolGetNonce(ctx, deployerAddr)
	if err != nil {
		return fmt.Errorf("failed to get nonce: %w", err)
	}

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

	fmt.Println("Signing and submitting transaction...")
	if key != nil {
		SignTransaction(&tx, key.PrivateKey)
	}

	txHash := SubmitTransaction(ctx, api, &tx)
	if txHash == ethtypes.EmptyEthHash {
		return fmt.Errorf("failed to submit transaction")
	}

	fmt.Println("Waiting for transaction to be mined...")
	time.Sleep(10 * time.Second)

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

		if err := saveDeploymentArtifacts(contractPath, receipt.ContractAddress.String(), txHash, deployerAddr, ethAddr, key, generateBindings, workspace, contractName, abiPath); err != nil {
			fmt.Printf("Warning: failed to save deployment artifacts: %v\n", err)
		}

	} else {
		return fmt.Errorf("transaction failed with status: %d", receipt.Status)
	}

	return nil
}

func saveDeploymentArtifacts(contractPath, contractAddress string, txHash ethtypes.EthHash, deployerAddr address.Address, ethAddr ethtypes.EthAddress, key *key.Key, generateBindings bool, workspace, contractName, abiPath string) error {
	manager := NewContractManager(workspace, "")

	if contractName == "" {
		baseName := filepath.Base(contractPath)
		contractName = strings.TrimSuffix(baseName, filepath.Ext(baseName))
	}

	contractEthAddr, err := ethtypes.ParseEthAddress(contractAddress)
	if err != nil {
		return fmt.Errorf("failed to parse contract address: %w", err)
	}

	var deployerPrivateKey string
	if key != nil {
		deployerPrivateKey = fmt.Sprintf("0x%x", key.PrivateKey)
	}

	deployedContract := &DeployedContract{
		Name:               contractName,
		Address:            contractEthAddr,
		DeployerAddress:    ethAddr,
		DeployerPrivateKey: deployerPrivateKey,
		TransactionHash:    txHash,
	}

	contractHex, err := os.ReadFile(contractPath)
	if err != nil {
		return fmt.Errorf("failed to read contract file: %w", err)
	}

	contractsDir := filepath.Join(workspace, "contracts")
	if err := os.MkdirAll(contractsDir, 0755); err != nil {
		return fmt.Errorf("failed to create contracts directory: %w", err)
	}

	bytecodePath := filepath.Join(contractsDir, fmt.Sprintf("%s.bin", strings.ToLower(contractName)))
	if err := os.WriteFile(bytecodePath, contractHex, 0644); err != nil {
		return fmt.Errorf("failed to save bytecode: %w", err)
	}

	fmt.Printf("Saved contract bytecode to %s\n", bytecodePath)

	finalAbiPath := filepath.Join(contractsDir, fmt.Sprintf("%s.abi.json", strings.ToLower(contractName)))

	if abiPath != "" {
		abiData, err := os.ReadFile(abiPath)
		if err != nil {
			return fmt.Errorf("failed to read provided ABI file: %w", err)
		}

		var abiDataParsed interface{}
		if err := json.Unmarshal(abiData, &abiDataParsed); err != nil {
			return fmt.Errorf("invalid ABI JSON in provided file: %w", err)
		}

		if err := os.WriteFile(finalAbiPath, abiData, 0644); err != nil {
			return fmt.Errorf("failed to save ABI: %w", err)
		}

		fmt.Printf("Saved ABI from provided file to %s\n", finalAbiPath)
	} else {
		fmt.Printf("Creating minimal ABI. Use --abi flag to provide proper ABI file.\n")

		minimalABI := []interface{}{}
		abiBytes, err := json.Marshal(minimalABI)
		if err != nil {
			return fmt.Errorf("failed to marshal minimal ABI: %w", err)
		}

		if err := os.WriteFile(finalAbiPath, abiBytes, 0644); err != nil {
			return fmt.Errorf("failed to save minimal ABI: %w", err)
		}

		fmt.Printf("Saved minimal ABI to %s\n", finalAbiPath)
	}

	deployedContract.AbiPath = finalAbiPath

	if generateBindings {
		if bindingsPath, err := generateGoBindingsFromHex(contractName, finalAbiPath, bytecodePath, contractsDir); err == nil {
			deployedContract.BindingsPath = bindingsPath
			fmt.Printf("Generated Go bindings to %s\n", bindingsPath)
		} else {
			fmt.Printf("Warning: failed to generate Go bindings: %v\n", err)
		}
	}

	if err := manager.saveDeployment(deployedContract); err != nil {
		return fmt.Errorf("failed to save deployment info: %w", err)
	}

	fmt.Printf("Saved deployment information to workspace\n")
	return nil
}

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

func getForgeABI(contractPath, contractName, contractsDir string) (string, error) {
	cmd := exec.Command("forge", "inspect", contractPath, contractName, "abi")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to get ABI with forge inspect: %w, output: %s", err, string(output))
	}

	var abiJSON interface{}
	if err := json.Unmarshal(output, &abiJSON); err != nil {
		return "", fmt.Errorf("invalid ABI JSON from forge inspect: %w", err)
	}

	finalAbiPath := filepath.Join(contractsDir, fmt.Sprintf("%s.abi.json", strings.ToLower(contractName)))
	if err := os.WriteFile(finalAbiPath, output, 0644); err != nil {
		return "", fmt.Errorf("failed to save ABI: %w", err)
	}

	return finalAbiPath, nil
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
			Name:  "clone-config",
			Usage: "Clone repositories listed in config/contracts.json",
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:  "config",
					Usage: "Path to contracts.json",
					Value: "config/contracts.json",
				},
				&cli.StringFlag{
					Name:  "workspace",
					Usage: "Workspace directory to clone repositories into",
					Value: "./workspace",
				},
			},
			Action: func(c *cli.Context) error {
				configPath := c.String("config")
				workspace := c.String("workspace")

				data, err := os.ReadFile(configPath)
				if err != nil {
					return fmt.Errorf("failed to read config file: %w", err)
				}

				var cfg struct {
					Contracts []struct {
						Name            string   `json:"name"`
						ProjectType     string   `json:"project_type"`
						GitURL          string   `json:"git_url"`
						GitRef          string   `json:"git_ref"`
						MainContract    string   `json:"main_contract"`
						ContractPath    string   `json:"contract_path"`
						ConstructorArgs []string `json:"constructor_args"`
					} `json:"contracts"`
				}

				if err := json.Unmarshal(data, &cfg); err != nil {
					return fmt.Errorf("failed to parse config file: %w", err)
				}

				manager := NewContractManager(workspace, "")

				for _, cdef := range cfg.Contracts {
					name := strings.ToLower(cdef.Name)
					name = strings.ReplaceAll(name, " ", "-")
					project := &ContractProject{
						Name:         cdef.Name,
						GitURL:       cdef.GitURL,
						GitRef:       cdef.GitRef,
						ProjectType:  ProjectType(cdef.ProjectType),
						MainContract: cdef.MainContract,
						ContractPath: cdef.ContractPath,
						CloneDir:     filepath.Join(name),
						Env:          make(map[string]string),
					}

					fmt.Printf("Cloning %s into workspace...\n", project.GitURL)
					if err := manager.CloneRepository(project); err != nil {
						fmt.Printf("Warning: failed to clone %s: %v\n", project.GitURL, err)
						continue
					}
					fmt.Printf("Cloned to: %s\n", project.CloneDir)
				}

				return nil
			},
		},
		{
			Name:  "deploy-local",
			Usage: "Deploy contracts from local cloned repositories based on config/contracts.json (for air-gapped environments)",
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:  "config",
					Usage: "Path to contracts.json",
					Value: "config/contracts.json",
				},
				&cli.StringFlag{
					Name:  "workspace",
					Usage: "Workspace directory containing cloned repositories",
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
					Usage: "Private key for deployment (hex format, 0x prefix optional)",
				},
				&cli.BoolFlag{
					Name:  "bindings",
					Usage: "Generate Go bindings using abigen and save to disk",
				},
			},
			Action: deployFromLocal,
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

func deployFromLocal(c *cli.Context) error {
	configPath := c.String("config")
	workspace := c.String("workspace")
	rpcURL := c.String("rpc-url")
	createDeployer := c.Bool("create-deployer")
	deployerKey := c.String("deployer-key")
	generateBindings := c.Bool("bindings")

	contractsConfig, err := config.LoadContractsConfig(configPath)
	if err != nil {
		return fmt.Errorf("failed to load contracts config: %w", err)
	}

	deploymentsPath := filepath.Join(workspace, "deployments.json")
	deployments, err := config.LoadDeploymentRecords(deploymentsPath)
	if err != nil {
		return fmt.Errorf("failed to load deployment records: %w", err)
	}

	orderedContracts, err := config.GetDeploymentOrder(contractsConfig.Contracts)
	if err != nil {
		return fmt.Errorf("failed to determine deployment order: %w", err)
	}

	fmt.Printf("Deployment order: ")
	for i, contract := range orderedContracts {
		if i > 0 {
			fmt.Print(" -> ")
		}
		fmt.Print(contract.Name)
	}
	fmt.Println()

	manager := NewContractManager(workspace, rpcURL)
	if createDeployer {
		fmt.Println("Creating new deployer account...")
		privateKey, address, err := manager.CreateDeployerAccount()
		if err != nil {
			return fmt.Errorf("failed to create deployer account: %w", err)
		}
		fmt.Printf("Created deployer account: %s\n", address.String())
		fmt.Printf("Private key: %s\n", privateKey)
	} else if deployerKey != "" {
		manager.SetDeployerKey(deployerKey)
	} else {
		return fmt.Errorf("either --create-deployer or --deployer-key must be provided")
	}

	for _, cdef := range orderedContracts {
		name := strings.ToLower(cdef.Name)
		name = strings.ReplaceAll(name, " ", "-")
		localCloneDir := filepath.Join(workspace, name)

		absLocalCloneDir, err := filepath.Abs(localCloneDir)
		if err != nil {
			fmt.Printf("Warning: failed to get absolute path for %s: %v, skipping %s\n", localCloneDir, err, cdef.Name)
			continue
		}

		if _, err := os.Stat(absLocalCloneDir); os.IsNotExist(err) {
			fmt.Printf("Warning: local clone directory %s does not exist, skipping %s\n", absLocalCloneDir, cdef.Name)
			continue
		}

		fmt.Printf("====== Deploying %s from local clone ======\n", cdef.Name)

		deployments, err = config.LoadDeploymentRecords(deploymentsPath)
		if err != nil {
			return fmt.Errorf("failed to reload deployment records before resolving dependencies for %s: %w", cdef.Name, err)
		}

		resolvedArgs, err := config.ResolveDependencies(cdef, deployments)
		if err != nil {
			return fmt.Errorf("failed to resolve dependencies for %s: %w", cdef.Name, err)
		}

		if len(resolvedArgs) > 0 {
			fmt.Printf("Constructor args: %v\n", resolvedArgs)
		}

		project := &ContractProject{
			Name:         cdef.Name,
			GitURL:       cdef.GitURL,
			GitRef:       cdef.GitRef,
			ProjectType:  ProjectType(cdef.ProjectType),
			MainContract: cdef.MainContract,
			ContractPath: cdef.ContractPath,
			CloneDir:     absLocalCloneDir,
			Env:          make(map[string]string),
		}

		contractPath := fmt.Sprintf("%s:%s", project.ContractPath, project.MainContract)
		deployedContract, err := manager.DeployContract(project, contractPath, resolvedArgs, generateBindings, false)

		if err != nil {
			fmt.Printf("Error: failed to deploy contract %s: %v\n", cdef.Name, err)
			continue
		}

		deployments, err = config.LoadDeploymentRecords(deploymentsPath)
		if err != nil {
			return fmt.Errorf("failed to reload deployment records: %w", err)
		}

		fmt.Printf("\nContract %s deployed successfully!\n", cdef.Name)
		fmt.Printf("Contract: %s\n", deployedContract.Name)
		fmt.Printf("Address: %s\n", deployedContract.Address.String())
		fmt.Printf("Transaction: %s\n", deployedContract.TransactionHash.String())
		fmt.Printf("Deployer: %s\n", deployedContract.DeployerAddress.String())
		if deployedContract.AbiPath != "" {
			fmt.Printf("ABI Path: %s\n", deployedContract.AbiPath)
		}
		if deployedContract.BindingsPath != "" {
			fmt.Printf("Go Bindings: %s\n", deployedContract.BindingsPath)
		}
		fmt.Printf("====== Finished %s ======\n\n", cdef.Name)

		if err := config.ExecutePostDeployment(cdef, deployedContract.Address.String(), convertToDeploymentRecords(deployments), rpcURL, manager.GetDeployerKey()); err != nil {
			fmt.Printf("Warning: Post-deployment actions failed for %s: %v\n", cdef.Name, err)
		}

		time.Sleep(5 * time.Second)
	}

	fmt.Println("All deployments completed. Check deployments with: ./mpool-tx contract list")
	return nil
}

func deployFromGit(c *cli.Context) error {
	if deployScript := c.String("deploy-script"); deployScript != "" {
		return deployWithCustomScript(c)
	}

	if commands := c.String("commands"); commands != "" {
		return deployWithShellCommands(c)
	}

	if c.String("main-contract") == "" {
		return fmt.Errorf("main-contract is required for deployment")
	}

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

	projectType := ProjectType(c.String("project-type"))
	project := &ContractProject{
		Name:         c.String("main-contract"),
		GitURL:       c.String("git-url"),
		GitRef:       c.String("git-ref"),
		ProjectType:  projectType,
		MainContract: c.String("main-contract"),
		CloneDir:     "",
		Env:          make(map[string]string),
	}

	if contractPath := c.String("contract-path"); contractPath != "" {
		project.ContractPath = contractPath
	}

	envVars := c.StringSlice("env")
	for _, envVar := range envVars {
		parts := strings.SplitN(envVar, "=", 2)
		if len(parts) == 2 {
			project.Env[parts[0]] = parts[1]
		}
	}

	fmt.Printf("Cloning repository: %s\n", project.GitURL)
	if err := manager.CloneRepository(project); err != nil {
		return fmt.Errorf("failed to clone repository: %w", err)
	}
	fmt.Printf("Repository cloned to: %s\n", project.CloneDir)
	if project.ProjectType == ProjectTypeHardhat {
		fmt.Printf("Hardhat project detected - compiling first...\n")
		if err := manager.CompileHardhatProject(project); err != nil {
			return fmt.Errorf("failed to compile Hardhat project: %w", err)
		}
		fmt.Printf("Hardhat compilation completed\n")
	} else {
		fmt.Printf("Foundry project - deploying directly with forge create...\n")
	}

	var constructorArgs []string
	if argsStr := c.String("constructor-args"); argsStr != "" {
		constructorArgs = strings.Split(argsStr, ",")
		for i, arg := range constructorArgs {
			constructorArgs[i] = strings.TrimSpace(arg)
		}
	}

	fmt.Printf("Deploying contract: %s\n", project.MainContract)
	if len(constructorArgs) > 0 {
		fmt.Printf("Constructor args: %v\n", constructorArgs)
	}

	if deployScript := c.String("deploy-script"); deployScript != "" {
		fmt.Printf("Running custom deployment script: %s\n", deployScript)
		if err := manager.RunCustomDeployScript(project, deployScript); err != nil {
			return fmt.Errorf("failed to run deployment script: %w", err)
		}
		fmt.Printf("Custom deployment script completed successfully\n")
		return nil
	}

	contractPath := fmt.Sprintf("%s:%s", project.ContractPath, project.MainContract)
	generateBindings := c.Bool("bindings")
	deployedContract, err := manager.DeployContract(project, contractPath, constructorArgs, generateBindings, true)

	if err != nil {
		return fmt.Errorf("failed to deploy contract: %w", err)
	}

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
		fmt.Printf("   Go binding generation: %v\n", deployment.BindingsPath != "")
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

func deployWithCustomScript(c *cli.Context) error {
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

	wrapper, err := config.NewContractWrapper(rpcURL, contractAddress)
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
	privateKeyStr = strings.TrimPrefix(privateKeyStr, "0x")

	privateKeyBytes, err := hex.DecodeString(privateKeyStr)
	if err != nil {
		return nil, fmt.Errorf("invalid hex format: %w", err)
	}

	// Validate key length (32 bytes for secp256k1)
	if len(privateKeyBytes) != 32 {
		return nil, fmt.Errorf("invalid private key length: got %d bytes, want 32 bytes (secp256k1)", len(privateKeyBytes))
	}
	// Create private key
	privateKey, err := crypto.ToECDSA(privateKeyBytes)
	if err != nil {
		return nil, fmt.Errorf("invalid private key: %w", err)
	}

	return privateKey, nil
}

func convertToDeploymentRecords(deployments []config.DeploymentRecord) []config.DeploymentRecord {
	return deployments
}
