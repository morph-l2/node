package e2e

import (
	"math/big"

	"github.com/morphism-labs/node/e2e/configs"
	"github.com/morphism-labs/node/types"
	"github.com/morphism-labs/node/types/bindings"
	"github.com/scroll-tech/go-ethereum/common"
	eth "github.com/scroll-tech/go-ethereum/core/types"
)

func MockL1Message(from, to common.Address, nonce uint64, depositAmount *big.Int) (*types.L1Message, []byte, error) {
	bridgeAbi, err := bindings.L1StandardBridgeMetaData.GetAbi()
	if err != nil {
		return nil, nil, err
	}
	// FinalizeBridgeETH(opts *bind.TransactOpts, _from common.Address, _to common.Address, _amount *big.Int, _extraData []byte)
	finalizeBridgeETHInput, err := bridgeAbi.Pack("finalizeBridgeETH", from, to, depositAmount, []byte{})

	messengerAbi, err := bindings.L1CrossDomainMessengerMetaData.GetAbi()
	if err != nil {
		return nil, nil, err
	}
	// RelayMessage(opts *bind.TransactOpts, _nonce *big.Int, _sender common.Address, _target common.Address, _value *big.Int, _minGasLimit *big.Int, _message []byte)
	// L1StandardBridge as the sender to relay message
	relayMessageSender := configs.L1StandardBridgeAddress
	// L2StandardBridge address
	target := configs.L2StandardBridgeAddress
	value := depositAmount
	minGasLimit := configs.DepositETHGasLimit
	encodedNonce := types.EncodeNonce(nonce)
	relayMessageInput, err := messengerAbi.Pack("relayMessage", encodedNonce, relayMessageSender, target, value, minGasLimit, finalizeBridgeETHInput)
	if err != nil {
		return nil, nil, err
	}

	// sender is the alias of L1CrossMessenger address on L2
	sender := configs.L1CrossDomainMessengerAddressAlias
	l1Message := types.L1Message{
		L1MessageTx: eth.L1MessageTx{
			QueueIndex: nonce,
			Gas:        500_000,
			To:         &configs.L2CrossDomainMessengerAddress,
			Value:      value,
			Sender:     sender,
			Data:       relayMessageInput,
		},
	}
	return &l1Message, relayMessageInput, nil
}
