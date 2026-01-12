package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	filbig "github.com/filecoin-project/go-state-types/big"
	lotustypes "github.com/filecoin-project/lotus/chain/types"
	"github.com/filecoin-project/lotus/chain/types/ethtypes"
	"github.com/urfave/cli/v2"
)

var PaymentsCmd = &cli.Command{
	Name:  "payments",
	Usage: "Payments contract operations",
	Subcommands: []*cli.Command{
		{
			Name:  "info",
			Usage: "Show deployed contract addresses",
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:     "workspace",
					Usage:    "Workspace directory",
					Required: true,
				},
				&cli.StringSliceFlag{
					Name:  "contracts",
					Usage: "Contract names to show (default: all)",
				},
			},
			Action: showInfo,
		},
		{
			Name:  "mint",
			Usage: "Mint tokens to account",
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:     "workspace",
					Usage:    "Workspace directory",
					Required: true,
				},
				&cli.StringFlag{
					Name:     "token",
					Usage:    "Token contract name",
					Required: true,
				},
				&cli.StringFlag{
					Name:     "to",
					Usage:    "Recipient role name",
					Required: true,
				},
				&cli.StringFlag{
					Name:     "amount",
					Usage:    "Amount in wei",
					Required: true,
				},
				&cli.StringFlag{
					Name:     "minter",
					Usage:    "Minter role name (must be token owner)",
					Required: true,
				},
			},
			Action: mintTokens,
		},
		{
			Name:  "mint-private-key",
			Usage: "Mint tokens and optionally fund FIL for a raw private key",
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:     "workspace",
					Usage:    "Workspace directory",
					Required: true,
				},
				&cli.StringFlag{
					Name:  "token",
					Usage: "Token contract name",
					Value: "USDFC",
				},
				&cli.StringFlag{
					Name:     "private-key",
					Usage:    "Recipient private key (hex)",
					Required: true,
				},
				&cli.StringFlag{
					Name:     "amount",
					Usage:    "Token amount in wei",
					Required: true,
				},
				&cli.StringFlag{
					Name:  "fil",
					Usage: "Optional FIL amount to send to the derived Filecoin address",
					Value: "0",
				},
				&cli.StringFlag{
					Name:  "minter-private-key",
					Usage: "Override minter private key (defaults to token deployer)",
				},
			},
			Action: mintAndFundPrivateKey,
		},
		{
			Name:  "approve",
			Usage: "Approve spender for tokens",
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:     "workspace",
					Usage:    "Workspace directory",
					Required: true,
				},
				&cli.StringFlag{
					Name:     "token",
					Usage:    "Token contract name",
					Required: true,
				},
				&cli.StringFlag{
					Name:     "spender",
					Usage:    "Spender contract name",
					Required: true,
				},
				&cli.StringFlag{
					Name:     "amount",
					Usage:    "Amount in wei",
					Required: true,
				},
				&cli.StringFlag{
					Name:     "from",
					Usage:    "From role name",
					Required: true,
				},
			},
			Action: approveTokens,
		},
		{
			Name:  "deposit",
			Usage: "Deposit tokens into Payments contract",
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:     "workspace",
					Usage:    "Workspace directory",
					Required: true,
				},
				&cli.StringFlag{
					Name:     "token",
					Usage:    "Token contract name",
					Required: true,
				},
				&cli.StringFlag{
					Name:     "amount",
					Usage:    "Amount in wei",
					Required: true,
				},
				&cli.StringFlag{
					Name:     "from",
					Usage:    "From role name",
					Required: true,
				},
			},
			Action: depositTokens,
		},
		{
			Name:  "approve-operator",
			Usage: "Approve operator for payments",
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:     "workspace",
					Usage:    "Workspace directory",
					Required: true,
				},
				&cli.StringFlag{
					Name:     "token",
					Usage:    "Token contract name",
					Required: true,
				},
				&cli.StringFlag{
					Name:     "operator",
					Usage:    "Operator address (e.g., WarmStorage contract)",
					Required: true,
				},
				&cli.StringFlag{
					Name:     "rate-allowance",
					Usage:    "Rate allowance in wei per epoch",
					Required: true,
				},
				&cli.StringFlag{
					Name:     "lockup-allowance",
					Usage:    "Lockup allowance in wei",
					Required: true,
				},
				&cli.StringFlag{
					Name:     "max-lockup-period",
					Usage:    "Max lockup period in epochs",
					Required: true,
				},
				&cli.StringFlag{
					Name:     "from",
					Usage:    "From role name",
					Required: true,
				},
			},
			Action: approveOperator,
		},
		{
			Name:  "balance",
			Usage: "Check balance (token balance or Payments contract balance)",
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:  "workspace",
					Value: "./workspace",
					Usage: "Workspace directory",
				},
				&cli.StringFlag{
					Name:     "account",
					Usage:    "Account role to check",
					Required: true,
				},
				&cli.StringFlag{
					Name:     "contract",
					Usage:    "Contract name (e.g., USDFC for token balance, Payments for deposited balance)",
					Required: true,
				},
			},
			Action: checkBalance,
		},
	},
}

func showInfo(c *cli.Context) error {
	workspace := c.String("workspace")
	filter := c.StringSlice("contracts")

	deployments, err := loadDeployments(workspace)
	if err != nil {
		return err
	}

	filterMap := make(map[string]bool)
	for _, name := range filter {
		filterMap[name] = true
	}

	for _, d := range deployments {
		if len(filterMap) == 0 || filterMap[d.Name] {
			fmt.Printf("%s: %s\n", d.Name, d.Address)
		}
	}

	return nil
}

func mintTokens(c *cli.Context) error {
	workspace := c.String("workspace")
	tokenName := c.String("token")
	toRole := c.String("to")
	amountStr := c.String("amount")
	minterRole := c.String("minter")

	deployments, err := loadDeployments(workspace)
	if err != nil {
		return err
	}

	accounts, err := loadAccounts(workspace)
	if err != nil {
		return err
	}

	tokenRecord, err := findContract(deployments, tokenName)
	if err != nil {
		return err
	}

	toAccount, ok := accounts.Accounts[toRole]
	if !ok {
		return fmt.Errorf("account role '%s' not found", toRole)
	}

	minterAccount, ok := accounts.Accounts[minterRole]
	if !ok {
		return fmt.Errorf("minter role '%s' not found", minterRole)
	}

	amount := new(big.Int)
	amount, ok = amount.SetString(amountStr, 10)
	if !ok {
		return fmt.Errorf("invalid amount: %s", amountStr)
	}

	privateKey, err := parsePrivateKey(minterAccount.PrivateKey)
	if err != nil {
		return fmt.Errorf("invalid private key for minter '%s': %w", minterRole, err)
	}

	auth, err := bind.NewKeyedTransactorWithChainID(privateKey, big.NewInt(31415926))
	if err != nil {
		return fmt.Errorf("failed to create transactor: %w", err)
	}

	tokenABI, err := os.ReadFile(tokenRecord.ABIPath)
	if err != nil {
		return fmt.Errorf("failed to read ABI: %w", err)
	}

	client, err := ethclient.Dial(cfg.RPC)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer client.Close()

	parsedABI, err := parseABI(tokenABI)
	if err != nil {
		return err
	}
	contract := bind.NewBoundContract(common.HexToAddress(tokenRecord.Address), parsedABI, client, client, client)

	tx, err := contract.Transact(auth, "mint", common.HexToAddress(toAccount.EthAddress), amount)
	if err != nil {
		return fmt.Errorf("mint failed: %w", err)
	}

	fmt.Printf("Minted %s to %s\n", amountStr, toAccount.EthAddress)
	fmt.Printf("Tx: %s\n", tx.Hash().Hex())
	return nil
}

func mintAndFundPrivateKey(c *cli.Context) error {
	workspace := c.String("workspace")
	tokenName := c.String("token")
	recipientKey := c.String("private-key")
	amountStr := c.String("amount")
	filAmountStr := strings.TrimSpace(c.String("fil"))
	minterKey := c.String("minter-private-key")

	if workspace == "" {
		return fmt.Errorf("workspace is required")
	}
	if tokenName == "" {
		tokenName = "USDFC"
	}
	if recipientKey == "" {
		return fmt.Errorf("private-key is required")
	}
	if amountStr == "" {
		return fmt.Errorf("amount is required")
	}

	deployments, err := loadDeployments(workspace)
	if err != nil {
		return err
	}

	tokenRecord, err := findContract(deployments, tokenName)
	if err != nil {
		return err
	}

	if minterKey == "" {
		minterKey = tokenRecord.PrivateKey
		if minterKey == "" {
			return fmt.Errorf("deployment record for %s is missing deployer private key; supply --minter-private-key", tokenName)
		}
	}

	minterECDSA, err := parsePrivateKey(minterKey)
	if err != nil {
		return fmt.Errorf("invalid minter private key: %w", err)
	}

	recipientECDSA, err := parsePrivateKey(recipientKey)
	if err != nil {
		return fmt.Errorf("invalid recipient private key: %w", err)
	}

	tokenAmount := new(big.Int)
	if _, ok := tokenAmount.SetString(amountStr, 10); !ok {
		return fmt.Errorf("invalid amount: %s", amountStr)
	}

	tokenABI, err := os.ReadFile(tokenRecord.ABIPath)
	if err != nil {
		return fmt.Errorf("failed to read ABI: %w", err)
	}

	client, err := ethclient.Dial(cfg.RPC)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer client.Close()

	auth, err := bind.NewKeyedTransactorWithChainID(minterECDSA, big.NewInt(31415926))
	if err != nil {
		return fmt.Errorf("failed to create transactor: %w", err)
	}

	parsedABI, err := parseABI(tokenABI)
	if err != nil {
		return err
	}
	contract := bind.NewBoundContract(common.HexToAddress(tokenRecord.Address), parsedABI, client, client, client)

	recipientEthAddr := crypto.PubkeyToAddress(recipientECDSA.PublicKey)

	tx, err := contract.Transact(auth, "mint", recipientEthAddr, tokenAmount)
	if err != nil {
		return fmt.Errorf("mint failed: %w", err)
	}

	fmt.Printf("Minted %s wei to %s\n", amountStr, recipientEthAddr.Hex())
	fmt.Printf("Mint transaction: %s\n", tx.Hash().Hex())

	filAmountStr = strings.TrimSpace(filAmountStr)

	castAddr, err := ethtypes.CastEthAddress(recipientEthAddr.Bytes())
	if err != nil {
		return fmt.Errorf("failed to cast Ethereum address: %w", err)
	}

	filAddr, err := castAddr.ToFilecoinAddress()
	if err != nil {
		return fmt.Errorf("failed to derive Filecoin address: %w", err)
	}

	fmt.Printf("Derived Ethereum address: %s\n", recipientEthAddr.Hex())
	fmt.Printf("Derived Filecoin address: %s\n", filAddr)

	if filAmountStr == "" || filAmountStr == "0" {
		return nil
	}

	filAmount, err := filbig.FromString(filAmountStr)
	if err != nil {
		return fmt.Errorf("invalid FIL amount: %w", err)
	}

	fundAmount := lotustypes.BigMul(filAmount, lotustypes.NewInt(1e18))

	smsg, err := FundWallet(context.Background(), filAddr, fundAmount, true)
	if err != nil {
		return fmt.Errorf("failed to fund wallet: %w", err)
	}

	fmt.Printf("Funded %s FIL to %s\n", filAmountStr, filAddr)
	fmt.Printf("Funding message CID: %s\n", smsg.Cid())

	return nil
}

func approveTokens(c *cli.Context) error {
	workspace := c.String("workspace")
	tokenName := c.String("token")
	spenderName := c.String("spender")
	amountStr := c.String("amount")
	fromRole := c.String("from")

	deployments, err := loadDeployments(workspace)
	if err != nil {
		return err
	}

	accounts, err := loadAccounts(workspace)
	if err != nil {
		return err
	}

	tokenRecord, err := findContract(deployments, tokenName)
	if err != nil {
		return err
	}

	spenderRecord, err := findContract(deployments, spenderName)
	if err != nil {
		return err
	}

	fromAccount, ok := accounts.Accounts[fromRole]
	if !ok {
		return fmt.Errorf("account role '%s' not found", fromRole)
	}

	amount := new(big.Int)
	amount, ok = amount.SetString(amountStr, 10)
	if !ok {
		return fmt.Errorf("invalid amount: %s", amountStr)
	}

	privateKey, err := parsePrivateKey(fromAccount.PrivateKey)
	if err != nil {
		return fmt.Errorf("invalid private key for '%s': %w", fromRole, err)
	}

	auth, err := bind.NewKeyedTransactorWithChainID(privateKey, big.NewInt(31415926))
	if err != nil {
		return fmt.Errorf("failed to create transactor: %w", err)
	}

	tokenABI, err := os.ReadFile(tokenRecord.ABIPath)
	if err != nil {
		return fmt.Errorf("failed to read ABI: %w", err)
	}

	client, err := ethclient.Dial(cfg.RPC)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer client.Close()

	parsedABI, err := parseABI(tokenABI)
	if err != nil {
		return err
	}
	contract := bind.NewBoundContract(common.HexToAddress(tokenRecord.Address), parsedABI, client, client, client)

	tx, err := contract.Transact(auth, "approve", common.HexToAddress(spenderRecord.Address), amount)
	if err != nil {
		return fmt.Errorf("approve failed: %w", err)
	}

	fmt.Printf("Approved %s for %s to spend %s\n", spenderName, fromRole, amountStr)
	fmt.Printf("Tx: %s\n", tx.Hash().Hex())
	return nil
}

func depositTokens(c *cli.Context) error {
	workspace := c.String("workspace")
	tokenName := c.String("token")
	amountStr := c.String("amount")
	fromRole := c.String("from")

	deployments, err := loadDeployments(workspace)
	if err != nil {
		return err
	}

	accounts, err := loadAccounts(workspace)
	if err != nil {
		return err
	}

	tokenRecord, err := findContract(deployments, tokenName)
	if err != nil {
		return err
	}

	paymentsRecord, err := findContract(deployments, "Payments")
	if err != nil {
		return err
	}

	fromAccount, ok := accounts.Accounts[fromRole]
	if !ok {
		return fmt.Errorf("account role '%s' not found", fromRole)
	}

	amount := new(big.Int)
	amount, ok = amount.SetString(amountStr, 10)
	if !ok {
		return fmt.Errorf("invalid amount: %s", amountStr)
	}

	privateKey, err := parsePrivateKey(fromAccount.PrivateKey)
	if err != nil {
		return fmt.Errorf("invalid private key for '%s': %w", fromRole, err)
	}

	auth, err := bind.NewKeyedTransactorWithChainID(privateKey, big.NewInt(31415926))
	if err != nil {
		return fmt.Errorf("failed to create transactor: %w", err)
	}

	paymentsABI, err := os.ReadFile(paymentsRecord.ABIPath)
	if err != nil {
		return fmt.Errorf("failed to read ABI: %w", err)
	}

	client, err := ethclient.Dial(cfg.RPC)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer client.Close()

	parsedABI, err := parseABI(paymentsABI)
	if err != nil {
		return err
	}
	contract := bind.NewBoundContract(common.HexToAddress(paymentsRecord.Address), parsedABI, client, client, client)

	tx, err := contract.Transact(auth, "deposit", common.HexToAddress(tokenRecord.Address), common.HexToAddress(fromAccount.EthAddress), amount)
	if err != nil {
		return fmt.Errorf("deposit failed: %w", err)
	}

	fmt.Printf("Deposited %s from %s\n", amountStr, fromRole)
	fmt.Printf("Tx: %s\n", tx.Hash().Hex())
	return nil
}

func approveOperator(c *cli.Context) error {
	workspace := c.String("workspace")
	tokenName := c.String("token")
	operatorAddr := c.String("operator")
	rateAllowanceStr := c.String("rate-allowance")
	lockupAllowanceStr := c.String("lockup-allowance")
	maxLockupPeriodStr := c.String("max-lockup-period")
	fromRole := c.String("from")

	deployments, err := loadDeployments(workspace)
	if err != nil {
		return err
	}

	accounts, err := loadAccounts(workspace)
	if err != nil {
		return err
	}

	tokenRecord, err := findContract(deployments, tokenName)
	if err != nil {
		return err
	}

	paymentsRecord, err := findContract(deployments, "Payments")
	if err != nil {
		return err
	}

	fromAccount, ok := accounts.Accounts[fromRole]
	if !ok {
		return fmt.Errorf("account role '%s' not found", fromRole)
	}

	rateAllowance := new(big.Int)
	rateAllowance, ok = rateAllowance.SetString(rateAllowanceStr, 10)
	if !ok {
		return fmt.Errorf("invalid rate allowance: %s", rateAllowanceStr)
	}

	lockupAllowance := new(big.Int)
	lockupAllowance, ok = lockupAllowance.SetString(lockupAllowanceStr, 10)
	if !ok {
		return fmt.Errorf("invalid lockup allowance: %s", lockupAllowanceStr)
	}

	maxLockupPeriod := new(big.Int)
	maxLockupPeriod, ok = maxLockupPeriod.SetString(maxLockupPeriodStr, 10)
	if !ok {
		return fmt.Errorf("invalid max lockup period: %s", maxLockupPeriodStr)
	}

	privateKey, err := parsePrivateKey(fromAccount.PrivateKey)
	if err != nil {
		return fmt.Errorf("invalid private key for '%s': %w", fromRole, err)
	}

	auth, err := bind.NewKeyedTransactorWithChainID(privateKey, big.NewInt(31415926))
	if err != nil {
		return fmt.Errorf("failed to create transactor: %w", err)
	}

	paymentsABI, err := os.ReadFile(paymentsRecord.ABIPath)
	if err != nil {
		return fmt.Errorf("failed to read ABI: %w", err)
	}

	client, err := ethclient.Dial(cfg.RPC)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer client.Close()

	parsedABI, err := parseABI(paymentsABI)
	if err != nil {
		return err
	}
	contract := bind.NewBoundContract(common.HexToAddress(paymentsRecord.Address), parsedABI, client, client, client)

	tx, err := contract.Transact(auth, "setOperatorApproval",
		common.HexToAddress(tokenRecord.Address),
		common.HexToAddress(operatorAddr),
		true,
		rateAllowance,
		lockupAllowance,
		maxLockupPeriod)
	if err != nil {
		return fmt.Errorf("approve operator failed: %w", err)
	}

	fmt.Printf("Approved operator %s\n", operatorAddr)
	fmt.Printf("Tx: %s\n", tx.Hash().Hex())
	return nil
}

func checkBalance(c *cli.Context) error {
	workspace := c.String("workspace")
	accountRole := c.String("account")
	contractName := c.String("contract")

	cfg, err := loadWorkspaceConfig()
	if err != nil {
		return err
	}

	accounts, err := loadAccounts(workspace)
	if err != nil {
		return err
	}

	account, exists := accounts.Accounts[accountRole]
	if !exists {
		return fmt.Errorf("account role '%s' not found", accountRole)
	}

	deployments, err := loadDeployments(workspace)
	if err != nil {
		return err
	}

	client, err := ethclient.Dial(cfg.RPC)
	if err != nil {
		return fmt.Errorf("failed to connect to RPC: %w", err)
	}
	defer client.Close()

	isPaymentsContract := strings.EqualFold(contractName, "Payments")

	if isPaymentsContract {
		paymentsRecord, err := findContract(deployments, "Payments")
		if err != nil {
			return err
		}

		abiData, err := os.ReadFile(paymentsRecord.ABIPath)
		if err != nil {
			return fmt.Errorf("failed to read ABI: %w", err)
		}

		parsedABI, err := parseABI(abiData)
		if err != nil {
			return err
		}
		accountAddr := common.HexToAddress(account.EthAddress)
		data, err := parsedABI.Pack("accountBalances", accountAddr)
		if err != nil {
			return fmt.Errorf("failed to pack accountBalances call: %w", err)
		}

		paymentsAddress := common.HexToAddress(paymentsRecord.Address)
		result, err := client.CallContract(context.Background(), ethereum.CallMsg{
			To:   &paymentsAddress,
			Data: data,
		}, nil)
		if err != nil {
			return fmt.Errorf("failed to call accountBalances: %w", err)
		}

		var balance *big.Int
		err = parsedABI.UnpackIntoInterface(&balance, "accountBalances", result)
		if err != nil {
			return fmt.Errorf("failed to unpack balance: %w", err)
		}

		fmt.Printf("Account: %s (%s)\n", accountRole, account.EthAddress)
		fmt.Printf("Payments Contract: %s\n", paymentsRecord.Address)
		fmt.Printf("Balance in Payments: %s wei\n", balance.String())

		balanceFloat := new(big.Float).SetInt(balance)
		divisor := new(big.Float).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil))
		tokenBalance := new(big.Float).Quo(balanceFloat, divisor)
		fmt.Printf("Balance in Payments: %s tokens\n", tokenBalance.Text('f', 6))

		return nil
	}

	// Check token balance
	tokenRecord, err := findContract(deployments, contractName)
	if err != nil {
		return err
	}

	abiData, err := os.ReadFile(tokenRecord.ABIPath)
	if err != nil {
		return fmt.Errorf("failed to read ABI: %w", err)
	}

	parsedABI, err := parseABI(abiData)
	if err != nil {
		return err
	}
	accountAddr := common.HexToAddress(account.EthAddress)
	data, err := parsedABI.Pack("balanceOf", accountAddr)
	if err != nil {
		return fmt.Errorf("failed to pack balanceOf call: %w", err)
	}

	tokenAddress := common.HexToAddress(tokenRecord.Address)
	result, err := client.CallContract(context.Background(), ethereum.CallMsg{
		To:   &tokenAddress,
		Data: data,
	}, nil)
	if err != nil {
		return fmt.Errorf("failed to call balanceOf: %w", err)
	}

	var balance *big.Int
	err = parsedABI.UnpackIntoInterface(&balance, "balanceOf", result)
	if err != nil {
		return fmt.Errorf("failed to unpack balance: %w", err)
	}

	fmt.Printf("Account: %s (%s)\n", accountRole, account.EthAddress)
	fmt.Printf("Token: %s (%s)\n", contractName, tokenRecord.Address)
	fmt.Printf("Balance: %s wei\n", balance.String())

	balanceFloat := new(big.Float).SetInt(balance)
	divisor := new(big.Float).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil))
	tokenBalance := new(big.Float).Quo(balanceFloat, divisor)
	fmt.Printf("Balance: %s tokens\n", tokenBalance.Text('f', 6))

	return nil
}

func loadWorkspaceConfig() (*WorkspaceConfig, error) {
	return &WorkspaceConfig{
		RPC: cfg.RPC,
	}, nil
}

type WorkspaceConfig struct {
	RPC string
}

func loadDeployments(workspace string) ([]DeploymentRecord, error) {
	path := filepath.Join(workspace, "deployments.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var deployments []DeploymentRecord
	if err := json.Unmarshal(data, &deployments); err != nil {
		return nil, err
	}

	return deployments, nil
}

func loadAccounts(workspace string) (*AccountsFile, error) {
	path := filepath.Join(workspace, "accounts.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var accounts AccountsFile
	if err := json.Unmarshal(data, &accounts); err != nil {
		return nil, err
	}

	return &accounts, nil
}

func findContract(deployments []DeploymentRecord, name string) (*DeploymentRecord, error) {
	for i := range deployments {
		if deployments[i].Name == name {
			return &deployments[i], nil
		}
	}
	return nil, fmt.Errorf("contract '%s' not found", name)
}

func parseABI(abiJSON []byte) (abi.ABI, error) {
	parsedABI, err := abi.JSON(strings.NewReader(string(abiJSON)))
	if err != nil {
		return abi.ABI{}, fmt.Errorf("failed to parse ABI: %w", err)
	}
	return parsedABI, nil
}

type DeploymentRecord struct {
	Name       string `json:"name"`
	Address    string `json:"address"`
	ABIPath    string `json:"abi_path"`
	PrivateKey string `json:"deployer_private_key"`
}
