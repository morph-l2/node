package node

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"

	"github.com/morphism-labs/morphism-bindings/bindings"
	"github.com/morphism-labs/node/types"
	"github.com/scroll-tech/go-ethereum/common"
	eth "github.com/scroll-tech/go-ethereum/core/types"
	"github.com/tendermint/tendermint/l2node"
	tmtypes "github.com/tendermint/tendermint/types"
)

type BatchingCache struct {
	parentBatchHeader types.BatchHeader
	prevStateRoot     common.Hash

	// accumulated batch data
	chunks               *types.Chunks
	totalL1MessagePopped uint64
	skippedBitmap        []*big.Int
	postStateRoot        common.Hash
	withdrawRoot         common.Hash
	// caches sealedBatchHeader according to the above accumulated batch data
	sealedBatchHeader *types.BatchHeader

	currentBlockContext               []byte
	currentTxsPayload                 []byte
	currentTxs                        tmtypes.Txs
	currentTxsHashes                  []common.Hash
	totalL1MessagePoppedAfterCurBlock uint64
	skippedBitmapAfterCurBlock        []*big.Int
	currentStateRoot                  common.Hash
	currentWithdrawRoot               common.Hash
}

func NewBatchingCache() *BatchingCache {
	return &BatchingCache{
		chunks: types.NewChunks(),
	}
}

func (bc *BatchingCache) IsEmpty() bool {
	return bc.chunks == nil || bc.chunks.Size() == 0
}

func (bc *BatchingCache) IsCurrentEmpty() bool {
	return len(bc.currentBlockContext) == 0
}

func (bc *BatchingCache) ClearCurrent() {
	bc.currentTxsPayload = nil
	bc.currentTxs = nil
	bc.currentTxsHashes = nil
	bc.currentBlockContext = nil
	bc.skippedBitmapAfterCurBlock = nil
	bc.totalL1MessagePoppedAfterCurBlock = 0
	bc.currentStateRoot = common.Hash{}
	bc.currentWithdrawRoot = common.Hash{}
}

func (e *Executor) CalculateBatchSizeWithProposalBlock(currentBlockBytes []byte, currentTxs tmtypes.Txs, get l2node.GetFromBatchStartFunc) (int64, error) {
	e.logger.Info("CalculateBatchSizeWithProposalBlock request", "block size", len(currentBlockBytes), "txs size", len(currentTxs))
	if e.batchingCache.IsEmpty() {
		parentBatchHeaderBytes, blocks, transactions, err := get()
		if err != nil {
			return 0, err
		}

		var parentBatchHeader types.BatchHeader
		if len(parentBatchHeaderBytes) == 0 {
			if parentBatchHeader, err = GenesisBatchHeader(e.l2Client); err != nil {
				return 0, err
			}
		} else {
			if parentBatchHeader, err = types.DecodeBatchHeader(parentBatchHeaderBytes); err != nil {
				return 0, err
			}
		}

		// skipped L1 message bitmap, an array of 256-bit bitmaps
		var skippedBitmap []*big.Int
		var txsPayload []byte
		var txHashes []common.Hash
		var totalL1MessagePopped = parentBatchHeader.TotalL1MessagePopped
		var lastHeightBeforeCurrentBatch uint64

		for i, blockBz := range blocks {
			wBlock := new(types.WrappedBlock)
			if err = wBlock.UnmarshalBinary(blockBz); err != nil {
				return 0, err
			}

			if i == 0 {
				lastHeightBeforeCurrentBatch = wBlock.Number - 1
			}

			totalL1MessagePoppedBefore := totalL1MessagePopped
			txsPayload, txHashes, totalL1MessagePopped, skippedBitmap, err = ParsingTxs(transactions[i], parentBatchHeader.TotalL1MessagePopped, totalL1MessagePoppedBefore, skippedBitmap)
			if err != nil {
				return 0, err
			}
			blockContext := wBlock.BlockContextBytes(len(transactions), int(totalL1MessagePopped-totalL1MessagePoppedBefore))
			e.batchingCache.chunks.Append(blockContext, txsPayload, txHashes)
			e.batchingCache.totalL1MessagePopped = totalL1MessagePopped
		}
		e.batchingCache.parentBatchHeader = parentBatchHeader
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
	e.batchingCache.currentTxs = currentTxs
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
	batchSize := 97 + len(e.batchingCache.skippedBitmapAfterCurBlock)*32 + len(e.batchingCache.parentBatchHeader.Encode()) + chunksSizeWithCurBlock
	e.logger.Info("CalculateBatchSizeWithProposalBlock response", "batchSize", batchSize)
	return int64(batchSize), nil
}

func (e *Executor) SealBatch() ([]byte, []byte, error) {
	if e.batchingCache.IsEmpty() {
		return nil, nil, errors.New("failed to seal batch. No data found in batch cache")
	}

	var skippedL1MessageBitmapBytes []byte
	for _, each := range e.batchingCache.skippedBitmap {
		skippedL1MessageBitmapBytes = append(skippedL1MessageBitmapBytes, each.Bytes()...)
	}
	batchHeader := types.BatchHeader{
		Version:                0,
		BatchIndex:             e.batchingCache.parentBatchHeader.BatchIndex + 1,
		L1MessagePopped:        e.batchingCache.totalL1MessagePopped - e.batchingCache.parentBatchHeader.TotalL1MessagePopped,
		TotalL1MessagePopped:   e.batchingCache.totalL1MessagePopped,
		DataHash:               e.batchingCache.chunks.DataHash(),
		ParentBatchHash:        e.batchingCache.parentBatchHeader.Hash(),
		SkippedL1MessageBitmap: skippedL1MessageBitmapBytes,
	}
	e.batchingCache.sealedBatchHeader = &batchHeader
	batchHash := batchHeader.Hash()
	e.logger.Info("Sealed batch header", "batchHash", batchHash.Hex())
	return batchHash[:], batchHeader.Encode(), nil
}

func (e *Executor) CommitBatch() error {
	if e.batchingCache.IsEmpty() || e.batchingCache.sealedBatchHeader == nil { // nothing to commit
		return nil
	}
	chunksBytes, err := e.batchingCache.chunks.Encode()
	if err != nil {
		return err
	}
	batchData := bindings.IRollupBatchData{
		Version:                0,
		ParentBatchHeader:      e.batchingCache.parentBatchHeader.Encode(),
		Chunks:                 chunksBytes,
		SkippedL1MessageBitmap: e.batchingCache.sealedBatchHeader.SkippedL1MessageBitmap,
		PrevStateRoot:          e.batchingCache.prevStateRoot,
		PostStateRoot:          e.batchingCache.postStateRoot,
		WithdrawRoot:           e.batchingCache.withdrawRoot,
	}
	batchDataBytes, err := e.rollupABI.Pack("commitBatch", batchData)
	if err != nil {
		return err
	}
	// todo store the data
	e.logger.Info("packed batch data", "batch data", batchDataBytes)

	// commit sealed batch header; move current block into the next batch
	e.batchingCache.parentBatchHeader = *e.batchingCache.sealedBatchHeader
	e.batchingCache.prevStateRoot = e.batchingCache.postStateRoot
	e.batchingCache.sealedBatchHeader = nil

	_, _, totalL1MessagePopped, skippedBitmap, err := ParsingTxs(e.batchingCache.currentTxs, e.batchingCache.totalL1MessagePopped, e.batchingCache.totalL1MessagePopped, nil)
	if err != nil {
		return err
	}
	e.batchingCache.totalL1MessagePopped = totalL1MessagePopped
	e.batchingCache.skippedBitmap = skippedBitmap
	e.batchingCache.postStateRoot = e.batchingCache.currentStateRoot
	e.batchingCache.withdrawRoot = e.batchingCache.currentWithdrawRoot
	e.batchingCache.chunks = types.NewChunks()
	e.batchingCache.chunks.Append(e.batchingCache.currentBlockContext, e.batchingCache.currentTxsPayload, e.batchingCache.currentTxsHashes)
	e.batchingCache.ClearCurrent()

	e.logger.Info("Committed batch")
	return nil
}

func (e *Executor) PackCurrentBlock() error {
	if e.batchingCache.IsCurrentEmpty() {
		return nil
	}
	if e.batchingCache.chunks == nil {
		e.batchingCache.chunks = types.NewChunks()
	}
	e.batchingCache.chunks.Append(e.batchingCache.currentBlockContext, e.batchingCache.currentTxsPayload, e.batchingCache.currentTxsHashes)
	e.batchingCache.skippedBitmap = e.batchingCache.skippedBitmapAfterCurBlock
	e.batchingCache.totalL1MessagePopped = e.batchingCache.totalL1MessagePoppedAfterCurBlock
	e.batchingCache.withdrawRoot = e.batchingCache.currentWithdrawRoot
	e.batchingCache.postStateRoot = e.batchingCache.currentStateRoot
	e.batchingCache.ClearCurrent()

	e.logger.Info("Packed current block into the batch")
	return nil
}

func (e *Executor) BatchHash(batchHeaderBytes []byte) ([]byte, error) {
	batchHeader, err := types.DecodeBatchHeader(batchHeaderBytes)
	if err != nil {
		return nil, err
	}
	return batchHeader.Hash().Bytes(), nil
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

func GenesisBatchHeader(l2Client *types.RetryableClient) (types.BatchHeader, error) {
	genesisHeader, err := l2Client.HeaderByNumber(context.Background(), big.NewInt(0))
	if err != nil {
		return types.BatchHeader{}, err
	}

	wb := types.WrappedBlock{
		ParentHash:  genesisHeader.ParentHash,
		Miner:       genesisHeader.Coinbase,
		Number:      genesisHeader.Number.Uint64(),
		GasLimit:    genesisHeader.GasLimit,
		BaseFee:     genesisHeader.BaseFee,
		Timestamp:   genesisHeader.Time,
		StateRoot:   genesisHeader.Root,
		GasUsed:     genesisHeader.GasUsed,
		ReceiptRoot: genesisHeader.ReceiptHash,
	}
	blockContext := wb.BlockContextBytes(0, 0)
	chunks := types.NewChunks()
	chunks.Append(blockContext, nil, nil)

	return types.BatchHeader{
		Version:              0,
		BatchIndex:           0,
		L1MessagePopped:      0,
		TotalL1MessagePopped: 0,
		DataHash:             chunks.DataHash(),
		ParentBatchHash:      common.Hash{},
	}, nil
}