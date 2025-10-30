package cmd

import (
	"fmt"
	"os"

	"github.com/parthshah1/mpool-tx/config"
	"github.com/urfave/cli/v2"
)

var (
	cfg     *config.Config
	clientt *config.Client
)

// NewApp creates a new CLI app
func NewApp() *cli.App {
	app := &cli.App{
		Name:  "mpool-tx",
		Usage: "Filecoin testing tool",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "rpc",
				Usage:   "Filecoin RPC URL (env: FILECOIN_RPC)",
				EnvVars: []string{"FILECOIN_RPC"},
			},
			&cli.StringFlag{
				Name:    "token",
				Usage:   "JWT token file path (env: FILECOIN_TOKEN)",
				EnvVars: []string{"FILECOIN_TOKEN"},
			},
			&cli.BoolFlag{
				Name:    "antithesis",
				Usage:   "Enable Antithesis property assertions (env: ANTITHESIS_MODE)",
				EnvVars: []string{"ANTITHESIS_MODE"},
			},
			&cli.BoolFlag{
				Name:    "verbose",
				Usage:   "Verbose output (env: VERBOSE)",
				EnvVars: []string{"VERBOSE"},
			},
		},
		Before: func(c *cli.Context) error {
			cfg = config.Load()

			if c.IsSet("rpc") {
				cfg.RPC = c.String("rpc")
			}
			if c.IsSet("token") {
				cfg.Token = c.String("token")
			}
			if c.IsSet("verbose") {
				cfg.Verbose = c.Bool("verbose")
			}

			// Set global antithesis mode
			config.SetAntithesisMode(c.Bool("antithesis"))
			if config.IsAntithesisEnabled() {
				fmt.Println("Antithesis property assertions enabled")
			}

			// Initialize client
			var err error
			clientt, err = config.New(cfg)
			if err != nil {
				return fmt.Errorf("failed to connect to Filecoin node: %w", err)
			}

			return nil
		},
		After: func(c *cli.Context) error {
			if clientt != nil {
				clientt.Close()
			}
			return nil
		},
		Commands: []*cli.Command{
			WalletCmd,
			MempoolCmd,
			ContractCmd,
			PropertiesCmd,
			AccountsCmd,
			PaymentsCmd,
		},
	}
	return app
}

func Execute() {
	if err := NewApp().Run(os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func init() {
	// No initialization needed for urfave/cli
}
