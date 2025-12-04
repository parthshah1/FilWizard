# Network Properties Checking

Check Filecoin network properties for testing and validation.

## Overview

FilWizard can verify critical network properties across multiple nodes to ensure system health and correctness.

## Usage

```bash
# Check all properties
filwizard properties --check all

# Check specific property
filwizard properties --check chain-sync
filwizard properties --check progression
filwizard properties --check state-consistency
filwizard properties --check state-compute
filwizard properties --check finalized-tipset

# Custom timeout and monitoring duration
filwizard properties \
  --check all \
  --timeout 120s \
  --monitor-duration 60s
```

## Available Properties

### 1. Chain Sync (`chain-sync`)

Verifies all nodes are in a synced or syncing state. This property checks:
- All nodes have active sync state
- No nodes are in error or failed sync states
- Sync progress is being made

### 2. Chain Progression (`progression`)

Monitors that the chain makes forward progress on at least one node. This property:
- Tracks chain height over time
- Verifies that at least one node is advancing
- Useful for detecting chain stalls

### 3. State Consistency (`state-consistency`)

Ensures all nodes produce identical state computation results. This property:
- Compares StateCompute results across nodes
- Verifies state root consistency
- Detects state divergence issues

### 4. State Compute Consistency (`state-compute`)

Verifies that all nodes produce identical StateCompute results at a common height. This property:
- Finds a common height that all nodes have reached
- Calls `StateCompute` on all nodes at the same height
- Compares state roots to ensure consistency
- More explicit than `state-consistency` as it ensures all nodes compute state at the exact same height

**API Reference:** Uses [StateCompute](https://github.com/filecoin-project/lotus/blob/master/api/api_full.go) method from the Lotus API.

### 5. Finalized TipSet Consistency (`finalized-tipset`)

Verifies that all nodes return the same finalized tipset. This property:
- Calls `ChainGetFinalizedTipSet` on all nodes
- Compares both the height and tipset key (CID) across nodes
- Ensures F3 finalization is consistent across the network
- Critical for verifying that all nodes agree on finalized chain state

**API Reference:** Uses [ChainGetFinalizedTipSet](https://github.com/filecoin-project/lotus/blob/master/api/api_full.go) method from the Lotus API.

## Configuration

Set `FILECOIN_NODES` environment variable with comma-separated RPC URLs:

```bash
export FILECOIN_NODES="http://node1:1234/rpc/v1,http://node2:1234/rpc/v1"
```

## Antithesis Integration

When Antithesis mode is enabled, property checks use Antithesis assertions:
- `AssertAlways`: Properties that must always be true (chain-sync, state-consistency)
- `AssertSometimes`: Properties that should be true at some point (progression)

Enable Antithesis mode programmatically or via environment variables.

## Example Output

```
Checking Filecoin network properties...
Checking chain synchronization property using SyncState...
Node node-0: synced=true, active_syncs=0, vm_applied=100
Node node-1: synced=true, active_syncs=0, vm_applied=100
Chain sync property satisfied: all 2 nodes are synced

Checking chain progression property using polling...
Node node-0 starting at height: 100
Node node-0 advanced 5 epochs: 100 → 105
Chain progression property satisfied

Checking state consistency property using StateCompute...
Node node-0 (height 105): StateCompute root abc123...
Node node-1 (height 105): StateCompute root abc123...
State consistency property satisfied across 2 nodes at height 104

Checking StateCompute consistency property at common height...
Node node-0 at height: 105
Node node-1 at height: 105
Using common height 104 for StateCompute consistency check
Using reference tipset bafy2... at height 104
Node node-0: StateCompute root abc123... at height 104
Node node-1: StateCompute root abc123... at height 104
StateCompute consistency property satisfied across 2 nodes at height 104

Checking finalized tipset consistency property using ChainGetFinalizedTipSet...
Node node-0: Finalized tipset at height 95, key: bafy2...
Node node-1: Finalized tipset at height 95, key: bafy2...
Finalized tipset consistency property satisfied across 2 nodes at height 95
```

