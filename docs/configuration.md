# Configuration-Based Deployment

The configuration-based deployment system is the most powerful and flexible way to deploy smart contracts. It supports advanced features like dependency management, template variables, post-deployment actions, environment variable resolution, and custom deployment scripts.

## Quick Start

**Step 1: Clone repositories**

```bash
filwizard contract clone-config \
  --config config/contracts.json \
  --workspace ./workspace
```

**Step 2: Deploy from local clones**

```bash
filwizard contract deploy-local \
  --config config/contracts.json \
  --workspace ./workspace \
  --rpc-url http://localhost:1234/rpc/v1 \
  --create-deployer \
  --bindings
```

## Configuration File Format

The configuration file supports a comprehensive set of features for flexible contract deployment:

```json
{
  "environment": {
    "CHAIN_ID": "31415926",
    "RPC_URL": "http://lotus-1:1234/rpc/v1",
    "BLOCK_TIME": "15"
  },
  "contracts": [
    {
      "name": "USDFC",
      "project_type": "foundry",
      "git_url": "https://github.com/parthshah1/ERC20.git",
      "git_ref": "main",
      "contract_path": "src/ERC20.sol",
      "main_contract": "USDFC",
      "constructor_args": [],
      "dependencies": [],
      "generate_bindings": true,
      "environment": {
        "USDFC_INITIAL_MINT_AMOUNT_WEI": "10000000000000000000"
      },
      "post_deployment": {
        "actions": [
          {
            "description": "Mint initial USDFC supply to deployer",
            "method": "mint",
            "args": [
              "{deployment:USDFC:deployer_private_key}",
              "{env:USDFC_INITIAL_MINT_AMOUNT_WEI}"
            ],
            "types": [
              "address_from_private_key",
              "uint256"
            ]
          }
        ]
      },
      "exports": {
        "USDFC_ADDRESS": "self"
      }
    },
    {
      "name": "FilecoinWarmStorage",
      "project_type": "foundry",
      "git_url": "https://github.com/parthshah1/filecoin-services.git",
      "git_ref": "main",
      "main_contract": "FilecoinWarmStorage",
      "constructor_args": [],
      "dependencies": ["USDFC"],
      "script_dir": "service_contracts",
      "deploy_script": "tools/deploy-all-warm-storage.sh",
      "clone_commands": ["git submodule update --init --recursive"],
      "environment": {
        "ETH_RPC_URL": "http://lotus-1:1234/rpc/v1",
        "CHAIN": "31415926",
        "SERVICE_NAME": "Synapse",
        "SERVICE_DESCRIPTION": "Synapse Service",
        "USDFC_TOKEN_ADDRESS": "{address:USDFC}"
      },
      "exports": {
        "FILECOIN_WARM_STORAGE_ADDRESS": "self"
      }
    }
  ]
}
```

## Configuration Properties

### Global Environment Variables

The top-level `environment` object sets global environment variables available to all contracts:

```json
{
  "environment": {
    "CHAIN_ID": "31415926",
    "RPC_URL": "http://localhost:1234/rpc/v1"
  }
}
```

### Contract Configuration Fields

- **`name`** (required): Unique identifier for the contract
- **`project_type`** (required): `"foundry"` or `"hardhat"`
- **`git_url`** (required): Git repository URL
- **`git_ref`**: Branch, tag, or commit hash (default: `"main"`)
- **`main_contract`** (required): Contract name to deploy
- **`contract_path`**: Relative path to contract file (e.g., `"src/MyContract.sol"`)
- **`constructor_args`**: Array of constructor arguments (supports template variables)
- **`dependencies`**: Array of contract names that must be deployed first
- **`generate_bindings`**: Generate Go bindings for this contract
- **`environment`**: Contract-specific environment variables
- **`deploy_script`**: Custom deployment script path (relative to project root)
- **`script_dir`**: Working directory for deployment script
- **`clone_commands`**: Commands to run after cloning (e.g., `["git submodule update --init --recursive"]`)
- **`post_deployment`**: Actions to execute after deployment
- **`exports`**: Environment variables to export with contract addresses

## Template Variable System

The configuration system supports powerful template variables for dynamic value resolution:

### 1. Address Placeholders

Reference deployed contract addresses:

```json
{
  "constructor_args": ["{address:USDFC}", "{address:Multicall3}"]
}
```

### 2. Environment Variable Placeholders

Reference environment variables:

```json
{
  "constructor_args": ["{env:CHAIN_ID}", "{env:INITIAL_SUPPLY}"]
}
```

### 3. Deployment Metadata Placeholders

Access deployment information:

```json
{
  "post_deployment": {
    "actions": [{
      "method": "initialize",
      "args": [
        "{deployment:MyContract:deployer_address}",
        "{deployment:MyContract:deployer_private_key}",
        "{deployment:MyContract:address}"
      ],
      "types": ["address", "address_from_private_key", "address"]
    }]
  }
}
```

Available deployment fields:
- `{deployment:ContractName:address}` - Contract address
- `{deployment:ContractName:deployer_address}` - Deployer's address
- `{deployment:ContractName:deployer_private_key}` - Deployer's private key

### 4. Legacy Format

Also supports `${ContractName}` for backward compatibility

## Dependency Management

Contracts can declare dependencies on other contracts. The system automatically:
- Determines deployment order based on dependencies
- Resolves circular dependency errors
- Ensures dependencies are deployed before dependents

```json
{
  "name": "TokenRouter",
  "dependencies": ["USDFC", "Multicall3"],
  "constructor_args": ["{address:USDFC}", "{address:Multicall3}"]
}
```

## Post-Deployment Actions

Execute contract methods immediately after deployment:

```json
{
  "post_deployment": {
    "initialize": {
      "method": "initialize",
      "args": ["arg1", "arg2"],
      "types": ["uint256", "address"],
      "description": "Initialize the contract"
    },
    "actions": [
      {
        "description": "Mint tokens to deployer",
        "method": "mint",
        "args": [
          "{deployment:Token:deployer_address}",
          "1000000000000000000000"
        ],
        "types": ["address", "uint256"]
      }
    ]
  }
}
```

### Supported Argument Types

- `address` - Ethereum address (0x...)
- `address_from_private_key` - Derive address from private key
- `uint256`, `uint64`, `uint32` - Unsigned integers
- `bool` - Boolean values
- `string` - String values
- `bytes` - Byte arrays (hex format)

## Exports System

Export contract addresses as environment variables for use in scripts or subsequent deployments:

```json
{
  "exports": {
    "USDFC_ADDRESS": "self",
    "TOKEN_ROUTER": "{address:TokenRouter}",
    "CUSTOM_ADDRESS": "0x1234567890123456789012345678901234567890"
  }
}
```

Export targets:
- `"self"` - Export this contract's address
- `"{address:ContractName}"` - Export another contract's address
- Direct address - Export a specific address

## Custom Deployment Scripts

For complex deployments, use custom scripts:

```json
{
  "name": "ComplexService",
  "deploy_script": "tools/deploy-all.sh",
  "script_dir": "service_contracts",
  "environment": {
    "DEPLOYMENT_ENV": "production",
    "TOKEN_ADDRESS": "{address:USDFC}"
  }
}
```

The script receives:
- Resolved environment variables (with address placeholders replaced)
- `PRIVATE_KEY` environment variable (deployer's private key)
- All contract addresses from previous deployments

Script output is automatically parsed to extract contract addresses in formats like:
- `ContractName: 0x...`
- `ContractName 0x...`
- Any line containing a 0x-prefixed address

## Import Script Output

Import contract addresses from custom script output:

```bash
filwizard contract deploy-local \
  --config config/contracts.json \
  --import-output /path/to/script-output.txt \
  --workspace ./workspace
```

The import system automatically:
- Parses script output for contract addresses
- Matches addresses to contract names from configuration
- Updates `deployments.json` with imported addresses
- Makes addresses available for subsequent deployments

## Deployment Options

```bash
filwizard contract deploy-local \
  --config config/contracts.json \
  --workspace ./workspace \
  --rpc-url http://localhost:1234/rpc/v1 \
  --create-deployer \
  --deployer-key 0x... \
  --bindings \
  --compile \
  --import-output script-output.txt
```

**Options:**
- `--config <path>`: Path to configuration file (default: `config/contracts.json`)
- `--workspace <path>`: Workspace directory (default: `./workspace`)
- `--rpc-url <url>`: RPC URL for deployment
- `--create-deployer`: Create a new deployer account
- `--deployer-key <key>`: Use existing deployer private key
- `--bindings`: Generate Go bindings for all contracts
- `--compile`: Compile contracts with forge before deployment
- `--import-output <path>`: Import addresses from script output file

## Use Cases

This approach is ideal for:
- **Air-gapped environments** where internet access is restricted
- **Deterministic testing** on platforms like Antithesis
- **Batch deployments** with consistent configuration
- **Complex ecosystems** with multiple interdependent contracts
- **Post-deployment initialization** and setup

