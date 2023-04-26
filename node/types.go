package node

import (
	"github.com/bebop-labs/l2-node/sync"
	"github.com/scroll-tech/go-ethereum/common"
	"github.com/scroll-tech/go-ethereum/rlp"
	"math/big"
)

// Configs need BLS signature
type blsMessage struct {
	ParentHash common.Hash    `json:"parentHash"     gencodec:"required"`
	Miner      common.Address `json:"miner"          gencodec:"required"`
	Number     uint64         `json:"number"         gencodec:"required"`
	GasLimit   uint64         `json:"gasLimit"       gencodec:"required"`
	BaseFee    *big.Int       `json:"baseFeePerGas"  gencodec:"required"`
	Timestamp  uint64         `json:"timestamp"      gencodec:"required"`
}

func (bm *blsMessage) MarshalBinary() ([]byte, error) {
	if bm == nil {
		return nil, nil
	}
	return rlp.EncodeToBytes(bm)
}

func (bm *blsMessage) UnmarshalBinary(b []byte) error {
	return rlp.DecodeBytes(b, bm)
}

// Configs do NOT need BLS signature
type nonBLSMessage struct {
	// execution result
	StateRoot   common.Hash `json:"stateRoot"`
	GasUsed     uint64      `json:"gasUsed"`
	ReceiptRoot common.Hash `json:"receiptsRoot"`
	LogsBloom   []byte      `json:"logsBloom"`

	Extra      []byte           `json:"extraData"`
	L1Messages []sync.L1Message `json:"l1Messages"`
}

func (nbm *nonBLSMessage) MarshalBinary() ([]byte, error) {
	if nbm == nil {
		return nil, nil
	}
	return rlp.EncodeToBytes(nbm)
}

func (nbm *nonBLSMessage) UnmarshalBinary(b []byte) error {
	return rlp.DecodeBytes(b, nbm)
}
