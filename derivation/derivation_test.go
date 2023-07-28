package derivation

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"fmt"
	"github.com/morphism-labs/morphism-bindings/bindings"
	"github.com/morphism-labs/node/types"
	"github.com/scroll-tech/go-ethereum"
	"github.com/scroll-tech/go-ethereum/common"
	"github.com/scroll-tech/go-ethereum/ethclient/authclient"
	"github.com/scroll-tech/go-ethereum/rpc"
	tmlog "github.com/tendermint/tendermint/libs/log"
	"math/big"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/scroll-tech/go-ethereum/accounts/abi/bind"
	"github.com/scroll-tech/go-ethereum/accounts/abi/bind/backends"
	"github.com/scroll-tech/go-ethereum/core"
	"github.com/scroll-tech/go-ethereum/core/rawdb"
	"github.com/scroll-tech/go-ethereum/ethclient"
	"github.com/scroll-tech/go-ethereum/ethdb"
	"github.com/stretchr/testify/require"
)

//
//func TestDerivationBlock(t *testing.T) {
//	//prepare msg
//	key, _ := crypto.GenerateKey()
//	sim, _ := newSimulatedBackend(key)
//	opts, _ := bind.NewKeyedTransactorWithChainID(key, big.NewInt(1337))
//	_, _, zkevm, err := bindings.DeployZKEVM(opts, sim, common.Address{}, opts.From, crypto.PubkeyToAddress(key.PublicKey),"")
//	require.NoError(t, err)
//	_, err = zkevm.SubmitBatches(opts, []bindings.ZKEVMBatchData{})
//	require.NoError(t, err)
//	sim.Commit()
//	context.Background()
//	dbConfig := db.DefaultConfig()
//	store, err := db.NewStore(dbConfig, "test")
//	require.NoError(t, err)
//	ctx := context.Background()
//	d := Derivation{
//		ctx:                  ctx,
//		l1Client:             sim,
//		ZKEvmContractAddress: &common.Address{},
//		confirmations:        rpc.BlockNumber(5),
//		l2Client:             nil,
//		validator:            nil,
//		latestDerivation:     9,
//		db:                   store,
//		fetchBlockRange:      100,
//	}
//
//	d.derivationBlock(ctx)
//	require.EqualError(t, err, "execution reverted: FetchBatch not exist")
//}

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
		ctx:                  ctx,
		l1Client:             l1Client,
		ZKEvmContractAddress: &addr,
		confirmations:        rpc.BlockNumber(5),
		l2Client:             types.NewRetryableClient(aClient, eClient, tmlog.NewTMLogger(tmlog.NewSyncWriter(os.Stdout))),
		validator:            nil,
		latestDerivation:     9,
		fetchBlockRange:      100,
		pollInterval:         1,
	}
	return &d
}

func TestDerivation_Block(t *testing.T) {
	d := testNewDerivationClient(t)
	batchs, err := d.fetchZkEvmData(context.Background(), 1, 1000)
	require.NoError(t, err)
	for _, batch := range batchs {
		for _, blockData := range batch.BlockDatas {
			latestBlockNumber, err := d.l2Client.BlockNumber(context.Background())
			if err != nil {
				return
			}
			if blockData.SafeL2Data.Number <= latestBlockNumber {
				continue
			}
			header, err := d.l2Client.NewSafeL2Block(context.Background(), blockData.SafeL2Data, blockData.blsData)
			require.NoError(t, err)
			if !bytes.Equal(header.Hash().Bytes(), blockData.Root.Bytes()) && d.validator != nil && d.validator.ChallengeEnable() {
				fmt.Println("block hash is not equal", "l1", blockData.Root, "l2", header.Hash())
				err := d.validator.ChallengeState(1)
				require.NoError(t, err)
				return
			}
		}
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
			*d.ZKEvmContractAddress,
		},
		Topics: [][]common.Hash{
			{ZKEvmEventTopicHash},
		},
	}
	zkEvm, err := bindings.NewZKEVM(*d.ZKEvmContractAddress, d.l1Client)
	require.NoError(t, err)
	d.zkEvm = zkEvm
	logs, err := d.l1Client.FilterLogs(context.Background(), query)
	require.NoError(t, err)
	require.True(t, len(logs) != 0)
	for _, lg := range logs {
		batchIndex, err := d.findBatchIndex(lg.TxHash, 20)
		if err != nil {
			continue
		}
		fmt.Printf("batch index :%v", batchIndex)
		break
	}
}
