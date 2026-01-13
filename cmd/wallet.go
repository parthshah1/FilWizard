package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/filecoin-project/go-address"
	"github.com/filecoin-project/go-state-types/abi"
	"github.com/filecoin-project/go-state-types/big"
	"github.com/filecoin-project/lotus/chain/types"
	"github.com/filecoin-project/lotus/chain/types/ethtypes"
	"github.com/filecoin-project/lotus/chain/wallet/key"
	_ "github.com/filecoin-project/lotus/lib/sigs/delegated"
	_ "github.com/filecoin-project/lotus/lib/sigs/secp"

	"github.com/urfave/cli/v2"
)

// CreateWallet creates a new wallet with the specified key type
func CreateWallet(ctx context.Context, keyType types.KeyType) (address.Address, error) {
	addr, err := clientt.GetAPI().WalletNew(ctx, keyType)
	if err != nil {
		return address.Undef, fmt.Errorf("failed to create wallet: %w", err)
	}
	return addr, nil
}

// ListWallets returns all wallets
func ListWallets(ctx context.Context) ([]address.Address, error) {
	addrs, err := clientt.GetAPI().WalletList(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list wallets: %w", err)
	}
	return addrs, nil
}

// NewAccount generates a new secp256k1 key pair and returns the private key, Ethereum address, and Filecoin address.
func NewAccount() (*key.Key, ethtypes.EthAddress, address.Address, error) {
	key, err := key.GenerateKey(types.KTSecp256k1)
	if err != nil {
		return nil, ethtypes.EthAddress{}, address.Address{}, fmt.Errorf("failed to generate key: %w", err)
	}

	ethAddr, err := ethtypes.EthAddressFromPubKey(key.PublicKey)
	if err != nil {
		return nil, ethtypes.EthAddress{}, address.Address{}, fmt.Errorf("failed to generate Ethereum address: %w", err)
	}

	ea, err := ethtypes.CastEthAddress(ethAddr)
	if err != nil {
		return nil, ethtypes.EthAddress{}, address.Address{}, fmt.Errorf("failed to cast Ethereum address: %w", err)
	}

	addr, err := ea.ToFilecoinAddress()
	if err != nil {
		return nil, ethtypes.EthAddress{}, address.Address{}, fmt.Errorf("failed to convert Ethereum address to Filecoin address: %w", err)
	}
	return key, *(*ethtypes.EthAddress)(ethAddr), addr, nil
}

func appendEthereumKeyToJSONFile(path string, name string, key *key.Key, ethAddr ethtypes.EthAddress, filAddr address.Address) error {
	if key == nil {
		return fmt.Errorf("key is nil")
	}

	// Read existing file or create new structure
	var accountsFile AccountsFile
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// File doesn't exist, create new structure
			accountsFile = AccountsFile{
				Accounts: make(map[string]AccountInfo),
			}
		} else {
			return fmt.Errorf("failed to read file: %w", err)
		}
	} else {
		// Parse existing JSON
		if err := json.Unmarshal(data, &accountsFile); err != nil {
			return fmt.Errorf("failed to parse JSON: %w", err)
		}
		if accountsFile.Accounts == nil {
			accountsFile.Accounts = make(map[string]AccountInfo)
		}
	}

	// Check if name already exists
	if _, exists := accountsFile.Accounts[name]; exists {
		return fmt.Errorf("account with name '%s' already exists", name)
	}

	// Add new account
	accountsFile.Accounts[name] = AccountInfo{
		Address:    filAddr.String(),
		EthAddress: ethAddr.String(),
		PrivateKey: fmt.Sprintf("0x%x", key.PrivateKey),
	}

	// Write back to file
	updatedData, err := json.MarshalIndent(accountsFile, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %w", err)
	}

	if err := os.WriteFile(path, updatedData, 0600); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	return nil
}

func CreateEthereumWallet(ctx context.Context, fund bool) (address.Address, error) {
	_, ethAddr, addr, err := NewAccount()
	if err != nil {
		return address.Undef, fmt.Errorf("failed to create account: %w", err)
	}
	if fund {
		_, err := FundWallet(ctx, addr, types.BigMul(types.NewInt(1e18), types.NewInt(1)), true)
		if err != nil {
			return address.Undef, fmt.Errorf("failed to fund wallet: %w", err)
		}
	}
	fmt.Printf("Ethereum Address: %s\n", ethAddr)
	fmt.Printf("Filecoin Address: %s\n", addr)
	return addr, nil
}

// GetBalance returns the balance of a wallet
func GetBalance(ctx context.Context, addr address.Address) (abi.TokenAmount, error) {
	balance, err := clientt.GetAPI().WalletBalance(ctx, addr)
	if err != nil {
		return big.Zero(), fmt.Errorf("failed to get balance for %s: %w", addr, err)
	}
	return balance, nil
}

// FundWallet sends funds to a wallet from the default wallet
func FundWallet(ctx context.Context, to address.Address, amount abi.TokenAmount, waitForConfirm bool) (*types.SignedMessage, error) {
	defaultAddr, err := clientt.GetAPI().WalletDefaultAddress(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get default wallet: %w", err)
	}

	// Create message
	msg := &types.Message{
		From:  defaultAddr,
		To:    to,
		Value: amount,
	}

	// Send message
	smsg, err := clientt.GetAPI().MpoolPushMessage(ctx, msg, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to send funds: %w", err)
	}

	if waitForConfirm {
		// Wait for message to be included in a block
		_, err = clientt.GetAPI().StateWaitMsg(ctx, smsg.Cid(), 5, abi.ChainEpoch(-1), true)
		if err != nil {
			return smsg, fmt.Errorf("failed to wait for message confirmation: %w", err)
		}
	}

	return smsg, nil
}

var WalletCmd = &cli.Command{
	Name:  "wallet",
	Usage: "Wallet operations",
	Subcommands: []*cli.Command{
		{
			Name:  "create",
			Usage: "Create wallets",
			Flags: []cli.Flag{
				&cli.IntFlag{
					Name:  "count",
					Value: 1,
					Usage: "Number of wallets to create",
				},
				&cli.StringFlag{
					Name:  "type",
					Value: "filecoin",
					Usage: "Wallet type: filecoin or ethereum",
				},
				&cli.StringFlag{
					Name:  "key-type",
					Value: "secp256k1",
					Usage: "Key type for Filecoin wallets (secp256k1, bls)",
				},
				&cli.StringFlag{
					Name:  "fund",
					Usage: "Amount to fund each wallet (FIL)",
				},
				&cli.BoolFlag{
					Name:  "show-private-key",
					Usage: "Show private key in output (for Ethereum wallets)",
				},
				&cli.StringFlag{
					Name:  "key-output",
					Usage: "JSON file to append generated accounts (Ethereum wallets)",
				},
				&cli.StringFlag{
					Name:  "name",
					Usage: "Account name for generated wallet (required with --key-output)",
				},
			},
			Action: func(c *cli.Context) error {
				ctx := context.Background()

				count := c.Int("count")
				walletType := c.String("type")
				keyTypeStr := c.String("key-type")
				fundAmountStr := c.String("fund")
				showPrivateKey := c.Bool("show-private-key")

				// Parse fund amount if provided
				var fundAmount abi.TokenAmount
				if fundAmountStr != "" {
					amount, err := big.FromString(fundAmountStr)
					if err != nil {
						return fmt.Errorf("invalid fund amount '%s': %w", fundAmountStr, err)
					}
					fundAmount = types.BigMul(amount, types.NewInt(1e18))
				}

				if walletType == "ethereum" {
					keyOutput := c.String("key-output")
					accountName := c.String("name")

					// Validate name is provided when key-output is specified
					if keyOutput != "" && accountName == "" {
						return fmt.Errorf("--name is required when using --key-output")
					}

					fmt.Printf("Creating %d Ethereum wallet(s):\n", count)

					for i := 0; i < count; i++ {
						key, ethAddr, filAddr, err := NewAccount()
						if err != nil {
							return fmt.Errorf("failed to create wallet %d: %w", i+1, err)
						}

						fmt.Printf("\nWallet %d:\n", i+1)
						fmt.Printf("  Ethereum Address: %s\n", ethAddr)
						fmt.Printf("  Filecoin Address: %s\n", filAddr)

						if showPrivateKey {
							fmt.Printf("  Private Key: %x\n", key.PrivateKey)
						}

						if keyOutput != "" {
							// Generate account name with suffix for multiple wallets
							name := accountName
							if count > 1 {
								name = fmt.Sprintf("%s_%d", accountName, i+1)
							}
							if err := appendEthereumKeyToJSONFile(keyOutput, name, key, ethAddr, filAddr); err != nil {
								return fmt.Errorf("failed to write key to %s: %w", keyOutput, err)
							}
							fmt.Printf("  Saved account '%s' to %s\n", name, keyOutput)
						}

						// Fund wallet if amount specified
						if !fundAmount.IsZero() {
							_, err := FundWallet(ctx, filAddr, fundAmount, true)
							if err != nil {
								fmt.Printf("  Warning: failed to fund wallet: %v\n", err)
							} else {
								fmt.Printf("  Funded with %s FIL\n", fundAmountStr)
							}
						}
					}

					fmt.Printf("\nSuccessfully created %d Ethereum wallet(s)\n", count)
				} else {
					// Create Filecoin wallets
					// Parse key type
					var keyType types.KeyType
					switch keyTypeStr {
					case "secp256k1":
						keyType = types.KTSecp256k1
					case "bls":
						keyType = types.KTBLS
					default:
						return fmt.Errorf("invalid key type: %s (use secp256k1 or bls)", keyTypeStr)
					}

					// Create wallets
					createdWallets := make([]address.Address, 0, count)
					for i := 0; i < count; i++ {
						addr, err := CreateWallet(ctx, keyType)
						if err != nil {
							return fmt.Errorf("failed to create wallet %d: %w", i+1, err)
						}
						createdWallets = append(createdWallets, addr)
						fmt.Printf("Created wallet %d: %s\n", i+1, addr)

						// Fund wallet if amount specified
						if !fundAmount.IsZero() {
							smsg, err := FundWallet(ctx, addr, fundAmount, true)
							if err != nil {
								fmt.Printf("Warning: failed to fund wallet %s: %v\n", addr, err)
							} else {
								fmt.Printf("Funded wallet %s with %s FIL (tx: %s)\n", addr, fundAmountStr, smsg.Cid())
							}
						}
					}

					fmt.Printf("\nSuccessfully created %d %s wallet(s)\n", len(createdWallets), walletType)
				}
				return nil
			},
		},
		{
			Name:  "list",
			Usage: "List wallets",
			Action: func(c *cli.Context) error {
				ctx := context.Background()

				wallets, err := ListWallets(ctx)
				if err != nil {
					return err
				}

				if len(wallets) == 0 {
					fmt.Println("No wallets found")
					return nil
				}

				fmt.Printf("Found %d wallet(s):\n", len(wallets))
				for i, addr := range wallets {
					balance, err := GetBalance(ctx, addr)
					if err != nil {
						fmt.Printf("%d. %s (balance: error - %v)\n", i+1, addr, err)
					} else {
						// Convert attoFIL to FIL for display
						filBalance := types.BigDiv(balance, types.NewInt(1e18))
						fmt.Printf("%d. %s (balance: %s FIL)\n", i+1, addr, filBalance.String())
					}
				}
				return nil
			},
		},
		{
			Name:      "fund",
			Usage:     "Fund a wallet",
			ArgsUsage: "<address> <amount>",
			Action: func(c *cli.Context) error {
				if c.NArg() != 2 {
					return fmt.Errorf("expected 2 arguments: <address> <amount>")
				}

				ctx := context.Background()

				addr, err := address.NewFromString(c.Args().Get(0))
				if err != nil {
					return fmt.Errorf("invalid address: %w", err)
				}

				amountStr := c.Args().Get(1)
				amount, err := big.FromString(amountStr)
				if err != nil {
					return fmt.Errorf("invalid amount '%s': %w", amountStr, err)
				}

				fundAmount := types.BigMul(amount, types.NewInt(1e18))

				smsg, err := FundWallet(ctx, addr, fundAmount, true)
				if err != nil {
					return err
				}

				fmt.Printf("Funded wallet %s with %s FIL\n", addr, amountStr)
				fmt.Printf("Transaction CID: %s\n", smsg.Cid())
				return nil
			},
		},
		{
			Name:      "balance",
			Usage:     "Get wallet balance",
			ArgsUsage: "<address>",
			Action: func(c *cli.Context) error {
				if c.NArg() != 1 {
					return fmt.Errorf("expected 1 argument: <address>")
				}

				ctx := context.Background()

				addr, err := address.NewFromString(c.Args().Get(0))
				if err != nil {
					return fmt.Errorf("invalid address: %w", err)
				}

				balance, err := GetBalance(ctx, addr)
				if err != nil {
					return err
				}

				// Convert attoFIL to FIL for display
				filBalance := types.BigDiv(balance, types.NewInt(1e18))
				fmt.Printf("Balance for %s: %s FIL (%s attoFIL)\n", addr, filBalance.String(), balance.String())
				return nil
			},
		},
	},
}
