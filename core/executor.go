package node

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/morphism-labs/node/sync"
	"github.com/morphism-labs/node/types"
	"github.com/morphism-labs/node/types/bindings"
	eth "github.com/scroll-tech/go-ethereum/core/types"
	"github.com/scroll-tech/go-ethereum/crypto/bls12381"
	"github.com/scroll-tech/go-ethereum/ethclient"
	"github.com/scroll-tech/go-ethereum/ethclient/authclient"
	"github.com/scroll-tech/go-ethereum/log"
	"github.com/tendermint/tendermint/blssignatures"
	"github.com/tendermint/tendermint/l2node"
	tdm "github.com/tendermint/tendermint/types"
)

type Executor struct {
	l2Client               *types.RetryableClient
	bc                     BlockConverter
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
	cdmCaller, err := bindings.NewL1CrossDomainMessengerCaller(config.L2CrossDomainMessengerAddress, eClient)
	if err != nil {
		return nil, err
	}

	latestProcessedL1Index, err := cdmCaller.ReceiveNonce(nil)
	if err != nil {
		var count = 0
		for err != nil && strings.Contains(err.Error(), "connection refused") {
			time.Sleep(5 * time.Second)
			count++
			log.Warn("connection refused, try again", "retryCount", count)
			latestProcessedL1Index, err = cdmCaller.ReceiveNonce(nil)
		}
		if err != nil {
			log.Error("failed to get ReceiveNonce", "error", err)
			latestProcessedL1Index = big.NewInt(0)
			err = nil // todo for testing consideration, ignore the err. will remove when we have the pre-deployed contracts integrated
		}
	}
	return &Executor{
		l2Client:               types.NewRetryableClient(aClient, eClient),
		bc:                     &Version1Converter{},
		latestProcessedL1Index: latestProcessedL1Index.Uint64(),
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
		l2Client: types.NewRetryableClient(aClient, eClient),
		bc:       &Version1Converter{},
	}, err
}

var _ l2node.L2Node = (*Executor)(nil)

func (e *Executor) RequestBlockData(height int64) (txs [][]byte, l2Config, zkConfig []byte, err error) {
	if e.syncer == nil {
		err = fmt.Errorf("RequestBlockData is not alllowed to be called")
		return
	}
	log.Info("======>RequestBlockData request", "height", height)
	// read the l1 messages
	l1Messages := e.syncer.ReadL1MessagesInRange(e.latestProcessedL1Index+1, e.latestProcessedL1Index+e.maxL1MsgNumPerBlock)
	transactions := make(eth.Transactions, len(l1Messages), len(l1Messages))
	for i, l1Message := range l1Messages {
		transaction := eth.NewTx(&l1Message.L1MessageTx)
		transactions[i] = transaction
	}

	l2Block, err := e.l2Client.AssembleL2Block(context.Background(), big.NewInt(height), transactions)
	if err != nil {
		log.Error("failed to assemble block", "height", height, "error", err)
		return
	}
	log.Info("AssembleL2Block returns l2Block", "tx length", len(l2Block.Transactions))

	if zkConfig, l2Config, err = e.bc.Separate(l2Block, l1Messages); err != nil {
		log.Info("failed to convert l2Block to separated bytes", "error", err)
		return
	}
	txs = l2Block.Transactions
	log.Info("RequestBlockData response",
		"txs.length", len(txs))
	return
}

func (e *Executor) CheckBlockData(txs [][]byte, l2Config, zkConfig []byte) (valid bool, err error) {
	log.Info("======>CheckBlockData requests",
		"txs.length", len(txs),
		"l2Config length", len(l2Config),
		"zkConfig length", len(zkConfig))
	if l2Config == nil || zkConfig == nil {
		log.Error("l2Config and zkConfig cannot be nil")
		return false, nil
	}
	l2Block, l1Messages, err := e.bc.Recover(zkConfig, l2Config, txs)
	if err != nil {
		log.Error("failed to recover block from separated bytes", "err", err)
		return false, err
	}

	if err := e.validateL1Messages(txs, l1Messages); err != nil {
		return false, err
	}

	validated, err := e.l2Client.ValidateL2Block(context.Background(), l2Block)
	log.Info("CheckBlockData response", "validated", validated, "error", err)
	return validated, err
}

func (e *Executor) DeliverBlock(txs [][]byte, l2Config, zkConfig []byte, validators []tdm.Address, blsSignatures [][]byte) error {
	log.Info("======>DeliverBlock request", "txs length", len(txs),
		"l2Config length", len(l2Config),
		"zkConfig length ", len(zkConfig),
		"validator length", len(validators),
		"blsSignatures length", len(blsSignatures))
	height, err := e.l2Client.BlockNumber(context.Background())
	if err != nil {
		return err
	}
	if l2Config == nil || zkConfig == nil {
		log.Error("l2Config and zkConfig cannot be nil")
		return nil
	}

	l2Block, _, err := e.bc.Recover(zkConfig, l2Config, txs)
	if err != nil {
		log.Error("failed to recover block from separated bytes", "err", err)
		return err
	}

	if l2Block.Number <= height {
		log.Warn("ignore it, the block was delivered", "block number", l2Block.Number)
		return nil
	}

	// We only accept the continuous blocks for now.
	// It acts like full sync. Snap sync is not enabled until the Geth enables snapshot with zkTrie
	if l2Block.Number > height+1 {
		return types.ErrWrongBlockNumber
	}

	signers := make([][]byte, 0)
	for _, v := range validators {
		if len(v) > 0 {
			signers = append(signers, v.Bytes())
		}
	}

	var blsData eth.BLSData
	if len(blsSignatures) > 0 {
		sigs := make([]blssignatures.Signature, 0)
		for _, bz := range blsSignatures {
			if len(bz) > 0 {
				sig, err := blssignatures.SignatureFromBytes(bz)
				if err != nil {
					log.Error("failed to recover bytes to signature", "error", err)
					return err
				}
				sigs = append(sigs, sig)
			}
		}
		if len(sigs) > 0 {
			aggregatedSig := blssignatures.AggregateSignatures(sigs)
			blsData = eth.BLSData{
				BLSSigners:   signers,
				BLSSignature: bls12381.NewG1().EncodePoint(aggregatedSig),
			}
		}
	}

	err = e.l2Client.NewL2Block(context.Background(), l2Block, &blsData)
	if err != nil {
		log.Error("failed to NewL2Block", "error", err)
		return err
	}

	// impossible getting an error here
	_ = e.updateLatestProcessedL1Index(txs)
	return nil
}

func (e *Executor) L2Client() *types.RetryableClient {
	return e.l2Client
}
