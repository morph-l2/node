package sync

import (
	"context"
	"fmt"
	"math/big"

	nodecommon "github.com/morphism-labs/node/common"
	"github.com/morphism-labs/node/types"
	"github.com/scroll-tech/go-ethereum"
	"github.com/scroll-tech/go-ethereum/common"
	eth "github.com/scroll-tech/go-ethereum/core/types"
	"github.com/scroll-tech/go-ethereum/ethclient"
	"github.com/scroll-tech/go-ethereum/log"
	"github.com/scroll-tech/go-ethereum/rpc"
)

type BridgeClient struct {
	l1Client               *ethclient.Client
	depositContractAddress common.Address
	confirmations          rpc.BlockNumber
}

func NewBridgeClient(l1Client *ethclient.Client, depositContractAddress common.Address, confirmations rpc.BlockNumber) *BridgeClient {
	return &BridgeClient{
		l1Client:               l1Client,
		depositContractAddress: depositContractAddress,
		confirmations:          confirmations,
	}
}

func (c *BridgeClient) L1Messages(ctx context.Context, from, to uint64) ([]types.L1Message, error) {
	query := ethereum.FilterQuery{
		FromBlock: big.NewInt(0).SetUint64(from),
		ToBlock:   big.NewInt(0).SetUint64(to),
		Addresses: []common.Address{
			c.depositContractAddress,
		},
		Topics: [][]common.Hash{
			{DepositEventABIHash},
		},
	}

	logs, err := c.l1Client.FilterLogs(ctx, query)
	if err != nil {
		log.Trace("eth_getLogs failed", "query", query, "err", err)
		return nil, fmt.Errorf("eth_getLogs failed: %w", err)
	}

	if len(logs) == 0 {
		return nil, nil
	}

	txs := make([]types.L1Message, len(logs), len(logs))
	for i, lg := range logs {
		l1MessageTx, err := UnmarshalDepositLogEvent(&lg)
		if err != nil {
			return nil, err
		}
		l1Message := types.L1Message{
			L1MessageTx: *l1MessageTx,
			L1TxHash:    lg.TxHash,
		}
		txs[i] = l1Message
	}
	return txs, nil
}

func (c *BridgeClient) L1MessagesFromTxHash(ctx context.Context, txHash common.Hash) ([]types.L1Message, error) {
	receipt, err := c.l1Client.TransactionReceipt(ctx, txHash)
	if err != nil {
		return nil, err
	}
	latestConfirmed, err := c.getLatestConfirmedBlockNumber(ctx)
	if err != nil {
		return nil, err
	}
	if receipt.BlockNumber.Uint64() > latestConfirmed {
		log.Warn("the target block has not been considered to be confirmed", "latestConfirmedHeight", latestConfirmed, "receiptAtBlockHeight", receipt.BlockNumber.Uint64())
		return nil, types.ErrNotConfirmedBlock
	}
	return deriveFromReceipt([]*eth.Receipt{receipt}, c.depositContractAddress)
}

func (c *BridgeClient) getLatestConfirmedBlockNumber(ctx context.Context) (uint64, error) {
	return nodecommon.GetLatestConfirmedBlockNumber(ctx, c.l1Client, c.confirmations)
}
