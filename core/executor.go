package node

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/morphism-labs/morphism-bindings/bindings"
	"github.com/morphism-labs/node/sync"
	"github.com/morphism-labs/node/types"
	"github.com/scroll-tech/go-ethereum/accounts/abi"
	"github.com/scroll-tech/go-ethereum/common"
	"github.com/scroll-tech/go-ethereum/common/hexutil"
	eth "github.com/scroll-tech/go-ethereum/core/types"
	"github.com/scroll-tech/go-ethereum/crypto/bls12381"
	"github.com/scroll-tech/go-ethereum/ethclient"
	"github.com/scroll-tech/go-ethereum/ethclient/authclient"
	"github.com/scroll-tech/go-ethereum/rlp"
	"github.com/tendermint/tendermint/blssignatures"
	"github.com/tendermint/tendermint/crypto"
	"github.com/tendermint/tendermint/l2node"
	tmlog "github.com/tendermint/tendermint/libs/log"
	tmproto "github.com/tendermint/tendermint/proto/tendermint/types"
	"github.com/urfave/cli"
)

type Executor struct {
	l2Client            *types.RetryableClient
	bc                  BlockConverter
	nextL1MsgIndex      uint64
	maxL1MsgNumPerBlock uint64
	l1MsgReader         types.L1MessageReader

	newSyncerFunc func() (*sync.Syncer, error)
	syncer        *sync.Syncer

	govContract       *bindings.Gov
	sequencerContract *bindings.L2Sequencer
	currentVersion    *uint64
	sequencerSet      map[[tmKeySize]byte]sequencerKey // tendermint pk -> bls pk
	validators        [][]byte
	batchParams       tmproto.BatchParams
	tmPubKey          []byte
	isSequencer       bool
	devSequencer      bool

	rollupABI     *abi.ABI
	batchingCache *BatchingCache

	logger  tmlog.Logger
	metrics *Metrics
}

func getNextL1MsgIndex(client *ethclient.Client, logger tmlog.Logger) (uint64, error) {
	currentHeader, err := client.HeaderByNumber(context.Background(), nil)
	if err != nil {
		return 0, err
	}
	if err != nil {
		var count = 0
		for err != nil && strings.Contains(err.Error(), "connection refused") {
			time.Sleep(5 * time.Second)
			count++
			logger.Error("connection refused, try again", "retryCount", count)
			currentHeader, err = client.HeaderByNumber(context.Background(), nil)
		}
		if err != nil {
			logger.Error("failed to get currentHeader", "error", err)
			return 0, fmt.Errorf("failed to get currentHeader, err: %v", err)
		}
	}

	return currentHeader.NextL1MsgIndex, nil
}

func NewExecutor(ctx *cli.Context, home string, config *Config, tmPubKey crypto.PubKey) (*Executor, error) {
	logger := config.Logger
	logger = logger.With("module", "executor")
	aClient, err := authclient.DialContext(context.Background(), config.L2.EngineAddr, config.L2.JwtSecret)
	if err != nil {
		return nil, err
	}
	eClient, err := ethclient.Dial(config.L2.EthAddr)
	if err != nil {
		return nil, err
	}

	index, err := getNextL1MsgIndex(eClient, logger)
	if err != nil {
		return nil, err
	}

	sequencer, err := bindings.NewL2Sequencer(config.L2SequencerAddress, eClient)
	if err != nil {
		return nil, err
	}
	gov, err := bindings.NewGov(config.L2GovAddress, eClient)
	if err != nil {
		return nil, err
	}

	rollupAbi, err := bindings.RollupMetaData.GetAbi()
	if err != nil {
		return nil, err
	}
	executor := &Executor{
		l2Client:            types.NewRetryableClient(aClient, eClient, config.Logger),
		bc:                  &Version1Converter{},
		sequencerContract:   sequencer,
		govContract:         gov,
		tmPubKey:            tmPubKey.Bytes(),
		nextL1MsgIndex:      index,
		maxL1MsgNumPerBlock: config.MaxL1MessageNumPerBlock,
		newSyncerFunc:       func() (*sync.Syncer, error) { return newSyncer(ctx, home, config) },
		devSequencer:        config.DevSequencer,
		rollupABI:           rollupAbi,
		batchingCache:       NewBatchingCache(),
		logger:              logger,
		metrics:             PrometheusMetrics("morphnode"),
	}

	if config.DevSequencer {
		executor.syncer, err = executor.newSyncerFunc()
		if err != nil {
			return nil, err
		}
		executor.syncer.Start()
		executor.l1MsgReader = executor.syncer
		return executor, nil
	}

	if _, err = executor.updateSequencerSet(); err != nil {
		return nil, err
	}

	return executor, nil
}

var _ l2node.L2Node = (*Executor)(nil)

func (e *Executor) RequestBlockData(height int64) (txs [][]byte, configs l2node.Configs, collectedL1Msgs bool, err error) {
	if e.l1MsgReader == nil {
		err = fmt.Errorf("RequestBlockData is not alllowed to be called")
		return
	}
	e.logger.Info("RequestBlockData request", "height", height)
	// read the l1 messages
	fromIndex := e.nextL1MsgIndex
	l1Messages := e.l1MsgReader.ReadL1MessagesInRange(fromIndex, fromIndex+e.maxL1MsgNumPerBlock-1)
	transactions := make(eth.Transactions, len(l1Messages))

	if len(l1Messages) > 0 {
		queueIndex := fromIndex
		for i, l1Message := range l1Messages {
			transaction := eth.NewTx(&l1Message.L1MessageTx)
			transactions[i] = transaction
			if queueIndex != l1Message.QueueIndex {
				e.logger.Error("unexpected l1message queue index", "expected", queueIndex, "actual", l1Message.QueueIndex)
				err = types.ErrInvalidL1MessageOrder
				return
			}
			queueIndex++
		}
		collectedL1Msgs = true
	}

	l2Block, err := e.l2Client.AssembleL2Block(context.Background(), big.NewInt(height), transactions)
	if err != nil {
		e.logger.Error("failed to assemble block", "height", height, "error", err)
		return
	}
	e.logger.Info("AssembleL2Block returns l2Block", "tx length", len(l2Block.Transactions))

	zkConfig, l2Config, err := e.bc.Separate(l2Block, l1Messages)
	if err != nil {
		e.logger.Info("failed to convert l2Block to separated bytes", "error", err)
		return
	}
	configs.ZKConfig = zkConfig
	configs.L2Config = l2Config
	configs.Root = l2Block.WithdrawTrieRoot.Bytes()
	txs = l2Block.Transactions
	e.logger.Info("RequestBlockData response",
		"txs.length", len(txs),
		"collectedL1Msgs", collectedL1Msgs)
	return
}

func (e *Executor) CheckBlockData(txs [][]byte, l2Data l2node.Configs) (valid bool, err error) {
	if e.l1MsgReader == nil {
		return false, fmt.Errorf("RequestBlockData is not alllowed to be called")
	}
	if l2Data.L2Config == nil || l2Data.ZKConfig == nil {
		e.logger.Error("l2Config and zkConfig cannot be nil")
		return false, nil
	}
	l2Block, collectedL1Messages, err := e.bc.Recover(l2Data.ZKConfig, l2Data.L2Config, txs)
	if err != nil {
		e.logger.Error("failed to recover block from separated bytes", "err", err)
		return false, nil
	}
	e.logger.Info("CheckBlockData requests",
		"txs.length", len(txs),
		"l2Config length", len(l2Data.L2Config),
		"zkConfig length", len(l2Data.ZKConfig),
		"eth block number", l2Block.Number)

	if err := e.validateL1Messages(l2Block, collectedL1Messages); err != nil {
		if err != types.ErrQueryL1Message { // only do not return error if it is not ErrQueryL1Message error
			err = nil
		}
		return false, err
	}

	if l2Data.Root != nil {
		l2Block.WithdrawTrieRoot = common.BytesToHash(l2Data.Root)
	}

	validated, err := e.l2Client.ValidateL2Block(context.Background(), l2Block, L1MessagesToTxs(collectedL1Messages))
	e.logger.Info("CheckBlockData response", "validated", validated, "error", err)
	return validated, err
}

func (e *Executor) DeliverBlock(txs [][]byte, l2Data l2node.Configs, consensusData l2node.ConsensusData) (nextBatchParams *tmproto.BatchParams, nextValidatorSet [][]byte, err error) {
	e.logger.Info("DeliverBlock request", "txs length", len(txs),
		"l2Config length", len(l2Data.L2Config),
		"zkConfig length ", len(l2Data.ZKConfig),
		"validator length", len(consensusData.Validators),
		"blsSignatures length", len(consensusData.BlsSignatures))
	height, err := e.l2Client.BlockNumber(context.Background())
	if err != nil {
		return nil, nil, err
	}
	if l2Data.L2Config == nil || l2Data.ZKConfig == nil {
		e.logger.Error("l2Config and zkConfig cannot be nil")
		return nil, nil, errors.New("l2Config and zkConfig cannot be nil")
	}

	l2Block, collectedL1Messages, err := e.bc.Recover(l2Data.ZKConfig, l2Data.L2Config, txs)
	if err != nil {
		e.logger.Error("failed to recover block from separated bytes", "err", err)
		return nil, nil, err
	}

	if l2Block.Number <= height {
		e.logger.Info("ignore it, the block was delivered", "block number", l2Block.Number)
		return nil, nil, nil
	}

	// We only accept the continuous blocks for now.
	// It acts like full sync. Snap sync is not enabled until the Geth enables snapshot with zkTrie
	if l2Block.Number > height+1 {
		return nil, nil, types.ErrWrongBlockNumber
	}

	signers := make([]uint64, 0)
	if !e.devSequencer {
		for _, v := range consensusData.Validators {
			if len(v) > 0 {
				var pk [tmKeySize]byte
				copy(pk[:], v)

				seqKey, ok := e.sequencerSet[pk]
				if !ok {
					return nil, nil, fmt.Errorf("found invalid validator: %s", hexutil.Encode(v))
				}
				signers = append(signers, seqKey.index)
			}
		}
	}

	var blsData eth.BLSData
	if len(consensusData.BlsSignatures) > 0 {
		sigs := make([]blssignatures.Signature, 0)
		for _, bz := range consensusData.BlsSignatures {
			if len(bz) > 0 {
				sig, err := blssignatures.SignatureFromBytes(bz)
				if err != nil {
					e.logger.Error("failed to recover bytes to signature", "error", err)
					return nil, nil, err
				}
				sigs = append(sigs, sig)
			}
		}
		if len(sigs) > 0 {
			var curVersion uint64
			if e.currentVersion != nil {
				curVersion = *e.currentVersion
			}
			aggregatedSig := blssignatures.AggregateSignatures(sigs)
			blsData = eth.BLSData{
				Version:      curVersion,
				BLSSigners:   signers,
				BLSSignature: bls12381.NewG1().EncodePoint(aggregatedSig),
			}
			e.metrics.BatchPointHeight.Set(float64(l2Block.Number))
		}
	}

	err = e.l2Client.NewL2Block(context.Background(), l2Block, &blsData, L1MessagesToTxs(collectedL1Messages))
	if err != nil {
		e.logger.Error("failed to NewL2Block", "error", err)
		return nil, nil, err
	}

	// end block
	e.updateNextL1MessageIndex(l2Block)

	var newValidatorSet = consensusData.ValidatorSet
	var newBatchParams *tmproto.BatchParams
	if !e.devSequencer {
		if newValidatorSet, err = e.updateSequencerSet(); err != nil {
			return nil, nil, err
		}
		if newBatchParams, err = e.batchParamsUpdates(l2Block.Number); err != nil {
			return nil, nil, err
		}
	}

	e.metrics.Height.Set(float64(l2Block.Number))

	return newBatchParams, newValidatorSet,
		nil
}

// EncodeTxs
// decode each transaction bytes into Transaction, and wrap them into an array, then rlpEncode the whole array
func (e *Executor) EncodeTxs(batchTxs [][]byte) ([]byte, error) {
	if len(batchTxs) == 0 {
		return []byte{}, nil
	}
	transactions := make([]*eth.Transaction, len(batchTxs))
	for i, txBz := range batchTxs {
		if len(txBz) == 0 {
			return nil, fmt.Errorf("transaction %d is empty", i)
		}
		var tx eth.Transaction
		if err := tx.UnmarshalBinary(txBz); err != nil {
			return nil, fmt.Errorf("transaction %d is not valid: %v", i, err)
		}
		transactions[i] = &tx
	}
	return rlp.EncodeToBytes(transactions)
}

func (e *Executor) RequestHeight(tmHeight int64) (int64, error) {
	curHeight, err := e.l2Client.BlockNumber(context.Background())
	if err != nil {
		return 0, err
	}
	return int64(curHeight), nil
}

func (e *Executor) L2Client() *types.RetryableClient {
	return e.l2Client
}

func L1MessagesToTxs(l1Messages []types.L1Message) []eth.L1MessageTx {
	txs := make([]eth.L1MessageTx, len(l1Messages))
	for i, l1Message := range l1Messages {
		txs[i] = l1Message.L1MessageTx
	}
	return txs
}
