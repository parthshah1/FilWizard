package synapse

import (
	"context"
	"fmt"
	"log"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

// ContractAddresses holds the addresses of Synapse contracts to monitor
type ContractAddresses struct {
	WarmStorage common.Address
	Payments    common.Address
	PDPVerifier common.Address
}

// Event topic signatures (keccak256 hashes) - only the 3 we care about
// These MUST match the exact ABI signatures from the contracts
var (
	// FaultRecord(uint256 indexed dataSetId, uint256 periodsFaulted, uint256 deadline)
	FaultRecordTopic = crypto.Keccak256Hash([]byte("FaultRecord(uint256,uint256,uint256)"))

	// PieceAdded(uint256 indexed dataSetId, uint256 indexed pieceId, Cids.Cid pieceCid, string[] keys, string[] values)
	// Note: Cids.Cid struct is encoded as (bytes) in the ABI
	PieceAddedTopic = crypto.Keccak256Hash([]byte("PieceAdded(uint256,uint256,(bytes),string[],string[])"))

	// RailSettled(uint256 indexed railId, uint256 totalSettledAmount, uint256 totalNetPayeeAmount, uint256 operatorCommission, uint256 networkFee, uint256 settledUpTo)
	RailSettledTopic = crypto.Keccak256Hash([]byte("RailSettled(uint256,uint256,uint256,uint256,uint256,uint256)"))
)

// SynapseMonitor monitors Synapse contract events and tracks invariants
type SynapseMonitor struct {
	client    *ethclient.Client
	contracts ContractAddresses
	state     *InvariantState
}

// NewSynapseMonitor creates a new Synapse event monitor
func NewSynapseMonitor(rpcURL string, contracts ContractAddresses) (*SynapseMonitor, error) {
	client, err := ethclient.Dial(rpcURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to RPC: %w", err)
	}

	return &SynapseMonitor{
		client:    client,
		contracts: contracts,
		state:     NewInvariantState(),
	}, nil
}

// GetState returns the current invariant state
func (m *SynapseMonitor) GetState() *InvariantState {
	return m.state
}

// Start begins monitoring events using polling (works with HTTP RPC)
func (m *SynapseMonitor) Start(ctx context.Context, pollInterval time.Duration) error {
	// Get all contract addresses to watch
	addresses := []common.Address{
		m.contracts.WarmStorage,
		m.contracts.Payments,
		m.contracts.PDPVerifier,
	}

	// Get starting block
	latestBlock, err := m.client.BlockNumber(ctx)
	if err != nil {
		return fmt.Errorf("failed to get latest block: %w", err)
	}

	fromBlock := latestBlock
	log.Printf("[SynapseMonitor] Starting from block %d", fromBlock)
	log.Printf("[SynapseMonitor] Watching contracts: WarmStorage=%s, Payments=%s, PDPVerifier=%s",
		m.contracts.WarmStorage.Hex(),
		m.contracts.Payments.Hex(),
		m.contracts.PDPVerifier.Hex(),
	)

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("[SynapseMonitor] Stopping, emitting final assertions...")
			m.state.EmitFinalAssertions()
			return nil
		case <-ticker.C:
			toBlock, err := m.client.BlockNumber(ctx)
			if err != nil {
				log.Printf("[SynapseMonitor] Error getting block number: %v", err)
				continue
			}

			if toBlock <= fromBlock {
				continue
			}

			query := ethereum.FilterQuery{
				FromBlock: big.NewInt(int64(fromBlock + 1)),
				ToBlock:   big.NewInt(int64(toBlock)),
				Addresses: addresses,
				Topics: [][]common.Hash{{
					FaultRecordTopic,
					PieceAddedTopic,
					RailSettledTopic,
				}},
			}

			logs, err := m.client.FilterLogs(ctx, query)
			if err != nil {
				log.Printf("[SynapseMonitor] Error filtering logs: %v", err)
				continue
			}

			for _, vLog := range logs {
				m.processLog(vLog)
			}

			fromBlock = toBlock
		}
	}
}

// processLog processes a single event log
func (m *SynapseMonitor) processLog(vLog types.Log) {
	if len(vLog.Topics) == 0 {
		return
	}

	topic := vLog.Topics[0]

	switch topic {
	case FaultRecordTopic:
		m.handleFaultRecord(vLog)
	case PieceAddedTopic:
		m.handlePieceAdded(vLog)
	case RailSettledTopic:
		m.handleRailSettled(vLog)
	}
}

// handleFaultRecord processes FaultRecord events - CRITICAL invariant violation
func (m *SynapseMonitor) handleFaultRecord(vLog types.Log) {
	var dataSetId, periodsFaulted uint64

	if len(vLog.Data) >= 64 {
		dataSetId = new(big.Int).SetBytes(vLog.Data[0:32]).Uint64()
		periodsFaulted = new(big.Int).SetBytes(vLog.Data[32:64]).Uint64()
	}

	log.Printf("[SynapseMonitor] ⚠️ FAULT RECORD: dataSetId=%d, periodsFaulted=%d, block=%d, tx=%s",
		dataSetId, periodsFaulted, vLog.BlockNumber, vLog.TxHash.Hex())

	m.state.RecordFault(dataSetId, periodsFaulted, vLog.BlockNumber, vLog.TxHash.Hex())
}

// handlePieceAdded processes PieceAdded events - successful upload
func (m *SynapseMonitor) handlePieceAdded(vLog types.Log) {
	var dataSetId uint64

	if len(vLog.Topics) > 1 {
		dataSetId = new(big.Int).SetBytes(vLog.Topics[1].Bytes()).Uint64()
	}

	log.Printf("[SynapseMonitor] ✓ PIECE ADDED: dataSetId=%d, block=%d, tx=%s",
		dataSetId, vLog.BlockNumber, vLog.TxHash.Hex())

	m.state.RecordPieceAdded(dataSetId, vLog.BlockNumber, vLog.TxHash.Hex())
}

// handleRailSettled processes RailSettled events - payment settlement
func (m *SynapseMonitor) handleRailSettled(vLog types.Log) {
	var railId, settledUpTo uint64
	var amount string

	if len(vLog.Topics) > 1 {
		railId = new(big.Int).SetBytes(vLog.Topics[1].Bytes()).Uint64()
	}
	if len(vLog.Data) >= 64 {
		settledUpTo = new(big.Int).SetBytes(vLog.Data[0:32]).Uint64()
		amount = new(big.Int).SetBytes(vLog.Data[32:64]).String()
	}

	log.Printf("[SynapseMonitor] ✓ RAIL SETTLED: railId=%d, settledUpTo=%d, amount=%s, block=%d",
		railId, settledUpTo, amount, vLog.BlockNumber)

	m.state.RecordSettlement(railId, settledUpTo, vLog.BlockNumber, amount, vLog.TxHash.Hex())
}
