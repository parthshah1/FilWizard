# Wallet Operations

The wallet management system supports both Filecoin and Ethereum address formats.

## Create Wallets

Create new wallets with optional funding:

```bash
# Create a single Filecoin wallet
filwizard wallet create

# Create multiple Ethereum wallets with private keys shown
filwizard wallet create --count 10 --type ethereum --show-private-key

# Create and fund wallets
filwizard wallet create --count 5 --type ethereum --fund 100

# Create BLS wallet
filwizard wallet create --type filecoin --key-type bls
```

**Options:**
- `--count <n>`: Number of wallets to create (default: 1)
- `--type <type>`: Wallet type: `filecoin` or `ethereum` (default: `filecoin`)
- `--key-type <type>`: Key type for Filecoin wallets: `secp256k1` or `bls` (default: `secp256k1`)
- `--fund <amount>`: Amount to fund each wallet in FIL
- `--show-private-key`: Display private keys (for Ethereum wallets)

## List Wallets

Display all wallets with their balances:

```bash
filwizard wallet list
```

## Fund a Wallet

Send FIL to a specific wallet:

```bash
filwizard wallet fund <address> <amount>

# Example
filwizard wallet fund f410fx... 50
```

## Check Balance

Get the balance of a specific wallet:

```bash
filwizard wallet balance <address>

# Example
filwizard wallet balance f410fx...
```

