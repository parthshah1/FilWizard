package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/parthshah1/mpool-tx/config"
	"github.com/urfave/cli/v2"
)

var EnvCmd = &cli.Command{
	Name:  "env",
	Usage: "Environment management commands",
	Subcommands: []*cli.Command{
		{
			Name:  "export",
			Usage: "Export deployment addresses and accounts to environment file",
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:     "workspace",
					Usage:    "Path to the FilWizard workspace directory",
					Required: true,
				},
				&cli.StringFlag{
					Name:  "output",
					Usage: "Path to write the output env file",
					Value: "environment.env",
				},
				&cli.StringFlag{
					Name:  "rpc-url",
					Usage: "RPC URL to include in output (will be set as RPC_URL and ETH_RPC_URL)",
					Value: "",
				},
				&cli.StringFlag{
					Name:  "rpc-ws-url",
					Usage: "WebSocket RPC URL to include in output (will be set as RPC_WS_URL)",
					Value: "",
				},
				&cli.StringFlag{
					Name:  "chain-id",
					Usage: "Chain ID to include in output and use for service contract selection",
					Value: "31415926",
				},
				&cli.StringSliceFlag{
					Name:  "extra",
					Usage: "Extra KEY=VALUE pairs to include in the output (can be specified multiple times)",
				},
			},
			Action: envExportAction,
		},
		{
			Name:  "show",
			Usage: "Show environment variables from workspace (for debugging)",
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:     "workspace",
					Usage:    "Path to the FilWizard workspace directory",
					Required: true,
				},
				&cli.StringFlag{
					Name:  "chain-id",
					Usage: "Chain ID to use for service contract selection",
					Value: "31415926",
				},
			},
			Action: envShowAction,
		},
	},
}

func envExportAction(c *cli.Context) error {
	workspacePath := c.String("workspace")
	outputPath := c.String("output")
	rpcURL := c.String("rpc-url")
	rpcWsURL := c.String("rpc-ws-url")
	chainID := c.String("chain-id")
	extraVars := c.StringSlice("extra")

	// Verify workspace exists
	if _, err := os.Stat(workspacePath); os.IsNotExist(err) {
		return fmt.Errorf("workspace directory does not exist: %s", workspacePath)
	}

	// Collect all environment data
	envData, err := collectEnvironmentData(workspacePath, chainID)
	if err != nil {
		return fmt.Errorf("failed to collect environment data: %w", err)
	}

	// Write the output file
	if err := writeEnvFile(outputPath, envData, rpcURL, rpcWsURL, chainID, extraVars); err != nil {
		return fmt.Errorf("failed to write environment file: %w", err)
	}

	// Print summary
	fmt.Printf("\n✓ Environment export complete!\n")
	fmt.Printf("  Contract addresses: %d\n", len(envData.Addresses))
	fmt.Printf("  Accounts: %d\n", len(envData.Accounts))
	fmt.Printf("  Output file: %s\n\n", outputPath)

	return nil
}

func envShowAction(c *cli.Context) error {
	workspacePath := c.String("workspace")
	chainID := c.String("chain-id")

	// Verify workspace exists
	if _, err := os.Stat(workspacePath); os.IsNotExist(err) {
		return fmt.Errorf("workspace directory does not exist: %s", workspacePath)
	}

	// Collect all environment data
	envData, err := collectEnvironmentData(workspacePath, chainID)
	if err != nil {
		return fmt.Errorf("failed to collect environment data: %w", err)
	}

	// Print to stdout
	fmt.Println("# Contract Addresses")
	keys := make([]string, 0, len(envData.Addresses))
	for k := range envData.Addresses {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("%s=%s\n", k, envData.Addresses[k])
	}

	fmt.Println("\n# Accounts")
	accountKeys := make([]string, 0, len(envData.Accounts))
	for k := range envData.Accounts {
		accountKeys = append(accountKeys, k)
	}
	sort.Strings(accountKeys)
	for _, k := range accountKeys {
		acc := envData.Accounts[k]
		fmt.Printf("%s_PRIVATE_KEY=%s\n", strings.ToUpper(k), acc.PrivateKey)
		fmt.Printf("%s_ETH_ADDRESS=%s\n", strings.ToUpper(k), acc.EthAddress)
	}

	if envData.DeployerPrivateKey != "" {
		fmt.Println("\n# Deployer")
		fmt.Printf("DEPLOYER_PRIVATE_KEY=%s\n", envData.DeployerPrivateKey)
	}

	return nil
}

// EnvironmentData holds all collected environment information
type EnvironmentData struct {
	Addresses          map[string]string      // CONTRACT_NAME_ADDRESS -> address
	Accounts           map[string]AccountInfo // account_name -> AccountInfo
	DeployerPrivateKey string                 // Deployer private key from first deployment
}

// collectEnvironmentData reads all deployment and account files from the workspace
func collectEnvironmentData(workspacePath, chainID string) (*EnvironmentData, error) {
	envData := &EnvironmentData{
		Addresses: make(map[string]string),
		Accounts:  make(map[string]AccountInfo),
	}

	// Step A: Read workspace/deployments.json (base deployments)
	deploymentsPath := filepath.Join(workspacePath, "deployments.json")
	if _, err := os.Stat(deploymentsPath); err == nil {
		deployments, err := config.LoadDeploymentRecords(deploymentsPath)
		if err != nil {
			return nil, fmt.Errorf("failed to load deployments.json: %w", err)
		}

		for i, record := range deployments {
			// Transform name to UPPER_SNAKE_CASE and add _ADDRESS suffix
			key := toUpperSnakeCase(record.Name) + "_ADDRESS"
			envData.Addresses[key] = record.Address

			// Extract deployer private key from first record
			if i == 0 && record.DeployerPrivateKey != "" {
				envData.DeployerPrivateKey = record.DeployerPrivateKey
			}
		}
	} else {
		fmt.Printf("Warning: No deployments.json found at %s\n", deploymentsPath)
	}

	// Step B: Scan for service contract deployment files
	serviceAddresses, err := scanServiceContracts(workspacePath, chainID)
	if err != nil {
		fmt.Printf("Warning: Error scanning service contracts: %v\n", err)
	} else {
		// Service contracts take precedence (overlay on top of base deployments)
		for key, address := range serviceAddresses {
			envData.Addresses[key] = address
		}
	}

	// Step C: Read workspace/accounts.json
	accountsPath := filepath.Join(workspacePath, "accounts.json")
	if _, err := os.Stat(accountsPath); err == nil {
		data, err := os.ReadFile(accountsPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read accounts.json: %w", err)
		}

		var accountsFile AccountsFile
		if err := json.Unmarshal(data, &accountsFile); err != nil {
			return nil, fmt.Errorf("failed to parse accounts.json: %w", err)
		}

		envData.Accounts = accountsFile.Accounts
	}

	return envData, nil
}

// scanServiceContracts walks the workspace looking for service contract deployment files
func scanServiceContracts(workspacePath, chainID string) (map[string]string, error) {
	addresses := make(map[string]string)

	// Patterns to match:
	// - workspace/*/deployments.json
	// - workspace/*/service_contracts/deployments.json
	err := filepath.Walk(workspacePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip the root deployments.json (handled separately)
		if path == filepath.Join(workspacePath, "deployments.json") {
			return nil
		}

		// Look for deployments.json files in subdirectories
		if info.Name() == "deployments.json" && path != filepath.Join(workspacePath, "deployments.json") {
			data, err := os.ReadFile(path)
			if err != nil {
				fmt.Printf("Warning: Failed to read %s: %v\n", path, err)
				return nil
			}

			// Try to parse as service contract format (map[chainID]map[key]address)
			var serviceFormat map[string]map[string]string
			if err := json.Unmarshal(data, &serviceFormat); err == nil {
				if chainData, ok := serviceFormat[chainID]; ok {
					for key, address := range chainData {
						addresses[key] = address
					}
					fmt.Printf("Loaded %d addresses from service contracts: %s\n", len(chainData), path)
				}
			}
			// If it doesn't parse as service format, skip it (it's likely the base format)
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("error walking workspace: %w", err)
	}

	return addresses, nil
}

// writeEnvFile writes the formatted environment file
func writeEnvFile(outputPath string, envData *EnvironmentData, rpcURL, rpcWsURL, chainID string, extraVars []string) error {
	f, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer f.Close()

	// Header
	fmt.Fprintf(f, "# Generated by filwizard env export\n")
	fmt.Fprintf(f, "# Timestamp: %s\n\n", time.Now().UTC().Format(time.RFC3339))

	// Network section
	fmt.Fprintf(f, "# Network\n")
	fmt.Fprintf(f, "CHAIN_ID=%s\n", chainID)
	if rpcURL != "" {
		fmt.Fprintf(f, "RPC_URL=%s\n", rpcURL)
		fmt.Fprintf(f, "ETH_RPC_URL=%s\n", rpcURL)
	}
	if rpcWsURL != "" {
		fmt.Fprintf(f, "RPC_WS_URL=%s\n", rpcWsURL)
	}
	fmt.Fprintf(f, "\n")

	// Contract Addresses section
	fmt.Fprintf(f, "# Contract Addresses\n")
	keys := make([]string, 0, len(envData.Addresses))
	for k := range envData.Addresses {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(f, "%s=%s\n", k, envData.Addresses[k])
	}
	fmt.Fprintf(f, "\n")

	// Accounts section
	if len(envData.Accounts) > 0 || envData.DeployerPrivateKey != "" {
		fmt.Fprintf(f, "# Accounts\n")
		if envData.DeployerPrivateKey != "" {
			fmt.Fprintf(f, "DEPLOYER_PRIVATE_KEY=%s\n", envData.DeployerPrivateKey)
		}
		accountKeys := make([]string, 0, len(envData.Accounts))
		for k := range envData.Accounts {
			accountKeys = append(accountKeys, k)
		}
		sort.Strings(accountKeys)
		for _, k := range accountKeys {
			acc := envData.Accounts[k]
			upperName := strings.ToUpper(k)
			fmt.Fprintf(f, "%s_PRIVATE_KEY=%s\n", upperName, acc.PrivateKey)
			fmt.Fprintf(f, "%s_ETH_ADDRESS=%s\n", upperName, acc.EthAddress)
		}
		fmt.Fprintf(f, "\n")
	}

	// Extra variables section
	if len(extraVars) > 0 {
		fmt.Fprintf(f, "# Extra Variables\n")
		for _, extra := range extraVars {
			// Validate format: KEY=VALUE
			if strings.Contains(extra, "=") {
				fmt.Fprintf(f, "%s\n", extra)
			} else {
				fmt.Printf("Warning: Skipping invalid extra variable (missing =): %s\n", extra)
			}
		}
		fmt.Fprintf(f, "\n")
	}

	return nil
}

// toUpperSnakeCase converts a string to UPPER_SNAKE_CASE
func toUpperSnakeCase(s string) string {
	// Replace hyphens and spaces with underscores
	s = strings.ReplaceAll(s, "-", "_")
	s = strings.ReplaceAll(s, " ", "_")
	// Convert to uppercase
	return strings.ToUpper(s)
}
