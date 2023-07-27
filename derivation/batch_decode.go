package derivation

import (
	"bytes"
	"encoding/binary"
	"github.com/morphism-labs/morphism-bindings/bindings"
	"github.com/scroll-tech/go-ethereum/core/types"
	"github.com/scroll-tech/go-ethereum/rlp"
	"math/big"
)

type BatchData struct {
	Txs           []*types.Transaction
	BlockContexts []*BlockInfo
	Signature     *bindings.ZKEVMBatchSignature
}

// prev_state_root || last block state root || last block withdraw_trie_root || [block1, block2, ..., blockN] || [txHash1, txHash2, ..., txHashN] || [dummy_tx_hash, ..., dummy_tx_hash]
//type BlockContexts struct {
//	Blocks []*BlockInfo
//	TxHashes []common.Hash
//}

//type BlockContexts []*BlockInfo

// number || timestamp || base_fee || gas_limit || num_txs || tx_hashs
type BlockInfo struct {
	Number    *big.Int
	Timestamp uint64
	BaseFee   *big.Int
	GasLimit  uint64
	NumTxs    uint64
}

// decode blockcontext
func (b BatchData) DecodeBlockContext(endBlock uint64, bs []byte) error {
	b.BlockContexts = []*BlockInfo{}
	// [block1, block2, ..., blockN]
	reader := bytes.NewReader(bs)
	for {
		block := new(BlockInfo)
		// number || timestamp || base_fee || gas_limit || num_txs
		bsBlockNumber := make([]byte, 32)
		if _, err := reader.Read(bsBlockNumber[:]); err != nil {
			return err
		}
		block.Number = new(big.Int).SetBytes(bsBlockNumber)

		if err := binary.Read(reader, binary.BigEndian, &block.Timestamp); err != nil {
			return err
		}
		// [32]byte uint256
		bsBaseFee := make([]byte, 32)
		if _, err := reader.Read(bsBaseFee[:]); err != nil {
			return err
		}
		block.BaseFee = new(big.Int).SetBytes(bsBaseFee)
		if err := binary.Read(reader, binary.BigEndian, &block.GasLimit); err != nil {
			return err
		}
		if err := binary.Read(reader, binary.BigEndian, &block.NumTxs); err != nil {
			return err
		}
		b.BlockContexts = append(b.BlockContexts, block)
		//txCount += int(block.NumTxs)
		if block.Number.Uint64() == endBlock {
			break
		}
	}
	return nil
}

func (b BatchData) DecodeTransactions(bs []byte) error {
	return rlp.DecodeBytes(bs, &b.Txs)
}
