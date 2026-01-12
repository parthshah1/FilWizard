package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/filecoin-project/lotus/chain/types"
	"github.com/urfave/cli/v2"
)

var AccountsCmd = &cli.Command{
	Name:  "accounts",
	Usage: "Manage accounts for different roles",
	Subcommands: []*cli.Command{
		{
			Name:  "create",
			Usage: "Create accounts with roles",
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:     "workspace",
					Usage:    "Workspace directory",
					Required: true,
				},
				&cli.StringSliceFlag{
					Name:     "role",
					Usage:    "Role names (can specify multiple)",
					Required: true,
				},
				&cli.BoolFlag{
					Name:  "fund",
					Usage: "Fund accounts with FIL",
					Value: true,
				},
			},
			Action: createAccounts,
		},
		{
			Name:  "list",
			Usage: "List all accounts",
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:     "workspace",
					Usage:    "Workspace directory",
					Required: true,
				},
			},
			Action: listAccounts,
		},
	},
}

type AccountInfo struct {
	Address    string `json:"address"`
	EthAddress string `json:"ethAddress"`
	PrivateKey string `json:"privateKey"`
}

type AccountsFile struct {
	Accounts map[string]AccountInfo `json:"accounts"`
}

func createAccounts(c *cli.Context) error {
	workspace := c.String("workspace")
	roles := c.StringSlice("role")
	fund := c.Bool("fund")

	accountsPath := filepath.Join(workspace, "accounts.json")

	accounts := AccountsFile{Accounts: make(map[string]AccountInfo)}

	if _, err := os.Stat(accountsPath); err == nil {
		data, err := os.ReadFile(accountsPath)
		if err != nil {
			return fmt.Errorf("failed to read accounts file: %w", err)
		}
		if err := json.Unmarshal(data, &accounts); err != nil {
			return fmt.Errorf("failed to parse accounts file: %w", err)
		}
	}

	for _, role := range roles {
		if _, exists := accounts.Accounts[role]; exists {
			fmt.Printf("Account '%s' already exists, skipping\n", role)
			continue
		}

		key, ethAddr, filAddr, err := NewAccount()
		if err != nil {
			return fmt.Errorf("failed to create account for role '%s': %w", role, err)
		}

		if fund {
			fundAmount := types.FromFil(10)
			_, err := FundWallet(c.Context, filAddr, fundAmount, true)
			if err != nil {
				return fmt.Errorf("failed to fund %s: %w", role, err)
			}
		}

		accounts.Accounts[role] = AccountInfo{
			Address:    filAddr.String(),
			EthAddress: ethAddr.String(),
			PrivateKey: fmt.Sprintf("0x%x", key.PrivateKey),
		}

		fmt.Printf("Created '%s': %s (ETH: %s)\n", role, filAddr, ethAddr)
	}

	data, err := json.MarshalIndent(accounts, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal accounts: %w", err)
	}

	if err := os.WriteFile(accountsPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write accounts file: %w", err)
	}

	fmt.Printf("\nAccounts saved to %s\n", accountsPath)
	return nil
}

func listAccounts(c *cli.Context) error {
	workspace := c.String("workspace")
	accountsPath := filepath.Join(workspace, "accounts.json")

	data, err := os.ReadFile(accountsPath)
	if err != nil {
		return fmt.Errorf("failed to read accounts file: %w", err)
	}

	var accounts AccountsFile
	if err := json.Unmarshal(data, &accounts); err != nil {
		return fmt.Errorf("failed to parse accounts file: %w", err)
	}

	for role, info := range accounts.Accounts {
		fmt.Printf("%s:\n", role)
		fmt.Printf("  Filecoin: %s\n", info.Address)
		fmt.Printf("  Ethereum: %s\n", info.EthAddress)
		fmt.Printf("  PrivKey:  %s\n\n", info.PrivateKey)
	}

	return nil
}
