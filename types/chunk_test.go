package types

import (
	"math/big"
	"testing"

	"github.com/scroll-tech/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestChunks_Append(t *testing.T) {
	BlocksPerChunk = 2
	chunks := NewChunks()

	blockContext := []byte("123")
	txPayloads := []byte("abc")
	txHashes := []common.Hash{common.BigToHash(big.NewInt(1)), common.BigToHash(big.NewInt(2))}
	chunks.Append(blockContext, txPayloads, txHashes)
	require.EqualValues(t, 1, len(chunks.data))
	require.EqualValues(t, 1, chunks.data[0].blockNum)
	require.EqualValues(t, blockContext, chunks.data[0].blockContext)
	require.EqualValues(t, txPayloads, chunks.data[0].txsPayload)
	require.EqualValues(t, len(txHashes), len(chunks.data[0].txHashes))
	require.EqualValues(t, txHashes[0], chunks.data[0].txHashes[0])
	require.EqualValues(t, txHashes[1], chunks.data[0].txHashes[1])
	require.False(t, chunks.IsChunksFull())

	blockContext1 := []byte("456")
	txPayloads1 := []byte("def")
	chunks.Append(blockContext1, txPayloads1, nil)
	require.EqualValues(t, 1, len(chunks.data))
	require.EqualValues(t, 2, chunks.data[0].blockNum)
	require.EqualValues(t, append(blockContext, blockContext1...), chunks.data[0].blockContext)
	require.EqualValues(t, append(txPayloads, txPayloads1...), chunks.data[0].txsPayload)
	require.EqualValues(t, len(txHashes), len(chunks.data[0].txHashes))
	require.True(t, chunks.IsChunksFull())

	blockContext2 := []byte("789")
	txPayloads2 := []byte("ghi")
	txHashes2 := []common.Hash{common.BigToHash(big.NewInt(3))}
	chunks.Append(blockContext2, txPayloads2, txHashes2)
	require.EqualValues(t, 2, len(chunks.data))
	require.EqualValues(t, 2, chunks.data[0].blockNum)
	require.EqualValues(t, 1, chunks.data[1].blockNum)
	require.EqualValues(t, append(blockContext, blockContext1...), chunks.data[0].blockContext)
	require.EqualValues(t, append(txPayloads, txPayloads1...), chunks.data[0].txsPayload)
	require.EqualValues(t, blockContext2, chunks.data[1].blockContext)
	require.EqualValues(t, txPayloads2, chunks.data[1].txsPayload)
	require.EqualValues(t, len(txHashes), len(chunks.data[0].txHashes))
	require.EqualValues(t, len(txHashes2), len(chunks.data[1].txHashes))
	require.EqualValues(t, txHashes2[0], chunks.data[1].txHashes[0])
	require.False(t, chunks.IsChunksFull())

	require.EqualValues(t, 3, chunks.blockNum)
	require.EqualValues(t, 2+len(blockContext)+len(blockContext1)+len(blockContext2)+len(txPayloads)+len(txPayloads1)+len(txPayloads2), chunks.size)
}
