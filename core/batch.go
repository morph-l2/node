package node

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/morphism-labs/morphism-bindings/bindings"
	"github.com/morphism-labs/node/types"
	"github.com/scroll-tech/go-ethereum/common"
	eth "github.com/scroll-tech/go-ethereum/core/types"
	tmtypes "github.com/tendermint/tendermint/types"
	"math/big"
)

type BatchingCache struct {
	parentBatchHeader types.BatchHeader
	prevStateRoot     common.Hash

	chunks               types.Chunks
	totalL1MessagePopped uint64
	skippedBitmap        []*big.Int
	postStateRoot        common.Hash
	withdrawRoot         common.Hash

	currentBlockContext               []byte
	currentTxsPayload                 []byte
	currentTxsHashes                  []common.Hash
	totalL1MessagePoppedAfterCurBlock uint64
	skippedBitmapAfterCurBlock        []*big.Int
	currentStateRoot                  common.Hash
	currentWithdrawRoot               common.Hash
}

func NewBatchingCache() *BatchingCache {
	return &BatchingCache{}
}

func (bc *BatchingCache) IsEmpty() bool {
	return len(bc.currentBlockContext) == 0
}

func (bc *BatchingCache) ClearCurrent() {
	bc.currentTxsPayload = nil
	bc.currentBlockContext = nil
	bc.skippedBitmapAfterCurBlock = nil
	bc.totalL1MessagePoppedAfterCurBlock = 0
}

type GetFromBatchStartFunc func() ([][]byte, []tmtypes.Txs, error)

func (e *Executor) CalculateBatchSizeWithCurrentBlock(parentBatchHeaderBytes []byte, get GetFromBatchStartFunc, currentBlockBytes []byte, currentTxs tmtypes.Txs) (int, error) {
	if e.batchingCache.IsEmpty() {
		parentBatchHeader, err := types.DecodeBatchHeader(parentBatchHeaderBytes)
		if err != nil {
			return 0, err
		}

		blocks, transactions, err := get()
		if err != nil {
			return 0, err
		}

		// skipped L1 message bitmap, an array of 256-bit bitmaps
		var skippedBitmap []*big.Int
		var txsPayload []byte
		var txHashes []common.Hash
		var totalL1MessagePopped = parentBatchHeader.TotalL1MessagePopped
		var lastHeightBeforeCurrentBatch uint64

		for i, blockBz := range blocks {
			wBlock := new(types.WrappedBlock)
			if err = new(types.WrappedBlock).UnmarshalBinary(blockBz); err != nil {
				return 0, err
			}

			if i == 0 {
				lastHeightBeforeCurrentBatch = wBlock.Number - 1
			}

			totalL1MessagePoppedBefore := totalL1MessagePopped
			txsPayload, txHashes, totalL1MessagePopped, skippedBitmap, err = ParsingTxs(transactions[i], parentBatchHeader.TotalL1MessagePopped, totalL1MessagePoppedBefore, skippedBitmap)
			blockContext := wBlock.BlockContextBytes(len(transactions), int(totalL1MessagePopped-totalL1MessagePoppedBefore))
			e.batchingCache.chunks.Append(blockContext, txsPayload, txHashes)
			e.batchingCache.totalL1MessagePopped = totalL1MessagePopped
		}
		e.batchingCache.parentBatchHeader = *parentBatchHeader
		e.batchingCache.skippedBitmap = skippedBitmap

		header, err := e.l2Client.HeaderByNumber(context.Background(), big.NewInt(int64(lastHeightBeforeCurrentBatch)))
		if err != nil {
			return 0, err
		}
		e.batchingCache.prevStateRoot = header.Root
	}

	currentTxsPayload, currentTxsHashes, totalL1MessagePopped, skippedBitmap, err := ParsingTxs(currentTxs, e.batchingCache.parentBatchHeader.TotalL1MessagePopped, e.batchingCache.totalL1MessagePopped, e.batchingCache.skippedBitmap)
	if err != nil {
		return 0, err
	}
	var curBlock = new(types.WrappedBlock)
	if err = curBlock.UnmarshalBinary(currentBlockBytes); err != nil {
		return 0, err
	}
	currentBlockContext := curBlock.BlockContextBytes(currentTxs.Len(), int(totalL1MessagePopped-e.batchingCache.totalL1MessagePopped))
	e.batchingCache.currentBlockContext = currentBlockContext
	e.batchingCache.currentTxsPayload = currentTxsPayload
	e.batchingCache.currentTxsHashes = currentTxsHashes
	e.batchingCache.totalL1MessagePoppedAfterCurBlock = totalL1MessagePopped
	e.batchingCache.skippedBitmapAfterCurBlock = skippedBitmap
	e.batchingCache.currentStateRoot = curBlock.StateRoot
	e.batchingCache.currentWithdrawRoot = curBlock.WithdrawTrieRoot

	/**
	 * commit batch which includes the fields:
	 *  version: 1 byte
	 *  parentBatchHeader
	 *  chunks
	 *  skippedL1MessageBitmap
	 *  prevStateRoot: 32 byte
	 *  postStateRoot: 32 byte
	 *  withdrawRoot:  32 byte
	 */
	chunksSizeWithCurBlock := e.batchingCache.chunks.Size() + len(currentTxsPayload) + len(currentBlockContext)
	// current block will be filled in a new chunk
	if e.batchingCache.chunks.IsChunksFull() {
		chunksSizeWithCurBlock += 1
	}
	batchSize := 97 + len(e.batchingCache.skippedBitmapAfterCurBlock)*32 + len(parentBatchHeaderBytes) + chunksSizeWithCurBlock
	return batchSize, nil
}

func (e *Executor) PackCurrentBlock() error {
	if e.batchingCache.IsEmpty() {
		return errors.New("incorrect process. should not pack current block before calculating batch size with current block ")
	}
	e.batchingCache.chunks.Append(e.batchingCache.currentBlockContext, e.batchingCache.currentTxsPayload, e.batchingCache.currentTxsHashes)
	e.batchingCache.skippedBitmap = e.batchingCache.skippedBitmapAfterCurBlock
	e.batchingCache.totalL1MessagePopped = e.batchingCache.totalL1MessagePoppedAfterCurBlock
	e.batchingCache.withdrawRoot = e.batchingCache.currentWithdrawRoot
	e.batchingCache.postStateRoot = e.batchingCache.currentStateRoot
	e.batchingCache.ClearCurrent()
	return nil
}

func (e *Executor) SealBatch() ([]byte, []byte, error) {
	if e.batchingCache.IsEmpty() {
		return nil, nil, errors.New("failed to seal batch. No data found in batch cache")
	}
	chunksBytes, err := e.batchingCache.chunks.Encode()
	if err != nil {
		return nil, nil, err
	}

	var skippedL1MessageBitmapBytes []byte
	for _, each := range e.batchingCache.skippedBitmap {
		skippedL1MessageBitmapBytes = append(skippedL1MessageBitmapBytes, each.Bytes()...)
	}
	batchData := bindings.IRollupBatchData{
		Version:                0,
		ParentBatchHeader:      e.batchingCache.parentBatchHeader.Encode(),
		Chunks:                 chunksBytes,
		SkippedL1MessageBitmap: skippedL1MessageBitmapBytes,
		PrevStateRoot:          e.batchingCache.prevStateRoot,
		PostStateRoot:          e.batchingCache.postStateRoot,
		WithdrawRoot:           e.batchingCache.withdrawRoot,
	}
	batchDataBytes, err := e.rollupABI.Pack("commitBatch", batchData)
	if err != nil {
		return nil, nil, err
	}
	// todo store the data
	e.logger.Info("packed batch data", "batch data", batchDataBytes)

	batchHeader := types.BatchHeader{
		Version:                0,
		BatchIndex:             e.batchingCache.parentBatchHeader.BatchIndex + 1,
		L1MessagePopped:        e.batchingCache.totalL1MessagePopped - e.batchingCache.parentBatchHeader.TotalL1MessagePopped,
		TotalL1MessagePopped:   e.batchingCache.totalL1MessagePopped,
		DataHash:               e.batchingCache.chunks.DataHash(),
		ParentBatchHash:        e.batchingCache.parentBatchHeader.Hash(),
		SkippedL1MessageBitmap: skippedL1MessageBitmapBytes,
	}
	batchHash := batchHeader.Hash()

	return batchHeader.Encode(), batchHash[:], err
}

func ParsingTxs(transactions tmtypes.Txs, totalL1MessagePoppedBeforeTheBatch, totalL1MessagePoppedBefore uint64, skippedBitmapBefore []*big.Int) (txsPayload []byte, txHashes []common.Hash, totalL1MessagePopped uint64, skippedBitmap []*big.Int, err error) {
	// the first queue index that belongs to this batch
	baseIndex := totalL1MessagePoppedBeforeTheBatch
	// the next queue index that we need to process
	nextIndex := totalL1MessagePoppedBefore

	skippedBitmap = make([]*big.Int, len(skippedBitmapBefore))
	for i, bm := range skippedBitmapBefore {
		skippedBitmap[i] = new(big.Int).SetBytes(bm.Bytes())
	}

	for i, txBz := range transactions {
		var tx eth.Transaction
		if err = tx.UnmarshalBinary(txBz); err != nil {
			return nil, nil, 0, nil, fmt.Errorf("transaction %d is not valid: %v", i, err)
		}
		txHashes = append(txHashes, tx.Hash())

		if isL1MessageTxType(txBz) {

			currentIndex := tx.L1MessageQueueIndex()

			if currentIndex < nextIndex {
				return nil, nil, 0, nil, fmt.Errorf("unexpected batch payload, expected queue index: %d, got: %d. transaction hash: %v", nextIndex, currentIndex, tx.Hash())
			}

			// mark skipped messages
			for skippedIndex := nextIndex; skippedIndex < currentIndex; skippedIndex++ {
				quo := int((skippedIndex - baseIndex) / 256)
				rem := int((skippedIndex - baseIndex) % 256)
				for len(skippedBitmap) <= quo {
					bitmap := big.NewInt(0)
					skippedBitmap = append(skippedBitmap, bitmap)
				}
				skippedBitmap[quo].SetBit(skippedBitmap[quo], rem, 1)
			}

			// process included message
			quo := int((currentIndex - baseIndex) / 256)
			for len(skippedBitmap) <= quo {
				bitmap := big.NewInt(0)
				skippedBitmap = append(skippedBitmap, bitmap)
			}

			nextIndex = currentIndex + 1
			continue
		}

		var txLen [4]byte
		binary.BigEndian.PutUint32(txLen[:], uint32(len(txBz)))
		txsPayload = append(txsPayload, txLen[:]...)
		txsPayload = append(txsPayload, txBz...)
	}

	totalL1MessagePopped = nextIndex
	return
}
