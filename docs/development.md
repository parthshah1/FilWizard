# Development Guide

Guide for building, developing, and contributing to FilWizard.

## Project Structure

```
mpool-tx/
├── cmd/                           # Command implementations
│   ├── root.go                   # Root command and CLI setup
│   ├── wallet.go                 # Wallet operations
│   ├── mempool.go                # Mempool/transaction operations
│   ├── contract.go               # Contract deployment commands
│   ├── manager.go                # Contract management logic
│   ├── properties.go             # Network properties checking
│   ├── accounts.go               # Account management
│   └── payments.go               # Payment operations
├── config/                        # Configuration management
│   ├── config.go                 # Config loading and client setup
│   ├── contracts.json            # Default contract configuration
│   ├── filecoin-synapse.json     # Example configuration
│   ├── contract_dependency.go    # Dependency resolution and template variables
│   ├── contract_wrapper.go       # Contract interaction wrapper
│   └── properties.go             # Network properties checking logic
├── contracts/                     # Example contracts
├── docs/                          # Documentation
├── main.go                        # Entry point
├── Makefile                       # Build configuration
└── README.md                      # Main documentation
```

## Building

```bash
# Build the binary
make build

# Clean build artifacts
make clean
```

## Adding New Commands

To add new commands, follow the pattern in existing command files:

1. Create your command structure using `urfave/cli/v2`
2. Add it to the `Commands` slice in `cmd/root.go`
3. Implement the command logic
4. Update relevant documentation files in `docs/`

## Dependencies

Key dependencies:
- `github.com/filecoin-project/lotus`: Filecoin node API
- `github.com/ethereum/go-ethereum`: Ethereum libraries for contract interaction
- `github.com/urfave/cli/v2`: CLI framework
- `github.com/antithesishq/antithesis-sdk-go`: Antithesis assertions (optional)

## Code Organization

- **Commands** (`cmd/`): CLI command implementations
- **Configuration** (`config/`): Configuration loading, dependency resolution, and contract interaction
- **Documentation** (`docs/`): User-facing documentation organized by topic

## Testing

When adding new features:
- Test with both Foundry and Hardhat projects
- Verify template variable resolution
- Test dependency ordering
- Validate post-deployment actions
- Check network properties when applicable

## Contributing

Contributions are welcome! Please ensure your changes:
- Follow Go best practices
- Include appropriate documentation
- Add examples for new features
- Update relevant documentation files in `docs/`
- Test thoroughly before submitting

