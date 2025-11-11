# Examples

Complete workflow examples for using FilWizard.

## Complete Workflow Example

Here's a complete example workflow for testing a DApp on Filecoin:

```bash
# 1. Create test wallets
filwizard --rpc http://localhost:1234/rpc/v1 wallet create \
  --count 10 \
  --type ethereum \
  --fund 100 \
  --show-private-key

# 2. Deploy a contract from Git
filwizard contract from-git \
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
filwizard contract list --workspace ./workspace

# 4. Call a contract method
filwizard contract call read \
  --contract 0x... \
  --method totalSupply \
  --rpc-url http://localhost:1234/rpc/v1

# 5. Send transactions to interact with the contract
filwizard contract call write \
  --contract 0x... \
  --method transfer \
  --args "0xrecipient...,1000" \
  --types "address,uint256" \
  --private-key 0xdeployer_key... \
  --gas-limit 100000

# 6. Stress test the network
filwizard mempool spam \
  --count 10000 \
  --amount 0.01 \
  --concurrent 20
```

## Batch Contract Deployment

For deploying multiple contracts in sequence:

```bash
# Create a deployer account once
filwizard contract from-git \
  --git-url https://github.com/project1/contracts.git \
  --create-deployer \
  --main-contract Token \
  --contract-path contracts/Token.sol

# Use the same deployer for subsequent deployments
# (The deployer key will be in workspace/deployments.json)
filwizard contract from-git \
  --git-url https://github.com/project2/contracts.git \
  --deployer-key 0x... \
  --main-contract NFT \
  --contract-path contracts/NFT.sol
```

## Advanced Configuration-Based Deployment

Deploy complex contract ecosystems with dependencies, post-deployment actions, and custom scripts:

```bash
# 1. Create configuration file with all contracts
cat > config/my-contracts.json <<EOF
{
  "environment": {
    "CHAIN_ID": "31415926",
    "RPC_URL": "http://localhost:1234/rpc/v1"
  },
  "contracts": [
    {
      "name": "Token",
      "project_type": "foundry",
      "git_url": "https://github.com/user/token.git",
      "git_ref": "main",
      "contract_path": "src/Token.sol",
      "main_contract": "Token",
      "constructor_args": ["1000000"],
      "generate_bindings": true,
      "post_deployment": {
        "actions": [{
          "method": "transfer",
          "args": ["{deployment:Token:deployer_address}", "500000"],
          "types": ["address", "uint256"]
        }]
      },
      "exports": {
        "TOKEN_ADDRESS": "self"
      }
    },
    {
      "name": "Router",
      "project_type": "foundry",
      "git_url": "https://github.com/user/router.git",
      "dependencies": ["Token"],
      "constructor_args": ["{address:Token}"],
      "environment": {
        "TOKEN_ADDRESS": "{address:Token}"
      }
    }
  ]
}
EOF

# 2. Clone all repositories
filwizard contract clone-config \
  --config config/my-contracts.json \
  --workspace ./workspace

# 3. Deploy all contracts (dependencies resolved automatically)
filwizard contract deploy-local \
  --config config/my-contracts.json \
  --workspace ./workspace \
  --rpc-url http://localhost:1234/rpc/v1 \
  --create-deployer \
  --bindings

# 4. Verify deployments
filwizard contract list --workspace ./workspace

# 5. Check network properties
export FILECOIN_NODES="http://localhost:1234/rpc/v1"
filwizard properties --check all
```

## Custom Deployment Script Example

For complex multi-contract deployments:

```bash
# Configuration with custom script
{
  "contracts": [{
    "name": "ServiceDeployment",
    "deploy_script": "scripts/deploy-service.sh",
    "script_dir": "service",
    "environment": {
      "TOKEN_ADDRESS": "{address:Token}",
      "ROUTER_ADDRESS": "{address:Router}"
    }
  }]
}

# The script receives all environment variables and PRIVATE_KEY
# Script output is automatically parsed for contract addresses
```

## Testing with Configuration

For reproducible testing environments, use the configuration-based approach:

1. Create `config/contracts.json` with your contracts
2. Clone all repositories: `filwizard contract clone-config`
3. Deploy all contracts: `filwizard contract deploy-local --create-deployer`

This approach is ideal for:
- **Air-gapped environments** where internet access is restricted
- **Deterministic testing** on platforms like Antithesis
- **Batch deployments** with consistent configuration
- **Complex ecosystems** with multiple interdependent contracts
- **Post-deployment initialization** and setup

