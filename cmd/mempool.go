package cmd

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/rand"
	"time"

	"github.com/filecoin-project/go-address"
	"github.com/filecoin-project/go-state-types/abi"
	"github.com/filecoin-project/lotus/api"
	"github.com/filecoin-project/lotus/chain/types"
	"github.com/ipfs/go-cid"
	"github.com/parthshah1/mpool-tx/config"
	"github.com/urfave/cli/v2"
)

// MempoolManager handles mempool operations
type MempoolManager struct {
	api    api.FullNode
	config *config.Config
}

// TransactionSpammer handles high-volume transaction generation
type TransactionSpammer struct {
	api          api.FullNode
	wallets      []address.Address
	minBalance   abi.TokenAmount
	refillAmount abi.TokenAmount
	txAmount     abi.TokenAmount
	concurrent   int
	waitConfirm  bool
}

// NewMempoolManager creates a new mempool manager
func NewMempoolManager(a api.FullNode, cfg *config.Config) *MempoolManager {
	return &MempoolManager{
		api:    a,
		config: cfg,
	}
}

// NewTransactionSpammer creates a new transaction spammer
func NewTransactionSpammer(api api.FullNode, wallets []address.Address, config SpammerConfig) *TransactionSpammer {
	return &TransactionSpammer{
		api:          api,
		wallets:      wallets,
		minBalance:   config.MinBalance,
		refillAmount: config.RefillAmount,
		txAmount:     config.TxAmount,
		concurrent:   config.Concurrent,
		waitConfirm:  config.WaitConfirm,
	}
}

// SpammerConfig holds configuration for transaction spammer
type SpammerConfig struct {
	MinBalance   abi.TokenAmount
	RefillAmount abi.TokenAmount
	TxAmount     abi.TokenAmount
	Concurrent   int
	WaitConfirm  bool
}

// SendTransaction sends a single transaction
func (mm *MempoolManager) SendTransaction(ctx context.Context, from, to address.Address, amount abi.TokenAmount, waitForConfirm bool) (cid.Cid, error) {
	// Create message
	msg := &types.Message{
		From:  from,
		To:    to,
		Value: amount,
	}

	// Send message
	smsg, err := mm.api.MpoolPushMessage(ctx, msg, nil)
	if err != nil {
		return cid.Undef, fmt.Errorf("failed to send transaction: %w", err)
	}

	if waitForConfirm {
		// Wait for message to be included in a block
		_, err = mm.api.StateWaitMsg(ctx, smsg.Cid(), 5, abi.ChainEpoch(-1), true)
		if err != nil {
			return smsg.Cid(), fmt.Errorf("failed to wait for confirmation: %w", err)
		}
	}

	return smsg.Cid(), nil
}

func (ts *TransactionSpammer) SpamTransactions(ctx context.Context, count int) error {
	if len(ts.wallets) < 2 {
		return fmt.Errorf("need at least 2 wallets for transaction spam")
	}

	// Create worker pool
	jobs := make(chan int, count)
	results := make(chan error, count)

	// Start workers
	for i := 0; i < ts.concurrent; i++ {
		go ts.worker(ctx, jobs, results)
	}

	// Send jobs
	for i := 0; i < count; i++ {
		jobs <- i
	}
	close(jobs)

	// Collect results
	var errors []error
	sent := 0
	for i := 0; i < count; i++ {
		if err := <-results; err != nil {
			errors = append(errors, err)
		} else {
			sent++
		}
	}

	fmt.Printf("Completed: %d sent, %d failed\n", sent, len(errors))

	if len(errors) > 0 {
		return fmt.Errorf("failed %d/%d transactions, first error: %v", len(errors), count, errors[0])
	}

	return nil
}

// worker processes transaction spam jobs
func (ts *TransactionSpammer) worker(ctx context.Context, jobs <-chan int, results chan<- error) {
	for range jobs {
		err := ts.sendRandomTransaction(ctx)
		results <- err
	}
}

// sendRandomTransaction sends a transaction between random wallets
func (ts *TransactionSpammer) sendRandomTransaction(ctx context.Context) error {
	// Select random from and to wallets
	fromIdx := rand.Intn(len(ts.wallets))
	toIdx := rand.Intn(len(ts.wallets))

	// Ensure from != to
	for fromIdx == toIdx {
		toIdx = rand.Intn(len(ts.wallets))
	}

	from := ts.wallets[fromIdx]
	to := ts.wallets[toIdx]

	// Check and refill wallet if needed
	if err := ts.checkAndRefillWallet(ctx, from); err != nil {
		return fmt.Errorf("failed to refill wallet %s: %w", from, err)
	}

	// Create and send transaction
	msg := &types.Message{
		From:  from,
		To:    to,
		Value: ts.txAmount,
	}

	smsg, err := ts.api.MpoolPushMessage(ctx, msg, nil)
	if err != nil {
		return fmt.Errorf("failed to send transaction from %s to %s: %w", from, to, err)
	}

	if ts.waitConfirm {
		_, err = ts.api.StateWaitMsg(ctx, smsg.Cid(), 1, abi.ChainEpoch(10), true)
		if err != nil {
			return fmt.Errorf("failed to wait for confirmation: %w", err)
		}
	}

	return nil
}

// checkAndRefillWallet checks wallet balance and refills if below minimum
func (ts *TransactionSpammer) checkAndRefillWallet(ctx context.Context, wallet address.Address) error {
	balance, err := ts.api.WalletBalance(ctx, wallet)
	if err != nil {
		return fmt.Errorf("failed to get balance: %w", err)
	}

	if balance.LessThan(ts.minBalance) {
		// Refill wallet
		_, err = FundWallet(ctx, wallet, ts.refillAmount, false)
		if err != nil {
			return fmt.Errorf("failed to refill wallet: %w", err)
		}

		fmt.Printf("Refilled wallet %s with %s FIL\n", wallet, types.BigDiv(ts.refillAmount, types.NewInt(1e18)).String())
	}

	return nil
}

// GetMempoolStatus returns current mempool status
func (mm *MempoolManager) GetMempoolStatus(ctx context.Context) (map[string]interface{}, error) {
	pending, err := mm.api.MpoolPending(ctx, types.EmptyTSK)
	if err != nil {
		return nil, fmt.Errorf("failed to get pending messages: %w", err)
	}

	status := map[string]interface{}{
		"pending_count": len(pending),
		"timestamp":     time.Now().Unix(),
	}

	return status, nil
}

var MempoolCmd = &cli.Command{
	Name:  "mempool",
	Usage: "Mempool operations",
	Subcommands: []*cli.Command{
		{
			Name:      "send",
			Usage:     "Send a single transaction",
			ArgsUsage: "<from> <to> <amount>",
			Flags: []cli.Flag{
				&cli.BoolFlag{
					Name:  "wait",
					Usage: "Wait for transaction confirmation",
				},
			},
			Action: func(c *cli.Context) error {
				if c.NArg() != 3 {
					return fmt.Errorf("expected 3 arguments: <from> <to> <amount>")
				}

				ctx := context.Background()
				mm := NewMempoolManager(clientt.GetAPI(), cfg)

				from, err := address.NewFromString(c.Args().Get(0))
				if err != nil {
					return fmt.Errorf("invalid from address: %w", err)
				}

				to, err := address.NewFromString(c.Args().Get(1))
				if err != nil {
					return fmt.Errorf("invalid to address: %w", err)
				}

				amountStr := c.Args().Get(2)
				amount, _ := types.BigFromString(amountStr)

				// Convert FIL to attoFIL
				attoAmount := types.BigMul(amount, types.NewInt(1e18))
				waitConfirm := c.Bool("wait")

				txCid, err := mm.SendTransaction(ctx, from, to, attoAmount, waitConfirm)
				if err != nil {
					return err
				}

				fmt.Printf("Transaction sent: %s\n", txCid)
				if waitConfirm {
					fmt.Printf("Transaction confirmed\n")
				}
				return nil
			},
		},
		{
			Name:  "spam",
			Usage: "Spam transactions between random wallets",
			Flags: []cli.Flag{
				&cli.IntFlag{
					Name:  "count",
					Value: 100,
					Usage: "Number of transactions to send",
				},
				&cli.StringFlag{
					Name:  "amount",
					Value: "0.1",
					Usage: "Amount per transaction (FIL)",
				},
				&cli.IntFlag{
					Name:  "concurrent",
					Value: 2,
					Usage: "Number of concurrent workers",
				},
				&cli.StringFlag{
					Name:  "min-balance",
					Value: "1",
					Usage: "Minimum wallet balance before refill (FIL)",
				},
				&cli.StringFlag{
					Name:  "refill-amount",
					Value: "10",
					Usage: "Amount to refill wallets (FIL)",
				},
				&cli.BoolFlag{
					Name:  "wait",
					Usage: "Wait for transaction confirmations",
				},
			},
			Action: func(c *cli.Context) error {
				ctx := context.Background()

				// Get all wallets
				wallets, err := ListWallets(ctx)
				if err != nil {
					return fmt.Errorf("failed to get wallets: %w", err)
				}

				if len(wallets) < 2 {
					return fmt.Errorf("need at least 2 wallets for spam, found %d", len(wallets))
				}

				count := c.Int("count")
				concurrent := c.Int("concurrent")
				waitConfirm := c.Bool("wait")

				txAmountStr := c.String("amount")
				txAmount, _ := types.BigFromString(txAmountStr)

				minBalanceStr := c.String("min-balance")
				minBalance, _ := types.BigFromString(minBalanceStr)

				refillAmountStr := c.String("refill-amount")
				refillAmount, _ := types.BigFromString(refillAmountStr)

				// Convert to attoFIL
				config := SpammerConfig{
					TxAmount:     types.BigMul(txAmount, types.NewInt(1e18)),
					MinBalance:   types.BigMul(minBalance, types.NewInt(1e18)),
					RefillAmount: types.BigMul(refillAmount, types.NewInt(1e18)),
					Concurrent:   concurrent,
					WaitConfirm:  waitConfirm,
				}

				// Create spammer
				spammer := NewTransactionSpammer(clientt.GetAPI(), wallets, config)

				fmt.Printf(" Starting transaction spam:\n")
				fmt.Printf("   Wallets: %d\n", len(wallets))
				fmt.Printf("   Transactions: %d\n", count)
				fmt.Printf("   Concurrent workers: %d\n", concurrent)
				fmt.Printf("   Amount per tx: %s FIL\n", txAmountStr)
				fmt.Printf("   Wait for confirmation: %v\n", waitConfirm)

				// Execute spam
				start := time.Now()
				err = spammer.SpamTransactions(ctx, count)
				duration := time.Since(start)

				fmt.Printf("\nSpam completed in %v\n", duration.Round(time.Millisecond))

				return err
			},
		},
		{
			Name:  "status",
			Usage: "Get mempool status",
			Action: func(c *cli.Context) error {
				ctx := context.Background()
				mm := NewMempoolManager(clientt.GetAPI(), cfg)

				status, err := mm.GetMempoolStatus(ctx)
				if err != nil {
					return err
				}

				fmt.Printf("Mempool Status:\n")
				fmt.Printf("Pending transactions: %v\n", status["pending_count"])
				fmt.Printf("Timestamp: %v\n", time.Unix(status["timestamp"].(int64), 0))

				return nil
			},
		},
		{
			Name:  "eth",
			Usage: "Send EIP-1559 Ethereum transactions",
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:     "from",
					Usage:    "From address",
					Required: true,
				},
				&cli.StringFlag{
					Name:     "to",
					Usage:    "To address",
					Required: true,
				},
				&cli.StringFlag{
					Name:  "value",
					Value: "0",
					Usage: "Value to send (FIL)",
				},
				&cli.StringFlag{
					Name:  "data",
					Usage: "Transaction data (hex)",
				},
				&cli.StringFlag{
					Name:  "gas-limit",
					Value: "21000",
					Usage: "Gas limit",
				},
				&cli.StringFlag{
					Name:  "max-fee",
					Value: "1000000000",
					Usage: "Max fee per gas (wei)",
				},
				&cli.StringFlag{
					Name:  "max-priority-fee",
					Value: "1000000000",
					Usage: "Max priority fee per gas (wei)",
				},
				&cli.BoolFlag{
					Name:  "wait",
					Usage: "Wait for transaction confirmation",
				},
			},
			Action: func(c *cli.Context) error {
				ctx := context.Background()

				fromAddr, err := address.NewFromString(c.String("from"))
				if err != nil {
					return fmt.Errorf("invalid from address: %w", err)
				}

				toAddr, err := address.NewFromString(c.String("to"))
				if err != nil {
					return fmt.Errorf("invalid to address: %w", err)
				}

				// Parse value
				valueStr := c.String("value")
				value, _ := types.BigFromString(valueStr)
				valueAtto := types.BigMul(value, types.NewInt(1e18))

				// Parse data if provided
				var data []byte
				if dataStr := c.String("data"); dataStr != "" {
					data, err = hex.DecodeString(dataStr)
					if err != nil {
						return fmt.Errorf("invalid data hex: %w", err)
					}
				}

				api := clientt.GetAPI()

				// Get nonce
				nonce, err := api.MpoolGetNonce(ctx, fromAddr)
				if err != nil {
					return fmt.Errorf("failed to get nonce: %w", err)
				}

				// Parse gas parameters
				gasLimit := c.Int64("gas-limit")
				maxFee, _ := types.BigFromString(c.String("max-fee"))
				maxPriorityFee, _ := types.BigFromString(c.String("max-priority-fee"))

				// Create EIP-1559 transaction
				tx := &types.Message{
					From:       fromAddr,
					To:         toAddr,
					Value:      valueAtto,
					Method:     0,
					Params:     data,
					GasLimit:   gasLimit,
					GasFeeCap:  maxFee,
					GasPremium: maxPriorityFee,
					Nonce:      nonce,
				}

				fmt.Printf("Sending EIP-1559 transaction:\n")
				fmt.Printf("  From: %s\n", fromAddr)
				fmt.Printf("  To: %s\n", toAddr)
				fmt.Printf("  Value: %s FIL\n", valueStr)
				fmt.Printf("  Gas Limit: %d\n", gasLimit)
				fmt.Printf("  Nonce: %d\n", nonce)

				smsg, err := api.MpoolPushMessage(ctx, tx, nil)
				if err != nil {
					return fmt.Errorf("failed to send transaction: %w", err)
				}

				fmt.Printf("Transaction sent: %s\n", smsg.Cid())

				if c.Bool("wait") {
					fmt.Println("Waiting for confirmation...")
					_, err = api.StateWaitMsg(ctx, smsg.Cid(), 1, abi.ChainEpoch(10), true)
					if err != nil {
						return fmt.Errorf("failed to wait for confirmation: %w", err)
					}
					fmt.Println("Transaction confirmed")
				}

				return nil
			},
		},
	},
}
