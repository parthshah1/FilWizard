# FilWizard

A comprehensive Filecoin testing tool designed for contract developers, ecosystem teams, and implementation teams to test, deploy, and interact with smart contracts on Filecoin networks. Specifically optimized for deterministic testing on platforms like Antithesis.

## Overview

`FilWizard` is a command-line tool that provides extensive capabilities for:
- **Wallet Management**: Create and manage Filecoin and Ethereum wallets
- **Transaction Testing**: Send individual transactions or spam the mempool with high-volume transaction loads
- **Smart Contract Operations**: Deploy, call, and manage smart contracts (both Foundry and Hardhat projects)
- **Automated Deployment**: Deploy contracts from Git repositories with full automation support
- **Go Bindings Generation**: Generate Go bindings for deployed contracts using abigen

This tool is particularly useful for testing Filecoin implementations in controlled, deterministic environments and stress-testing network behavior under various conditions.

## Table of Contents

- [Installation](#installation)
- [Prerequisites](#prerequisites)
- [Configuration](#configuration)
- [Usage](#usage)
  - [Wallet Operations](#wallet-operations)
  - [Mempool Operations](#mempool-operations)
  - [Contract Operations](#contract-operations)
- [Examples](#examples)
- [Testing on Antithesis](#testing-on-antithesis)
- [Development](#development)

## Installation

### From Source

```bash
# Clone the repository
git clone https://github.com/parthshah1/FilWizard.git
cd FilWizard

# Build the binary
make build

# The binary will be available as ./FilWizard
```

### Prerequisites

Before using `FilWizard`, ensure you have the following installed:

- **Go 1.24.3+**: Required for building the tool
- **Foundry** (optional): Required for deploying Foundry-based contracts
  ```bash
  curl -L https://foundry.paradigm.xyz | bash
  foundryup
  ```
- **Node.js & npm** (optional): Required for deploying Hardhat-based contracts
- **abigen** (optional): Required for generating Go bindings
  ```bash
  go install github.com/ethereum/go-ethereum/cmd/abigen@latest
  ```
- **Access to a Filecoin node**: Either a local node or remote RPC endpoint

## Configuration

`FilWizard` can be configured through environment variables or command-line flags:

### Environment Variables

- `FILECOIN_RPC`: Filecoin RPC URL (e.g., `http://localhost:1234/rpc/v1`)
- `FILECOIN_TOKEN`: Path to JWT token file for authentication (optional)
- `VERBOSE`: Enable verbose output (default: `false`)

### Command-Line Flags

Global flags available for all commands:

```bash
--rpc <url>      # Filecoin RPC URL
--token <path>   # JWT token file path
--verbose        # Enable verbose output
```

## Usage

### Wallet Operations

The wallet management system supports both Filecoin and Ethereum address formats.

#### Create Wallets

Create new wallets with optional funding:

```bash
# Create a single Filecoin wallet
FilWizard wallet create

# Create multiple Ethereum wallets with private keys shown
FilWizard wallet create --count 10 --type ethereum --show-private-key

# Create and fund wallets
FilWizard wallet create --count 5 --type ethereum --fund 100

# Create BLS wallet
FilWizard wallet create --type filecoin --key-type bls
```

**Options:**
- `--count <n>`: Number of wallets to create (default: 1)
- `--type <type>`: Wallet type: `filecoin` or `ethereum` (default: `filecoin`)
- `--key-type <type>`: Key type for Filecoin wallets: `secp256k1` or `bls` (default: `secp256k1`)
- `--fund <amount>`: Amount to fund each wallet in FIL
- `--show-private-key`: Display private keys (for Ethereum wallets)

#### List Wallets

Display all wallets with their balances:

```bash
FilWizard wallet list
```

#### Fund a Wallet

Send FIL to a specific wallet:

```bash
FilWizard wallet fund <address> <amount>

# Example
FilWizard wallet fund f410fx... 50
```

#### Check Balance

Get the balance of a specific wallet:

```bash
FilWizard wallet balance <address>

# Example
FilWizard wallet balance f410fx...
```

### Mempool Operations

Test transaction throughput and mempool behavior under various conditions.

#### Send a Single Transaction

```bash
FilWizard mempool send <from> <to> <amount>

# Wait for confirmation
FilWizard mempool send <from> <to> <amount> --wait

# Example
FilWizard mempool send f410fx... f410fy... 1.5 --wait
```

#### Spam Transactions

Generate high-volume transaction load for stress testing:

```bash
# Send 1000 transactions with default settings
FilWizard mempool spam --count 1000

# Advanced spam with custom parameters
FilWizard mempool spam \
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

**Note:** The spam command requires at least 2 wallets. Create wallets first using `FilWizard wallet create`.

#### Send EIP-1559 Ethereum Transaction

Send Ethereum-style transactions with custom gas parameters:

```bash
FilWizard mempool eth \
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

#### Check Mempool Status

Get current mempool statistics:

```bash
FilWizard mempool status
```

### Contract Operations

Comprehensive smart contract deployment and interaction capabilities.

#### Deploy Contract from Hex File

Deploy a pre-compiled contract from a hex file:

```bash
FilWizard contract deploy <contract-file.hex> \
  --contract-name MyContract \
  --deployer <address> \
  --fund 10 \
  --bindings \
  --workspace ./workspace
```

**Options:**
- `--deployer <address>`: Deployer wallet address (creates new if not specified)
- `--fund <amount>`: Amount to fund deployer wallet in FIL (default: "10")
- `--value <amount>`: Value to send with deployment in FIL (default: "0")
- `--bindings`: Generate Go bindings using abigen
- `--workspace <path>`: Workspace directory for artifacts (default: "./workspace")
- `--contract-name <name>`: Name of the contract
- `--abi <path>`: Path to ABI file (optional)

#### Deploy Contract from Git Repository

Clone and deploy contracts directly from Git repositories (supports both Foundry and Hardhat projects):

```bash
# Deploy a Foundry contract
FilWizard contract from-git \
  --git-url https://github.com/username/project.git \
  --git-ref main \
  --project-type foundry \
  --main-contract SimpleCoin \
  --contract-path contracts/SimpleCoin.sol \
  --constructor-args "1000,MyToken" \
  --create-deployer \
  --bindings

# Deploy with custom deployment script
FilWizard contract from-git \
  --git-url https://github.com/username/hardhat-project.git \
  --project-type hardhat \
  --deploy-script scripts/deploy.sh \
  --env NODE_ENV=production \
  --deployer-key 0x1234...
```

**Options:**
- `--git-url <url>`: Git repository URL (required)
- `--git-ref <ref>`: Git reference (tag, branch, or commit hash)
- `--project-type <type>`: Project type: `foundry` or `hardhat` (default: "foundry")
- `--main-contract <name>`: Main contract name to deploy
- `--contract-path <path>`: Relative path to contract file
- `--constructor-args <args>`: Constructor arguments (comma-separated)
- `--workspace <path>`: Workspace directory (default: "./workspace")
- `--rpc-url <url>`: RPC URL for deployment (default: "http://localhost:1234/rpc/v1")
- `--create-deployer`: Create a new deployer account
- `--deployer-key <key>`: Private key for deployment (if not creating new)
- `--deploy-script <path>`: Custom deployment script to run
- `--env <key=value>`: Environment variables (can be specified multiple times)
- `--commands <cmds>`: Shell commands to run after cloning (semicolon-separated)
- `--bindings`: Generate Go bindings

#### Deploy Contracts from Configuration File

For air-gapped or batch deployment scenarios, use the configuration-based workflow:

**Step 1: Clone repositories**

```bash
FilWizard contract clone-config \
  --config config/contracts.json \
  --workspace ./workspace
```

**Step 2: Deploy from local clones**

```bash
FilWizard contract deploy-local \
  --config config/contracts.json \
  --workspace ./workspace \
  --rpc-url http://localhost:1234/rpc/v1 \
  --create-deployer \
  --bindings
```

**Configuration file format** (`config/contracts.json`):

```json
{
  "contracts": [
    {
      "name": "SimpleCoin",
      "project_type": "foundry",
      "git_url": "https://github.com/username/project.git",
      "git_ref": "main",
      "contract_path": "contracts/SimpleCoin.sol",
      "main_contract": "SimpleCoin",
      "constructor_args": ["1000"]
    },
    {
      "name": "PDPVerifier",
      "project_type": "foundry",
      "git_url": "https://github.com/FilOzone/pdp.git",
      "git_ref": "main",
      "contract_path": "src/PDPVerifier.sol",
      "main_contract": "PDPVerifier",
      "constructor_args": []
    }
  ]
}
```

#### Call Contract Methods

Interact with deployed contracts:

```bash
# Read-only call (view/pure functions)
FilWizard contract call \
  --contract 0x1234... \
  --method balanceOf \
  --args "0xabcd..." \
  --types "address" \
  --rpc-url http://localhost:1234/rpc/v1

# State-changing transaction
FilWizard contract call \
  --contract 0x1234... \
  --method transfer \
  --args "0xabcd...,100" \
  --types "address,uint256" \
  --transaction \
  --private-key 0x5678... \
  --gas-limit 100000
```

**Options:**
- `--contract <address>`: Contract address (required)
- `--method <name>`: Method name to call (required)
- `--args <values>`: Method arguments (comma-separated)
- `--types <types>`: Argument types (comma-separated): `address`, `uint256`, `bool`, `string`
- `--rpc-url <url>`: RPC URL (default: "http://localhost:1234/rpc/v1")
- `--transaction`: Send as transaction (for state-changing functions)
- `--private-key <key>`: Private key for signing (hex format, 0x prefix optional)
- `--gas-limit <n>`: Gas limit for transaction (0 = auto-estimate)

#### List Deployed Contracts

View all contracts deployed through FilWizard:

```bash
FilWizard contract list --workspace ./workspace
```

#### Get Contract Information

Get detailed information about a deployed contract:

```bash
FilWizard contract info <contract-name> --workspace ./workspace
```

#### Cleanup

Remove temporary project directories:

```bash
FilWizard contract cleanup --workspace ./workspace
```

## Examples

### Complete Workflow Example

Here's a complete example workflow for testing a DApp on Filecoin:

```bash
# 1. Create test wallets
FilWizard --rpc http://localhost:1234/rpc/v1 wallet create \
  --count 10 \
  --type ethereum \
  --fund 100 \
  --show-private-key

# 2. Deploy a contract from Git
FilWizard contract from-git \
  --git-url https://github.com/parthshah1/fevm-kit.git \
  --git-ref main \
  --project-type foundry \
  --main-contract SimpleCoin \
  --contract-path contracts/basic-solidity-examples/SimpleCoin.sol \
  --constructor-args "1000000" \
  --create-deployer \
  --bindings \
  --workspace ./workspace

# 3. Get the deployed contract address
FilWizard contract list --workspace ./workspace

# 4. Call a contract method
FilWizard contract call \
  --contract 0x... \
  --method totalSupply \
  --rpc-url http://localhost:1234/rpc/v1

# 5. Send transactions to interact with the contract
FilWizard contract call \
  --contract 0x... \
  --method transfer \
  --args "0xrecipient...,1000" \
  --types "address,uint256" \
  --transaction \
  --private-key 0xdeployer_key... \
  --gas-limit 100000

# 6. Stress test the network
FilWizard mempool spam \
  --count 10000 \
  --amount 0.01 \
  --concurrent 20
```

### Batch Contract Deployment

For deploying multiple contracts in sequence:

```bash
# Create a deployer account once
FilWizard contract from-git \
  --git-url https://github.com/project1/contracts.git \
  --create-deployer \
  --main-contract Token \
  --contract-path contracts/Token.sol

# Use the same deployer for subsequent deployments
# (The deployer key will be in workspace/deployments.json)
FilWizard contract from-git \
  --git-url https://github.com/project2/contracts.git \
  --deployer-key 0x... \
  --main-contract NFT \
  --contract-path contracts/NFT.sol
```

### Testing with Configuration

For reproducible testing environments, use the configuration-based approach:

1. Create `config/contracts.json` with your contracts
2. Clone all repositories: `FilWizard contract clone-config`
3. Deploy all contracts: `FilWizard contract deploy-local --create-deployer`

This approach is ideal for:
- **Air-gapped environments** where internet access is restricted
- **Deterministic testing** on platforms like Antithesis
- **Batch deployments** with consistent configuration

## Testing on Antithesis

`FilWizard` is specifically designed to work in deterministic testing environments like Antithesis. Here's how to use it effectively:

### Antithesis Integration

Antithesis provides deterministic replay capabilities for finding and reproducing bugs. When using `FilWizard` in Antithesis:

1. **Deterministic Wallet Creation**: Use seeded wallet creation to ensure reproducible addresses
2. **Configuration-Based Deployment**: Use the `config/contracts.json` approach to ensure consistent contract deployments
3. **Mempool Testing**: Use the spam functionality to test network behavior under load
4. **State Verification**: Use contract call methods to verify state at various points

### Example Antithesis Test Scenario

```bash
# Setup phase
export FILECOIN_RPC=http://filecoin-node:1234/rpc/v1

# Create predictable wallets
FilWizard wallet create --count 20 --type ethereum --fund 1000

# Deploy test contracts from configuration
FilWizard contract clone-config --workspace /test-workspace
FilWizard contract deploy-local \
  --workspace /test-workspace \
  --create-deployer

# Execute test scenario
FilWizard mempool spam --count 10000 --concurrent 50

# Verify contract state
for contract in $(FilWizard contract list --workspace /test-workspace); do
  FilWizard contract call --contract $contract --method verify
done
```

### Best Practices for Antithesis

- **Use configuration files** instead of command-line arguments for reproducibility
- **Pre-fund wallets** during setup to avoid balance issues during testing
- **Enable verbose logging** (`--verbose`) to capture detailed execution traces
- **Use the workspace directory** to maintain deployment artifacts across test runs
- **Generate bindings** to enable programmatic interaction with contracts in Go test code

## Development

### Project Structure

```
FilWizard/
├── cmd/                    # Command implementations
│   ├── root.go            # Root command and CLI setup
│   ├── wallet.go          # Wallet operations
│   ├── mempool.go         # Mempool/transaction operations
│   ├── contract.go        # Contract deployment commands
│   └── manager.go         # Contract management logic
├── client/                # Filecoin client wrapper
├── config/                # Configuration management
│   ├── config.go          # Config loading
│   ├── contracts.json     # Default contract configuration
│   └── contract_wrapper.go # Contract interaction wrapper
├── main.go                # Entry point
├── Makefile               # Build configuration
└── README.md              # This file
```

### Building

```bash
# Build the binary
make build

# Clean build artifacts
make clean
```

### Adding New Commands

To add new commands, follow the pattern in existing command files:

1. Create your command structure using `urfave/cli/v2`
2. Add it to the `Commands` slice in `cmd/root.go`
3. Implement the command logic
4. Update this README with documentation

### Dependencies

Key dependencies:
- `github.com/filecoin-project/lotus`: Filecoin node API
- `github.com/ethereum/go-ethereum`: Ethereum libraries for contract interaction
- `github.com/urfave/cli/v2`: CLI framework

## Contributing

Contributions are welcome! Please ensure your changes:
- Follow Go best practices
- Include appropriate documentation
- Add examples for new features
- Update this README

## License

[Add license information here]

## Support

For issues, questions, or contributions, please visit the [GitHub repository](https://github.com/parthshah1/FilWizard).

---

**Note**: This tool is designed for testing purposes. Use caution when deploying contracts or sending transactions on production networks.
