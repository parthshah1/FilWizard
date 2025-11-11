# Contract Operations

Comprehensive smart contract deployment and interaction capabilities.

## Deploy Contract from Hex File

Deploy a pre-compiled contract from a hex file:

```bash
filwizard contract deploy <contract-file.hex> \
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

## Deploy Contract from Git Repository

Clone and deploy contracts directly from Git repositories (supports both Foundry and Hardhat projects):

```bash
# Deploy a Foundry contract
filwizard contract from-git \
  --git-url https://github.com/username/project.git \
  --git-ref main \
  --project-type foundry \
  --main-contract SimpleCoin \
  --contract-path contracts/SimpleCoin.sol \
  --constructor-args "1000,MyToken" \
  --create-deployer \
  --bindings

# Deploy with custom deployment script
filwizard contract from-git \
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

## Deploy Contracts from Configuration File

For advanced deployments with dependencies, post-deployment actions, and custom scripts, see the [Configuration System Guide](configuration.md).

**Quick Start:**

```bash
# Step 1: Clone repositories
filwizard contract clone-config \
  --config config/contracts.json \
  --workspace ./workspace

# Step 2: Deploy from local clones
filwizard contract deploy-local \
  --config config/contracts.json \
  --workspace ./workspace \
  --rpc-url http://localhost:1234/rpc/v1 \
  --create-deployer \
  --bindings
```

## Call Contract Methods

Interact with deployed contracts using the universal contract interaction system:

### Read-only calls (view/pure functions)

```bash
# Using contract name from deployments
filwizard contract call read Token balanceOf 0xabcd...

# Using contract address directly
filwizard contract call read \
  --contract 0x1234... \
  --method balanceOf \
  --args "0xabcd..." \
  --types "address" \
  --rpc-url http://localhost:1234/rpc/v1
```

### State-changing transactions

```bash
# Using contract name with account role
filwizard contract call write Token transfer \
  --from deployer \
  --args "0xrecipient...,1000"

# Using contract address with private key
filwizard contract call write \
  --contract 0x1234... \
  --method transfer \
  --args "0xabcd...,100" \
  --types "address,uint256" \
  --private-key 0x5678... \
  --gas-limit 100000
```

**Options for `read` subcommand:**
- Contract name or address (positional argument)
- Method name (positional argument)
- Method arguments (positional arguments, auto-detected types)

**Options for `write` subcommand:**
- `--from <role>`: Account role to send from (creates new if doesn't exist)
- `--fund <amount>`: Amount to fund new accounts in FIL (default: "1")
- `--gas <n>`: Gas limit (0 = auto-estimate, default: 0)
- Contract name or address (positional argument)
- Method name (positional argument)
- Method arguments (positional arguments, auto-detected types)

**Legacy Options (still supported):**
- `--contract <address>`: Contract address (required if not using positional)
- `--method <name>`: Method name to call (required if not using positional)
- `--args <values>`: Method arguments (comma-separated)
- `--types <types>`: Argument types (comma-separated): `address`, `uint256`, `bool`, `string`, `bytes`
- `--rpc-url <url>`: RPC URL (default: "http://localhost:1234/rpc/v1")
- `--transaction`: Send as transaction (for state-changing functions)
- `--private-key <key>`: Private key for signing (hex format, 0x prefix optional)
- `--gas-limit <n>`: Gas limit for transaction (0 = auto-estimate)

**Note:** The new `read`/`write` subcommands support automatic type detection, making contract interaction simpler. The legacy `--contract`, `--method`, `--args`, `--types` flags are still supported for backward compatibility.

## List Deployed Contracts

View all contracts deployed through filwizard:

```bash
filwizard contract list --workspace ./workspace
```

## Get Contract Information

Get detailed information about a deployed contract:

```bash
filwizard contract info <contract-name> --workspace ./workspace
```

## Cleanup

Remove temporary project directories:

```bash
filwizard contract cleanup --workspace ./workspace
```

## How to Deploy Smart Contracts

FilWizard provides multiple ways to deploy smart contracts, allowing you to choose the approach that best fits your needs:

### 1. Simple Single Contract Deployment

For deploying a single contract from a Git repository:

```bash
filwizard contract from-git \
  --git-url https://github.com/user/contract.git \
  --main-contract MyContract \
  --contract-path src/MyContract.sol \
  --create-deployer \
  --bindings
```

### 2. Configuration-Based Deployment (Recommended)

For complex deployments with multiple contracts, dependencies, and post-deployment actions, see the [Configuration System Guide](configuration.md).

### 3. Custom Deployment Scripts

For deployments that require custom logic or multiple contracts, see the [Configuration System Guide](configuration.md#custom-deployment-scripts).

### 4. Air-Gapped Deployment

For environments without internet access:

```bash
# On a machine with internet access:
filwizard contract clone-config --config config/contracts.json

# Copy workspace directory to air-gapped machine, then:
filwizard contract deploy-local \
  --config config/contracts.json \
  --workspace ./workspace \
  --create-deployer
```

### 5. Import Existing Deployments

Import contract addresses from external deployment scripts:

```bash
filwizard contract deploy-local \
  --config config/contracts.json \
  --import-output /path/to/deployment-output.txt
```

The import system automatically parses addresses from script output and makes them available for subsequent deployments.

