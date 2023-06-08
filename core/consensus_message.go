package node

import (
	"github.com/morphism-labs/node/types"
	"github.com/scroll-tech/go-ethereum/eth/catalyst"
)

type BlockConverter interface {
	Separate(l2Block *catalyst.ExecutableL2Data, l1Msg []types.L1Message) (blsMsg []byte, restMsg []byte, err error)
	Recover(blsMsg []byte, restMsg []byte) (l2Block *catalyst.ExecutableL2Data, l1Message []types.L1Message, err error)
}

type Version2Converter struct{}

func (bc *Version2Converter) Separate(l2Block *catalyst.ExecutableL2Data, l1Msg []types.L1Message) (blsMsg []byte, restMsg []byte, err error) {
	bm := &types.BLSMessage{
		ParentHash: l2Block.ParentHash,
		Miner:      l2Block.Miner,
		Number:     l2Block.Number,
		GasLimit:   l2Block.GasLimit,
		BaseFee:    l2Block.BaseFee,
		Timestamp:  l2Block.Timestamp,
		Extra:      l2Block.Extra,
	}
	if blsMsg, err = bm.MarshalBinary(); err != nil {
		return
	}
	nbm := &types.NonBLSMessage{
		StateRoot:   l2Block.StateRoot,
		GasUsed:     l2Block.GasUsed,
		ReceiptRoot: l2Block.ReceiptRoot,
		LogsBloom:   l2Block.LogsBloom,
		L1Messages:  l1Msg,
	}
	if restMsg, err = nbm.MarshalBinary(); err != nil {
		return
	}
	return
}

func (bc *Version2Converter) Recover(blsMsg []byte, restMsg []byte) (*catalyst.ExecutableL2Data, []types.L1Message, error) {
	bm := new(types.BLSMessage)
	if err := bm.UnmarshalBinary(blsMsg); err != nil {
		return nil, nil, err
	}
	nbm := new(types.NonBLSMessage)
	if err := nbm.UnmarshalBinary(restMsg); err != nil {
		return nil, nil, err
	}
	return &catalyst.ExecutableL2Data{
		ParentHash:  bm.ParentHash,
		Miner:       bm.Miner,
		Number:      bm.Number,
		GasLimit:    bm.GasLimit,
		BaseFee:     bm.BaseFee,
		Timestamp:   bm.Timestamp,
		Extra:       bm.Extra,
		StateRoot:   nbm.StateRoot,
		GasUsed:     nbm.GasUsed,
		ReceiptRoot: nbm.ReceiptRoot,
		LogsBloom:   nbm.LogsBloom,
	}, nbm.L1Messages, nil
}
