package derivation

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"os"
	"time"

	"github.com/morph-l2/bindings/bindings"
	node "github.com/morph-l2/node/core"
	"github.com/morph-l2/node/sync"
	"github.com/morph-l2/node/types"
	"github.com/morph-l2/node/validator"
	"github.com/scroll-tech/go-ethereum"
	"github.com/scroll-tech/go-ethereum/accounts/abi/bind"
	"github.com/scroll-tech/go-ethereum/common"
	"github.com/scroll-tech/go-ethereum/common/hexutil"
	eth "github.com/scroll-tech/go-ethereum/core/types"
	"github.com/scroll-tech/go-ethereum/crypto"
	geth "github.com/scroll-tech/go-ethereum/eth"
	"github.com/scroll-tech/go-ethereum/eth/catalyst"
	"github.com/scroll-tech/go-ethereum/ethclient"
	"github.com/scroll-tech/go-ethereum/ethclient/authclient"
	"github.com/scroll-tech/go-ethereum/rpc"
	tmlog "github.com/tendermint/tendermint/libs/log"
)

var (
	RollupEventTopic     = "CommitBatch(uint256,bytes32)"
	RollupEventTopicHash = crypto.Keccak256Hash([]byte(RollupEventTopic))
)

// BatchInfo is all rollup data of one l1 block,maybe contain many rollup batch
type BatchInfo struct {
	BatchIndex       uint64
	BlockNum         uint64
	TxNum            uint64
	Version          uint64
	DataHash         common.Hash
	BatchHash        common.Hash
	Chunks           []*Chunk
	L1BlockNumber    uint64
	TxHash           common.Hash
	Nonce            uint64
	LastBlockNumber  uint64
	FirstBlockNumber uint64

	Root                   common.Hash
	skippedL1MessageBitmap *big.Int
}

func newRollupData(blockNumber uint64, txHash common.Hash, nonce uint64) *BatchInfo {
	return &BatchInfo{
		L1BlockNumber: blockNumber,
		TxHash:        txHash,
		Nonce:         nonce,
	}
}

type Derivation struct {
	ctx                   context.Context
	syncer                *sync.Syncer
	l1Client              DeployContractBackend
	RollupContractAddress common.Address
	confirmations         rpc.BlockNumber
	l2Client              *types.RetryableClient
	validator             *validator.Validator
	logger                tmlog.Logger
	rollup                *bindings.Rollup
	metrics               *Metrics

	// TODO delete
	sequencerClient *ethclient.Client

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

func NewDerivationClient(ctx context.Context, cfg *Config, syncer *sync.Syncer, db Database, validator *validator.Validator, rollup *bindings.Rollup, logger tmlog.Logger) (*Derivation, error) {
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
	// TODO delete
	sequencerClient, err := ethclient.Dial("http://morhp-geth-0:8545")
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
		syncer:                syncer,
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

		// TODO delete
		sequencerClient: sequencerClient,
	}, nil
}

func (d *Derivation) Start() {
	// block node startup during initial sync and print some helpful logs
	go func() {
		d.syncer.Start()
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
	//latest, err := nodecommon.GetLatestConfirmedBlockNumber(ctx, d.l1Client.(*ethclient.Client), d.confirmations)
	//if err != nil {
	//	d.logger.Error("GetLatestConfirmedBlockNumber failed", "error", err)
	//	return
	//}
	latest := d.syncer.LatestSynced()
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
	latestBatchIndex, err := d.rollup.LastCommittedBatchIndex(nil)
	if err != nil {
		d.logger.Error("query rollup latestCommitted batch Index failed", "err", err)
		return
	}
	// parse latest batch
	d.logger.Info(fmt.Sprintf("rollup latest batch index:%v", latestBatchIndex))
	d.logger.Info("fetched rollup tx", "txNum", len(logs))

	for _, lg := range logs {
		batchInfo, err := d.fetchRollupDataByTxHash(lg.TxHash, lg.BlockNumber)
		if err != nil {
			rollupCommitBatch, parseErr := d.rollup.ParseCommitBatch(lg)
			//blockNumber, err := d.l2Client.BlockNumber(ctx)
			if parseErr != nil {
				d.logger.Error("get l2 BlockNumber", "err", err)
				return
			}
			if rollupCommitBatch.BatchIndex.Uint64() == 0 {
				continue
			}
			d.logger.Error("fetch batch info failed", "txHash", lg.TxHash, "blockNumber", lg.BlockNumber, "error", err)
			return
		}
		d.logger.Info("fetch rollup transaction success", "txNonce", batchInfo.Nonce, "txHash", batchInfo.TxHash,
			"l1BlockNumber", batchInfo.L1BlockNumber, "firstL2BlockNumber", batchInfo.FirstBlockNumber, "lastL2BlockNumber", batchInfo.LastBlockNumber)

		// derivation
		lastHeader, err := d.derive(batchInfo)
		if err != nil {
			d.logger.Error("derive blocks interrupt", "error", err)
			return
		}
		// only last block of batch
		d.logger.Info("batch derivation complete", "currentBatchEndBlock", lastHeader.Number.Uint64())
		d.metrics.SetL2DeriveHeight(lastHeader.Number.Uint64())
		if !bytes.Equal(lastHeader.Root.Bytes(), batchInfo.Root.Bytes()) && d.validator != nil && d.validator.ChallengeEnable() {
			d.logger.Info("root hash is not equal", "originStateRootHash", batchInfo.Root, "deriveStateRootHash", lastHeader.Root.Hex())
			//batchIndex, err := d.findBatchIndex(batchInfo.TxHash, batchInfo[len(batchInfo)-1].SafeL2Data.Number)
			//if err != nil {
			//	d.logger.Error("find batch index failed", "error", err)
			//	return
			//}
			//d.logger.Info("validator start challenge", "batchIndex", batchIndex)
			//if err := d.validator.ChallengeState(batchIndex); err != nil {
			//	d.logger.Error("challenge state failed", "error", err)
			//
			//}
			return
		}
		//}
		d.db.WriteLatestDerivationL1Height(lg.BlockNumber)
		d.metrics.SetL1SyncHeight(lg.BlockNumber)
		d.logger.Info("WriteLatestDerivationL1Height success", "L1BlockNumber", lg.BlockNumber)
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

func (d *Derivation) fetchRollupDataByTxHash(txHash common.Hash, blockNumber uint64) (*BatchInfo, error) {
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
	args, err := abi.Methods["commitBatch"].Inputs.Unpack(tx.Data()[4:])
	if err != nil {
		return nil, fmt.Errorf("submitBatches Unpack error:%v", err)
	}

	rollupBatchData := args[0].(struct {
		Version                uint8     "json:\"version\""
		ParentBatchHeader      []uint8   "json:\"parentBatchHeader\""
		Chunks                 [][]uint8 "json:\"chunks\""
		SkippedL1MessageBitmap []uint8   "json:\"skippedL1MessageBitmap\""
		PrevStateRoot          [32]uint8 "json:\"prevStateRoot\""
		PostStateRoot          [32]uint8 "json:\"postStateRoot\""
		WithdrawalRoot         [32]uint8 "json:\"withdrawalRoot\""
		Signature              struct {
			Version   *big.Int   "json:\"version\""
			Signers   []*big.Int "json:\"signers\""
			Signature []uint8    "json:\"signature\""
		} "json:\"signature\""
	})
	var chunks []hexutil.Bytes
	for _, chunk := range rollupBatchData.Chunks {
		chunks = append(chunks, chunk)
	}
	batch := geth.RPCRollupBatch{
		Version:                uint(rollupBatchData.Version),
		ParentBatchHeader:      rollupBatchData.ParentBatchHeader,
		Chunks:                 chunks,
		SkippedL1MessageBitmap: rollupBatchData.SkippedL1MessageBitmap,
		PrevStateRoot:          common.BytesToHash(rollupBatchData.PrevStateRoot[:]),
		PostStateRoot:          common.BytesToHash(rollupBatchData.PostStateRoot[:]),
		WithdrawRoot:           common.BytesToHash(rollupBatchData.WithdrawalRoot[:]),
	}
	//rollupData := newRollupData(blockNumber, txHash, tx.Nonce())
	rollupData, err := d.parseBatch(batch)
	if err != nil {
		d.logger.Error("ParseBatch failed", "txNonce", tx.Nonce(), "txHash", txHash,
			"l1BlockNumber", blockNumber)
		return rollupData, fmt.Errorf("ParseBatch error:%v\n", err)
	}
	rollupData.L1BlockNumber = blockNumber
	rollupData.TxHash = txHash
	rollupData.Nonce = tx.Nonce()
	return rollupData, nil
}

type Chunk struct {
	blockContext []*BlockContext
	txsPayload   [][]*eth.Transaction
	txHashes     [][]common.Hash
	blockNum     int
}

type BlockContext struct {
	Number    uint64 `json:"number"`
	Timestamp uint64 `json:"timestamp"`
	BaseFee   *big.Int
	GasLimit  uint64
	txsNum    uint16
	l1MsgNum  uint16

	SafeL2Data *catalyst.SafeL2Data
}

func (b *BlockContext) Decode(bc []byte) error {
	reader := bytes.NewReader(bc)
	bsBaseFee := make([]byte, 32)
	if err := binary.Read(reader, binary.BigEndian, &b.Number); err != nil {
		return err
	}
	if err := binary.Read(reader, binary.BigEndian, &b.Timestamp); err != nil {
		return err
	}
	if err := binary.Read(reader, binary.BigEndian, &bsBaseFee); err != nil {
		return err
	}
	b.BaseFee = new(big.Int).SetBytes(bsBaseFee)
	if err := binary.Read(reader, binary.BigEndian, &b.GasLimit); err != nil {
		return err
	}
	if err := binary.Read(reader, binary.BigEndian, &b.txsNum); err != nil {
		return err
	}
	if err := binary.Read(reader, binary.BigEndian, &b.l1MsgNum); err != nil {
		return err
	}
	return nil
}

func parseChunk(chunkBytes []byte) (*types.Chunk, error) {
	reader := bytes.NewReader(chunkBytes)
	var blockNum uint8
	if err := binary.Read(reader, binary.BigEndian, &blockNum); err != nil {
		return nil, err
	}

	blockCtx := make([]byte, 0)
	for i := 0; i < int(blockNum); i++ {
		bc := make([]byte, 60)
		if err := binary.Read(reader, binary.BigEndian, &bc); err != nil {
			return nil, err
		}
		blockCtx = append(blockCtx, bc...)
	}
	txsPayload := make([]byte, len(chunkBytes)-int(blockNum)*60-1)
	if err := binary.Read(reader, binary.BigEndian, &txsPayload); err != nil {
		return nil, err
	}
	chunk := types.NewChunk(blockCtx, txsPayload, nil, nil)
	chunk.ResetBlockNum(int(blockNum))
	return chunk, nil
}

func (d *Derivation) parseBatch(batch geth.RPCRollupBatch) (*BatchInfo, error) {
	parentBatchHeader, err := types.DecodeBatchHeader(batch.ParentBatchHeader)
	if err != nil {
		return nil, fmt.Errorf("DecodeBatchHeader error:%v", err)
	}
	rollupData, err := ParseBatch(batch)
	if err != nil {
		return nil, fmt.Errorf("parse batch error:%v", err)
	}
	if err := d.handleL1Message(rollupData, &parentBatchHeader); err != nil {
		return nil, fmt.Errorf("handleL1Message error:%v", err)
	}
	rollupData.BatchIndex = parentBatchHeader.BatchIndex + 1
	return rollupData, nil
}

func ParseBatch(batch geth.RPCRollupBatch) (*BatchInfo, error) {
	var rollupData BatchInfo
	rollupData.Root = batch.PostStateRoot
	rollupData.skippedL1MessageBitmap = new(big.Int).SetBytes(batch.SkippedL1MessageBitmap[:])
	rollupData.Version = uint64(batch.Version)
	chunks := types.NewChunks()
	for cbIndex, chunkByte := range batch.Chunks {
		chunk, err := parseChunk(chunkByte)
		if err != nil {
			return nil, fmt.Errorf("parse chunk error:%v", err)
		}
		rollupData.BlockNum += uint64(chunk.BlockNum())
		rollupData.TxNum += uint64(len(chunk.TxHashes()))
		chunks.Append(chunk.BlockContext(), chunk.TxsPayload(), nil, nil)
		ck := Chunk{}
		var txsNum uint64
		var l1MsgNum uint64
		reader := bytes.NewReader(chunk.TxsPayload())
		for i := 0; i < chunk.BlockNum(); i++ {
			var block BlockContext
			err = block.Decode(chunk.BlockContext()[i*60 : i*60+60])
			if err != nil {
				return nil, fmt.Errorf("decode chunk block context error:%v", err)
			}
			if cbIndex == 0 && i == 0 {
				rollupData.FirstBlockNumber = block.Number
			}
			if cbIndex == len(batch.Chunks)-1 && i == chunk.BlockNum()-1 {
				rollupData.LastBlockNumber = block.Number
			}
			var safeL2Data catalyst.SafeL2Data
			safeL2Data.Number = block.Number
			safeL2Data.GasLimit = block.GasLimit
			safeL2Data.BaseFee = block.BaseFee
			safeL2Data.Timestamp = block.Timestamp
			if block.BaseFee != nil && block.BaseFee.Cmp(big.NewInt(0)) == 0 {
				safeL2Data.BaseFee = nil
			}
			if block.txsNum < block.l1MsgNum {
				return nil, fmt.Errorf("txsNum must be or equal to or greater than l1MsgNum,txsNum:%v,l1MsgNum:%v", block.txsNum, block.l1MsgNum)
			}

			txs, err := node.DecodeTxsPayload(reader, int(block.txsNum)-int(block.l1MsgNum))
			if err != nil {
				return nil, fmt.Errorf("DecodeTxsPayload error:%v", err)
			}
			txsNum += uint64(block.txsNum)
			l1MsgNum += uint64(block.l1MsgNum)
			safeL2Data.Transactions = encodeTransactions(txs)
			if block.txsNum > 0 {
				safeL2Data.Transactions = encodeTransactions(txs)
			} else {
				safeL2Data.Transactions = [][]byte{}
			}
			block.SafeL2Data = &safeL2Data
			ck.blockContext = append(ck.blockContext, &block)
		}
		rollupData.Chunks = append(rollupData.Chunks, &ck)
	}
	rollupData.DataHash = chunks.DataHash()
	return &rollupData, nil
}

func (d *Derivation) handleL1Message(rollupData *BatchInfo, parentBatchHeader *types.BatchHeader) error {
	batchHeader := types.BatchHeader{
		Version:                uint8(rollupData.Version),
		BatchIndex:             parentBatchHeader.BatchIndex + 1,
		DataHash:               rollupData.DataHash,
		ParentBatchHash:        parentBatchHeader.ParentBatchHash,
		SkippedL1MessageBitmap: rollupData.skippedL1MessageBitmap.Bytes(),
	}
	var l1MessagePopped, totalL1MessagePopped uint64
	totalL1MessagePopped = parentBatchHeader.TotalL1MessagePopped
	for index, chunk := range rollupData.Chunks {
		for bIndex, block := range chunk.blockContext {
			var l1Transactions []*eth.Transaction
			l1Messages, err := d.getL1Message(totalL1MessagePopped, uint64(block.l1MsgNum))
			if err != nil {
				return fmt.Errorf("getL1Message error:%v", err)
			}
			l1MessagePopped += uint64(block.l1MsgNum)
			totalL1MessagePopped += uint64(block.l1MsgNum)
			if len(l1Messages) > 0 {
				for _, l1Message := range l1Messages {
					if rollupData.skippedL1MessageBitmap.Bit(int(l1Message.QueueIndex)-int(parentBatchHeader.TotalL1MessagePopped)) == 1 {
						continue
					}
					transaction := eth.NewTx(&l1Message.L1MessageTx)
					l1Transactions = append(l1Transactions, transaction)
				}
			}
			rollupData.Chunks[index].blockContext[bIndex].SafeL2Data.Transactions = append(chunk.blockContext[bIndex].SafeL2Data.Transactions, encodeTransactions(l1Transactions)...)
		}

	}
	batchHeader.TotalL1MessagePopped = totalL1MessagePopped
	batchHeader.L1MessagePopped = l1MessagePopped
	batchHeader.Encode()
	rollupData.BatchHash = batchHeader.Hash()
	return nil
}

func (d *Derivation) getL1Message(l1MessagePopped, l1MsgNum uint64) ([]types.L1Message, error) {
	start := l1MessagePopped + 1
	end := l1MessagePopped + l1MsgNum
	return d.syncer.ReadL1MessagesInRange(start, end), nil
}

func (d *Derivation) derive(rollupData *BatchInfo) (*eth.Header, error) {
	var lastHeader *eth.Header
	for _, chunk := range rollupData.Chunks {
		for _, blockData := range chunk.blockContext {
			blockData.SafeL2Data.BatchHash = &rollupData.BatchHash
			latestBlockNumber, err := d.l2Client.BlockNumber(context.Background())
			if err != nil {
				return nil, fmt.Errorf("get derivation geth block number error:%v", err)
			}
			// TODO delete
			if int(blockData.txsNum) != len(blockData.SafeL2Data.Transactions) {
				fmt.Println("block data txsNum:", blockData.txsNum)
				fmt.Println("blockData.SafeL2Data.Transactions len:", len(blockData.SafeL2Data.Transactions))
				return nil, fmt.Errorf("invalid block nums")
			} else {
				fmt.Println("txnums equal txs length")
			}
			block, err := d.sequencerClient.BlockByNumber(d.ctx, big.NewInt(int64(blockData.Number)))
			//blockTxs := encodeTransactions(block.Transactions())
			for i := 0; i < len(block.Transactions()); i++ {
				var tx eth.Transaction
				if err := tx.UnmarshalBinary(blockData.SafeL2Data.Transactions[i]); err != nil {
					return nil, fmt.Errorf("tx.UnmarshalBinary error:%v", err)
				}
				if block.Transactions()[i].Hash() == tx.Hash() {
					fmt.Println("block.Transactions()[i].Hash()=========", block.Transactions()[i].Hash())
					fmt.Println("tx.Hash()=========", block.Transactions()[i].Hash())
					return nil, fmt.Errorf("tx hash not equal")
				}
			}
			time.Sleep(time.Second)
			if blockData.SafeL2Data.Number <= latestBlockNumber {
				d.logger.Info("SafeL2Data block number less than latestBlockNumber", "safeL2DataNumber", blockData.SafeL2Data.Number, "latestBlockNumber", latestBlockNumber)
				lastHeader, err = d.l2Client.HeaderByNumber(d.ctx, big.NewInt(int64(latestBlockNumber)))
				continue
			}
			d.logger.Info("NewSafeL2Block start...", "blockNumber", blockData.Number)
			fmt.Printf("blockData.SafeL2Data===========%+v\n", blockData.SafeL2Data)
			err = func() error {
				ctx, cancel := context.WithTimeout(context.Background(), time.Duration(60)*time.Second)
				defer cancel()
				lastHeader, err = d.l2Client.NewSafeL2Block(ctx, blockData.SafeL2Data)
				if err != nil {
					d.logger.Error("NewL2Block failed", "latestBlockNumber", latestBlockNumber, "error", err)
					return err
				}
				return nil
			}()
			if err != nil {
				return nil, fmt.Errorf("derivation error:%v", err)
			}
			d.logger.Info("NewSafeL2Block end...")
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
