package synapse

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/antithesishq/antithesis-sdk-go/assert"
)

// InvariantState tracks the 3 core invariants for Synapse storage
type InvariantState struct {
	mu sync.RWMutex

	// Invariant 1: No PDP faults
	FaultRecords []FaultEvent

	// Invariant 2: Pieces get added
	PiecesAdded []PieceAddedEvent

	// Invariant 3: Settlements progress
	Settlements []SettlementEvent

	// Metadata
	StartTime   time.Time
	LastEventAt time.Time
}

// FaultEvent records a PDP fault
type FaultEvent struct {
	DataSetId      uint64 `json:"dataSetId"`
	PeriodsFaulted uint64 `json:"periodsFaulted"`
	BlockNumber    uint64 `json:"blockNumber"`
	TxHash         string `json:"txHash"`
}

// PieceAddedEvent records a piece addition
type PieceAddedEvent struct {
	DataSetId   uint64 `json:"dataSetId"`
	BlockNumber uint64 `json:"blockNumber"`
	TxHash      string `json:"txHash"`
}

// SettlementEvent records a rail settlement
type SettlementEvent struct {
	RailId        uint64 `json:"railId"`
	SettledUpTo   uint64 `json:"settledUpTo"`
	AmountSettled string `json:"amountSettled"`
	BlockNumber   uint64 `json:"blockNumber"`
	TxHash        string `json:"txHash"`
}

// NewInvariantState creates a new invariant state tracker
func NewInvariantState() *InvariantState {
	return &InvariantState{
		StartTime:    time.Now(),
		FaultRecords: make([]FaultEvent, 0),
		PiecesAdded:  make([]PieceAddedEvent, 0),
		Settlements:  make([]SettlementEvent, 0),
	}
}

// RecordFault records a PDP fault event - this is a CRITICAL invariant violation
func (s *InvariantState) RecordFault(dataSetId, periodsFaulted, blockNum uint64, txHash string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.FaultRecords = append(s.FaultRecords, FaultEvent{
		DataSetId:      dataSetId,
		PeriodsFaulted: periodsFaulted,
		BlockNumber:    blockNum,
		TxHash:         txHash,
	})
	s.LastEventAt = time.Now()

	// Use Unreachable to indicate this code path should never be hit
	// When a fault occurs, calling Unreachable signals the invariant was violated
	assert.Unreachable(
		"synapse_pdp_no_faults",
		map[string]any{
			"message":        "PDP fault detected - storage proof was not submitted in time",
			"dataSetId":      dataSetId,
			"periodsFaulted": periodsFaulted,
			"blockNumber":    blockNum,
			"txHash":         txHash,
		},
	)
}

// RecordPieceAdded records a piece addition event
func (s *InvariantState) RecordPieceAdded(dataSetId, blockNum uint64, txHash string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.PiecesAdded = append(s.PiecesAdded, PieceAddedEvent{
		DataSetId:   dataSetId,
		BlockNumber: blockNum,
		TxHash:      txHash,
	})
	s.LastEventAt = time.Now()
}

// RecordSettlement records a rail settlement event
func (s *InvariantState) RecordSettlement(railId, settledUpTo, blockNum uint64, amount, txHash string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Settlements = append(s.Settlements, SettlementEvent{
		RailId:        railId,
		SettledUpTo:   settledUpTo,
		AmountSettled: amount,
		BlockNumber:   blockNum,
		TxHash:        txHash,
	})
	s.LastEventAt = time.Now()
}

// EmitFinalAssertions emits Antithesis assertions based on collected state
func (s *InvariantState) EmitFinalAssertions() {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Invariant 1: No PDP faults
	// (Already signaled via Unreachable when faults occur)
	// Emit Always(true) to confirm the invariant was checked
	assert.Always(
		len(s.FaultRecords) == 0,
		"synapse_pdp_healthy",
		map[string]any{
			"message":      fmt.Sprintf("PDP health check: %d faults detected", len(s.FaultRecords)),
			"faultCount":   len(s.FaultRecords),
			"testDuration": time.Since(s.StartTime).String(),
		},
	)

	// Invariant 2: Pieces get added (should happen at least once in a healthy test)
	assert.Sometimes(
		len(s.PiecesAdded) > 0,
		"synapse_pieces_added",
		map[string]any{
			"message":      fmt.Sprintf("%d pieces were added during test", len(s.PiecesAdded)),
			"pieceCount":   len(s.PiecesAdded),
			"testDuration": time.Since(s.StartTime).String(),
		},
	)

	// Invariant 3: Settlements progress (if storage is used, settlements should occur)
	if len(s.PiecesAdded) > 0 {
		assert.Sometimes(
			len(s.Settlements) > 0,
			"synapse_settlements_progress",
			map[string]any{
				"message":         fmt.Sprintf("%d settlements processed", len(s.Settlements)),
				"settlementCount": len(s.Settlements),
				"testDuration":    time.Since(s.StartTime).String(),
			},
		)
	}
}

// GetSummary returns a summary of the invariant state
func (s *InvariantState) GetSummary() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return map[string]any{
		"faultCount":      len(s.FaultRecords),
		"pieceCount":      len(s.PiecesAdded),
		"settlementCount": len(s.Settlements),
		"duration":        time.Since(s.StartTime).String(),
		"lastEventAt":     s.LastEventAt,
	}
}

// SaveToFile saves the invariant state to a JSON file
func (s *InvariantState) SaveToFile(path string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data := map[string]any{
		"startTime":   s.StartTime,
		"lastEventAt": s.LastEventAt,
		"faults":      s.FaultRecords,
		"pieces":      s.PiecesAdded,
		"settlements": s.Settlements,
	}

	bytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, bytes, 0644)
}

// LoadFromFile loads invariant state from a JSON file
func LoadInvariantStateFromFile(path string) (*InvariantState, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var data struct {
		StartTime   time.Time         `json:"startTime"`
		LastEventAt time.Time         `json:"lastEventAt"`
		Faults      []FaultEvent      `json:"faults"`
		Pieces      []PieceAddedEvent `json:"pieces"`
		Settlements []SettlementEvent `json:"settlements"`
	}

	if err := json.Unmarshal(bytes, &data); err != nil {
		return nil, err
	}

	return &InvariantState{
		StartTime:    data.StartTime,
		LastEventAt:  data.LastEventAt,
		FaultRecords: data.Faults,
		PiecesAdded:  data.Pieces,
		Settlements:  data.Settlements,
	}, nil
}
