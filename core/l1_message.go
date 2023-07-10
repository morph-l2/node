package node

import (
	"fmt"

	"github.com/morphism-labs/node/types"
	"github.com/scroll-tech/go-ethereum/common"
	eth "github.com/scroll-tech/go-ethereum/core/types"
	"github.com/scroll-tech/go-ethereum/log"
)

func (e *Executor) updateLatestProcessedL1Index(txs [][]byte) error {
	for i, txBytes := range txs {
		if !isL1MessageTxType(txBytes) {
			break
		}
		var tx eth.Transaction
		if err := tx.UnmarshalBinary(txBytes); err != nil {
			return fmt.Errorf("transaction %d is not valid: %v", i, err)
		}
		e.latestProcessedL1Index = &tx.AsL1MessageTx().QueueIndex
	}
	return nil
}

func (e *Executor) validateL1Messages(txs [][]byte, l1Msgs []types.L1Message) error {
	cache := make(map[uint64]common.Hash, len(l1Msgs))
	for _, msg := range l1Msgs {
		cache[msg.QueueIndex] = msg.L1TxHash
	}

	L1SectionOver := false
	var queueIndex int64 = -1
	if e.latestProcessedL1Index != nil {
		queueIndex = int64(*e.latestProcessedL1Index)
	}
	for i, txBz := range txs {
		if !isL1MessageTxType(txBz) {
			L1SectionOver = true
			continue
		}
		// check that L1 messages are before L2 transactions
		if L1SectionOver {
			return types.ErrInvalidL1MessageOrder
		}

		var tx eth.Transaction
		if err := tx.UnmarshalBinary(txBz); err != nil {
			return fmt.Errorf("transaction %d is not valid: %v", i, err)
		}
		queueIndex += 1

		// check queue index
		if tx.AsL1MessageTx().QueueIndex != uint64(queueIndex) {
			return types.ErrInvalidL1MessageOrder
		}

		txHash, ok := cache[uint64(queueIndex)]
		if !ok {
			return types.ErrInvalidL1Message
		}
		l1Message, err := e.syncer.GetL1Message(uint64(queueIndex), txHash)
		if err != nil {
			log.Warn("error getting L1 message from syncer", "error", err)
			return err
		}
		if l1Message == nil { // has not been synced from L1 yet
			log.Warn("the L1 message is not valid", "index", queueIndex, "L1TxHash", txHash.Hex())
			return types.ErrUnknownL1Message
		}

		if tx.Hash() != eth.NewTx(&l1Message.L1MessageTx).Hash() {
			return types.ErrUnknownL1Message
		}

	}
	return nil
}

func isL1MessageTxType(rlpEncoded []byte) bool {
	if len(rlpEncoded) == 0 {
		return false
	}
	return rlpEncoded[0] == eth.L1MessageTxType
}
