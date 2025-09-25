package config

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/antithesishq/antithesis-sdk-go/assert"
	"github.com/filecoin-project/go-state-types/abi"
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
				heightDiff := currentHeight - lastReportedHeight
				fmt.Printf("Node %s advanced %d epochs: %d → %d\n",
					nodeID, heightDiff, lastReportedHeight, currentHeight)
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
