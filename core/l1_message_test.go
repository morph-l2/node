package node

import (
	"bytes"
	"context"
	"math/big"
	"testing"

	"github.com/morphism-labs/node/db"
	"github.com/morphism-labs/node/sync"
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

func TestExecutor_validateL1Messages(t *testing.T) {
	//prepare msg
	to := common.BigToAddress(big.NewInt(101))
	msg := types.L1Message{
		L1MessageTx: gethTypes.L1MessageTx{
			QueueIndex: 1,
			Gas:        500000,
			To:         &to,
			Value:      big.NewInt(3e9),
			Data:       []byte("0x1a2b3c"),
			Sender:     common.BigToAddress(big.NewInt(202)),
		},
		L1TxHash: common.BigToHash(big.NewInt(1111)),
	}
	nbm := types.NonBLSMessage{
		StateRoot:   common.BigToHash(big.NewInt(1111)),
		GasUsed:     50000000,
		ReceiptRoot: common.BigToHash(big.NewInt(2222)),
		LogsBloom:   []byte("0x1a2b3c4d"),
		L1Messages:  []types.L1Message{msg},
	}

	//prepare context
	ctx := PrepareContext()

	//syncer
	store := prepareDB(msg)
	store.WriteLatestSyncedL1Height(100)
	syncConfig := sync.DefaultConfig()
	syncConfig.SetCliContext(ctx)
	syncer, err := sync.NewSyncer(context.Background(), store, syncConfig)
	require.NotNil(t, syncer)
	require.NoError(t, err)

	//SequencerExecutor
	nodeConfig := DefaultConfig()
	nodeConfig.SetCliContext(ctx)
	executor, err := NewSequencerExecutor(nodeConfig, syncer)
	require.NotNil(t, executor)
	require.NoError(t, err)

	//eip2718 rlp
	tx := msg.L1MessageTx
	var buf bytes.Buffer
	buf.WriteByte(126) //7E
	rlp.Encode(&buf, tx)
	var txs [][]byte = make([][]byte, 1)
	txs[0] = buf.Bytes()

	err = executor.validateL1Messages(txs, &nbm)
	require.NoError(t, err)
}

func prepareDB(msg types.L1Message) *db.Store {
	db := db.NewMemoryStore()
	msgs := make([]types.L1Message, 0)
	msgs = append(msgs, msg)
	db.WriteSyncedL1Messages(msgs, 0)
	return db
}
