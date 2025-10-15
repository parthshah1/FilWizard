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
	"regexp"
	"strconv"
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

func waitForTransactionReceipt(ctx context.Context, api api.FullNode, txHash ethtypes.EthHash) (*api.EthTxReceipt, error) {
	for i := 0; i < 60; i++ {
		receipt, err := api.EthGetTransactionReceipt(ctx, txHash)
		if err == nil && receipt != nil {
			if receipt.Status == 1 {
				fmt.Printf("Transaction confirmed: %s\n", txHash.String())
				return receipt, nil
			} else {
				return nil, fmt.Errorf("transaction failed: %s", txHash.String())
			}
		}

		fmt.Printf("Waiting for transaction confirmation... %s\n", txHash.String())
		time.Sleep(1 * time.Second)
	}

	return nil, fmt.Errorf("transaction not confirmed after waiting")
}

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
	receipt, err := waitForTransactionReceipt(ctx, api, txHash)
	if err != nil {
		return fmt.Errorf("failed to wait for transaction receipt: %w", err)
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

	if abiPath == "" {
		possiblePaths := []string{
			fmt.Sprintf("contracts/%s.abi", contractName),
			fmt.Sprintf("contracts/%s.abi.json", contractName),
			fmt.Sprintf("contracts/%s.abi", strings.ToLower(contractName)),
			fmt.Sprintf("contracts/%s.abi.json", strings.ToLower(contractName)),
		}

		for _, path := range possiblePaths {
			if _, err := os.Stat(path); err == nil {
				abiPath = path
				fmt.Printf("Auto-detected ABI file: %s\n", abiPath)
				break
			}
		}

		if abiPath == "" {
			fmt.Printf("No pre-compiled ABI found, attempting to generate from source...\n")

			possibleSources := []string{
				fmt.Sprintf("contracts/%s.sol", contractName),
				fmt.Sprintf("contracts/%s.sol", strings.ToLower(contractName)),
			}

			var solPath string
			for _, path := range possibleSources {
				if _, err := os.Stat(path); err == nil {
					solPath = path
					fmt.Printf("Found Solidity source: %s\n", solPath)
					break
				}
			}

			if solPath != "" {
				tempAbiPath := fmt.Sprintf("contracts/%s.abi", contractName)
				if generatedAbi, err := generateABIFromSolidity(solPath, contractName, tempAbiPath); err == nil {
					abiPath = generatedAbi
					fmt.Printf("Generated ABI from Solidity source: %s\n", abiPath)
				} else {
					fmt.Printf("Warning: Failed to generate ABI from Solidity: %v\n", err)
				}
			} else {
				fmt.Printf("WARNING: No Solidity source found in contracts/ directory\n")
			}
		}
	}

	if abiPath != "" {
		abiData, err := os.ReadFile(abiPath)
		if err != nil {
			return fmt.Errorf("failed to read ABI file: %w", err)
		}

		var abiDataParsed interface{}
		if err := json.Unmarshal(abiData, &abiDataParsed); err != nil {
			return fmt.Errorf("invalid ABI JSON: %w", err)
		}

		if err := os.WriteFile(finalAbiPath, abiData, 0644); err != nil {
			return fmt.Errorf("failed to save ABI: %w", err)
		}

		fmt.Printf("Saved ABI to %s\n", finalAbiPath)
	} else {
		fmt.Printf("WARNING: Could not find or generate ABI\n")
		fmt.Printf("Creating empty ABI - Go bindings will NOT have contract methods\n")
		fmt.Printf("To fix: Place Solidity source at contracts/%s.sol\n", contractName)

		minimalABI := []interface{}{}
		abiBytes, err := json.Marshal(minimalABI)
		if err != nil {
			return fmt.Errorf("failed to marshal minimal ABI: %w", err)
		}

		if err := os.WriteFile(finalAbiPath, abiBytes, 0644); err != nil {
			return fmt.Errorf("failed to save minimal ABI: %w", err)
		}

		fmt.Printf("Saved empty ABI to %s\n", finalAbiPath)
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

	fmt.Printf("Saved deployment information to workspace/deployments.json\n")

	if err := manager.saveDeployerAccount(deployedContract); err != nil {
		fmt.Printf("Warning: failed to save deployer account: %v\n", err)
	}

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

func generateABIFromSolidity(solPath, contractName, outputPath string) (string, error) {
	cmd := exec.Command("solc", "--abi", solPath, "-o", "contracts/", "--overwrite")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("solc compilation failed: %w, output: %s", err, output)
	}

	possibleAbiFiles := []string{
		fmt.Sprintf("contracts/%s.abi", contractName),
	}

	solContent, err := os.ReadFile(solPath)
	if err == nil {
		re := regexp.MustCompile(`contract\s+(\w+)`)
		matches := re.FindStringSubmatch(string(solContent))
		if len(matches) > 1 {
			actualContractName := matches[1]
			possibleAbiFiles = append(possibleAbiFiles, fmt.Sprintf("contracts/%s.abi", actualContractName))
		}
	}

	var generatedAbi string
	for _, path := range possibleAbiFiles {
		if _, err := os.Stat(path); err == nil {
			generatedAbi = path
			break
		}
	}

	if generatedAbi == "" {
		return "", fmt.Errorf("solc succeeded but ABI file not found")
	}

	return generatedAbi, nil
}

func compileWithSolc(contractPath string) error {
	if _, err := exec.LookPath("solc"); err != nil {
		return fmt.Errorf("solc not found in PATH")
	}

	fmt.Printf("Compiling %s with solc...\n", contractPath)

	cmd := exec.Command("solc", "--bin", "--abi", "--optimize", contractPath, "-o", "contracts/", "--overwrite")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("solc compilation failed: %w", err)
	}

	fmt.Println("Compilation successful")
	return nil
}

func compileWithForge() error {
	if _, err := exec.LookPath("forge"); err != nil {
		return fmt.Errorf("forge not found in PATH")
	}

	fmt.Println("Compiling contracts with forge...")

	cmd := exec.Command("forge", "build")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("forge build failed: %w", err)
	}

	fmt.Println("Compilation successful")
	return nil
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
				&cli.BoolFlag{
					Name:  "compile",
					Usage: "Compile contract before deployment using solc",
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
				shouldCompile := c.Bool("compile")
				workspace := c.String("workspace")
				contractName := c.String("contract-name")
				abiPath := c.String("abi")

				if shouldCompile {
					if err := compileWithSolc(contractFile); err != nil {
						return fmt.Errorf("compilation failed: %w", err)
					}
				}

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
						CloneCommands   []string `json:"clone_commands,omitempty"`
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
						Name:          cdef.Name,
						GitURL:        cdef.GitURL,
						GitRef:        cdef.GitRef,
						ProjectType:   ProjectType(cdef.ProjectType),
						MainContract:  cdef.MainContract,
						ContractPath:  cdef.ContractPath,
						CloneDir:      filepath.Join(name),
						Env:           make(map[string]string),
						CloneCommands: cdef.CloneCommands,
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
				&cli.BoolFlag{
					Name:  "compile",
					Usage: "Compile contracts with forge before deployment",
				},
				&cli.StringFlag{
					Name:  "import-output",
					Usage: "Path to file containing custom deployment script output to import addresses from",
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
			Name:  "call",
			Usage: "Universal contract interaction with automatic type detection",
			Subcommands: []*cli.Command{
				{
					Name:      "read",
					Usage:     "Call a read-only contract method (view/pure)",
					ArgsUsage: "<contract-name> <method-name> [args...]",
					Action:    callReadMethod,
				},
				{
					Name:      "write",
					Usage:     "Send a transaction to a contract method",
					ArgsUsage: "<contract-name> <method-name> [args...]",
					Flags: []cli.Flag{
						&cli.StringFlag{
							Name:  "from",
							Usage: "Account role to send transaction from (creates new if doesn't exist)",
						},
						&cli.Uint64Flag{
							Name:  "gas",
							Value: 0,
							Usage: "Gas limit (0 = auto-estimate)",
						},
						&cli.StringFlag{
							Name:  "fund",
							Value: "1",
							Usage: "Amount to fund new accounts (FIL)",
						},
					},
					Action: callWriteMethod,
				},
			},
		},
	},
}

func deployFromLocal(c *cli.Context) error {
	configPath := c.String("config")
	workspace := c.String("workspace")
	rpcURL := c.String("rpc-url")
	generateBindings := c.Bool("bindings")
	shouldCompile := c.Bool("compile")
	importOutput := c.String("import-output")

	if shouldCompile {
		if err := compileWithForge(); err != nil {
			return fmt.Errorf("compilation failed: %w", err)
		}
	}

	contractsConfig, err := config.LoadContractsConfig(configPath)
	if err != nil {
		return fmt.Errorf("failed to load contracts config: %w", err)
	}

	deploymentsPath := filepath.Join(workspace, "deployments.json")
	deployments, err := config.LoadDeploymentRecords(deploymentsPath)
	if err != nil {
		return fmt.Errorf("failed to load deployment records: %w", err)
	}

	// If user supplied an import-output file, import addresses into deployments.json
	if importOutput != "" {
		managerForImport := NewContractManager(workspace, rpcURL)
		fmt.Printf("Importing script output from %s into %s...\n", importOutput, deploymentsPath)
		if err := managerForImport.ImportScriptOutputToDeployments(configPath, deploymentsPath, importOutput); err != nil {
			return fmt.Errorf("failed to import script output: %w", err)
		}
		// reload deployments after import
		deployments, err = config.LoadDeploymentRecords(deploymentsPath)
		if err != nil {
			return fmt.Errorf("failed to reload deployment records after import: %w", err)
		}
		fmt.Println("Import completed and deployments reloaded.")
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

	// Try to load existing deployer account from accounts.json
	var deployerKey string
	if accounts, err := loadAccounts(workspace); err == nil {
		if deployerAccount, exists := accounts.Accounts["deployer"]; exists {
			deployerKey = deployerAccount.PrivateKey
			fmt.Printf("Using existing deployer account: %s\n", deployerAccount.EthAddress)
		}
	}

	if deployerKey != "" {
		manager.SetDeployerKey(deployerKey)
	} else {
		fmt.Println("Creating new deployer account...")
		privateKey, address, err := manager.CreateDeployerAccount()
		if err != nil {
			return fmt.Errorf("failed to create deployer account: %w", err)
		}
		fmt.Printf("Created deployer account: %s\n", address.String())
		fmt.Printf("Private key: %s\n", privateKey)
	}

	// Set PRIVATE_KEY environment variable for deployment scripts
	os.Setenv("PRIVATE_KEY", manager.GetDeployerKey())

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

		// Set and resolve environment variables for this contract deployment
		fmt.Printf("Setting environment variables for %s...\n", cdef.Name)

		// Start with contract-configured env values
		envVars := contractsConfig.GetEnvironmentForContract(cdef.Name)

		// Resolve placeholders using already-loaded deployments (config.DeploymentRecord)
		for k, v := range envVars {
			if strings.Contains(v, "{address:") {
				resolved := contractsConfig.ResolveAddressPlaceholdersWithDeployments(v, deployments)
				// If still unresolved, fall back to reading workspace/deployments.json which may contain
				// the manager's DeployedContract format
				if strings.Contains(resolved, "{address:") {
					depsFile := filepath.Join(workspace, "deployments.json")
					if data, err := os.ReadFile(depsFile); err == nil {
						var mgrDeps []*DeployedContract
						if err := json.Unmarshal(data, &mgrDeps); err == nil {
							// try to find each placeholder and replace
							for {
								start := strings.Index(resolved, "{address:")
								if start == -1 {
									break
								}
								end := strings.Index(resolved[start:], "}")
								if end == -1 {
									break
								}
								end += start
								placeholder := resolved[start : end+1]
								name := placeholder[9 : len(placeholder)-1]
								// lookup in mgrDeps
								var found string
								for _, md := range mgrDeps {
									if strings.EqualFold(md.Name, name) {
										if md.Address.String() != "" {
											found = md.Address.String()
											break
										}
									}
								}
								if found != "" {
									resolved = strings.Replace(resolved, placeholder, found, 1)
								} else {
									// leave unresolved and break
									break
								}
							}
						}
					}
				}
				envVars[k] = resolved
			}
		}

		// Export resolved vars to the process environment and print them
		if len(envVars) > 0 {
			fmt.Printf("Environment variables:\n")
			for key, value := range envVars {
				// export so scripts see them
				os.Setenv(key, value)
				// Don't print sensitive values except for testing we'll show them
				if strings.Contains(strings.ToLower(key), "secret") {
					fmt.Printf("  %s=***\n", key)
				} else {
					fmt.Printf("  %s=%s\n", key, value)
				}
			}
		}

		// Also export and show PRIVATE_KEY if it's set
		if manager.GetDeployerKey() != "" {
			os.Setenv("PRIVATE_KEY", manager.GetDeployerKey())
			fmt.Printf("  PRIVATE_KEY=%s\n", manager.GetDeployerKey())
		}

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
			Name:          cdef.Name,
			GitURL:        cdef.GitURL,
			GitRef:        cdef.GitRef,
			ProjectType:   ProjectType(cdef.ProjectType),
			MainContract:  cdef.MainContract,
			ContractPath:  cdef.ContractPath,
			CloneDir:      absLocalCloneDir,
			ScriptDir:     cdef.ScriptDir,
			Env:           envVars,
			CloneCommands: cdef.CloneCommands,
		}

		var deployedContract *DeployedContract
		var scriptOutput string

		if cdef.DeployScript != "" {
			// Ensure clone commands are executed (e.g., git submodule init)
			if err := manager.EnsureCloneCommandsExecuted(project); err != nil {
				fmt.Printf("Warning: failed to ensure clone commands for %s: %v\n", cdef.Name, err)
			}

			fmt.Printf("Running custom deployment script: %s\n", cdef.DeployScript)
			var err error
			scriptOutput, err = manager.RunCustomDeployScript(project, cdef.DeployScript)
			scriptFailed := err != nil
			if scriptFailed {
				fmt.Printf("Warning: deployment script for %s exited with error: %v\n", cdef.Name, err)
				fmt.Printf("Attempting to import any contract addresses that were successfully deployed...\n")
			} else {
				fmt.Printf("Custom deployment script completed successfully\n")
			}

			// Import addresses from script output even if script failed
			// (scripts may fail on final steps but still deploy successfully)
			if scriptOutput != "" {
				// Write script output to a temporary file for importing
				tempFile, err := os.CreateTemp("", "script_output_*.txt")
				if err != nil {
					fmt.Printf("Error: failed to create temp file for script output: %v\n", err)
					if scriptFailed {
						continue
					}
				} else {
					defer os.Remove(tempFile.Name())
					defer tempFile.Close()

					if _, err := tempFile.WriteString(scriptOutput); err != nil {
						fmt.Printf("Error: failed to write script output to temp file: %v\n", err)
						if scriptFailed {
							continue
						}
					} else {
						tempFile.Close()

						// Import addresses from script output
						fmt.Printf("Importing contract addresses from script output...\n")
						if err := manager.ImportScriptOutputToDeployments(configPath, deploymentsPath, tempFile.Name()); err != nil {
							fmt.Printf("Error: failed to import script output: %v\n", err)
							if scriptFailed {
								continue
							}
						} else {
							fmt.Printf("Successfully imported contract addresses\n")
						}
					}
				}
			}

			if scriptFailed {
				continue
			}

			// Reload deployments to get the imported contract
			deploymentsFromManager, err := manager.LoadDeployments()
			if err != nil {
				fmt.Printf("Error: failed to reload deployments after script import: %v\n", err)
				continue
			}

			// Find the deployed contract in the updated deployments
			for _, d := range deploymentsFromManager {
				if strings.EqualFold(d.Name, cdef.Name) {
					deployedContract = d
					break
				}
			}
			if deployedContract == nil {
				fmt.Printf("Warning: contract %s not found in deployments after script execution\n", cdef.Name)
				// Create a dummy deployed contract for post-deployment steps
				deployedContract = &DeployedContract{
					Name: cdef.Name,
				}
			}
		} else {
			contractPath := fmt.Sprintf("%s:%s", project.ContractPath, project.MainContract)
			var err error
			deployedContract, err = manager.DeployContract(project, contractPath, resolvedArgs, generateBindings, false)

			if err != nil {
				fmt.Printf("Error: failed to deploy contract %s: %v\n", cdef.Name, err)
				continue
			}

			deployments, err = config.LoadDeploymentRecords(deploymentsPath)
			if err != nil {
				return fmt.Errorf("failed to reload deployment records: %w", err)
			}
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

		// Update environment variables with newly deployed contract addresses
		deployments, err = config.LoadDeploymentRecords(deploymentsPath)
		if err != nil {
			fmt.Printf("Warning: failed to reload deployments for environment update: %v\n", err)
		} else {
			fmt.Printf("Updating environment variables with new contract addresses...\n")
			contractsConfig.UpdateEnvironmentWithDeployments(cdef.Name, deployments)
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
		if _, err := manager.RunCustomDeployScript(project, deployScript); err != nil {
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
		CloneDir: "",
		Env:      make(map[string]string),
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

	deployScript := c.String("deploy-script")
	fmt.Printf("Running custom deployment script: %s\n", deployScript)
	if _, err := manager.RunCustomDeployScript(project, deployScript); err != nil {
		return fmt.Errorf("failed to run deployment script: %w", err)
	}
	fmt.Printf("Custom deployment script completed successfully\n")
	return nil
}

func deployWithShellCommands(c *cli.Context) error {
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
		CloneDir: "",
		Env:      make(map[string]string),
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

	commands := c.String("commands")
	fmt.Printf("Running shell commands: %s\n", commands)
	if err := manager.RunShellCommands(project, commands); err != nil {
		return fmt.Errorf("failed to run shell commands: %w", err)
	}
	fmt.Printf("Shell commands completed successfully\n")
	return nil
}

func callReadMethod(c *cli.Context) error {
	if c.NArg() < 2 {
		return fmt.Errorf("usage: contract call read <contract-name> <method-name> [args...]")
	}

	workspace := "./workspace"
	contractName := c.Args().Get(0)
	methodName := c.Args().Get(1)

	methodArgs := []string{}
	for i := 2; i < c.NArg(); i++ {
		arg := c.Args().Get(i)
		methodArgs = append(methodArgs, arg)
	}

	deployments, err := loadDeployments(workspace)
	if err != nil {
		return err
	}

	var contractAddr string
	for _, d := range deployments {
		if strings.EqualFold(d.Name, contractName) {
			contractAddr = d.Address
			break
		}
	}
	if contractAddr == "" {
		return fmt.Errorf("contract '%s' not found in deployments", contractName)
	}

	cfg, err := loadWorkspaceConfig()
	if err != nil {
		return err
	}

	wrapper, err := config.NewContractWrapper(cfg.RPC, contractAddr)
	if err != nil {
		return fmt.Errorf("failed to create contract wrapper: %w", err)
	}
	defer wrapper.Close()

	args, err := parseArguments(methodArgs)
	if err != nil {
		return fmt.Errorf("failed to parse arguments: %w", err)
	}

	fmt.Printf("Calling %s.%s(%v)\n", contractName, methodName, formatArgs(args))

	result, err := wrapper.CallMethod(methodName, args)
	if err != nil {
		return fmt.Errorf("call failed: %w", err)
	}

	fmt.Printf("Contract: %s (%s)\n", contractName, contractAddr)
	fmt.Printf("Method: %s\n", methodName)
	fmt.Printf("Result (hex): 0x%x\n", result)
	fmt.Printf("Result (uint256): %s\n", new(big.Int).SetBytes(result).String())

	return nil
}

func callWriteMethod(c *cli.Context) error {
	if c.NArg() < 2 {
		return fmt.Errorf("usage: contract call write <contract-name> <method-name> [args...]")
	}

	ctx := context.Background()
	workspace := "./workspace"

	allArgs := c.Args().Slice()
	var contractName, methodName, fromRole string
	var methodArgs []string
	gasLimit := c.Uint64("gas")
	fundAmount := "1"

	parsedFlags := make(map[string]string)
	i := 0
	for i < len(allArgs) {
		arg := allArgs[i]

		if arg == "--from" && i+1 < len(allArgs) {
			parsedFlags["from"] = allArgs[i+1]
			i += 2
			continue
		}
		if arg == "--fund" && i+1 < len(allArgs) {
			parsedFlags["fund"] = allArgs[i+1]
			i += 2
			continue
		}
		if arg == "--gas" && i+1 < len(allArgs) {
			val := allArgs[i+1]
			if gasVal, err := strconv.ParseUint(val, 10, 64); err == nil {
				gasLimit = gasVal
			}
			i += 2
			continue
		}

		if contractName == "" {
			contractName = arg
		} else if methodName == "" {
			methodName = arg
		} else {
			methodArgs = append(methodArgs, arg)
		}
		i++
	}

	if contractName == "" || methodName == "" {
		return fmt.Errorf("usage: contract call write <contract-name> <method-name> [args...] [--from <role>] [--fund <amount>] [--gas <limit>]")
	}

	fromRole = parsedFlags["from"]
	if val, ok := parsedFlags["fund"]; ok {
		fundAmount = val
	}

	deployments, err := loadDeployments(workspace)
	if err != nil {
		return err
	}

	accounts, err := loadAccounts(workspace)
	if err != nil {
		accounts = &AccountsFile{Accounts: make(map[string]AccountInfo)}
	}

	var contractAddr string
	for _, d := range deployments {
		if strings.EqualFold(d.Name, contractName) {
			contractAddr = d.Address
			break
		}
	}
	if contractAddr == "" {
		return fmt.Errorf("contract '%s' not found in deployments", contractName)
	}

	var fromAccount AccountInfo
	var needsCreation bool

	if fromRole == "" {
		fromRole = "caller"
		needsCreation = true
	} else {
		var ok bool
		fromAccount, ok = accounts.Accounts[fromRole]
		if !ok {
			needsCreation = true
		}
	}

	if needsCreation {
		fmt.Printf("Creating new account '%s'...\n", fromRole)

		key, ethAddr, filAddr := NewAccount()
		if key == nil {
			return fmt.Errorf("failed to create account")
		}

		privateKeyHex := fmt.Sprintf("0x%x", key.PrivateKey)

		fromAccount = AccountInfo{
			Address:    filAddr.String(),
			EthAddress: ethAddr.String(),
			PrivateKey: privateKeyHex,
		}

		amount, _ := filbig.FromString(fundAmount)
		fundAmountAtto := types.BigMul(amount, types.NewInt(1e18))

		fmt.Printf("Funding %s with %s FIL...\n", fromRole, fundAmount)
		_, err := FundWallet(ctx, filAddr, fundAmountAtto, true)
		if err != nil {
			return fmt.Errorf("failed to fund account: %w", err)
		}

		accounts.Accounts[fromRole] = fromAccount

		accountsPath := filepath.Join(workspace, "accounts.json")
		accountsData, err := json.MarshalIndent(accounts, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal accounts: %w", err)
		}

		if err := os.WriteFile(accountsPath, accountsData, 0644); err != nil {
			return fmt.Errorf("failed to save accounts: %w", err)
		}

		fmt.Printf("Account '%s' created and saved: %s\n", fromRole, ethAddr.String())

		fmt.Println("Waiting for funds to be available...")
		time.Sleep(5 * time.Second)
	}

	cfg, err := loadWorkspaceConfig()
	if err != nil {
		return err
	}

	wrapper, err := config.NewContractWrapper(cfg.RPC, contractAddr)
	if err != nil {
		return fmt.Errorf("failed to create contract wrapper: %w", err)
	}
	defer wrapper.Close()

	args, err := parseArguments(methodArgs)
	if err != nil {
		return fmt.Errorf("failed to parse arguments: %w", err)
	}

	privateKey, err := crypto.HexToECDSA(fromAccount.PrivateKey[2:])
	if err != nil {
		return fmt.Errorf("invalid private key: %w", err)
	}

	fmt.Printf("Sending transaction to %s.%s(%v)\n", contractName, methodName, formatArgs(args))
	fmt.Printf("From: %s (%s)\n", fromRole, fromAccount.EthAddress)

	tx, err := wrapper.SendTransaction(methodName, args, privateKey, gasLimit)
	if err != nil {
		return fmt.Errorf("transaction failed: %w", err)
	}

	fmt.Printf("Transaction successful: %s\n", tx.Hash().Hex())

	return nil
}

func parseArguments(args []string) ([]interface{}, error) {
	parsed := make([]interface{}, len(args))

	for i, arg := range args {
		if strings.HasPrefix(arg, "0x") && len(arg) == 42 {
			parsed[i] = common.HexToAddress(arg)
		} else if arg == "true" || arg == "false" {
			parsed[i] = arg == "true"
		} else if val, ok := new(big.Int).SetString(arg, 10); ok {
			parsed[i] = val
		} else {
			parsed[i] = arg
		}
	}

	return parsed, nil
}

func formatArgs(args []interface{}) string {
	if len(args) == 0 {
		return ""
	}

	formatted := make([]string, len(args))
	for i, arg := range args {
		switch v := arg.(type) {
		case common.Address:
			formatted[i] = v.Hex()
		case *big.Int:
			formatted[i] = v.String()
		case bool:
			formatted[i] = fmt.Sprintf("%v", v)
		case string:
			formatted[i] = fmt.Sprintf(`"%s"`, v)
		default:
			formatted[i] = fmt.Sprintf("%v", v)
		}
	}

	return strings.Join(formatted, ", ")
}

func parsePrivateKey(privateKeyStr string) (*ecdsa.PrivateKey, error) {
	privateKeyStr = strings.TrimPrefix(privateKeyStr, "0x")

	privateKeyBytes, err := hex.DecodeString(privateKeyStr)
	if err != nil {
		return nil, fmt.Errorf("invalid hex format: %w", err)
	}

	if len(privateKeyBytes) != 32 {
		return nil, fmt.Errorf("invalid private key length: got %d bytes, want 32 bytes (secp256k1)", len(privateKeyBytes))
	}
	privateKey, err := crypto.ToECDSA(privateKeyBytes)
	if err != nil {
		return nil, fmt.Errorf("invalid private key: %w", err)
	}

	return privateKey, nil
}

func convertToDeploymentRecords(deployments []config.DeploymentRecord) []config.DeploymentRecord {
	return deployments
}
