package synapse

import (
	"context"
	"fmt"
	"log"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

// DataSetRails contains the payment rail IDs for a data set
type DataSetRails struct {
	PDPRailID       uint64
	CDNRailID       uint64
	CacheMissRailID uint64
}

// SettlementResult contains the result of a settlement operation
type SettlementResult struct {
	RailID      uint64
	TxHash      string
	BlockNumber uint64
	Amount      string
}

// Settler handles payment settlement operations
type Settler struct {
	client      *ethclient.Client
	warmStorage common.Address
	payments    common.Address
}

// NewSettler creates a new Settler instance
func NewSettler(rpcURL string, warmStorage, payments common.Address) (*Settler, error) {
	client, err := ethclient.Dial(rpcURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to RPC: %w", err)
	}

	return &Settler{
		client:      client,
		warmStorage: warmStorage,
		payments:    payments,
	}, nil
}

// GetDataSetRails fetches the payment rail IDs for a data set
func (s *Settler) GetDataSetRails(ctx context.Context, dataSetID uint64) (*DataSetRails, error) {
	// ABI for getDataSet function on WarmStorage
	// getDataSet(uint256 dataSetId) returns (DataSetInfoView)
	// DataSetInfoView has fields: pdpRailId, cacheMissRailId, cdnRailId, payer, payee, serviceProvider, commissionBps, clientDataSetId, pdpEndEpoch, providerId, dataSetId
	const getDataSetABI = `[{
		"inputs": [{"name": "dataSetId", "type": "uint256"}],
		"name": "getDataSet",
		"outputs": [{
			"components": [
				{"name": "pdpRailId", "type": "uint256"},
				{"name": "cacheMissRailId", "type": "uint256"},
				{"name": "cdnRailId", "type": "uint256"},
				{"name": "payer", "type": "address"},
				{"name": "payee", "type": "address"},
				{"name": "serviceProvider", "type": "address"},
				{"name": "commissionBps", "type": "uint256"},
				{"name": "clientDataSetId", "type": "uint256"},
				{"name": "pdpEndEpoch", "type": "uint256"},
				{"name": "providerId", "type": "uint256"},
				{"name": "dataSetId", "type": "uint256"}
			],
			"name": "info",
			"type": "tuple"
		}],
		"stateMutability": "view",
		"type": "function"
	}]`

	parsed, err := abi.JSON(strings.NewReader(getDataSetABI))
	if err != nil {
		return nil, fmt.Errorf("failed to parse ABI: %w", err)
	}

	callData, err := parsed.Pack("getDataSet", big.NewInt(int64(dataSetID)))
	if err != nil {
		return nil, fmt.Errorf("failed to pack call data: %w", err)
	}

	result, err := s.client.CallContract(ctx, ethereum.CallMsg{
		To:   &s.warmStorage,
		Data: callData,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to call getDataSet: %w", err)
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("empty result from getDataSet")
	}

	// The result is ABI encoded - decode the tuple manually
	// Tuple layout: 11 fields, each 32 bytes
	// [0-31] pdpRailId, [32-63] cacheMissRailId, [64-95] cdnRailId, ...
	if len(result) < 352 { // 11 fields * 32 bytes
		return nil, fmt.Errorf("result too short: got %d bytes, expected at least 352", len(result))
	}

	pdpRailId := new(big.Int).SetBytes(result[0:32]).Uint64()
	cacheMissRailId := new(big.Int).SetBytes(result[32:64]).Uint64()
	cdnRailId := new(big.Int).SetBytes(result[64:96]).Uint64()

	log.Printf("[Settler] GetDataSetRails: pdp=%d, cacheMiss=%d, cdn=%d", pdpRailId, cacheMissRailId, cdnRailId)

	return &DataSetRails{
		PDPRailID:       pdpRailId,
		CDNRailID:       cdnRailId,
		CacheMissRailID: cacheMissRailId,
	}, nil
}

// SettleRail settles a single payment rail
func (s *Settler) SettleRail(ctx context.Context, privateKey string, railID uint64) (*SettlementResult, error) {
	if railID == 0 {
		return nil, fmt.Errorf("invalid rail ID: 0")
	}

	// Parse private key
	key, err := crypto.HexToECDSA(strings.TrimPrefix(privateKey, "0x"))
	if err != nil {
		return nil, fmt.Errorf("invalid private key: %w", err)
	}

	// Get chain ID
	chainID, err := s.client.ChainID(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get chain ID: %w", err)
	}

	// Create transactor
	// Get current block to calculate current epoch
	currentBlock, err := s.client.BlockNumber(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get current block: %w", err)
	}
	// Use current block as epoch (devnet: 4 seconds/block, calibration: 30 seconds/block)
	// For simplicity, we'll settle up to current epoch
	untilEpoch := currentBlock

	log.Printf("[Settler] Settling rail %d up to epoch %d", railID, untilEpoch)

	// ABI for settleRail function
	// settleRail(uint256 railId, uint256 untilEpoch) returns (...)
	const settleRailABI = `[{
		"inputs": [
			{"name": "railId", "type": "uint256"},
			{"name": "untilEpoch", "type": "uint256"}
		],
		"name": "settleRail",
		"outputs": [
			{"name": "totalSettledAmount", "type": "uint256"},
			{"name": "totalNetPayeeAmount", "type": "uint256"},
			{"name": "totalOperatorCommission", "type": "uint256"},
			{"name": "totalNetworkFee", "type": "uint256"},
			{"name": "finalSettledEpoch", "type": "uint256"},
			{"name": "note", "type": "string"}
		],
		"stateMutability": "nonpayable",
		"type": "function"
	}]`

	parsed, err := abi.JSON(strings.NewReader(settleRailABI))
	if err != nil {
		return nil, fmt.Errorf("failed to parse ABI: %w", err)
	}

	callData, err := parsed.Pack("settleRail", big.NewInt(int64(railID)), big.NewInt(int64(untilEpoch)))
	if err != nil {
		return nil, fmt.Errorf("failed to pack call data: %w", err)
	}

	// Get nonce
	fromAddress := crypto.PubkeyToAddress(key.PublicKey)
	nonce, err := s.client.PendingNonceAt(ctx, fromAddress)
	if err != nil {
		return nil, fmt.Errorf("failed to get nonce: %w", err)
	}

	// Get gas price
	gasPrice, err := s.client.SuggestGasPrice(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get gas price: %w", err)
	}

	// Estimate gas (settleRail is nonpayable, no value needed)
	gasLimit, err := s.client.EstimateGas(ctx, ethereum.CallMsg{
		From: fromAddress,
		To:   &s.payments,
		Data: callData,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to estimate gas: %w", err)
	}

	// Create transaction (no value - function is nonpayable)
	tx := types.NewTransaction(
		nonce,
		s.payments,
		big.NewInt(0), // No value for nonpayable function
		gasLimit,
		gasPrice,
		callData,
	)

	// Sign transaction
	signedTx, err := types.SignTx(tx, types.NewEIP155Signer(chainID), key)
	if err != nil {
		return nil, fmt.Errorf("failed to sign transaction: %w", err)
	}

	// Send transaction
	if err := s.client.SendTransaction(ctx, signedTx); err != nil {
		return nil, fmt.Errorf("failed to send transaction: %w", err)
	}

	log.Printf("[Settler] Settlement tx sent for rail %d: %s", railID, signedTx.Hash().Hex())

	// Wait for receipt
	receipt, err := bind.WaitMined(ctx, s.client, signedTx)
	if err != nil {
		return nil, fmt.Errorf("failed to wait for transaction: %w", err)
	}

	if receipt.Status != types.ReceiptStatusSuccessful {
		return nil, fmt.Errorf("settlement transaction failed")
	}

	log.Printf("[Settler] Settlement confirmed for rail %d in block %d", railID, receipt.BlockNumber.Uint64())

	// Try to extract amount from logs
	amount := "0"
	for _, vLog := range receipt.Logs {
		if len(vLog.Topics) > 0 && vLog.Topics[0] == RailSettledTopic {
			if len(vLog.Data) >= 32 {
				amount = new(big.Int).SetBytes(vLog.Data[0:32]).String()
			}
			break
		}
	}

	return &SettlementResult{
		RailID:      railID,
		TxHash:      signedTx.Hash().Hex(),
		BlockNumber: receipt.BlockNumber.Uint64(),
		Amount:      amount,
	}, nil
}

// SettleDataSet settles all payment rails for a data set
func (s *Settler) SettleDataSet(ctx context.Context, privateKey string, dataSetID uint64) ([]*SettlementResult, error) {
	// Get rail IDs
	rails, err := s.GetDataSetRails(ctx, dataSetID)
	if err != nil {
		return nil, fmt.Errorf("failed to get data set rails: %w", err)
	}

	log.Printf("[Settler] Data set %d rails: PDP=%d, CDN=%d, CacheMiss=%d",
		dataSetID, rails.PDPRailID, rails.CDNRailID, rails.CacheMissRailID)

	var results []*SettlementResult

	// Settle PDP rail
	if rails.PDPRailID > 0 {
		log.Printf("[Settler] Settling PDP rail %d...", rails.PDPRailID)
		result, err := s.SettleRail(ctx, privateKey, rails.PDPRailID)
		if err != nil {
			log.Printf("[Settler] Warning: Failed to settle PDP rail: %v", err)
		} else {
			results = append(results, result)
		}
	}

	// Settle CDN rail (if exists)
	if rails.CDNRailID > 0 {
		log.Printf("[Settler] Settling CDN rail %d...", rails.CDNRailID)
		result, err := s.SettleRail(ctx, privateKey, rails.CDNRailID)
		if err != nil {
			log.Printf("[Settler] Warning: Failed to settle CDN rail: %v", err)
		} else {
			results = append(results, result)
		}
	}

	// Settle CacheMiss rail (if exists)
	if rails.CacheMissRailID > 0 {
		log.Printf("[Settler] Settling CacheMiss rail %d...", rails.CacheMissRailID)
		result, err := s.SettleRail(ctx, privateKey, rails.CacheMissRailID)
		if err != nil {
			log.Printf("[Settler] Warning: Failed to settle CacheMiss rail: %v", err)
		} else {
			results = append(results, result)
		}
	}

	if len(results) == 0 {
		return nil, fmt.Errorf("no rails were settled")
	}

	return results, nil
}

// Close closes the client connection
func (s *Settler) Close() {
	s.client.Close()
}
