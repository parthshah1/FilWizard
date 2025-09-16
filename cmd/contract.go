package cmd

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/filecoin-project/go-address"
	"github.com/filecoin-project/go-state-types/big"
	"github.com/filecoin-project/go-state-types/crypto"
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
	signature, err := sigs.Sign(crypto.SigTypeDelegated, privateKey, preimage)
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
func DeployContract(ctx context.Context, contractPath string, deployer string, fundAmount string) error {
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
		amount, _ := big.FromString(fundAmount)
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
		Value:                big.Zero(),
		Nonce:                int(nonce),
		MaxFeePerGas:         types.NanoFil,
		MaxPriorityFeePerGas: big.Int(maxPriorityFee),
		GasLimit:             int(gasLimit),
		Input:                contract,
		V:                    big.Zero(),
		R:                    big.Zero(),
		S:                    big.Zero(),
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
	} else {
		return fmt.Errorf("transaction failed with status: %d", receipt.Status)
	}

	return nil
}

var ContractCmd = &cli.Command{
	Name:  "contract",
	Usage: "Contract operations",
	Subcommands: []*cli.Command{
		{
			Name:      "deploy",
			Usage:     "Deploy a contract",
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
			},
			Action: func(c *cli.Context) error {
				if c.NArg() != 1 {
					return fmt.Errorf("expected 1 argument: <contract-file>")
				}

				ctx := context.Background()
				contractFile := c.Args().Get(0)
				deployer := c.String("deployer")
				fundAmount := c.String("fund")

				return DeployContract(ctx, contractFile, deployer, fundAmount)
			},
		},
		{
			Name:      "call",
			Usage:     "Call a contract method",
			ArgsUsage: "<address> <method> [args...]",
			Action: func(c *cli.Context) error {
				if c.NArg() < 2 {
					return fmt.Errorf("expected at least 2 arguments: <address> <method>")
				}
				fmt.Println("Contract calls are not yet implemented")
				fmt.Println("This feature will be added in a future iteration")
				return nil
			},
		},
	},
}
