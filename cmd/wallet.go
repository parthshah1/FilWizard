package cmd

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/crypto"
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

// CreateEthKeystore creates an Ethereum keystore file from a private key
// Returns the path to the created keystore file and the address
func CreateEthKeystore(privateKey *ecdsa.PrivateKey, password string, outputDir string) (string, string, error) {
	if err := os.MkdirAll(outputDir, 0700); err != nil {
		return "", "", fmt.Errorf("failed to create output directory: %w", err)
	}

	ks := keystore.NewKeyStore(outputDir, keystore.StandardScryptN, keystore.StandardScryptP)

	account, err := ks.ImportECDSA(privateKey, password)
	if err != nil {
		return "", "", fmt.Errorf("failed to create keystore: %w", err)
	}

	// Find the created keystore file
	files, err := os.ReadDir(outputDir)
	if err != nil {
		return "", "", fmt.Errorf("failed to read output directory: %w", err)
	}

	// Find the newest file with the account address in its name
	addrLower := strings.ToLower(account.Address.Hex()[2:])
	var keystoreFile string
	for _, f := range files {
		if strings.Contains(strings.ToLower(f.Name()), addrLower) {
			keystoreFile = filepath.Join(outputDir, f.Name())
			break
		}
	}

	if keystoreFile == "" {
		return "", "", fmt.Errorf("keystore file not found after creation")
	}

	return keystoreFile, account.Address.Hex(), nil
}

// CreateEthKeystoreFromHex creates an Ethereum keystore from a hex-encoded private key
func CreateEthKeystoreFromHex(privateKeyHex string, password string, outputDir string) (string, string, error) {
	privateKeyHex = strings.TrimPrefix(privateKeyHex, "0x")

	privateKeyBytes, err := hex.DecodeString(privateKeyHex)
	if err != nil {
		return "", "", fmt.Errorf("failed to decode private key: %w", err)
	}

	privateKey, err := crypto.ToECDSA(privateKeyBytes)
	if err != nil {
		return "", "", fmt.Errorf("failed to parse private key: %w", err)
	}

	return CreateEthKeystore(privateKey, password, outputDir)
}

// GenerateNewEthKeystore creates a new Ethereum keystore with a fresh key
func GenerateNewEthKeystore(password string, outputDir string) (string, string, string, error) {
	privateKey, err := crypto.GenerateKey()
	if err != nil {
		return "", "", "", fmt.Errorf("failed to generate key: %w", err)
	}

	keystoreFile, address, err := CreateEthKeystore(privateKey, password, outputDir)
	if err != nil {
		return "", "", "", err
	}

	privateKeyHex := hex.EncodeToString(crypto.FromECDSA(privateKey))
	return keystoreFile, address, privateKeyHex, nil
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
		{
			Name:  "eth-keystore",
			Usage: "Create Ethereum keystore file (for use with forge/cast tools)",
			Flags: []cli.Flag{
				&cli.StringFlag{
					Name:     "password",
					Usage:    "Password to encrypt the keystore (required)",
					Required: true,
				},
				&cli.StringFlag{
					Name:  "output",
					Value: ".",
					Usage: "Output directory for keystore file",
				},
				&cli.StringFlag{
					Name:  "private-key",
					Usage: "Existing private key to convert (hex format, with or without 0x prefix). If not provided, generates new key",
				},
				&cli.StringFlag{
					Name:  "from-accounts",
					Usage: "Path to accounts JSON file to extract private key from (use with --account-name)",
				},
				&cli.StringFlag{
					Name:  "account-name",
					Usage: "Account name to extract from accounts JSON file (use with --from-accounts)",
				},
				&cli.BoolFlag{
					Name:  "show-private-key",
					Usage: "Show the private key in output (only for newly generated keys)",
				},
			},
			Action: func(c *cli.Context) error {
				password := c.String("password")
				outputDir := c.String("output")
				privateKeyHex := c.String("private-key")
				fromAccounts := c.String("from-accounts")
				accountName := c.String("account-name")
				showPrivateKey := c.Bool("show-private-key")

				// Validate mutually exclusive options
				if privateKeyHex != "" && fromAccounts != "" {
					return fmt.Errorf("--private-key and --from-accounts are mutually exclusive")
				}

				// Handle --from-accounts option
				if fromAccounts != "" {
					if accountName == "" {
						return fmt.Errorf("--account-name is required when using --from-accounts")
					}

					data, err := os.ReadFile(fromAccounts)
					if err != nil {
						return fmt.Errorf("failed to read accounts file: %w", err)
					}

					var accountsFile AccountsFile
					if err := json.Unmarshal(data, &accountsFile); err != nil {
						return fmt.Errorf("failed to parse accounts file: %w", err)
					}

					account, exists := accountsFile.Accounts[accountName]
					if !exists {
						return fmt.Errorf("account '%s' not found in accounts file", accountName)
					}

					privateKeyHex = account.PrivateKey
					fmt.Printf("Extracting key for account '%s' (address: %s)\n", accountName, account.EthAddress)
				}

				if privateKeyHex != "" {
					keystoreFile, address, err := CreateEthKeystoreFromHex(privateKeyHex, password, outputDir)
					if err != nil {
						return err
					}
					fmt.Printf("Created ETH keystore from existing key:\n")
					fmt.Printf("  Address: %s\n", address)
					fmt.Printf("  Keystore: %s\n", keystoreFile)
				} else {
					keystoreFile, address, privKey, err := GenerateNewEthKeystore(password, outputDir)
					if err != nil {
						return err
					}
					fmt.Printf("Generated new ETH keystore:\n")
					fmt.Printf("  Address: %s\n", address)
					fmt.Printf("  Keystore: %s\n", keystoreFile)
					if showPrivateKey {
						fmt.Printf("  Private Key: 0x%s\n", privKey)
					}
				}

				fmt.Printf("\nUsage with forge/cast:\n")
				fmt.Printf("  export ETH_KEYSTORE=<keystore-file-path>\n")
				fmt.Printf("  export PASSWORD=<your-password>\n")
				fmt.Printf("  cast wallet address --password \"$PASSWORD\"\n")

				return nil
			},
		},
	},
}
