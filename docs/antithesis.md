# Testing on Antithesis

`FilWizard` is specifically designed to work in deterministic testing environments like Antithesis. Here's how to use it effectively.

## Antithesis Integration

Antithesis provides deterministic replay capabilities for finding and reproducing bugs. When using `FilWizard` in Antithesis:

1. **Deterministic Wallet Creation**: Use seeded wallet creation to ensure reproducible addresses
2. **Configuration-Based Deployment**: Use the `config/contracts.json` approach to ensure consistent contract deployments
3. **Mempool Testing**: Use the spam functionality to test network behavior under load
4. **State Verification**: Use contract call methods to verify state at various points

## Example Antithesis Test Scenario

```bash
# Setup phase
export FILECOIN_RPC=http://filecoin-node:1234/rpc/v1

# Create predictable wallets
filwizard wallet create --count 20 --type ethereum --fund 1000

# Deploy test contracts from configuration
filwizard contract clone-config --workspace /test-workspace
filwizard contract deploy-local \
  --workspace /test-workspace \
  --create-deployer

# Execute test scenario
filwizard mempool spam --count 10000 --concurrent 50

# Verify contract state
for contract in $(filwizard contract list --workspace /test-workspace); do
  filwizard contract call read --contract $contract --method verify
done

# Check network properties
export FILECOIN_NODES="http://node1:1234/rpc/v1,http://node2:1234/rpc/v1"
filwizard properties --check all
```

## Best Practices for Antithesis

- **Use configuration files** instead of command-line arguments for reproducibility
- **Pre-fund wallets** during setup to avoid balance issues during testing
- **Enable verbose logging** (`--verbose`) to capture detailed execution traces
- **Use the workspace directory** to maintain deployment artifacts across test runs
- **Generate bindings** to enable programmatic interaction with contracts in Go test code
- **Use template variables** for dynamic address resolution
- **Leverage post-deployment actions** for contract initialization
- **Check network properties** to verify system health during testing

## Network Properties with Antithesis

When Antithesis mode is enabled, property checks use Antithesis assertions:
- `AssertAlways`: Properties that must always be true (chain-sync, state-consistency)
- `AssertSometimes`: Properties that should be true at some point (progression)

See the [Network Properties Guide](properties.md) for more details.

