package types

import (
	"math/big"

	"github.com/scroll-tech/go-ethereum/common"
	"github.com/scroll-tech/go-ethereum/rlp"
)

// Configs need BLS signature
type BLSMessage struct {
	ParentHash common.Hash    `json:"parentHash"     gencodec:"required"`
	Miner      common.Address `json:"miner"          gencodec:"required"`
	Number     uint64         `json:"number"         gencodec:"required"`
	GasLimit   uint64         `json:"gasLimit"       gencodec:"required"`
	BaseFee    *big.Int       `json:"baseFeePerGas"  gencodec:"required"`
	Timestamp  uint64         `json:"timestamp"      gencodec:"required"`
}

func (bm *BLSMessage) MarshalBinary() ([]byte, error) {
	if bm == nil {
		return nil, nil
	}
	return rlp.EncodeToBytes(bm)
}

func (bm *BLSMessage) UnmarshalBinary(b []byte) error {
	return rlp.DecodeBytes(b, bm)
}

// Configs do NOT need BLS signature
type NonBLSMessage struct {
	// execution result
	StateRoot   common.Hash `json:"stateRoot"`
	GasUsed     uint64      `json:"gasUsed"`
	ReceiptRoot common.Hash `json:"receiptsRoot"`
	LogsBloom   []byte      `json:"logsBloom"`

	Extra      []byte      `json:"extraData"`
	L1Messages []L1Message `json:"l1Messages"`
}

func (nbm *NonBLSMessage) MarshalBinary() ([]byte, error) {
	if nbm == nil {
		return nil, nil
	}
	return rlp.EncodeToBytes(nbm)
}

func (nbm *NonBLSMessage) UnmarshalBinary(b []byte) error {
	return rlp.DecodeBytes(b, nbm)
}
