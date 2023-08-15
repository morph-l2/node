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
		log.Error("GetLatestConfirmedBlockNumber failed", "error", err)
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
	batchBls := d.db.ReadLatestBatchBls()
	fetchBatches, err := d.fetchZkEvmData(ctx, start, end, &batchBls)
	if err != nil {
		d.logger.Error("FetchZkEvmData failed", "error", err)
		return
	}

	//derivation
	for _, fetchBatch := range fetchBatches {
		if err := d.derive(fetchBatch); err != nil {
			d.logger.Error("derive blocks interrupt", "error", err)
			return
		}
		d.db.WriteLatestDerivationL1Height(fetchBatch.L1BlockNumber)
		d.logger.Info("WriteLatestDerivationL1Height success", "L1BlockNumber", fetchBatch.L1BlockNumber)
		d.db.WriteLatestBatchBls(types.BatchBls{
			// All Batch Block counts are greater than or equal to 1
			BlockNumber: fetchBatch.BlockDatas[len(fetchBatch.BlockDatas)-1].SafeL2Data.Number,
			BlsData:     fetchBatch.BlockDatas[len(fetchBatch.BlockDatas)-1].blsData,
		})
		d.logger.Info("WriteLatestBatchBls success", "lastBlockNumber", fetchBatch.BlockDatas[len(fetchBatch.BlockDatas)-1].SafeL2Data.Number)
	}
	d.db.WriteLatestDerivationL1Height(end)
}

func (d *Derivation) fetchZkEvmData(ctx context.Context, from, to uint64, batchBls *types.BatchBls) ([]*FetchBatch, error) {
	if batchBls == nil {
		// checks are ignored only in a test environment
		d.logger.Info("batch bls is empty and will skip the bls check")
	}
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
		d.logger.Error("eth_getLogs failed", "query", query, "err", err)
		return nil, fmt.Errorf("eth_getLogs failed: %w", err)
	}

	if len(logs) == 0 {
		return nil, nil
	}
	var fetchDatas []*FetchBatch
	for _, lg := range logs {
		d.logger.Info("fetch log", "txHash", lg.TxHash, "blockNumber", lg.BlockNumber)
		fetchData, err := d.fetchRollupData(lg.TxHash, lg.BlockNumber, batchBls)
		if err != nil {
			return nil, err
		}
		fetchDatas = append(fetchDatas, fetchData)
	}
	return fetchDatas, nil
}

func (d *Derivation) fetchRollupData(txHash common.Hash, blockNumber uint64, batchBls *types.BatchBls) (*FetchBatch, error) {
	tx, pending, err := d.l1Client.TransactionByHash(context.Background(), txHash)
	if err != nil {
		return nil, err
	}
	if pending {
		return nil, errors.New("pending transaction")
	}
	d.logger.Info("fetch rollup transaction success", "nonce", tx.Nonce(), "txHash", tx.Hash().Hex(), "blockNumber", blockNumber)
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
	if err := d.argsToBlockDatas(args, fetchBatch, batchBls); err != nil {
		return nil, fmt.Errorf("argsToBlockDatas failed,txHash:%v,txNonce:%v\n,error:%v\n", tx.Hash().Hex(), tx.Nonce(), err)
	}
	return fetchBatch, nil
}

func (d *Derivation) argsToBlockDatas(args []interface{}, fetchBatch *FetchBatch, batchBls *types.BatchBls) error {
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
	//batchBls := d.db.ReadLatestBatchBls()
	for _, zkEVMBatchData := range zkEVMBatchDatas {
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
		for index, block := range bd.BlockContexts {
			d.logger.Info("fetched rollup block", "blockNumber", block.Number.Uint64())
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
				//if batchBls.BlockNumber != blockData.SafeL2Data.Number-1 {
				//	return fmt.Errorf("miss last batch bls data,expect:%v but got %v", blockData.SafeL2Data.Number-1, batchBls.BlockNumber)
				//}
				fmt.Printf("miss last batch bls data,expect:%v but got %v\n", blockData.SafeL2Data.Number-1, batchBls.BlockNumber)

				// Puts the Bls signature of the previous Batch in the first
				// block of the current batch
				blockData.blsData = batchBls.BlsData
			}
			if index == len(bd.BlockContexts)-1 {
				// only last block of batch
				if zkEVMBatchData.Signature.Signature == nil || zkEVMBatchData.Signature.Signers == nil {
					d.logger.Error("invalid batch", "l1BlockNumber", fetchBatch.L1BlockNumber)
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
			d.logger.Error("NewL2Block failed", "error", err)
			return err
		}
		d.logger.Info("block derivation complete", "currentBatchEndBlock", header.Number.Uint64())
		var zeroHash common.Hash
		if bytes.Equal(blockData.Root.Bytes(), zeroHash.Bytes()) {
			// only last block of batch
			d.logger.Info("batch derivation complete")
			if !bytes.Equal(header.Root.Bytes(), blockData.Root.Bytes()) && d.validator != nil && d.validator.ChallengeEnable() {
				d.logger.Info("root hash is not equal", "originStateRootHash", blockData.Root.Hex(), "deriveStateRootHash", header.Root.Hex())
				batchIndex, err := d.findBatchIndex(fetchBatch.TxHash, blockData.SafeL2Data.Number)
				if err != nil {
					return fmt.Errorf("find batch index error:%v", err)
				}
				d.logger.Info("validator start challenge", "batchIndex", batchIndex)
				if err := d.validator.ChallengeState(batchIndex); err != nil {
					d.logger.Error("challenge state failed", "error", err)
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
