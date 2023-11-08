package types

import (
	"errors"

	"github.com/scroll-tech/go-ethereum/common"
	"github.com/scroll-tech/go-ethereum/crypto"
)

type Chunk struct {
	blockContext []byte
	txsPayload   []byte
	txHashes     []common.Hash
	blockNum     int
}

func NewChunk(blockContext, txsPayload []byte, txHashes []common.Hash) *Chunk {
	return &Chunk{
		blockContext: blockContext,
		txsPayload:   txsPayload,
		txHashes:     txHashes,
		blockNum:     1,
	}
}

func (ck *Chunk) Append(blockContext, txsPayload []byte, txHashes []common.Hash) {
	ck.blockContext = append(ck.blockContext, blockContext...)
	ck.txsPayload = append(ck.txsPayload, txsPayload...)
	ck.txHashes = append(ck.txHashes, txHashes...)
	ck.blockNum++
}

// Encode encodes the chunk into bytes
// Below is the encoding for `Chunk`, total 60*n+1+m bytes.
// Field           Bytes       Type            Index       Comments
// numBlocks       1           uint8           0           The number of blocks in this chunk
// block[0]        60          BlockContext    1           The first block in this chunk
// ......
// block[i]        60          BlockContext    60*i+1      The (i+1)'th block in this chunk
// ......
// block[n-1]      60          BlockContext    60*n-59     The last block in this chunk
// l2Transactions  dynamic     bytes           60*n+1
func (ck *Chunk) Encode() ([]byte, error) {
	if ck == nil || ck.blockNum == 0 {
		return []byte{}, nil
	}
	if ck.blockNum > 255 {
		return nil, errors.New("number of blocks exceeds 1 byte")
	}
	var chunkBytes []byte
	chunkBytes = append(chunkBytes, byte(ck.blockNum))
	chunkBytes = append(chunkBytes, ck.blockContext...)
	chunkBytes = append(chunkBytes, ck.txsPayload...)
	return chunkBytes, nil
}

func (ck *Chunk) Hash() common.Hash {
	var bytes []byte
	for i := 0; i < ck.blockNum; i++ {
		bytes = append(bytes, ck.blockContext[i*60:i*60+58]...)
	}
	for _, txHash := range ck.txHashes {
		bytes = append(bytes, txHash[:]...)
	}
	return crypto.Keccak256Hash(bytes)
}

// BlocksPerChunk further make it configable
var BlocksPerChunk = 1

type Chunks struct {
	data     []*Chunk
	blockNum int

	size int
}

func NewChunks() *Chunks {
	return &Chunks{
		data: make([]*Chunk, 0),
	}
}

func (cks *Chunks) Append(blockContext, txsPayload []byte, txHashes []common.Hash) {
	if cks == nil {
		return
	}

	if len(cks.data) == 0 {
		cks.data = append(cks.data, NewChunk(blockContext, txsPayload, txHashes))
		cks.blockNum = 1
		cks.size = 1 + len(blockContext) + len(txsPayload)
		return
	}

	lastChunk := cks.data[len(cks.data)-1]
	if lastChunk.blockNum >= BlocksPerChunk {
		cks.data = append(cks.data, NewChunk(blockContext, txsPayload, txHashes))
		cks.blockNum++
		cks.size += 1 + len(blockContext) + len(txsPayload)
		return
	}

	lastChunk.Append(blockContext, txsPayload, txHashes)
	cks.blockNum++
	cks.size += len(blockContext) + len(txsPayload)
	return
}

func (cks *Chunks) Encode() ([][]byte, error) {
	var bytes [][]byte
	for _, ck := range cks.data {
		ckBytes, err := ck.Encode()
		if err != nil {
			return nil, err
		}
		bytes = append(bytes, ckBytes)
	}
	return bytes, nil
}

func (cks *Chunks) DataHash() common.Hash {
	var chunkHashes []byte
	for _, ck := range cks.data {
		hash := ck.Hash()
		chunkHashes = append(chunkHashes, hash[:]...)
	}
	return crypto.Keccak256Hash(chunkHashes)
}

func (cks *Chunks) BlockNum() int { return cks.blockNum }
func (cks *Chunks) Size() int     { return cks.size }
func (cks *Chunks) IsChunksFull() bool {
	return cks.blockNum%BlocksPerChunk == 0
}
