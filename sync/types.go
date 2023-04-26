package sync

import (
	"github.com/scroll-tech/go-ethereum/common"
	"github.com/scroll-tech/go-ethereum/core/types"
)

// L1MessageTx is used temporally, until l2-geth introduces new features from scroll
//type L1MessageTx struct {
//	QueueIndex uint64
//	Gas        uint64          // gas limit
//	To         *common.Address // can not be nil, we do not allow contract creation from L1
//	Value      *big.Int
//	Data       []byte
//	Sender     common.Address
//}

type L1Message struct {
	types.L1MessageTx
	L1Height uint64
	L1TxHash common.Hash
}
