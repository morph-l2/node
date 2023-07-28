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
	"github.com/scroll-tech/go-ethereum/log"
	"github.com/scroll-tech/go-ethereum/rpc"
	tmlog "github.com/tendermint/tendermint/libs/log"
)

var (
	ZKEvmEventTopic     = "SubmitBatches(uint64,uint64)"
	ZKEvmEventTopicHash = crypto.Keccak256Hash([]byte(ZKEvmEventTopic))
)

type Derivation struct {
	ctx                  context.Context
	l1Client             DeployContractBackend
	ZKEvmContractAddress *common.Address
	confirmations        rpc.BlockNumber
	l2Client             *types.RetryableClient
	validator            *validator.Validator
	logger               tmlog.Logger
	zkEvm                *bindings.ZKEVM

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

func NewDerivationClient(ctx context.Context, cfg *Config, db Database, validator *validator.Validator, zkEvm *bindings.ZKEVM, logger tmlog.Logger) (*Derivation, error) {
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
	logger = logger.With("module", "derivation")
	return &Derivation{
		ctx:                  ctx,
		db:                   db,
		l1Client:             l1Client,
		validator:            validator,
		zkEvm:                zkEvm,
		logger:               logger,
		ZKEvmContractAddress: cfg.ZKEvmContractAddress,
		confirmations:        cfg.L1.Confirmations,
		l2Client:             types.NewRetryableClient(aClient, eClient, tmlog.NewTMLogger(tmlog.NewSyncWriter(os.Stdout))),
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

	log.Info("Stopping Derivation service")

	if d.cancel != nil {
		d.cancel()
	}
	<-d.stop
	log.Info("Derivation service is stopped")
}

func (d *Derivation) derivationBlock(ctx context.Context) {
	latestDerivation := d.db.ReadLatestDerivationL1Height()
	latest, err := nodecommon.GetLatestConfirmedBlockNumber(ctx, d.l1Client.(*ethclient.Client), d.confirmations)
	if err != nil {
		log.Error("GetLatestConfirmedBlockNumber failed", "error", err)
		return
	}
	var end uint64
	if latest <= *latestDerivation {
		log.Info("latest <= *latestDerivation", "latest", latest, "latestDerivation", *latestDerivation)
		return
	} else if latest-*latestDerivation >= d.fetchBlockRange {
		end = *latestDerivation + d.fetchBlockRange - 1
	} else {
		end = latest
	}
	d.logger.Info("derivation start pull block form l1", "startBlock", *latestDerivation, "end", end)
	fetchBatches, err := d.fetchZkEvmData(ctx, *latestDerivation, end)
	if err != nil {
		log.Error("FetchZkEvmData failed", "error", err)
		return
	}

	//derivation
	for _, fetchBatch := range fetchBatches {
		if err := d.derive(fetchBatch); err != nil {
			d.logger.Error("derive blocks interrupt", "error", err)
			return
		}
		d.db.WriteLatestDerivationL1Height(fetchBatch.L1BlockNumber)
	}
	d.db.WriteLatestDerivationL1Height(end)
}

func (d *Derivation) fetchZkEvmData(ctx context.Context, from, to uint64) ([]*FetchBatch, error) {
	query := ethereum.FilterQuery{
		FromBlock: big.NewInt(0).SetUint64(from),
		ToBlock:   big.NewInt(0).SetUint64(to),
		Addresses: []common.Address{
			*d.ZKEvmContractAddress,
		},
		Topics: [][]common.Hash{
			{ZKEvmEventTopicHash},
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
	var fetchDatas []*FetchBatch
	for _, lg := range logs {
		fetchData, err := d.fetchRollupData(lg.TxHash, lg.BlockNumber)
		if err != nil {
			return nil, err
		}
		fetchDatas = append(fetchDatas, fetchData)
	}
	return fetchDatas, nil
}

// FetchBatch is all rollup data of one l1 block,maybe contain many rollup batch
type FetchBatch struct {
	BlockDatas    []*BlockData
	L1BlockNumber uint64
	TxHash        common.Hash
}

type BlockData struct {
	SafeL2Data *catalyst.SafeL2Data
	blsData    *eth.BLSData
	Root       common.Hash
}

func newFetchBatch(blockNumber uint64, txHash common.Hash) *FetchBatch {
	return &FetchBatch{
		L1BlockNumber: blockNumber,
		BlockDatas:    []*BlockData{},
		TxHash:        txHash,
	}
}

func (d *Derivation) fetchRollupData(txHash common.Hash, blockNumber uint64) (*FetchBatch, error) {
	tx, pending, err := d.l1Client.TransactionByHash(context.Background(), txHash)
	if err != nil {
		return nil, err
	}
	if pending {
		return nil, errors.New("pending transaction")
	}
	abi, err := bindings.ZKEVMMetaData.GetAbi()
	if err != nil {
		return nil, err
	}
	args, err := abi.Methods["submitBatches"].Inputs.Unpack(tx.Data()[4:])
	if err != nil {
		return nil, fmt.Errorf("submitBatches Unpack error:%v", err)
	}
	// parse calldata to zkevm batch data
	fetchBatch := newFetchBatch(blockNumber, txHash)
	if err := d.argsToBlockDatas(args, fetchBatch); err != nil {
		return nil, err
	}
	return fetchBatch, nil
}

func (d *Derivation) argsToBlockDatas(args []interface{}, fetchBatch *FetchBatch) error {
	zkEVMBatchDatas := args[0].([]struct {
		BlockNumber   uint64    "json:\"blockNumber\""
		Transactions  []uint8   "json:\"transactions\""
		BlockWitness  []uint8   "json:\"blockWitness\""
		PreStateRoot  [32]uint8 "json:\"preStateRoot\""
		PostStateRoot [32]uint8 "json:\"postStateRoot\""
		WithdrawRoot  [32]uint8 "json:\"withdrawRoot\""
		Signature     struct {
			Signers   [][]uint8 "json:\"signers\""
			Signature []uint8   "json:\"signature\""
		} "json:\"signature\""
	})
	for _, zkEVMBatchData := range zkEVMBatchDatas {
		bd := new(BatchData)
		if err := bd.DecodeBlockContext(zkEVMBatchData.BlockNumber, zkEVMBatchData.BlockWitness); err != nil {
			return fmt.Errorf("BatchData DecodeBlockContext error:%v", err)
		}
		if err := bd.DecodeTransactions(zkEVMBatchData.Transactions); err != nil {
			return fmt.Errorf("BatchData DecodeTransactions error:%v", err)
		}
		var last uint64
		for index, block := range bd.BlockContexts {
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
				safeL2Data.Transactions = encodeTransactions(bd.Txs[last : block.NumTxs-1])
			} else {
				safeL2Data.Transactions = [][]byte{}
			}
			last = block.NumTxs - 1
			blockData.SafeL2Data = &safeL2Data
			if index == len(bd.BlockContexts)-1 {
				// only last block of batch
				if blockData.blsData == nil {
					d.logger.Error("invalid batch", "l1BlockNumber", fetchBatch.L1BlockNumber)
				}
				var blsData eth.BLSData
				blsData.BLSSignature = zkEVMBatchData.Signature.Signature
				blsData.BLSSigners = zkEVMBatchData.Signature.Signers
				blockData.blsData = &blsData
				blockData.Root = zkEVMBatchData.PostStateRoot
			}
			fetchBatch.BlockDatas = append(fetchBatch.BlockDatas, &blockData)
		}
	}
	return nil
}

func (d *Derivation) derive(fetchBatch *FetchBatch) error {
	for _, blockData := range fetchBatch.BlockDatas {
		latestBlockNumber, err := d.l2Client.BlockNumber(context.Background())
		if err != nil {
			return fmt.Errorf("get derivation geth block number error:%v", err)
		}
		if blockData.SafeL2Data.Number <= latestBlockNumber {
			d.logger.Info("SafeL2Data block number less than latestBlockNumber", "safeL2DataNumber", blockData.SafeL2Data.Number, "latestBlockNumber", latestBlockNumber)
			continue
		}
		header, err := d.l2Client.NewSafeL2Block(context.Background(), blockData.SafeL2Data, blockData.blsData)
		if err != nil {
			log.Error("NewL2Block failed", "error", err)
			return err
		}
		if blockData.blsData != nil {
			// only last block of batch
			d.logger.Info("batch derivation complete", "currentBatchEndBlock", header.Number.Uint64())
			if !bytes.Equal(header.Hash().Bytes(), blockData.Root.Bytes()) && d.validator != nil && d.validator.ChallengeEnable() {
				batchIndex, err := d.findBatchIndex(fetchBatch.TxHash, blockData.SafeL2Data.Number)
				if err != nil {
					return fmt.Errorf("find batch index error:%v", err)
				}
				log.Info("block hash is not equal", "l1BlockNumber", blockData.Root.Hex(), "l2", header.Hash().Hex())
				if err := d.validator.ChallengeState(batchIndex); err != nil {
					log.Error("challenge state failed", "error", err)
				}
				return err
			}
		}
	}
	return nil
}

func (d *Derivation) findBatchIndex(txHash common.Hash, blockNumber uint64) (uint64, error) {
	receipt, err := d.l1Client.TransactionReceipt(context.Background(), txHash)
	if err != nil {
		return 0, err
	}
	if receipt.Status == eth.ReceiptStatusFailed {
		return 0, err
	}
	for _, lg := range receipt.Logs {
		batchStorage, err := d.zkEvm.ParseBatchStorage(*lg)
		if err != nil {
			continue
		}
		if batchStorage.BlockNumber == blockNumber {
			return batchStorage.BatchIndex, nil
		}
	}
	return 0, fmt.Errorf("event not found")
}

func encodeTransactions(txs []*eth.Transaction) [][]byte {
	var enc = make([][]byte, len(txs))
	for i, tx := range txs {
		enc[i], _ = tx.MarshalBinary()
	}
	return enc
}
