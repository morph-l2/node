package node

import (
	"math/big"
	"testing"

	"github.com/morphism-labs/node/types"
	"github.com/scroll-tech/go-ethereum/common"
	gethTypes "github.com/scroll-tech/go-ethereum/core/types"
	"github.com/scroll-tech/go-ethereum/rlp"
	"github.com/stretchr/testify/require"
)

func TestExecutor_updateLatestProcessedL1Index(t *testing.T) {

	to := common.BigToAddress(big.NewInt(101))
	msg := types.L1Message{
		L1MessageTx: gethTypes.L1MessageTx{
			QueueIndex: 200,
			Gas:        500000,
			To:         &to,
			Value:      big.NewInt(3e9),
			Data:       []byte("0x1a2b3c"),
			Sender:     common.BigToAddress(big.NewInt(202)),
		},
		L1TxHash: common.BigToHash(big.NewInt(1111)),
	}
	bytes, err := rlp.EncodeToBytes(&msg)
	require.NoError(t, err)
	require.NotNil(t, bytes)

	var txs [][]byte = make([][]byte, 1)
	txs[0] = bytes
	require.NotNil(t, txs)

	//prepare context
	ctx := PrepareContext()
	//executor
	nodeConfig := DefaultConfig()
	nodeConfig.SetCliContext(ctx)
	executor, err := NewExecutor(nodeConfig)
	require.NotNil(t, executor)
	require.NoError(t, err)

	err = executor.updateLatestProcessedL1Index(txs)
	require.NoError(t, err)

}
