package config

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/antithesishq/antithesis-sdk-go/assert"
	"github.com/filecoin-project/go-state-types/abi"
	"github.com/filecoin-project/lotus/chain/types"
)

var antithesisEnabled bool

func SetAntithesisMode(enabled bool) {
	antithesisEnabled = enabled
}

func IsAntithesisEnabled() bool {
	return antithesisEnabled
}

func AssertAlways(condition bool, message string, details map[string]interface{}) {
	if antithesisEnabled {
		assert.Always(condition, message, details)
	}
}

func AssertSometimes(condition bool, message string, details map[string]interface{}) {
	if antithesisEnabled {
		assert.Sometimes(condition, message, details)
	}
}

type PropertyConfig struct {
	MonitorDuration time.Duration
}

type PropertyChecker struct {
	clients []*Client
	config  *PropertyConfig
}

func NewPropertyChecker() *PropertyChecker {
	var clients []*Client

	if nodeURLs := getNodeURLsFromEnv(); len(nodeURLs) > 0 {
		for _, url := range nodeURLs {
			cfg := &Config{RPC: url}
			if client, err := New(cfg); err == nil {
				clients = append(clients, client)
			}
		}
	}

	return &PropertyChecker{
		clients: clients,
		config: &PropertyConfig{
			MonitorDuration: 45 * time.Second,
		},
	}
}

func (pc *PropertyChecker) SetConfig(cfg *PropertyConfig) {
	pc.config = cfg
}

func getNodeURLsFromEnv() []string {
	nodes := os.Getenv("FILECOIN_NODES")
	if nodes == "" {
		return nil
	}

	var urls []string
	for _, url := range strings.Split(nodes, ",") {
		if trimmed := strings.TrimSpace(url); trimmed != "" {
			urls = append(urls, trimmed)
		}
	}
	return urls
}

func (pc *PropertyChecker) CheckChainSync(ctx context.Context) error {
	if len(pc.clients) == 0 {
		return fmt.Errorf("no clients available")
	}

	fmt.Println("Checking chain synchronization property using SyncState...")

	syncStates := make(map[string]bool)
	syncDetails := make(map[string]interface{})

	for i, client := range pc.clients {
		nodeID := fmt.Sprintf("node-%d", i)

		syncState, err := client.GetAPI().SyncState(ctx)
		if err != nil {
			fmt.Printf("Failed to get SyncState from %s: %v\n", nodeID, err)
			syncStates[nodeID] = false
			continue
		}

		isSynced := true
		activeSync := len(syncState.ActiveSyncs)

		if activeSync > 0 {
			for _, sync := range syncState.ActiveSyncs {
				// Add nil checks for Target
				if sync.Target == nil {
					fmt.Printf("Node %s sync target is nil - Stage: %s, Height: %d\n",
						nodeID, sync.Stage.String(), sync.Height)
					continue
				}

				if sync.Height >= sync.Target.Height() {
					continue
				}

				stageStr := sync.Stage.String()
				if stageStr == "complete" || stageStr == "sync-complete" {
					continue
				} else if stageStr == "error" || stageStr == "failed" {
					isSynced = false
					fmt.Printf("Node %s sync failed - Stage: %s, Height: %d, Target: %d\n",
						nodeID, stageStr, sync.Height, sync.Target.Height())
				}
			}
		}

		syncStates[nodeID] = isSynced
		syncDetails[nodeID] = map[string]interface{}{
			"synced":       isSynced,
			"active_syncs": activeSync,
			"vm_applied":   syncState.VMApplied,
		}

		fmt.Printf("Node %s: synced=%t, active_syncs=%d, vm_applied=%d\n",
			nodeID, isSynced, activeSync, syncState.VMApplied)
	}

	allSynced := true
	for _, synced := range syncStates {
		if !synced {
			allSynced = false
			break
		}
	}

	AssertAlways(
		allSynced,
		"All nodes should be in synced or syncing state",
		map[string]interface{}{
			"sync_states":  syncStates,
			"sync_details": syncDetails,
			"all_synced":   allSynced,
		},
	)

	if allSynced {
		fmt.Printf("Chain sync property satisfied: all %d nodes are synced\n", len(syncStates))
	} else {
		fmt.Printf("Chain sync property violated: some nodes have sync issues\n")
		return fmt.Errorf("chain sync property failed")
	}

	return nil
}

func (pc *PropertyChecker) CheckChainProgression(ctx context.Context) error {
	if len(pc.clients) == 0 {
		return fmt.Errorf("no clients available")
	}

	fmt.Println("Checking chain progression property using polling...")

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errorChan := make(chan error, len(pc.clients))
	progressResults := make(map[string]bool)

	for i, client := range pc.clients {
		client := client
		nodeID := fmt.Sprintf("node-%d", i)
		go func() {
			progressed := pc.streamNodeUpdates(ctx, client, nodeID)
			progressResults[nodeID] = progressed
			errorChan <- nil
		}()
	}

	for i := 0; i < len(pc.clients); i++ {
		<-errorChan
	}

	anyProgression := false
	for nodeID, progressed := range progressResults {
		if progressed {
			anyProgression = true
			fmt.Printf("Chain progression detected on %s\n", nodeID)
		} else {
			fmt.Printf("No progression detected on %s\n", nodeID)
		}
	}

	AssertSometimes(
		anyProgression,
		"Chain should make forward progress on at least one node",
		map[string]interface{}{
			"progression_results": progressResults,
			"any_progression":     anyProgression,
		},
	)

	if anyProgression {
		fmt.Printf("Chain progression property satisfied\n")
	} else {
		fmt.Printf("Chain progression property: no progress detected on any node\n")
	}

	return nil
}

func (pc *PropertyChecker) streamNodeUpdates(ctx context.Context, client *Client, nodeID string) bool {
	initialHead, err := client.GetAPI().ChainHead(ctx)
	if err != nil {
		fmt.Printf("Failed to get initial chain head for %s: %v\n", nodeID, err)
		return false
	}

	initialHeight := initialHead.Height()
	fmt.Printf("Node %s starting at height: %d\n", nodeID, initialHeight)

	monitorDuration := pc.config.MonitorDuration
	ticker := time.NewTicker(7 * time.Second)
	defer ticker.Stop()

	timeout := time.After(monitorDuration)
	lastReportedHeight := initialHeight

	for {
		select {
		case <-ctx.Done():
			return false
		case <-timeout:
			return lastReportedHeight > initialHeight
		case <-ticker.C:
			currentHead, err := client.GetAPI().ChainHead(ctx)
			if err != nil {
				fmt.Printf("Failed to get current chain head for %s: %v\n", nodeID, err)
				continue
			}

			currentHeight := currentHead.Height()

			if currentHeight > lastReportedHeight {
				fmt.Printf("Node %s advanced %d epochs: %d → %d\n",
					nodeID, currentHeight-lastReportedHeight, lastReportedHeight, currentHeight)
				lastReportedHeight = currentHeight
			}
		}
	}
}

func (pc *PropertyChecker) CheckStateConsistency(ctx context.Context) error {
	if len(pc.clients) < 2 {
		fmt.Println("Need at least 2 clients for state consistency check")
		return nil
	}
	fmt.Println("Checking state consistency property using StateCompute...")

	var nodeHeights []int64
	var validClients []*Client
	var nodeIDs []string

	for i, client := range pc.clients {
		nodeID := fmt.Sprintf("node-%d", i)

		head, err := client.GetAPI().ChainHead(ctx)
		if err != nil {
			fmt.Printf("Failed to get head from %s: %v\n", nodeID, err)
			continue
		}

		height := int64(head.Height())
		nodeHeights = append(nodeHeights, height)
		validClients = append(validClients, client)
		nodeIDs = append(nodeIDs, nodeID)

		fmt.Printf("Node %s at height: %d\n", nodeID, height)
	}

	if len(validClients) < 2 {
		fmt.Println("Need at least 2 responsive nodes for state consistency check")
		return fmt.Errorf("insufficient responsive nodes")
	}

	lowestHeight := nodeHeights[0]
	for _, height := range nodeHeights {
		if height < lowestHeight {
			lowestHeight = height
		}
	}

	targetHeight := lowestHeight - 1
	fmt.Printf("Using lowest height %d (target: %d) for state consistency check\n", lowestHeight, targetHeight)

	var computeResults []string
	var validNodes []string

	for i, client := range validClients {
		nodeID := nodeIDs[i]
		nodeHeight := nodeHeights[i]

		head, err := client.GetAPI().ChainHead(ctx)
		if err != nil {
			fmt.Printf("Failed to get head from %s: %v\n", nodeID, err)
			continue
		}

		commonTipset := head.Parents()

		result, err := client.GetAPI().StateCompute(ctx, abi.ChainEpoch(targetHeight), nil, commonTipset)
		if err != nil {
			fmt.Printf("Failed StateCompute on %s at height %d: %v\n", nodeID, targetHeight, err)
			continue
		}

		stateRoot := result.Root.String()
		computeResults = append(computeResults, stateRoot)
		validNodes = append(validNodes, nodeID)

		fmt.Printf("Node %s (height %d): StateCompute root %s\n",
			nodeID, nodeHeight, stateRoot[:16]+"...")
	}

	if len(computeResults) < 2 {
		fmt.Println("Insufficient StateCompute results for consistency check")
		return fmt.Errorf("could not get StateCompute results from enough nodes")
	}

	referenceResult := computeResults[0]
	stateConsistent := true
	inconsistentNodes := []string{}

	for i := 1; i < len(computeResults); i++ {
		if computeResults[i] != referenceResult {
			inconsistentNodes = append(inconsistentNodes, validNodes[i])
			stateConsistent = false
		}
	}

	AssertAlways(
		stateConsistent,
		"All nodes should produce identical StateCompute results",
		map[string]interface{}{
			"state_consistent":   stateConsistent,
			"compute_height":     targetHeight,
			"lowest_height":      lowestHeight,
			"nodes_checked":      len(computeResults),
			"inconsistent_nodes": inconsistentNodes,
		},
	)

	if stateConsistent {
		fmt.Printf("State consistency property satisfied across %d nodes at height %d\n", len(computeResults), targetHeight)
	} else {
		fmt.Printf("State consistency property failed - nodes %v produced different results\n", inconsistentNodes)
	}

	return nil
}

// CheckStateComputeConsistency verifies that all nodes produce identical StateCompute results
// at a common height. This ensures state computation consistency across the network.
func (pc *PropertyChecker) CheckStateComputeConsistency(ctx context.Context) error {
	if len(pc.clients) < 2 {
		fmt.Println("Need at least 2 clients for StateCompute consistency check")
		return nil
	}
	fmt.Println("Checking StateCompute consistency property at common height...")

	// First, get current heads from all nodes to find a common height
	type nodeInfo struct {
		client *Client
		nodeID string
		head   abi.ChainEpoch
		tipset types.TipSetKey
	}

	var nodeInfos []nodeInfo
	for i, client := range pc.clients {
		nodeID := fmt.Sprintf("node-%d", i)

		head, err := client.GetAPI().ChainHead(ctx)
		if err != nil {
			fmt.Printf("Failed to get head from %s: %v\n", nodeID, err)
			continue
		}

		nodeInfos = append(nodeInfos, nodeInfo{
			client: client,
			nodeID: nodeID,
			head:   head.Height(),
			tipset: head.Key(),
		})

		fmt.Printf("Node %s at height: %d\n", nodeID, head.Height())
	}

	if len(nodeInfos) < 2 {
		fmt.Println("Need at least 2 responsive nodes for StateCompute consistency check")
		return fmt.Errorf("insufficient responsive nodes")
	}

	// Find the lowest height (common height all nodes have)
	lowestHeight := nodeInfos[0].head
	for _, info := range nodeInfos {
		if info.head < lowestHeight {
			lowestHeight = info.head
		}
	}

	// Use a height that all nodes definitely have (lowest - 1 for safety)
	targetHeight := lowestHeight - 1
	if targetHeight < 0 {
		targetHeight = 0
	}

	fmt.Printf("Using common height %d for StateCompute consistency check\n", targetHeight)

	// Get tipset at target height from the first node to use as reference
	referenceTipset, err := nodeInfos[0].client.GetAPI().ChainGetTipSetByHeight(ctx, targetHeight, nodeInfos[0].tipset)
	if err != nil {
		return fmt.Errorf("failed to get tipset at height %d from reference node: %w", targetHeight, err)
	}

	referenceTipsetKey := referenceTipset.Key()
	fmt.Printf("Using reference tipset %s at height %d\n", referenceTipsetKey.String(), targetHeight)

	// Call StateCompute on all nodes at the same height with the same tipset key
	// This ensures we're comparing state computation for the exact same tipset across all nodes
	var computeResults []struct {
		nodeID    string
		stateRoot string
		height    abi.ChainEpoch
	}

	for _, info := range nodeInfos {
		// Use the same reference tipset key for all nodes to ensure fair comparison
		// If a node doesn't have this tipset, it indicates a sync issue
		result, err := info.client.GetAPI().StateCompute(ctx, targetHeight, nil, referenceTipsetKey)
		if err != nil {
			fmt.Printf("Failed StateCompute on %s at height %d with tipset %s: %v\n",
				info.nodeID, targetHeight, referenceTipsetKey.String()[:32]+"...", err)
			continue
		}

		stateRoot := result.Root.String()
		computeResults = append(computeResults, struct {
			nodeID    string
			stateRoot string
			height    abi.ChainEpoch
		}{
			nodeID:    info.nodeID,
			stateRoot: stateRoot,
			height:    targetHeight,
		})

		fmt.Printf("Node %s: StateCompute root %s at height %d\n",
			info.nodeID, stateRoot[:16]+"...", targetHeight)
	}

	if len(computeResults) < 2 {
		fmt.Println("Insufficient StateCompute results for consistency check")
		return fmt.Errorf("could not get StateCompute results from enough nodes")
	}

	// Compare all results
	referenceResult := computeResults[0].stateRoot
	stateConsistent := true
	inconsistentNodes := []string{}

	for i := 1; i < len(computeResults); i++ {
		if computeResults[i].stateRoot != referenceResult {
			inconsistentNodes = append(inconsistentNodes, computeResults[i].nodeID)
			stateConsistent = false
			fmt.Printf("State root mismatch: %s has %s, expected %s\n",
				computeResults[i].nodeID, computeResults[i].stateRoot[:16]+"...", referenceResult[:16]+"...")
		}
	}

	AssertAlways(
		stateConsistent,
		"All nodes should produce identical StateCompute results at common height",
		map[string]interface{}{
			"state_consistent":   stateConsistent,
			"compute_height":     targetHeight,
			"nodes_checked":      len(computeResults),
			"inconsistent_nodes": inconsistentNodes,
			"reference_root":     referenceResult,
		},
	)

	if stateConsistent {
		fmt.Printf("StateCompute consistency property satisfied across %d nodes at height %d\n",
			len(computeResults), targetHeight)
	} else {
		fmt.Printf("StateCompute consistency property failed - nodes %v produced different state roots\n",
			inconsistentNodes)
		return fmt.Errorf("StateCompute consistency check failed")
	}

	return nil
}

// CheckFinalizedTipSetConsistency verifies that all nodes return the same finalized tipset.
// This ensures that F3 finalization is consistent across the network.
func (pc *PropertyChecker) CheckFinalizedTipSetConsistency(ctx context.Context) error {
	if len(pc.clients) < 2 {
		fmt.Println("Need at least 2 clients for finalized tipset consistency check")
		return nil
	}
	fmt.Println("Checking finalized tipset consistency property using ChainGetFinalizedTipSet...")

	type finalizedInfo struct {
		nodeID string
		tipset *types.TipSet
		height abi.ChainEpoch
		cid    string
	}

	var finalizedInfos []finalizedInfo

	for i, client := range pc.clients {
		nodeID := fmt.Sprintf("node-%d", i)

		tipset, err := client.GetAPI().ChainGetFinalizedTipSet(ctx)
		if err != nil {
			fmt.Printf("Failed to get finalized tipset from %s: %v\n", nodeID, err)
			continue
		}

		height := tipset.Height()
		key := tipset.Key()
		cidStr := key.String()

		finalizedInfos = append(finalizedInfos, finalizedInfo{
			nodeID: nodeID,
			tipset: tipset,
			height: height,
			cid:    cidStr,
		})

		fmt.Printf("Node %s: Finalized tipset at height %d, key: %s\n",
			nodeID, height, cidStr[:32]+"...")
	}

	if len(finalizedInfos) < 2 {
		fmt.Println("Need at least 2 responsive nodes for finalized tipset consistency check")
		return fmt.Errorf("insufficient responsive nodes")
	}

	// Compare all finalized tipsets
	referenceInfo := finalizedInfos[0]
	tipsetsConsistent := true
	inconsistentNodes := []string{}

	for i := 1; i < len(finalizedInfos); i++ {
		info := finalizedInfos[i]

		// Compare both height and tipset key (CID)
		if info.height != referenceInfo.height || info.cid != referenceInfo.cid {
			inconsistentNodes = append(inconsistentNodes, info.nodeID)
			tipsetsConsistent = false
			fmt.Printf("Finalized tipset mismatch: %s has height %d (key: %s), expected height %d (key: %s)\n",
				info.nodeID, info.height, info.cid[:32]+"...", referenceInfo.height, referenceInfo.cid[:32]+"...")
		}
	}

	AssertAlways(
		tipsetsConsistent,
		"All nodes should return the same finalized tipset",
		map[string]interface{}{
			"tipsets_consistent": tipsetsConsistent,
			"finalized_height":   referenceInfo.height,
			"finalized_tipset":   referenceInfo.cid,
			"nodes_checked":      len(finalizedInfos),
			"inconsistent_nodes": inconsistentNodes,
		},
	)

	if tipsetsConsistent {
		fmt.Printf("Finalized tipset consistency property satisfied across %d nodes at height %d\n",
			len(finalizedInfos), referenceInfo.height)
	} else {
		fmt.Printf("Finalized tipset consistency property failed - nodes %v have different finalized tipsets\n",
			inconsistentNodes)
		return fmt.Errorf("finalized tipset consistency check failed")
	}

	return nil
}
