package config

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"golang.org/x/crypto/sha3"
)

type ContractWrapper struct {
	client  *ethclient.Client
	address common.Address
}

func NewContractWrapper(rpcURL, contractAddress string) (*ContractWrapper, error) {
	client, err := ethclient.Dial(rpcURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to RPC: %w", err)
	}

	address := common.HexToAddress(contractAddress)

	return &ContractWrapper{
		client:  client,
		address: address,
	}, nil
}

func (cw *ContractWrapper) CallMethod(methodName string, args []interface{}) ([]byte, error) {
	callData, err := cw.buildCallData(methodName, args)
	if err != nil {
		return nil, fmt.Errorf("failed to build call data: %w", err)
	}

	callMsg := cw.buildCallMsg(callData)
	result, err := cw.client.CallContract(context.Background(), callMsg, nil)
	if err != nil {
		return nil, fmt.Errorf("contract call failed: %w", err)
	}

	return result, nil
}

func (cw *ContractWrapper) SendTransaction(methodName string, args []interface{}, privateKey *ecdsa.PrivateKey, gasLimit uint64) (*types.Transaction, error) {
	callData, err := cw.buildCallData(methodName, args)
	if err != nil {
		return nil, fmt.Errorf("failed to build call data: %w", err)
	}

	fromAddress := crypto.PubkeyToAddress(privateKey.PublicKey)

	nonce, err := cw.client.PendingNonceAt(context.Background(), fromAddress)
	if err != nil {
		return nil, fmt.Errorf("failed to get nonce: %w", err)
	}

	gasPrice, err := cw.client.SuggestGasPrice(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to get gas price: %w", err)
	}

	if gasLimit == 0 {
		callMsg := ethereum.CallMsg{
			From: fromAddress,
			To:   &cw.address,
			Data: callData,
		}
		gasLimit, err = cw.client.EstimateGas(context.Background(), callMsg)
		if err != nil {
			return nil, fmt.Errorf("failed to estimate gas: %w", err)
		}
	}

	tx := types.NewTransaction(nonce, cw.address, big.NewInt(0), gasLimit, gasPrice, callData)

	chainID, err := cw.client.NetworkID(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to get chain ID: %w", err)
	}

	signedTx, err := types.SignTx(tx, types.NewEIP155Signer(chainID), privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to sign transaction: %w", err)
	}

	err = cw.client.SendTransaction(context.Background(), signedTx)
	if err != nil {
		return nil, fmt.Errorf("failed to send transaction: %w", err)
	}

	return signedTx, nil
}

func (cw *ContractWrapper) buildCallData(methodName string, args []interface{}) ([]byte, error) {
	methodSig := fmt.Sprintf("%s(%s)", methodName, cw.getMethodSignature(args))

	hash := sha3.NewLegacyKeccak256()
	hash.Write([]byte(methodSig))
	hashBytes := hash.Sum(nil)
	methodSelector := hashBytes[:4]

	if len(args) == 0 {
		return methodSelector, nil
	}

	encodedArgs, err := cw.encodeArguments(args)
	if err != nil {
		return nil, fmt.Errorf("failed to encode arguments: %w", err)
	}

	callData := append(methodSelector, encodedArgs...)
	return callData, nil
}

func (cw *ContractWrapper) getMethodSignature(args []interface{}) string {
	signatures := make([]string, len(args))
	for i, arg := range args {
		switch arg.(type) {
		case common.Address:
			signatures[i] = "address"
		case *big.Int:
			signatures[i] = "uint256"
		case bool:
			signatures[i] = "bool"
		case string:
			signatures[i] = "string"
		default:
			signatures[i] = "bytes"
		}
	}
	return strings.Join(signatures, ",")
}

func (cw *ContractWrapper) buildCallMsg(data []byte) ethereum.CallMsg {
	return ethereum.CallMsg{
		To:   &cw.address,
		Data: data,
	}
}

func (cw *ContractWrapper) encodeArguments(args []interface{}) ([]byte, error) {
	var head []byte
	var tail []byte
	var dynamicArgs []int
	var dynamicData [][]byte

	// First pass: encode static types, collect dynamic types
	for i, arg := range args {
		switch v := arg.(type) {
		case common.Address:
			padded := make([]byte, 32)
			copy(padded[12:], v.Bytes())
			head = append(head, padded...)
		case *big.Int:
			padded := make([]byte, 32)
			bytes := v.Bytes()
			copy(padded[32-len(bytes):], bytes)
			head = append(head, padded...)
		case bool:
			padded := make([]byte, 32)
			if v {
				padded[31] = 1
			}
			head = append(head, padded...)
		case string:
			dynamicArgs = append(dynamicArgs, i)
			head = append(head, make([]byte, 32)...)
			strBytes := []byte(v)
			strLen := len(strBytes)
			lenBytes := make([]byte, 32)
			bigLen := big.NewInt(int64(strLen)).Bytes()
			copy(lenBytes[32-len(bigLen):], bigLen)
			paddedLen := ((strLen + 31) / 32) * 32
			paddedData := make([]byte, paddedLen)
			copy(paddedData, strBytes)
			dyn := append(lenBytes, paddedData...)
			dynamicData = append(dynamicData, dyn)
		default:
			return nil, fmt.Errorf("unsupported argument type: %T", arg)
		}
	}

	// Second pass: fill in offsets for dynamic types and build tail
	headLen := len(args) * 32
	headWithOffsets := make([]byte, len(head))
	copy(headWithOffsets, head)
	tailOffset := headLen
	dynIdx := 0
	for i, arg := range args {
		switch arg.(type) {
		case string:
			offsetBytes := make([]byte, 32)
			bigOffset := big.NewInt(int64(tailOffset)).Bytes()
			copy(offsetBytes[32-len(bigOffset):], bigOffset)
			copy(headWithOffsets[i*32:(i+1)*32], offsetBytes)
			tail = append(tail, dynamicData[dynIdx]...)
			tailOffset += len(dynamicData[dynIdx])
			dynIdx++
		}
	}

	encoded := append(headWithOffsets, tail...)
	return encoded, nil
}

func (cw *ContractWrapper) Close() {
	if cw.client != nil {
		cw.client.Close()
	}
}
