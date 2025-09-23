package cmd

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
	fmt.Printf("DEBUG: Calling method '%s' with %d arguments\n", methodName, len(args))
	for i, arg := range args {
		fmt.Printf("DEBUG: Arg %d: %v (type: %T)\n", i, arg, arg)
	}

	callData, err := cw.buildCallData(methodName, args)
	if err != nil {
		return nil, fmt.Errorf("failed to build call data: %w", err)
	}
	fmt.Printf("DEBUG: Call data: 0x%x\n", callData)

	callMsg := cw.buildCallMsg(callData)
	fmt.Printf("DEBUG: Contract address: %s\n", cw.address.Hex())
	fmt.Printf("DEBUG: Call message: To=%s, Data=0x%x\n", callMsg.To.Hex(), callMsg.Data)

	result, err := cw.client.CallContract(context.Background(), callMsg, nil)
	if err != nil {
		fmt.Printf("DEBUG: Contract call failed: %v\n", err)
		return nil, fmt.Errorf("contract call failed: %w", err)
	}

	fmt.Printf("DEBUG: Contract call successful, result: 0x%x\n", result)
	return result, nil
}

func (cw *ContractWrapper) SendTransaction(methodName string, args []interface{}, privateKey *ecdsa.PrivateKey, gasLimit uint64) (*types.Transaction, error) {
	fmt.Printf("DEBUG: Sending transaction for method '%s' with %d arguments\n", methodName, len(args))
	for i, arg := range args {
		fmt.Printf("DEBUG: Arg %d: %v (type: %T)\n", i, arg, arg)
	}

	callData, err := cw.buildCallData(methodName, args)
	if err != nil {
		return nil, fmt.Errorf("failed to build call data: %w", err)
	}
	fmt.Printf("DEBUG: Call data: 0x%x\n", callData)

	// Get the sender address
	fromAddress := crypto.PubkeyToAddress(privateKey.PublicKey)
	fmt.Printf("DEBUG: Sender address: %s\n", fromAddress.Hex())

	// Get nonce
	nonce, err := cw.client.PendingNonceAt(context.Background(), fromAddress)
	if err != nil {
		return nil, fmt.Errorf("failed to get nonce: %w", err)
	}
	fmt.Printf("DEBUG: Nonce: %d\n", nonce)

	// Get gas price
	gasPrice, err := cw.client.SuggestGasPrice(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to get gas price: %w", err)
	}
	fmt.Printf("DEBUG: Gas price: %s\n", gasPrice.String())

	// Estimate gas if not provided
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
		fmt.Printf("DEBUG: Estimated gas: %d\n", gasLimit)
	}

	// Create transaction
	tx := types.NewTransaction(nonce, cw.address, big.NewInt(0), gasLimit, gasPrice, callData)
	fmt.Printf("DEBUG: Transaction created: To=%s, Value=%s, GasLimit=%d, GasPrice=%s\n",
		tx.To().Hex(), tx.Value().String(), tx.Gas(), tx.GasPrice().String())

	// Sign transaction
	chainID, err := cw.client.NetworkID(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to get chain ID: %w", err)
	}
	fmt.Printf("DEBUG: Chain ID: %s\n", chainID.String())

	signedTx, err := types.SignTx(tx, types.NewEIP155Signer(chainID), privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to sign transaction: %w", err)
	}
	fmt.Printf("DEBUG: Transaction signed\n")

	// Send transaction
	err = cw.client.SendTransaction(context.Background(), signedTx)
	if err != nil {
		return nil, fmt.Errorf("failed to send transaction: %w", err)
	}

	fmt.Printf("DEBUG: Transaction sent successfully, hash: %s\n", signedTx.Hash().Hex())
	return signedTx, nil
}

func (cw *ContractWrapper) buildCallData(methodName string, args []interface{}) ([]byte, error) {
	methodSig := fmt.Sprintf("%s(%s)", methodName, cw.getMethodSignature(args))
	fmt.Printf("DEBUG: Method signature: %s\n", methodSig)

	hash := sha3.NewLegacyKeccak256()
	hash.Write([]byte(methodSig))
	hashBytes := hash.Sum(nil)
	methodSelector := hashBytes[:4]
	fmt.Printf("DEBUG: Keccak256 hash: 0x%x\n", hashBytes)
	fmt.Printf("DEBUG: Method selector (first 4 bytes): 0x%x\n", methodSelector)

	if len(args) == 0 {
		return methodSelector, nil
	}

	// Encode arguments
	encodedArgs, err := cw.encodeArguments(args)
	if err != nil {
		return nil, fmt.Errorf("failed to encode arguments: %w", err)
	}
	fmt.Printf("DEBUG: Encoded arguments: 0x%x\n", encodedArgs)

	// Combine method selector with encoded arguments
	callData := append(methodSelector, encodedArgs...)
	fmt.Printf("DEBUG: Final call data: 0x%x\n", callData)

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
	// Create a simple ABI encoding for the arguments
	var encoded []byte

	for _, arg := range args {
		switch v := arg.(type) {
		case common.Address:
			// Address: 32 bytes, padded with zeros on the left
			padded := make([]byte, 32)
			copy(padded[12:], v.Bytes())
			encoded = append(encoded, padded...)
		case *big.Int:
			// Uint256: 32 bytes, padded with zeros on the left
			padded := make([]byte, 32)
			bytes := v.Bytes()
			copy(padded[32-len(bytes):], bytes)
			encoded = append(encoded, padded...)
		case bool:
			// Bool: 32 bytes, 1 for true, 0 for false
			padded := make([]byte, 32)
			if v {
				padded[31] = 1
			}
			encoded = append(encoded, padded...)
		case string:
			// String: length + data (simplified encoding)
			// For now, treat as bytes32
			padded := make([]byte, 32)
			copy(padded, []byte(v))
			encoded = append(encoded, padded...)
		default:
			return nil, fmt.Errorf("unsupported argument type: %T", arg)
		}
	}

	return encoded, nil
}

func (cw *ContractWrapper) Close() {
	if cw.client != nil {
		cw.client.Close()
	}
}
