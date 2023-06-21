package node

import (
	"encoding/binary"
	"fmt"
	"github.com/morphism-labs/node/types"
	"github.com/scroll-tech/go-ethereum/common"
	"github.com/scroll-tech/go-ethereum/eth/catalyst"
	"math/big"
)

type BlockConverter interface {
	Separate(l2Block *catalyst.ExecutableL2Data, l1Msg []types.L1Message) (blsMsg []byte, restMsg []byte, err error)
	Recover(blsMsg []byte, restMsg []byte, txs [][]byte) (l2Block *catalyst.ExecutableL2Data, l1Message []types.L1Message, err error)
}

type Version1Converter struct{}

func (bc *Version1Converter) Separate(l2Block *catalyst.ExecutableL2Data, l1Msg []types.L1Message) ([]byte, []byte, error) {
	// Hash(32) || ParentHash(32) || Number(8) || Timestamp(8) || BaseFee(32) || GasLimit(8) || numTxs(2) || numL1Msg(2)
	blsBytes := make([]byte, 124)
	copy(blsBytes[:32], l2Block.Hash.Bytes())
	copy(blsBytes[32:64], l2Block.ParentHash.Bytes())
	copy(blsBytes[64:72], types.Uint64ToBigEndianBytes(l2Block.Number))
	copy(blsBytes[72:80], types.Uint64ToBigEndianBytes(l2Block.Timestamp))
	copy(blsBytes[80:112], l2Block.BaseFee.FillBytes(make([]byte, 32)))
	copy(blsBytes[112:120], types.Uint64ToBigEndianBytes(l2Block.GasLimit))
	copy(blsBytes[120:122], types.Uint16ToBigEndianBytes(uint16(len(l2Block.Transactions))))
	copy(blsBytes[122:124], types.Uint16ToBigEndianBytes(uint16(0))) // later replace it to len(l1Msg)

	rest := types.RestMessage{
		NonBLSMessage: types.NonBLSMessage{
			StateRoot:   l2Block.StateRoot,
			GasUsed:     l2Block.GasUsed,
			ReceiptRoot: l2Block.ReceiptRoot,
			LogsBloom:   l2Block.LogsBloom,
			L1Messages:  l1Msg,
		},
		Miner: l2Block.Miner,
	}
	restBytes, err := rest.MarshalBinary()
	if err != nil {
		return nil, nil, err
	}
	return blsBytes, restBytes, nil
}

func (bc *Version1Converter) Recover(blsMsg []byte, restMsg []byte, txs [][]byte) (*catalyst.ExecutableL2Data, []types.L1Message, error) {
	if len(blsMsg) != 124 {
		return nil, nil, fmt.Errorf("wrong blsMsg size, expected: %d, actual: %d", 124, len(blsMsg))
	}
	rest := new(types.RestMessage)
	if err := rest.UnmarshalBinary(restMsg); err != nil {
		return nil, nil, err
	}
	if binary.BigEndian.Uint16(blsMsg[120:122]) != uint16(len(txs)) {
		return nil, nil, fmt.Errorf("wrong blsMsg, numTxs(%d) is not equal to the length of txs(%d)", binary.BigEndian.Uint16(blsMsg[120:122]), len(txs))
	}
	if binary.BigEndian.Uint16(blsMsg[122:124]) != 0 { // later replace it to uint16(len(nbm.L1Messages))
		return nil, nil, fmt.Errorf("wrong blsMsg, numL1Msg(%d) is not equal to the length of L1Messages(%d)", binary.BigEndian.Uint16(blsMsg[122:124]), 0)
	}

	return &catalyst.ExecutableL2Data{
		Hash:         common.BytesToHash(blsMsg[:32]),
		ParentHash:   common.BytesToHash(blsMsg[32:64]),
		Miner:        rest.Miner,
		Number:       binary.BigEndian.Uint64(blsMsg[64:72]),
		Timestamp:    binary.BigEndian.Uint64(blsMsg[72:80]),
		BaseFee:      new(big.Int).SetBytes(blsMsg[80:112]),
		GasLimit:     binary.BigEndian.Uint64(blsMsg[112:120]),
		StateRoot:    rest.StateRoot,
		GasUsed:      rest.GasUsed,
		ReceiptRoot:  rest.ReceiptRoot,
		LogsBloom:    rest.LogsBloom,
		Transactions: txs,
	}, rest.L1Messages, nil
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

func (bc *Version2Converter) Recover(blsMsg []byte, restMsg []byte, txs [][]byte) (*catalyst.ExecutableL2Data, []types.L1Message, error) {
	bm := new(types.BLSMessage)
	if err := bm.UnmarshalBinary(blsMsg); err != nil {
		return nil, nil, err
	}
	nbm := new(types.NonBLSMessage)
	if err := nbm.UnmarshalBinary(restMsg); err != nil {
		return nil, nil, err
	}
	return &catalyst.ExecutableL2Data{
		ParentHash:   bm.ParentHash,
		Miner:        bm.Miner,
		Number:       bm.Number,
		GasLimit:     bm.GasLimit,
		BaseFee:      bm.BaseFee,
		Timestamp:    bm.Timestamp,
		StateRoot:    nbm.StateRoot,
		GasUsed:      nbm.GasUsed,
		ReceiptRoot:  nbm.ReceiptRoot,
		LogsBloom:    nbm.LogsBloom,
		Transactions: txs,
	}, nbm.L1Messages, nil
}
