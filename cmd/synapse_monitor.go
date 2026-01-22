package cmd

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/parthshah1/mpool-tx/synapse"
	"github.com/urfave/cli/v2"
)

const defaultEventFile = "/tmp/synapse-events.json"

var SynapseCmd = &cli.Command{
	Name:  "synapse",
	Usage: "Synapse storage invariant monitoring",
	Subcommands: []*cli.Command{
		{
			Name:  "monitor",
			Usage: "Monitor Synapse events (run in background during e2e test)",
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:    "warm-storage",
					Usage:   "WarmStorage contract address",
					EnvVars: []string{"LOCALNET_WARM_STORAGE_CONTRACT_ADDRESS", "WARM_STORAGE_ADDRESS"},
				},
				&cli.StringFlag{
					Name:    "payments",
					Usage:   "FilecoinPayV1 contract address",
					EnvVars: []string{"LOCALNET_PAYMENTS_ADDRESS", "PAYMENTS_ADDRESS"},
				},
				&cli.StringFlag{
					Name:    "pdp-verifier",
					Usage:   "PDPVerifier contract address",
					EnvVars: []string{"LOCALNET_PDP_VERIFIER_ADDRESS", "PDP_VERIFIER_ADDRESS"},
				},
				&cli.StringFlag{
					Name:    "rpc",
					Usage:   "RPC URL (overrides global --rpc)",
					EnvVars: []string{"LOCALNET_RPC_URL", "FILECOIN_RPC"},
				},
				&cli.IntFlag{
					Name:  "duration",
					Usage: "How long to monitor in seconds (0 = until killed)",
					Value: 0,
				},
				&cli.StringFlag{
					Name:  "output",
					Usage: "Output file for events",
					Value: defaultEventFile,
				},
			},
			Action: runMonitor,
		},
		{
			Name:  "assert",
			Usage: "Emit Antithesis assertions from collected events",
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:  "input",
					Usage: "Input file with collected events",
					Value: defaultEventFile,
				},
			},
			Action: runAssert,
		},
		{
			Name:  "summary",
			Usage: "Print summary of collected events (no assertions)",
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:  "input",
					Usage: "Input file with collected events",
					Value: defaultEventFile,
				},
			},
			Action: runSummary,
		},
		{
			Name:  "settle",
			Usage: "Settle payment rails for a data set",
			Flags: []cli.Flag{
				&cli.Uint64Flag{
					Name:     "data-set-id",
					Usage:    "Data set ID to settle",
					Required: true,
				},
				&cli.StringFlag{
					Name:    "warm-storage",
					Usage:   "WarmStorage contract address",
					EnvVars: []string{"LOCALNET_WARM_STORAGE_CONTRACT_ADDRESS", "WARM_STORAGE_ADDRESS"},
				},
				&cli.StringFlag{
					Name:    "payments",
					Usage:   "FilecoinPayV1 contract address",
					EnvVars: []string{"LOCALNET_PAYMENTS_ADDRESS", "PAYMENTS_ADDRESS"},
				},
				&cli.StringFlag{
					Name:    "rpc",
					Usage:   "RPC URL",
					EnvVars: []string{"LOCALNET_RPC_URL", "FILECOIN_RPC"},
				},
				&cli.StringFlag{
					Name:    "private-key",
					Usage:   "Private key for signing transactions",
					EnvVars: []string{"CLIENT_PRIVATE_KEY", "SP_PRIVATE_KEY", "PRIVATE_KEY"},
				},
			},
			Action: runSettle,
		},
	},
}

func runMonitor(c *cli.Context) error {
	warmStorageAddr := c.String("warm-storage")
	paymentsAddr := c.String("payments")
	pdpVerifierAddr := c.String("pdp-verifier")
	rpcURL := c.String("rpc")
	duration := c.Int("duration")
	output := c.String("output")

	// Validate required parameters
	if warmStorageAddr == "" {
		return fmt.Errorf("warm-storage address required (--warm-storage or LOCALNET_WARM_STORAGE_CONTRACT_ADDRESS)")
	}
	if paymentsAddr == "" {
		return fmt.Errorf("payments address required (--payments or LOCALNET_PAYMENTS_ADDRESS)")
	}
	if pdpVerifierAddr == "" {
		return fmt.Errorf("pdp-verifier address required (--pdp-verifier or LOCALNET_PDP_VERIFIER_ADDRESS)")
	}
	if rpcURL == "" {
		return fmt.Errorf("rpc URL required (--rpc or LOCALNET_RPC_URL)")
	}

	contracts := synapse.ContractAddresses{
		WarmStorage: common.HexToAddress(warmStorageAddr),
		Payments:    common.HexToAddress(paymentsAddr),
		PDPVerifier: common.HexToAddress(pdpVerifierAddr),
	}

	log.Println("[Synapse] Starting monitor...")
	log.Printf("[Synapse] RPC: %s", rpcURL)
	log.Printf("[Synapse] WarmStorage: %s", warmStorageAddr)
	log.Printf("[Synapse] Payments: %s", paymentsAddr)
	log.Printf("[Synapse] PDPVerifier: %s", pdpVerifierAddr)
	log.Printf("[Synapse] Output: %s", output)

	monitor, err := synapse.NewSynapseMonitor(rpcURL, contracts)
	if err != nil {
		return fmt.Errorf("failed to create monitor: %w", err)
	}

	// Context with optional timeout
	var ctx context.Context
	var cancel context.CancelFunc
	if duration > 0 {
		ctx, cancel = context.WithTimeout(context.Background(), time.Duration(duration)*time.Second)
		log.Printf("[Synapse] Will run for %d seconds", duration)
	} else {
		ctx, cancel = context.WithCancel(context.Background())
		log.Println("[Synapse] Running until killed (Ctrl+C)")
	}
	defer cancel()

	// Handle shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("\n[Synapse] Shutdown signal received...")
		cancel()
	}()

	// Run monitor
	if err := monitor.Start(ctx, 3*time.Second); err != nil {
		return fmt.Errorf("monitor error: %w", err)
	}

	// Save state
	state := monitor.GetState()
	if err := state.SaveToFile(output); err != nil {
		return fmt.Errorf("failed to save events: %w", err)
	}

	summary := state.GetSummary()
	log.Printf("[Synapse] Monitor complete. Faults=%d, Pieces=%d, Settlements=%d",
		summary["faultCount"], summary["pieceCount"], summary["settlementCount"])

	return nil
}

func runAssert(c *cli.Context) error {
	input := c.String("input")

	state, err := synapse.LoadInvariantStateFromFile(input)
	if err != nil {
		return fmt.Errorf("failed to load events from %s: %w", input, err)
	}

	summary := state.GetSummary()
	log.Printf("[Synapse] Loaded events: Faults=%d, Pieces=%d, Settlements=%d",
		summary["faultCount"], summary["pieceCount"], summary["settlementCount"])

	log.Println("[Synapse] Emitting Antithesis assertions...")
	state.EmitFinalAssertions()
	log.Println("[Synapse] Assertions emitted successfully")

	return nil
}

func runSummary(c *cli.Context) error {
	input := c.String("input")

	state, err := synapse.LoadInvariantStateFromFile(input)
	if err != nil {
		return fmt.Errorf("failed to load events from %s: %w", input, err)
	}

	summary := state.GetSummary()

	fmt.Println("=== Synapse Invariant Summary ===")
	fmt.Printf("Duration:    %s\n", summary["duration"])
	fmt.Printf("Faults:      %d\n", summary["faultCount"])
	fmt.Printf("Pieces:      %d\n", summary["pieceCount"])
	fmt.Printf("Settlements: %d\n", summary["settlementCount"])
	fmt.Println()

	if summary["faultCount"].(int) > 0 {
		fmt.Println("⚠️  WARNING: PDP faults detected!")
	} else {
		fmt.Println("✓ No PDP faults")
	}

	if summary["pieceCount"].(int) > 0 {
		fmt.Println("✓ Pieces were added")
	} else {
		fmt.Println("⚠️  No pieces added")
	}

	if summary["settlementCount"].(int) > 0 {
		fmt.Println("✓ Settlements occurred")
	} else {
		fmt.Println("⚠️  No settlements")
	}

	return nil
}

func runSettle(c *cli.Context) error {
	dataSetID := c.Uint64("data-set-id")
	warmStorageAddr := c.String("warm-storage")
	paymentsAddr := c.String("payments")
	rpcURL := c.String("rpc")
	privateKey := c.String("private-key")

	// Validate required parameters
	if warmStorageAddr == "" {
		return fmt.Errorf("warm-storage address required (--warm-storage or LOCALNET_WARM_STORAGE_CONTRACT_ADDRESS)")
	}
	if paymentsAddr == "" {
		return fmt.Errorf("payments address required (--payments or LOCALNET_PAYMENTS_ADDRESS)")
	}
	if rpcURL == "" {
		return fmt.Errorf("rpc URL required (--rpc or LOCALNET_RPC_URL)")
	}
	if privateKey == "" {
		return fmt.Errorf("private-key required (--private-key or CLIENT_PRIVATE_KEY)")
	}

	log.Printf("[Synapse] Settling data set %d...", dataSetID)
	log.Printf("[Synapse] RPC: %s", rpcURL)
	log.Printf("[Synapse] WarmStorage: %s", warmStorageAddr)
	log.Printf("[Synapse] Payments: %s", paymentsAddr)

	// Create settler
	settler, err := synapse.NewSettler(
		rpcURL,
		common.HexToAddress(warmStorageAddr),
		common.HexToAddress(paymentsAddr),
	)
	if err != nil {
		return fmt.Errorf("failed to create settler: %w", err)
	}
	defer settler.Close()

	// Settle data set
	ctx := context.Background()
	results, err := settler.SettleDataSet(ctx, privateKey, dataSetID)
	if err != nil {
		return fmt.Errorf("settlement failed: %w", err)
	}

	// Print results
	fmt.Println("=== Settlement Results ===")
	for _, result := range results {
		fmt.Printf("Rail %d: tx=%s, block=%d, amount=%s\n",
			result.RailID, result.TxHash, result.BlockNumber, result.Amount)
	}
	fmt.Printf("\n✓ Settled %d rail(s) for data set %d\n", len(results), dataSetID)

	return nil
}
