package node

import (
	"context"
	"errors"
	"fmt"
	"github.com/tendermint/tendermint/blssignatures"
	"github.com/tendermint/tendermint/ethutil/hex"
	"math/big"

	"github.com/bebop-labs/l2-node/sync"
	"github.com/bebop-labs/l2-node/types"
	eth "github.com/scroll-tech/go-ethereum/core/types"
	"github.com/scroll-tech/go-ethereum/eth/catalyst"
	"github.com/scroll-tech/go-ethereum/ethclient"
	"github.com/scroll-tech/go-ethereum/ethclient/authclient"
	"github.com/scroll-tech/go-ethereum/log"
	"github.com/tendermint/tendermint/l2node"
	tdm "github.com/tendermint/tendermint/types"
)

type Executor struct {
	authClient             *authclient.Client
	ethClient              *ethclient.Client
	latestProcessedL1Index uint64
	maxL1MsgNumPerBlock    uint64
	syncer                 *sync.Syncer // needed when it is configured as a sequencer
}

func NewSequencerExecutor(config *Config, syncer *sync.Syncer) (*Executor, error) {
	if syncer == nil {
		return nil, errors.New("syncer has to be provided for sequencer")
	}
	aClient, err := authclient.DialContext(context.Background(), config.L2.EngineAddr, config.L2.JwtSecret)
	if err != nil {
		return nil, err
	}
	eClient, err := ethclient.Dial(config.L2.EthAddr)
	if err != nil {
		return nil, err
	}
	latestProcessedL1Index := uint64(0) // todo it needs to be queried from l2 geth
	return &Executor{
		authClient:             aClient,
		ethClient:              eClient,
		latestProcessedL1Index: latestProcessedL1Index,
		maxL1MsgNumPerBlock:    config.MaxL1MessageNumPerBlock,
		syncer:                 syncer,
	}, err
}

func NewExecutor(config *Config) (*Executor, error) {
	aClient, err := authclient.DialContext(context.Background(), config.L2.EngineAddr, config.L2.JwtSecret)
	if err != nil {
		return nil, err
	}
	eClient, err := ethclient.Dial(config.L2.EthAddr)
	if err != nil {
		return nil, err
	}
	return &Executor{
		authClient: aClient,
		ethClient:  eClient,
	}, err
}

var _ l2node.L2Node = (*Executor)(nil)

func (e *Executor) RequestBlockData(height int64) (txs [][]byte, l2Config, zkConfig []byte, err error) {
	if e.syncer == nil {
		err = fmt.Errorf("RequestBlockData is not alllowed to be called")
		return
	}
	log.Info("RequestBlockData request", "height", height)
	// read the l1 messages
	l1Messages := e.syncer.ReadL1MessagesInRange(e.latestProcessedL1Index+1, e.latestProcessedL1Index+e.maxL1MsgNumPerBlock)
	transactions := make(eth.Transactions, len(l1Messages), len(l1Messages))
	for i, l1Message := range l1Messages {
		transaction := eth.NewTx(&l1Message.L1MessageTx)
		transactions[i] = transaction
	}

	l2Block, err := e.authClient.AssembleL2Block(context.Background(), big.NewInt(height), transactions)
	if err != nil {
		log.Error("failed to assemble block", "height", height, "error", err)
		return
	}
	log.Info("AssembleL2Block returns l2Block", "tx length", len(l2Block.Transactions))
	if len(l2Block.Transactions) == 0 {
		return
	}
	bm := &types.BLSMessage{
		ParentHash: l2Block.ParentHash,
		Miner:      l2Block.Miner,
		Number:     l2Block.Number,
		GasLimit:   l2Block.GasLimit,
		BaseFee:    l2Block.BaseFee,
		Timestamp:  l2Block.Timestamp,
	}
	if zkConfig, err = bm.MarshalBinary(); err != nil {
		return
	}
	nbm := &types.NonBLSMessage{
		StateRoot:   l2Block.StateRoot,
		GasUsed:     l2Block.GasUsed,
		ReceiptRoot: l2Block.ReceiptRoot,
		LogsBloom:   l2Block.LogsBloom,
		Extra:       l2Block.Extra,
		L1Messages:  l1Messages,
	}
	if l2Config, err = nbm.MarshalBinary(); err != nil {
		return
	}
	txs = l2Block.Transactions
	log.Info("RequestBlockData response",
		"txs.length", len(txs),
		"l2Config", hex.EncodeToHex(l2Config),
		"zkConfig", hex.EncodeToHex(zkConfig))
	return
}

func (e *Executor) CheckBlockData(txs [][]byte, l2Config, zkConfig []byte) (valid bool, err error) {
	if e.syncer == nil {
		err = fmt.Errorf("CheckBlockData is not alllowed to be called")
		return
	}
	log.Info("CheckBlockData requests",
		"txs.length", len(txs),
		"l2Config", hex.EncodeToHex(l2Config),
		"zkConfig", hex.EncodeToHex(zkConfig))
	if len(txs) == 0 || l2Config == nil || zkConfig == nil {
		return false, nil
	}
	bm := new(types.BLSMessage)
	if err := bm.UnmarshalBinary(zkConfig); err != nil {
		return false, err
	}
	nbm := new(types.NonBLSMessage)
	if err := nbm.UnmarshalBinary(l2Config); err != nil {
		return false, err
	}

	if err := e.validateL1Messages(txs, nbm); err != nil {
		return false, err
	}
	l2Block := &catalyst.ExecutableL2Data{
		ParentHash:   bm.ParentHash,
		Miner:        bm.Miner,
		Number:       bm.Number,
		GasLimit:     bm.GasLimit,
		BaseFee:      bm.BaseFee,
		Timestamp:    bm.Timestamp,
		Transactions: txs,
		StateRoot:    nbm.StateRoot,
		GasUsed:      nbm.GasUsed,
		ReceiptRoot:  nbm.ReceiptRoot,
		LogsBloom:    nbm.LogsBloom,
		Extra:        nbm.Extra,
	}
	validated, err := e.authClient.ValidateL2Block(context.Background(), l2Block)
	log.Info("CheckBlockData response", "validated", validated, "error", err)
	return validated, err
}

// validators []tdm.Address,
func (e *Executor) DeliverBlock(txs [][]byte, l2Config, zkConfig []byte, validators []tdm.Address, blsSignatures [][]byte) (int64, error) {
	log.Info("DeliverBlock request", "txs length", len(txs),
		"l2Config", hex.EncodeToHex(l2Config),
		"zkConfig", hex.EncodeToHex(zkConfig),
		"validator length", len(validators),
		"blsSignatures length", len(blsSignatures))
	height, err := e.ethClient.BlockNumber(context.Background())
	if err != nil {
		return 0, err
	}
	currentBlockNumber := int64(height)
	if len(txs) == 0 || l2Config == nil || zkConfig == nil {
		return currentBlockNumber, nil
	}
	bm := new(types.BLSMessage)
	if err := bm.UnmarshalBinary(zkConfig); err != nil {
		return currentBlockNumber, err
	}
	nbm := new(types.NonBLSMessage)
	if err := nbm.UnmarshalBinary(l2Config); err != nil {
		return currentBlockNumber, err
	}
	if height+1 != bm.Number {
		return currentBlockNumber, types.ErrWrongBlockNumber
	}
	l2Block := &catalyst.ExecutableL2Data{
		ParentHash:   bm.ParentHash,
		Miner:        bm.Miner,
		Number:       bm.Number,
		GasLimit:     bm.GasLimit,
		BaseFee:      bm.BaseFee,
		Timestamp:    bm.Timestamp,
		Transactions: txs,
		StateRoot:    nbm.StateRoot,
		GasUsed:      nbm.GasUsed,
		ReceiptRoot:  nbm.ReceiptRoot,
		LogsBloom:    nbm.LogsBloom,
		Extra:        nbm.Extra,
	}

	signers := make([][]byte, 0)
	for _, v := range validators {
		signers = append(signers, v.Bytes())
	}

	sigs := make([]blssignatures.Signature, len(blsSignatures), len(blsSignatures))
	for i, bz := range blsSignatures {
		sig, err := blssignatures.SignatureFromBytes(bz)
		if err != nil {
			log.Error("failed to recover bytes to signature", "error", err)
			return currentBlockNumber + 1, err
		}
		sigs[i] = sig
	}
	aggregatedSig := blssignatures.AggregateSignatures(sigs)

	blsData := &eth.BLSData{
		BLSSigners:   signers,
		BLSSignature: blssignatures.SignatureToBytes(aggregatedSig),
	}
	err = e.authClient.NewL2Block(context.Background(), l2Block, blsData)
	if err != nil {
		log.Error("failed to NewL2Block", "error", err)
		return currentBlockNumber + 1, err
	}

	// impossible getting an error here
	_ = e.updateLatestProcessedL1Index(txs)
	return currentBlockNumber + 1, nil
}

func (e *Executor) AuthClient() *authclient.Client {
	return e.authClient
}

func (e *Executor) EthClient() *ethclient.Client {
	return e.ethClient
}
