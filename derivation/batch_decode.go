package derivation

import (
	"bytes"
	"encoding/binary"
	"math/big"

	"github.com/morphism-labs/morphism-bindings/bindings"
	"github.com/scroll-tech/go-ethereum/common"
	"github.com/scroll-tech/go-ethereum/core/types"
	"github.com/scroll-tech/go-ethereum/rlp"
)

type BatchData struct {
	Txs           []*types.Transaction
	BlockContexts *BlockContexts
	Signature     *bindings.ZKEVMBatchSignature
}

// prev_state_root || last block state root || last block withdraw_trie_root || [block1, block2, ..., blockN] || [txHash1, txHash2, ..., txHashN] || [dummy_tx_hash, ..., dummy_tx_hash]
type BlockContexts struct {
	LastBlockStateRoot common.Hash
	WithdrawRoot       common.Hash
	Blocks             []*BlockInfo
	TxHashes           []common.Hash
	DummyTxHashes      []common.Hash
}

// number || timestamp || base_fee || gas_limit || num_txs || tx_hashs
type BlockInfo struct {
	Hash      common.Hash
	Number    *big.Int
	Timestamp uint64
	BaseFee   *big.Int
	GasLimit  uint64
	NumTxs    uint64
	NumL1Msgs uint64
}

// new batchdata
func NewBatchData(blocks []*types.Block, preStateRoot common.Hash) (*bindings.ZKEVMBatchData, error) {
	var txs []*types.Transaction
	bkWitness := make([]byte, 0)
	for _, block := range blocks {
		txs = append(txs, block.Transactions()...)
		// number || timestamp || base_fee || gas_limit || num_txs || tx_hashs
		bkWitness = append(bkWitness, block.Number().Bytes()...)
		bkWitness = binary.BigEndian.AppendUint64(bkWitness, block.Time())
		bkWitness = append(bkWitness, common.LeftPadBytes(block.BaseFee().Bytes(), 32)...)
		bkWitness = binary.BigEndian.AppendUint64(bkWitness, block.GasLimit())
		bkWitness = binary.BigEndian.AppendUint64(bkWitness, uint64(len(block.Transactions())))
		for _, tx := range block.Transactions() {
			bkWitness = append(bkWitness, tx.Hash().Bytes()...)
		}
	}

	txBs, err := rlp.EncodeToBytes(txs)
	if err != nil {
		return nil, err
	}
	return &bindings.ZKEVMBatchData{
		BlockNumber:   blocks[len(blocks)-1].Number().Uint64(),
		Transactions:  txBs,
		BlockWitness:  bkWitness,
		PreStateRoot:  preStateRoot,
		PostStateRoot: blocks[len(blocks)-1].Root(),
		WithdrawRoot:  *blocks[len(blocks)-1].Header().WithdrawalsHash,
		Signature: bindings.ZKEVMBatchSignature{
			Signers:   blocks[len(blocks)-1].Header().BLSData.BLSSigners,
			Signature: blocks[len(blocks)-1].Header().BLSData.BLSSignature,
		},
	}, nil

}

// decode blockcontext
func (b *BatchData) DecodeBlockContext(endBlock uint64, bs []byte) error {
	b.BlockContexts = new(BlockContexts)
	//prev_state_root || last block state root || last block withdraw_trie_root || [block1, block2, ..., blockN] || [txHash1, txHash2, ..., txHashN] || [dummy_tx_hash, ..., dummy_tx_hash]
	// read block count
	// read tx count
	_ = bindings.ZKEVMBatchData{}
	reader := bytes.NewReader(bs)

	//last block state root
	if _, err := reader.Read(b.BlockContexts.LastBlockStateRoot[:]); err != nil {
		return err
	}
	//last block withdraw_trie_root
	if _, err := reader.Read(b.BlockContexts.WithdrawRoot[:]); err != nil {
		return err
	}
	// read blocks
	txCount := 0
	for {
		block := new(BlockInfo)
		//blockHash || parentHash || number || timestamp || base_fee || gas_limit || num_txs || num_l1_msgs
		if _, err := reader.Read(block.Hash[:]); err != nil {
			return err
		}
		// [32]byte uint256
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
		if err := binary.Read(reader, binary.BigEndian, &block.NumL1Msgs); err != nil {
			return err
		}
		b.BlockContexts.Blocks = append(b.BlockContexts.Blocks, block)
		txCount += int(block.NumTxs)
		if block.Number.Uint64() == endBlock {
			break
		}
	}
	// read txhashes
	for i := 0; i < txCount; i++ {
		txHash := common.Hash{}
		if _, err := reader.Read(txHash[:]); err != nil {
			return err
		}
		b.BlockContexts.TxHashes = append(b.BlockContexts.TxHashes, txHash)
	}
	// read dummy txhashes
	for i := 0; i < txCount; i++ {
		dummyTxHash := common.Hash{}
		if _, err := reader.Read(dummyTxHash[:]); err != nil {
			return err
		}
		b.BlockContexts.DummyTxHashes = append(b.BlockContexts.DummyTxHashes, dummyTxHash)
	}

	return nil
}

func (b *BatchData) DecodeTransactions(bs []byte) error {
	// init b.txs
	//b.Txs = make([][]*types.TransactionData, 0)
	return rlp.DecodeBytes(bs, &b.Txs)
}
