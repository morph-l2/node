package derivation

import (
	"context"
	"errors"
	"fmt"
	nodecommon "github.com/morphism-labs/node/common"
	"github.com/scroll-tech/go-ethereum/eth/catalyst"
	"math/big"
	"time"

	"github.com/morphism-labs/node/types"
	"github.com/scroll-tech/go-ethereum"
	"github.com/scroll-tech/go-ethereum/common"
	eth "github.com/scroll-tech/go-ethereum/core/types"
	"github.com/scroll-tech/go-ethereum/crypto"
	"github.com/scroll-tech/go-ethereum/ethclient"
	"github.com/scroll-tech/go-ethereum/ethclient/authclient"
	"github.com/scroll-tech/go-ethereum/log"
	"github.com/scroll-tech/go-ethereum/rpc"
)

var (
	// TODO edit
	ZKEvmEventABI      = "TransactionDeposited(address,address,uint256,bytes)"
	ZKEvmEventABIHash  = crypto.Keccak256Hash([]byte(ZKEvmEventABI))
	ZKEvmEventVersion0 = common.Hash{}
)

type Derivation struct {
	ctx                  context.Context
	l1Client             *ethclient.Client
	ZKEvmContractAddress common.Address
	confirmations        rpc.BlockNumber
	l2Client             *types.RetryableClient

	latestDerivation uint64
	db               Database

	fetchBlockRange     uint64
	pollInterval        time.Duration
	logProgressInterval time.Duration
	stop                chan struct{}
}

func NewDerivationClient(l1Client *ethclient.Client, l2Cfg *types.L2Config, zkEvmContractAddress common.Address, confirmations rpc.BlockNumber) (*Derivation, error) {
	aClient, err := authclient.DialContext(context.Background(), l2Cfg.EngineAddr, l2Cfg.JwtSecret)
	if err != nil {
		return nil, err
	}
	eClient, err := ethclient.Dial(l2Cfg.EthAddr)
	if err != nil {
		return nil, err
	}
	return &Derivation{
		l1Client:             l1Client,
		ZKEvmContractAddress: zkEvmContractAddress,
		confirmations:        confirmations,
		l2Client:             types.NewRetryableClient(aClient, eClient),
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

func (d *Derivation) FetchZkEvmData(ctx context.Context, from, to uint64) ([]*Batch, error) {
	query := ethereum.FilterQuery{
		FromBlock: big.NewInt(0).SetUint64(from),
		ToBlock:   big.NewInt(0).SetUint64(to),
		Addresses: []common.Address{
			d.ZKEvmContractAddress,
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
			if err := d.l2Client.NewL2Block(ctx, blockData.ExecutableL2Data, blockData.blsData); err != nil {
				if errors.Is(err, fmt.Errorf("stateRoot is invalid")) {
					// TODO query challenge state
					// challenge

				} else {
					log.Error("NewL2Block failed", "error", err)
				}
				d.db.WriteLatestDerivationL1Height(batch.L1BlockNumber)

			}
		}
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
