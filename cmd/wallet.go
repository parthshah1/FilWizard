package cmd

import (
	"context"
	"fmt"
	"log"
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

// Returns the private key, Ethereum address, and Filecoin address
func NewAccount() (*key.Key, ethtypes.EthAddress, address.Address) {
	// Generate a secp256k1 key; this will back the Ethereum identity.
	key, err := key.GenerateKey(types.KTSecp256k1)
	if err != nil {
		log.Printf("Failed to generate key: %v", err)
		return nil, ethtypes.EthAddress{}, address.Address{}
	}

	ethAddr, err := ethtypes.EthAddressFromPubKey(key.PublicKey)
	if err != nil {
		log.Printf("Failed to generate Ethereum address: %v", err)
		return nil, ethtypes.EthAddress{}, address.Address{}
	}

	ea, err := ethtypes.CastEthAddress(ethAddr)
	if err != nil {
		log.Printf("Failed to cast Ethereum address: %v", err)
		return nil, ethtypes.EthAddress{}, address.Address{}
	}

	addr, err := ea.ToFilecoinAddress()
	if err != nil {
		log.Printf("Failed to convert Ethereum address to Filecoin address: %v", err)
		return nil, ethtypes.EthAddress{}, address.Address{}
	}
	return key, *(*ethtypes.EthAddress)(ethAddr), addr
}

func appendEthereumKeyToFile(path string, key *key.Key, ethAddr ethtypes.EthAddress, filAddr address.Address) error {
	if key == nil {
		return fmt.Errorf("key is nil")
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = fmt.Fprintf(file, "Ethereum Address: %s\nFilecoin Address: %s\nPrivate Key: 0x%x\n---\n", ethAddr.String(), filAddr.String(), key.PrivateKey)
	return err
}

func CreateEthereumWallet(ctx context.Context, fund bool) (address.Address, error) {
	key, ethAddr, addr := NewAccount()
	if fund {
		_, err := FundWallet(ctx, addr, types.BigMul(types.NewInt(1e18), types.NewInt(1)), true)
		if err != nil {
			return address.Undef, fmt.Errorf("failed to fund wallet: %w", err)
		}
	}
	log.Printf("Private Key: %s", key.PrivateKey)
	log.Printf("Ethereum Address: %s", ethAddr)
	log.Printf("Filecoin Address: %s", addr)
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
					Usage: "File to append generated private keys (Ethereum wallets)",
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
					amount, _ := big.FromString(fundAmountStr)
					// Convert FIL to attoFIL
					fundAmount = types.BigMul(amount, types.NewInt(1e18))
				}

				if walletType == "ethereum" {
					keyOutput := c.String("key-output")
					// Create Ethereum wallets
					fmt.Printf("Creating %d Ethereum wallet(s):\n", count)

					for i := 0; i < count; i++ {
						key, ethAddr, filAddr := NewAccount()
						if key == nil {
							return fmt.Errorf("failed to create wallet %d", i+1)
						}

						fmt.Printf("\nWallet %d:\n", i+1)
						fmt.Printf("  Ethereum Address: %s\n", ethAddr)
						fmt.Printf("  Filecoin Address: %s\n", filAddr)

						if showPrivateKey {
							fmt.Printf("  Private Key: %x\n", key.PrivateKey)
						}

						if keyOutput != "" {
							if err := appendEthereumKeyToFile(keyOutput, key, ethAddr, filAddr); err != nil {
								return fmt.Errorf("failed to write key to %s: %w", keyOutput, err)
							}
							fmt.Printf("  Saved key material to %s\n", keyOutput)
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
				amount, _ := big.FromString(amountStr)

				// Convert FIL to attoFIL
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
