package types

import (
	"github.com/scroll-tech/go-ethereum/common"
	"github.com/scroll-tech/go-ethereum/core/types"
)

type L1Message struct {
	types.L1MessageTx
	L1TxHash common.Hash
}
