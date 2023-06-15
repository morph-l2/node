package derivation

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	nodecommon "github.com/morphism-labs/node/common"
	"github.com/morphism-labs/node/types"
	"github.com/morphism-labs/node/validator"
	"github.com/scroll-tech/go-ethereum"
	"github.com/scroll-tech/go-ethereum/common"
	eth "github.com/scroll-tech/go-ethereum/core/types"
	"github.com/scroll-tech/go-ethereum/crypto"
	"github.com/scroll-tech/go-ethereum/eth/catalyst"
	"github.com/scroll-tech/go-ethereum/ethclient"
	"github.com/scroll-tech/go-ethereum/ethclient/authclient"
	"github.com/scroll-tech/go-ethereum/log"
	"github.com/scroll-tech/go-ethereum/rpc"
)

var (
	// TODO edit
	ZKEvmEventABI      = "SubmitBatches(address,address,uint256,bytes)"
	ZKEvmEventABIHash  = crypto.Keccak256Hash([]byte(ZKEvmEventABI))
	ZKEvmEventVersion0 = common.Hash{}
)

type Derivation struct {
	ctx                  context.Context
	l1Client             *ethclient.Client
	ZKEvmContractAddress *common.Address
	confirmations        rpc.BlockNumber
	l2Client             *types.RetryableClient
	validator            *validator.Validator

	latestDerivation uint64
	db               Database

	cancel context.CancelFunc

	fetchBlockRange     uint64
	pollInterval        time.Duration
	logProgressInterval time.Duration
	stop                chan struct{}
}

func NewDerivationClient(ctx context.Context, cfg *Config, db Database, validator *validator.Validator) (*Derivation, error) {
	l1Client, err := ethclient.Dial(cfg.L1.Addr)
	if err != nil {
		return nil, err
	}
	aClient, err := authclient.DialContext(context.Background(), cfg.L2.EngineAddr, cfg.L2.JwtSecret)
	if err != nil {
		return nil, err
	}
	eClient, err := ethclient.Dial(cfg.L2.EthAddr)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	latestDerivation := db.ReadLatestDerivationL1Height()
	if latestDerivation == nil {
		db.WriteLatestDerivationL1Height(cfg.StartHeight)
	}
	return &Derivation{
		ctx:                  ctx,
		db:                   db,
		l1Client:             l1Client,
		validator:            validator,
		ZKEvmContractAddress: cfg.ZKEvmContractAddress,
		confirmations:        cfg.L1.Confirmations,
		l2Client:             types.NewRetryableClient(aClient, eClient),
		cancel:               cancel,
		stop:                 make(chan struct{}),
		fetchBlockRange:      cfg.FetchBlockRange,
		pollInterval:         cfg.PollInterval,
		logProgressInterval:  cfg.LogProgressInterval,
	}, nil
}

func (d *Derivation) Start() {
	// block node startup during initial sync and print some helpful logs
	go func() {
		t := time.NewTicker(d.pollInterval)
		defer t.Stop()

		for {
			// don't wait for ticker during startup
			d.DerivationBlock(d.ctx)

			select {
			case <-d.ctx.Done():
				close(d.stop)
				return
			case <-t.C:
				continue
			}
		}
	}()
}

func (d *Derivation) Stop() {
	if d == nil {
		return
	}

	log.Info("Stopping sync service")

	if d.cancel != nil {
		d.cancel()
	}
	<-d.stop
	log.Info("Sync service is stopped")
}

func (d *Derivation) FetchZkEvmData(ctx context.Context, from, to uint64) ([]*Batch, error) {
	query := ethereum.FilterQuery{
		FromBlock: big.NewInt(0).SetUint64(from),
		ToBlock:   big.NewInt(0).SetUint64(to),
		Addresses: []common.Address{
			*d.ZKEvmContractAddress,
		},
		Topics: [][]common.Hash{
			{ZKEvmEventABIHash},
		},
	}
	logs, err := d.l1Client.FilterLogs(ctx, query)
	if err != nil {
		log.Trace("eth_getLogs failed", "query", query, "err", err)
		return nil, fmt.Errorf("eth_getLogs failed: %w", err)
	}

	if len(logs) == 0 {
		return nil, nil
	}

	var batches []*Batch
	for _, lg := range logs {
		batch, err := UnmarshalZKEvmLogEvent(&lg)
		if err != nil {
			return nil, err
		}
		batch.L1BlockNumber = lg.BlockNumber
		batches = append(batches, batch)
	}
	return batches, nil
}

func (d *Derivation) DerivationBlock(ctx context.Context) {
	latestDerivation := d.db.ReadLatestDerivationL1Height()
	latest, err := nodecommon.GetLatestConfirmedBlockNumber(ctx, d.l1Client, d.confirmations)
	if err != nil {
		log.Error("GetLatestConfirmedBlockNumber failed", "error", err)
		return
	}
	var end uint64
	if latest <= *latestDerivation {
		return
	} else if latest-*latestDerivation >= 100 {
		end = latest + 99
	} else {
		end = latest
	}
	batches, err := d.FetchZkEvmData(ctx, *latestDerivation, end)
	if err != nil {
		log.Error("FetchZkEvmData failed", "error", err)
		return
	}
	for _, batch := range batches {
		for _, blockData := range batch.BlockDatas {
			latestBlockNumber, err := d.l2Client.BlockNumber(ctx)
			if err != nil {
				return
			}
			if blockData.ExecutableL2Data.Number <= latestBlockNumber {
				continue
			}
			if err := d.l2Client.NewL2Block(ctx, blockData.ExecutableL2Data, blockData.blsData); err != nil {
				if errors.Is(err, fmt.Errorf("stateRoot is invalid")) && d.validator != nil {
					{
						if err := d.validator.ChallengeState(blockData.BatchIndex); err != nil {
							log.Error("challenge state failed", "error", err)
						}
					}
				} else {
					log.Error("NewL2Block failed", "error", err)
				}
				return
			}
		}
		// Update after a Batch is complete
		d.db.WriteLatestDerivationL1Height(batch.L1BlockNumber)
	}
}

type Batch struct {
	BlockDatas    []*BlockData
	L1BlockNumber uint64
}

type BlockData struct {
	BatchIndex       uint64
	ExecutableL2Data *catalyst.ExecutableL2Data
	blsData          *eth.BLSData
}

// TODO
func UnmarshalZKEvmLogEvent(ev *eth.Log) (*Batch, error) {
	return nil, nil
}
