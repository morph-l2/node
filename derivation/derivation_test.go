package derivation

import (
	"context"
	"crypto/ecdsa"
	"github.com/morph-l2/bindings/bindings"
	"github.com/scroll-tech/go-ethereum/common/hexutil"
	"github.com/scroll-tech/go-ethereum/eth"
	"math/big"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/morph-l2/node/types"
	"github.com/scroll-tech/go-ethereum"
	"github.com/scroll-tech/go-ethereum/accounts/abi/bind"
	"github.com/scroll-tech/go-ethereum/accounts/abi/bind/backends"
	"github.com/scroll-tech/go-ethereum/common"
	"github.com/scroll-tech/go-ethereum/core"
	"github.com/scroll-tech/go-ethereum/core/rawdb"
	"github.com/scroll-tech/go-ethereum/ethclient"
	"github.com/scroll-tech/go-ethereum/ethclient/authclient"
	"github.com/scroll-tech/go-ethereum/ethdb"
	"github.com/scroll-tech/go-ethereum/rpc"
	"github.com/stretchr/testify/require"
	tmlog "github.com/tendermint/tendermint/libs/log"
)

func TestCompareBlock(t *testing.T) {
	eClient, err := ethclient.Dial("http://localhost:7545")
	require.NoError(t, err)
	l2Client, err := ethclient.Dial("http://localhost:8545")
	blockNumber, err := eClient.BlockNumber(context.Background())
	require.NoError(t, err)
	for i := 0; i < int(blockNumber); i++ {
		block, err := l2Client.BlockByNumber(context.Background(), big.NewInt(int64(i)))
		require.NoError(t, err)
		dBlock, err := eClient.BlockByNumber(context.Background(), big.NewInt(int64(i)))
		require.True(t, reflect.DeepEqual(block.Header(), dBlock.Header()))
	}
}

func testNewDerivationClient(t *testing.T) *Derivation {
	ctx := context.Background()
	l1Client, err := ethclient.Dial("http://localhost:9545")
	addr := common.HexToAddress("0x6900000000000000000000000000000000000010")
	require.NoError(t, err)
	var secret [32]byte
	jwtSecret := common.FromHex(strings.TrimSpace("688f5d737bad920bdfb2fc2f488d6b6209eebda1dae949a8de91398d932c517a"))
	require.True(t, len(jwtSecret) == 32)
	copy(secret[:], jwtSecret)
	aClient, err := authclient.DialContext(context.Background(), "http://localhost:7551", secret)
	require.NoError(t, err)
	eClient, err := ethclient.Dial("http://localhost:7545")
	require.NoError(t, err)
	d := Derivation{
		ctx:                   ctx,
		l1Client:              l1Client,
		RollupContractAddress: addr,
		confirmations:         rpc.BlockNumber(5),
		l2Client:              types.NewRetryableClient(aClient, eClient, tmlog.NewTMLogger(tmlog.NewSyncWriter(os.Stdout))),
		validator:             nil,
		latestDerivation:      9,
		fetchBlockRange:       100,
		pollInterval:          1,
	}
	return &d
}

func TestFetchRollupData(t *testing.T) {
	d := testNewDerivationClient(t)
	logs, err := d.fetchRollupLog(context.Background(), 1, 1000)
	require.NoError(t, err)
	for _, lg := range logs {
		rollupData, err := d.fetchRollupDataByTxHash(lg.TxHash, lg.BlockNumber)
		if err != nil {
			d.logger.Error("fetch rollup data failed", "txHash", lg.TxHash, "blockNumber", lg.BlockNumber)
			return
		}
		d.logger.Info("fetch rollup transaction success", "txNonce", rollupData.Nonce, "txHash", rollupData.TxHash,
			"l1BlockNumber", rollupData.L1BlockNumber, "firstL2BlockNumber", rollupData.FirstBlockNumber, "lastL2BlockNumber", rollupData.LastBlockNumber)

	}
}

func newSimulatedBackend(key *ecdsa.PrivateKey) (*backends.SimulatedBackend, ethdb.Database) {
	var gasLimit uint64 = 9_000_000
	auth, _ := bind.NewKeyedTransactorWithChainID(key, big.NewInt(1337))
	genAlloc := make(core.GenesisAlloc)
	genAlloc[auth.From] = core.GenesisAccount{Balance: big.NewInt(9223372036854775807)}
	db := rawdb.NewMemoryDatabase()
	sim := backends.NewSimulatedBackendWithDatabase(db, genAlloc, gasLimit)
	return sim, db
}

func TestFindBatchIndex(t *testing.T) {
	d := testNewDerivationClient(t)
	query := ethereum.FilterQuery{
		FromBlock: big.NewInt(0).SetUint64(1),
		ToBlock:   big.NewInt(0).SetUint64(2000),
		Addresses: []common.Address{
			d.RollupContractAddress,
		},
		Topics: [][]common.Hash{
			{RollupEventTopicHash},
		},
	}
	rollup, err := bindings.NewRollup(d.RollupContractAddress, d.l1Client)
	require.NoError(t, err)
	d.rollup = rollup
	logs, err := d.l1Client.FilterLogs(context.Background(), query)
	require.NoError(t, err)
	require.True(t, len(logs) != 0)
	for _, lg := range logs {
		batchIndex, err := d.findBatchIndex(lg.TxHash, 20)
		if err != nil {
			continue
		}
		require.NotZero(t, batchIndex)
		break
	}
}

func TestDecodeBatch(t *testing.T) {
	abi, err := bindings.RollupMetaData.GetAbi()
	require.NoError(t, err)
	hexData := "0x16b799c9000000000000000000000000000000000000000000000000000000000000002000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000100000000000000000000000000000000000000000000000000000000000000018000000000000000000000000000000000000000000000000000000000000002201d4ceff4b2335970615354f0a03e2124745d1c193d509e9109e4910b869448ce0f199dc7fb956c94a69ba951785dae12d9d6c7ed3073dd5a5453151d9996a25127ae5ba08d7291c96c8cbddcc148bf48a6d68c7974b94356f53754ef6171d75700000000000000000000000000000000000000000000000000000000000002600000000000000000000000000000000000000000000000000000000000000059000000000000000000000000000000000000000000000000008cb3decea512e30f962b50492fee707a926cd3465d2ac9b2b9655553a915578d00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000010000000000000000000000000000000000000000000000000000000000000020000000000000000000000000000000000000000000000000000000000000003d010000000000000001000000006570805b0000000000000000000000000000000000000000000000000000000000000000000000000098968000010001000000000000000000000000000000000000000000000000000000000000000000002000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000001000000000000000000000000000000000000000000000000000000000000006000000000000000000000000000000000000000000000000000000000000001000000000000000000000000000000000000000000000000000000000000000004000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000010000000000000000000000000000000000000000000000000000000000000002000000000000000000000000000000000000000000000000000000000000000300000000000000000000000000000000000000000000000000000000000000800000000000000000000000000000000005bc55b7eb2a19d020541768ca2c88e9feed6df90edc34e7aac67e1c2ae0a2ae72e2b381671f72bf2067d74ab0c11a91000000000000000000000000000000000fbdc060a4fd3fb5923c5ff9a430acdba1fbc17136e76cb85af00b274dfeaed8ce982afd68ffea340dd71c1391e31ad3"
	txData, err := hexutil.Decode(hexData)
	require.NoError(t, err)
	args, err := abi.Methods["commitBatch"].Inputs.Unpack(txData[4:])
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
	batch := eth.RPCRollupBatch{
		Version:                uint(rollupBatchData.Version),
		ParentBatchHeader:      rollupBatchData.ParentBatchHeader,
		Chunks:                 chunks,
		SkippedL1MessageBitmap: rollupBatchData.SkippedL1MessageBitmap,
		PrevStateRoot:          common.BytesToHash(rollupBatchData.PrevStateRoot[:]),
		PostStateRoot:          common.BytesToHash(rollupBatchData.PostStateRoot[:]),
		WithdrawRoot:           common.BytesToHash(rollupBatchData.WithdrawalRoot[:]),
	}
	_, err = parseBatch(batch)
	require.NoError(t, err)
}
