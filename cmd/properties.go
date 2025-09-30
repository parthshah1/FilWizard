package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/parthshah1/mpool-tx/config"
	"github.com/urfave/cli/v2"
)

var PropertiesCmd = &cli.Command{
	Name:  "properties",
	Usage: "Check Filecoin network properties",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:  "check",
			Usage: "Property to check (chain-sync, progression, state-consistency, all)",
			Value: "all",
		},
		&cli.DurationFlag{
			Name:  "timeout",
			Usage: "Timeout for property checks",
			Value: 60 * time.Second,
		},
		&cli.DurationFlag{
			Name:  "monitor-duration",
			Usage: "Duration to monitor chain progression (default: 45s)",
			Value: 45 * time.Second,
		},
	},
	Action: runPropertyChecks,
}

func runPropertyChecks(c *cli.Context) error {
	ctx, cancel := context.WithTimeout(context.Background(), c.Duration("timeout"))
	defer cancel()

	checker := config.NewPropertyChecker()

	// Set configuration from flags
	checker.SetConfig(&config.PropertyConfig{
		MonitorDuration: c.Duration("monitor-duration"),
	})

	property := c.String("check")

	fmt.Printf("Checking Filecoin network properties...\n")

	if config.IsAntithesisEnabled() {
		fmt.Println("Antithesis assertions enabled")
	}

	switch property {
	case "chain-sync":
		return checker.CheckChainSync(ctx)
	case "progression":
		return checker.CheckChainProgression(ctx)
	case "state-consistency":
		return checker.CheckStateConsistency(ctx)
	case "all":
		fmt.Println("\n=== Running All Property Checks ===")

		if err := checker.CheckChainSync(ctx); err != nil {
			return fmt.Errorf("chain sync property failed: %w", err)
		}

		if err := checker.CheckChainProgression(ctx); err != nil {
			return fmt.Errorf("chain progression property failed: %w", err)
		}

		if err := checker.CheckStateConsistency(ctx); err != nil {
			return fmt.Errorf("state consistency property failed: %w", err)
		}

		fmt.Println("\nAll network properties satisfied!")
		return nil

	default:
		return fmt.Errorf("unknown property: %s (available: chain-sync, progression, state-consistency, all)", property)
	}
}
