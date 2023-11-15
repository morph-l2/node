package derivation

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/big"
	"os"
	"time"

	"github.com/morphism-labs/morphism-bindings/bindings"
	nodecommon "github.com/morphism-labs/node/common"
	"github.com/morphism-labs/node/types"
	"github.com/morphism-labs/node/validator"
	"github.com/scroll-tech/go-ethereum"
	"github.com/scroll-tech/go-ethereum/accounts/abi/bind"
	"github.com/scroll-tech/go-ethereum/common"
	eth "github.com/scroll-tech/go-ethereum/core/types"
	"github.com/scroll-tech/go-ethereum/crypto"
	"github.com/scroll-tech/go-ethereum/eth/catalyst"
	"github.com/scroll-tech/go-ethereum/ethclient"
	"github.com/scroll-tech/go-ethereum/ethclient/authclient"
	"github.com/scroll-tech/go-ethereum/rpc"
	tmlog "github.com/tendermint/tendermint/libs/log"
)

var (
	RollupEventTopic     = "SubmitBatches(uint64,uint64)"
	RollupEventTopicHash = crypto.Keccak256Hash([]byte(RollupEventTopic))
)

// RollupData is all rollup data of one l1 block,maybe contain many rollup batch
type RollupData struct {
	Batches          [][]*BlockData
	L1BlockNumber    uint64
	TxHash           common.Hash
	Nonce            uint64
	LastBlockNumber  uint64
	FirstBlockNumber uint64
}

type BlockData struct {
	SafeL2Data *catalyst.SafeL2Data
	//blsData    *eth.BLSData
	Root common.Hash
}

func newRollupData(blockNumber uint64, txHash common.Hash, nonce uint64) *RollupData {
	return &RollupData{
		L1BlockNumber: blockNumber,
		Batches:       [][]*BlockData{},
		TxHash:        txHash,
		Nonce:         nonce,
	}
}

type Derivation struct {
	ctx                   context.Context
	l1Client              DeployContractBackend
	RollupContractAddress common.Address
	confirmations         rpc.BlockNumber
	l2Client              *types.RetryableClient
	validator             *validator.Validator
	logger                tmlog.Logger
	rollup                *bindings.Rollup
	metrics               *Metrics

	latestDerivation uint64
	db               Database

	cancel context.CancelFunc

	fetchBlockRange     uint64
	preBatchLastBlock   uint64
	pollInterval        time.Duration
	logProgressInterval time.Duration
	stop                chan struct{}
}

type DeployContractBackend interface {
	bind.DeployBackend
	bind.ContractBackend
	ethereum.ChainReader
	ethereum.TransactionReader
}

func NewDerivationClient(ctx context.Context, cfg *Config, db Database, validator *validator.Validator, rollup *bindings.Rollup, logger tmlog.Logger) (*Derivation, error) {
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
	logger = logger.With("module", "derivation")
	metrics := PrometheusMetrics("morphnode")
	if cfg.MetricsServerEnable {
		go func() {
			_, err := metrics.Serve(cfg.MetricsHostname, cfg.MetricsPort)
			if err != nil {
				panic(fmt.Errorf("metrics server start error:%v", err))
			}
		}()
		logger.Info("metrics server enabled", "host", cfg.MetricsHostname, "port", cfg.MetricsPort)
	}
	return &Derivation{
		ctx:                   ctx,
		db:                    db,
		l1Client:              l1Client,
		validator:             validator,
		rollup:                rollup,
		logger:                logger,
		RollupContractAddress: cfg.RollupContractAddress,
		confirmations:         cfg.L1.Confirmations,
		l2Client:              types.NewRetryableClient(aClient, eClient, tmlog.NewTMLogger(tmlog.NewSyncWriter(os.Stdout))),
		cancel:                cancel,
		stop:                  make(chan struct{}),
		fetchBlockRange:       cfg.FetchBlockRange,
		pollInterval:          cfg.PollInterval,
		logProgressInterval:   cfg.LogProgressInterval,
		metrics:               metrics,
	}, nil
}

func (d *Derivation) Start() {
	// block node startup during initial sync and print some helpful logs
	go func() {
		t := time.NewTicker(d.pollInterval)
		defer t.Stop()

		for {
			// don't wait for ticker during startup
			d.derivationBlock(d.ctx)

			select {
			case <-d.ctx.Done():
				d.logger.Error("derivation node Unexpected exit")
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

	d.logger.Info("Stopping Derivation service")

	if d.cancel != nil {
		d.cancel()
	}
	<-d.stop
	d.logger.Info("Derivation service is stopped")
}

func (d *Derivation) derivationBlock(ctx context.Context) {
	latestDerivation := d.db.ReadLatestDerivationL1Height()
	latest, err := nodecommon.GetLatestConfirmedBlockNumber(ctx, d.l1Client.(*ethclient.Client), d.confirmations)
	if err != nil {
		d.logger.Error("GetLatestConfirmedBlockNumber failed", "error", err)
		return
	}
	start := *latestDerivation + 1
	end := latest
	if latest < start {
		d.logger.Info("latest less than or equal to start", "latest", latest, "start", start)
		return
	} else if latest-start >= d.fetchBlockRange {
		end = start + d.fetchBlockRange
	} else {
		end = latest
	}
	d.logger.Info("derivation start pull rollupData form l1", "startBlock", start, "end", end)
	logs, err := d.fetchRollupLog(ctx, start, end)
	if err != nil {
		d.logger.Error("eth_getLogs failed", "err", err)
		return
	}
	//latestL2BlockNumber, err := d.rollup.LastL2BlockNumber(nil)
	//if err != nil {
	//	d.logger.Error("query rollup LastL2BlockNumber failed", "err", err)
	//	return
	//}
	latestL2BlockNumber := uint64(0)
	d.logger.Info(fmt.Sprintf("rollup latest l2Blocknumber:%v", latestL2BlockNumber))
	d.logger.Info("fetched rollup tx", "txNum", len(logs))
	d.metrics.SetRollupL2Height(latestL2BlockNumber)

	for _, lg := range logs {
		batchBls := d.db.ReadLatestBatchBls()
		rollupData, err := d.fetchRollupDataByTxHash(lg.TxHash, lg.BlockNumber, &batchBls)
		if err != nil {
			d.logger.Error("fetch rollup data failed", "txHash", lg.TxHash, "blockNumber", lg.BlockNumber, "error", err)
			return
		}
		d.logger.Info("fetch rollup transaction success", "txNonce", rollupData.Nonce, "txHash", rollupData.TxHash,
			"l1BlockNumber", rollupData.L1BlockNumber, "firstL2BlockNumber", rollupData.FirstBlockNumber, "lastL2BlockNumber", rollupData.LastBlockNumber)

		//derivation
		for _, batchData := range rollupData.Batches {
			lastHeader, err := d.derive(batchData)
			if err != nil {
				d.logger.Error("derive blocks interrupt", "error", err)
				return
			}
			// only last block of batch
			d.logger.Info("batch derivation complete", "currentBatchEndBlock", lastHeader.Number.Uint64())
			d.metrics.SetL2DeriveHeight(lastHeader.Number.Uint64())
			if !bytes.Equal(lastHeader.Root.Bytes(), batchData[len(batchData)-1].Root.Bytes()) && d.validator != nil && d.validator.ChallengeEnable() {
				d.logger.Info("root hash is not equal", "originStateRootHash", batchData[len(batchData)-1].Root.Hex(), "deriveStateRootHash", lastHeader.Root.Hex())
				batchIndex, err := d.findBatchIndex(rollupData.TxHash, batchData[len(batchData)-1].SafeL2Data.Number)
				if err != nil {
					d.logger.Error("find batch index failed", "error", err)
					return
				}
				d.logger.Info("validator start challenge", "batchIndex", batchIndex)
				if err := d.validator.ChallengeState(batchIndex); err != nil {
					d.logger.Error("challenge state failed", "error", err)
				}
				return
			}
		}
		d.db.WriteLatestDerivationL1Height(lg.BlockNumber)
		d.metrics.SetL1SyncHeight(lg.BlockNumber)
		d.logger.Info("WriteLatestDerivationL1Height success", "L1BlockNumber", lg.BlockNumber)
		d.db.WriteLatestBatchBls(batchBls)
		d.logger.Info("WriteLatestBatchBls success", "lastBlockNumber", batchBls.BlockNumber)
	}

	d.db.WriteLatestDerivationL1Height(end)
	d.metrics.SetL1SyncHeight(end)
}

func (d *Derivation) fetchRollupLog(ctx context.Context, from, to uint64) ([]eth.Log, error) {
	query := ethereum.FilterQuery{
		FromBlock: big.NewInt(0).SetUint64(from),
		ToBlock:   big.NewInt(0).SetUint64(to),
		Addresses: []common.Address{
			d.RollupContractAddress,
		},
		Topics: [][]common.Hash{
			{RollupEventTopicHash},
		},
	}
	return d.l1Client.FilterLogs(ctx, query)
}

func (d *Derivation) fetchRollupDataByTxHash(txHash common.Hash, blockNumber uint64, batchBls *types.BatchBls) (*RollupData, error) {
	tx, pending, err := d.l1Client.TransactionByHash(context.Background(), txHash)
	if err != nil {
		return nil, err
	}
	if pending {
		return nil, errors.New("pending transaction")
	}
	abi, err := bindings.RollupMetaData.GetAbi()
	if err != nil {
		return nil, err
	}
	args, err := abi.Methods["submitBatches"].Inputs.Unpack(tx.Data()[4:])
	if err != nil {
		return nil, fmt.Errorf("submitBatches Unpack error:%v", err)
	}
	// parse input to rollup batch data
	rollupData := newRollupData(blockNumber, txHash, tx.Nonce())
	if err := d.parseArgs(args, rollupData, batchBls); err != nil {
		d.logger.Error("rollupData detail", "txNonce", rollupData.Nonce, "txHash", rollupData.TxHash,
			"l1BlockNumber", rollupData.L1BlockNumber, "firstL2BlockNumber", rollupData.FirstBlockNumber, "lastL2BlockNumber", rollupData.LastBlockNumber)
		return rollupData, fmt.Errorf("parseArgs error:%v\n", err)
	}
	return rollupData, nil
}

func (d *Derivation) parseArgs(args []interface{}, rollupData *RollupData, batchBls *types.BatchBls) error {
	// Currently cannot be asserted using custom structures
	rollupBatchDatas := args[0].([]struct {
		BlockNumber    uint64    "json:\"blockNumber\""
		Transactions   []uint8   "json:\"transactions\""
		BlockWitness   []uint8   "json:\"blockWitness\""
		PreStateRoot   [32]uint8 "json:\"preStateRoot\""
		PostStateRoot  [32]uint8 "json:\"postStateRoot\""
		WithdrawalRoot [32]uint8 "json:\"withdrawalRoot\""
		Signature      struct {
			Signers   []uint64 "json:\"signers\""
			Signature []uint8  "json:\"signature\""
		} "json:\"signature\""
	})
	for batchDataIndex, rollupBatchData := range rollupBatchDatas {
		bd := new(BatchData)
		if err := bd.DecodeBlockContext(rollupBatchData.BlockNumber, rollupBatchData.BlockWitness); err != nil {
			return fmt.Errorf("BatchData DecodeBlockContext error:%v", err)
		}
		if err := bd.DecodeTransactions(rollupBatchData.Transactions); err != nil {
			return fmt.Errorf("BatchData DecodeTransactions error:%v", err)
		}
		if batchBls != nil && bd.BlockContexts[len(bd.BlockContexts)-1].Number.Uint64() <= batchBls.BlockNumber {
			d.logger.Info("The current Batch already exists", "batchEndBlock", bd.BlockContexts[len(bd.BlockContexts)-1].Number.Uint64(), "localBatchBlsNumber", batchBls.BlockNumber)
			continue
		}
		var last uint64
		var batchBlocks []*BlockData
		for index, block := range bd.BlockContexts {
			if batchDataIndex == 0 && index == 0 {
				// first block in once submit
				rollupData.FirstBlockNumber = block.Number.Uint64()
				rollupData.LastBlockNumber = rollupBatchDatas[len(rollupBatchDatas)-1].BlockNumber
			}
			var blockData BlockData
			var safeL2Data catalyst.SafeL2Data
			safeL2Data.Number = block.Number.Uint64()
			safeL2Data.GasLimit = block.GasLimit
			safeL2Data.BaseFee = block.BaseFee
			// TODO Follow-up to be optimized
			if block.BaseFee != nil && block.BaseFee.Cmp(big.NewInt(0)) == 0 {
				safeL2Data.BaseFee = nil
			}
			safeL2Data.Timestamp = block.Timestamp
			if block.NumTxs > 0 {
				safeL2Data.Transactions = encodeTransactions(bd.Txs[last : last+uint64(block.NumTxs)])
				last += uint64(block.NumTxs)
			} else {
				safeL2Data.Transactions = [][]byte{}
			}
			blockData.SafeL2Data = &safeL2Data
			//if index == 0 && batchBls != nil {
			//	if batchBls.BlockNumber != blockData.SafeL2Data.Number-1 {
			//		return fmt.Errorf("miss last batch bls data,expect:%v but got %v", blockData.SafeL2Data.Number-1, batchBls.BlockNumber)
			//	}
			//	// Puts the Bls signature of the previous Batch in the first
			//	// block of the current batch
			//	blockData.blsData = batchBls.BlsData
			//}
			//if index == len(bd.BlockContexts)-1 {
			//	// only last block of batch
			//	if rollupBatchData.Signature.Signature == nil || rollupBatchData.Signature.Signers == nil {
			//		d.logger.Error("invalid batch", "l1BlockNumber", rollupData.L1BlockNumber)
			//	}
			//	var blsData eth.BLSData
			//	blsData.BLSSignature = rollupBatchData.Signature.Signature
			//	blsData.BLSSigners = rollupBatchData.Signature.Signers
			//	// The Bls signature of the current Batch is temporarily
			//	// stored and later placed in the first Block of the next Batch
			//	if batchBls != nil {
			//		batchBls.BlsData = &blsData
			//		batchBls.BlockNumber = block.Number.Uint64()
			//	}
			//	// StateRoot of the last Block of the Batch, used to verify the
			//	// validity of the Layer1 BlockData
			//	blockData.Root = rollupBatchData.PostStateRoot
			//}
			batchBlocks = append(batchBlocks, &blockData)
		}
		rollupData.Batches = append(rollupData.Batches, batchBlocks)
	}
	return nil
}

func (d *Derivation) derive(batchBlocks []*BlockData) (*eth.Header, error) {
	var lastHeader *eth.Header
	for _, blockData := range batchBlocks {
		latestBlockNumber, err := d.l2Client.BlockNumber(context.Background())
		if err != nil {
			return nil, fmt.Errorf("get derivation geth block number error:%v", err)
		}
		if blockData.SafeL2Data.Number <= latestBlockNumber {
			d.logger.Info("SafeL2Data block number less than latestBlockNumber", "safeL2DataNumber", blockData.SafeL2Data.Number, "latestBlockNumber", latestBlockNumber)
			continue
		}
		lastHeader, err = d.l2Client.NewSafeL2Block(context.Background(), blockData.SafeL2Data)
		if err != nil {
			d.logger.Error("NewL2Block failed", "latestBlockNumber", latestBlockNumber, "error", err)
			return nil, err
		}
	}
	return lastHeader, nil
}

func (d *Derivation) findBatchIndex(txHash common.Hash, blockNumber uint64) (uint64, error) {
	receipt, err := d.l1Client.TransactionReceipt(context.Background(), txHash)
	if err != nil {
		return 0, err
	}
	if receipt.Status == eth.ReceiptStatusFailed {
		return 0, err
	}
	//for _, lg := range receipt.Logs {
	//	batchStorage, err := d.rollup.ParseBatchStorage(*lg)
	//	if err != nil {
	//		continue
	//	}
	//	if batchStorage.BlockNumber == blockNumber {
	//		return batchStorage.BatchIndex, nil
	//	}
	//}
	return 0, fmt.Errorf("event not found")
}

func encodeTransactions(txs []*eth.Transaction) [][]byte {
	var enc = make([][]byte, len(txs))
	for i, tx := range txs {
		enc[i], _ = tx.MarshalBinary()
	}
	return enc
}
