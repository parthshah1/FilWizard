# FilWizard

A comprehensive Filecoin testing tool designed for contract developers, ecosystem teams, and implementation teams to test, deploy, and interact with smart contracts on Filecoin networks. Specifically optimized for deterministic testing on platforms like Antithesis.

## Overview

`FilWizard` is a command-line tool that provides extensive capabilities for:
- **Wallet Management**: Create and manage Filecoin and Ethereum wallets
- **Smart Contract Operations**: Deploy, call, and manage smart contracts (both Foundry and Hardhat projects)
- **Advanced Configuration-Based Deployment**: Deploy complex contract ecosystems with dependency management, template variables, post-deployment actions, and custom scripts
- **Automated Deployment**: Deploy contracts from Git repositories with full automation support
- **Go Bindings Generation**: Generate Go bindings for deployed contracts using abigen

This tool is particularly useful for testing Filecoin implementations in controlled, deterministic environments.

### Key Features

**Flexible Contract Deployment:**
- Deploy from Git repositories (Foundry and Hardhat)
- Configuration-based batch deployment with dependency resolution
- Custom deployment scripts with automatic address parsing
- Template variable system for dynamic configuration
- Post-deployment actions for contract initialization
- Environment variable resolution and exports

**Advanced Configuration System:**
- Automatic dependency ordering
- Template variables: `{address:Contract}`, `{env:VAR}`, `{deployment:Contract:field}`
- Post-deployment method calls with type conversion
- Export contract addresses as environment variables
- Support for custom deployment scripts
- Import addresses from script output

**Wallet Management:**
- Create Filecoin (secp256k1, BLS) and Ethereum-style wallets
- Fund wallets from the default node wallet
- Check balances and list wallets

### Quick Start: Deploying Contracts

FilWizard offers flexible deployment options:

1. **Simple Git Deployment**: Deploy a single contract directly from a Git repository
2. **Configuration-Based**: Use JSON configuration files for complex multi-contract deployments with dependencies
3. **Custom Scripts**: Use your own deployment scripts with automatic address parsing
4. **Air-Gapped**: Clone repositories separately, then deploy offline

See the [Contract Deployment Guide](docs/contracts.md) for detailed examples.

## Installation

### From Source

```bash
# Clone the repository
git clone https://github.com/parthshah1/FilWizard.git
cd FilWizard

# Build the binary
make build

# The binary will be available as ./filwizard
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
- `FILECOIN_TOKEN`: JWT token for authentication (the actual token string, not a file path). Get it from your Lotus node:
  ```bash
  export FILECOIN_TOKEN=$(cat ~/.lotus/token)
  ```
- `VERBOSE`: Enable verbose output (default: `false`)

### Command-Line Flags

Global flags available for all commands:

```bash
--rpc <url>      # Filecoin RPC URL
--token <path>   # JWT token file path
--verbose        # Enable verbose output
```

## Documentation

- **[Wallet Operations](docs/wallet.md)** - Create, manage, and fund wallets
- **[Contract Deployment](docs/contracts.md)** - Deploy and interact with smart contracts
- **[Configuration System](docs/configuration.md)** - Advanced configuration-based deployment
- **[Examples](docs/examples.md)** - Complete workflow examples
- **[Development Guide](docs/development.md)** - Building and contributing

## Quick Examples

### Create Wallets

```bash
filwizard wallet create --count 10 --type ethereum --fund 100
```

### Deploy a Contract

```bash
filwizard contract from-git \
  --git-url https://github.com/user/contract.git \
  --main-contract MyContract \
  --create-deployer \
  --bindings
```

### Deploy from Configuration

```bash
filwizard contract clone-config --config config/contracts.json
filwizard contract deploy-local --config config/contracts.json --create-deployer
```

### List Wallets

```bash
filwizard wallet list
```

## Contributing

Contributions are welcome! Please ensure your changes:
- Follow Go best practices
- Include appropriate documentation
- Add examples for new features
- Update relevant documentation files

## License

[Add license information here]

## Support

For issues, questions, or contributions, please visit the [GitHub repository](https://github.com/parthshah1/FilWizard).

---

**Note**: This tool is designed for testing purposes. Use caution when deploying contracts or sending transactions on production networks.
