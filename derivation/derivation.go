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
	ZKEvmEventTopic     = "SubmitBatches(uint64,uint64)"
	ZKEvmEventTopicHash = crypto.Keccak256Hash([]byte(ZKEvmEventTopic))
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
	blsData    *eth.BLSData
	Root       common.Hash
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
	d.logger.Info("derivation start pull block form l1", "startBlock", start, "end", end)
	logs, err := d.fetchZkEvmLog(ctx, start, end)
	if err != nil {
		d.logger.Error("eth_getLogs failed", "err", err)
		return
	}

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
		d.logger.Info("WriteLatestDerivationL1Height success", "L1BlockNumber", lg.BlockNumber)
		d.db.WriteLatestBatchBls(batchBls)
		d.logger.Info("WriteLatestBatchBls success", "lastBlockNumber", batchBls.BlockNumber)
	}

	d.db.WriteLatestDerivationL1Height(end)
}

func (d *Derivation) fetchZkEvmLog(ctx context.Context, from, to uint64) ([]eth.Log, error) {
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
	abi, err := bindings.ZKEVMMetaData.GetAbi()
	if err != nil {
		return nil, err
	}
	args, err := abi.Methods["submitBatches"].Inputs.Unpack(tx.Data()[4:])
	if err != nil {
		return nil, fmt.Errorf("submitBatches Unpack error:%v", err)
	}
	// parse input to zkevm batch data
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
	for batchDataIndex, zkEVMBatchData := range zkEVMBatchDatas {
		bd := new(BatchData)
		if err := bd.DecodeBlockContext(zkEVMBatchData.BlockNumber, zkEVMBatchData.BlockWitness); err != nil {
			return fmt.Errorf("BatchData DecodeBlockContext error:%v", err)
		}
		if err := bd.DecodeTransactions(zkEVMBatchData.Transactions); err != nil {
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
				rollupData.LastBlockNumber = zkEVMBatchDatas[len(zkEVMBatchDatas)-1].BlockNumber
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
				safeL2Data.Transactions = encodeTransactions(bd.Txs[last : last+block.NumTxs])
				last += block.NumTxs
			} else {
				safeL2Data.Transactions = [][]byte{}
			}
			blockData.SafeL2Data = &safeL2Data
			if index == 0 && batchBls != nil {
				if batchBls.BlockNumber != blockData.SafeL2Data.Number-1 {
					return fmt.Errorf("miss last batch bls data,expect:%v but got %v", blockData.SafeL2Data.Number-1, batchBls.BlockNumber)
				}
				// Puts the Bls signature of the previous Batch in the first
				// block of the current batch
				blockData.blsData = batchBls.BlsData
			}
			if index == len(bd.BlockContexts)-1 {
				// only last block of batch
				if zkEVMBatchData.Signature.Signature == nil || zkEVMBatchData.Signature.Signers == nil {
					d.logger.Error("invalid batch", "l1BlockNumber", rollupData.L1BlockNumber)
				}
				var blsData eth.BLSData
				blsData.BLSSignature = zkEVMBatchData.Signature.Signature
				blsData.BLSSigners = zkEVMBatchData.Signature.Signers
				// The Bls signature of the current Batch is temporarily
				// stored and later placed in the first Block of the next Batch
				if batchBls != nil {
					batchBls.BlsData = &blsData
					batchBls.BlockNumber = block.Number.Uint64()
				}
				// StateRoot of the last Block of the Batch, used to verify the
				// validity of the Layer1 BlockData
				blockData.Root = zkEVMBatchData.PostStateRoot
			}
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
		lastHeader, err = d.l2Client.NewSafeL2Block(context.Background(), blockData.SafeL2Data, blockData.blsData)
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
