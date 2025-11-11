# Mempool Operations

Test transaction throughput and mempool behavior under various conditions.

## Send a Single Transaction

```bash
filwizard mempool send <from> <to> <amount>

# Wait for confirmation
filwizard mempool send <from> <to> <amount> --wait

# Example
filwizard mempool send f410fx... f410fy... 1.5 --wait
```

## Spam Transactions

Generate high-volume transaction load for stress testing:

```bash
# Send 1000 transactions with default settings
filwizard mempool spam --count 1000

# Advanced spam with custom parameters
filwizard mempool spam \
  --count 5000 \
  --amount 0.1 \
  --concurrent 10 \
  --min-balance 5 \
  --refill-amount 50 \
  --wait
```

**Options:**
- `--count <n>`: Number of transactions to send (default: 100)
- `--amount <fil>`: Amount per transaction in FIL (default: "0.1")
- `--concurrent <n>`: Number of concurrent workers (default: 2)
- `--min-balance <fil>`: Minimum wallet balance before refill (default: "1")
- `--refill-amount <fil>`: Amount to refill wallets in FIL (default: "10")
- `--wait`: Wait for transaction confirmations

**Note:** The spam command requires at least 2 wallets. Create wallets first using `filwizard wallet create`.

## Send EIP-1559 Ethereum Transaction

Send Ethereum-style transactions with custom gas parameters:

```bash
filwizard mempool eth \
  --from <address> \
  --to <address> \
  --value 1.0 \
  --data <hex> \
  --gas-limit 100000 \
  --max-fee 1000000000 \
  --max-priority-fee 1000000000 \
  --wait
```

**Options:**
- `--from <address>`: Sender address (required)
- `--to <address>`: Recipient address (required)
- `--value <fil>`: Amount to send in FIL (default: "0")
- `--data <hex>`: Transaction data in hex format
- `--gas-limit <n>`: Gas limit (default: 21000)
- `--max-fee <wei>`: Max fee per gas in wei (default: 1000000000)
- `--max-priority-fee <wei>`: Max priority fee per gas in wei (default: 1000000000)
- `--wait`: Wait for confirmation

## Check Mempool Status

Get current mempool statistics:

```bash
filwizard mempool status
```

