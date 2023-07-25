package derivation

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/morphism-labs/node/db"
	"github.com/morphism-labs/node/types"
	"github.com/morphism-labs/node/types/bindings"
	"github.com/scroll-tech/go-ethereum/accounts/abi/bind"
	"github.com/scroll-tech/go-ethereum/accounts/abi/bind/backends"
	"github.com/scroll-tech/go-ethereum/common"
	"github.com/scroll-tech/go-ethereum/core"
	"github.com/scroll-tech/go-ethereum/core/rawdb"
	"github.com/scroll-tech/go-ethereum/crypto"
	"github.com/scroll-tech/go-ethereum/ethclient"
	"github.com/scroll-tech/go-ethereum/ethclient/authclient"
	"github.com/scroll-tech/go-ethereum/ethdb"
	"github.com/scroll-tech/go-ethereum/rpc"
	"github.com/stretchr/testify/require"
	tmlog "github.com/tendermint/tendermint/libs/log"
)

func TestDerivationBlock(t *testing.T) {
	//prepare msg
	key, _ := crypto.GenerateKey()
	sim, _ := newSimulatedBackend(key)
	opts, _ := bind.NewKeyedTransactorWithChainID(key, big.NewInt(1337))
	_, _, zkevm, err := bindings.DeployZKEVM(opts, sim, common.Address{}, opts.From, crypto.PubkeyToAddress(key.PublicKey))
	require.NoError(t, err)
	_, err = zkevm.SubmitBatches(opts, []bindings.ZKEVMBatchData{})
	require.NoError(t, err)
	sim.Commit()
	context.Background()
	dbConfig := db.DefaultConfig()
	store, err := db.NewStore(dbConfig, "test")
	require.NoError(t, err)
	ctx := context.Background()
	d := Derivation{
		ctx:                  ctx,
		l1Client:             sim,
		ZKEvmContractAddress: &common.Address{},
		confirmations:        rpc.BlockNumber(5),
		l2Client:             nil,
		validator:            nil,
		latestDerivation:     9,
		db:                   store,
		fetchBlockRange:      100,
	}

	d.derivationBlock(ctx)
	require.EqualError(t, err, "execution reverted: Batch not exist")
}

func TestDerivation_Start(t *testing.T) {
	ctx := context.Background()
	l1Client, err := ethclient.Dial("http://localhost:9545")
	addr := common.HexToAddress("0x6900000000000000000000000000000000000010")
	require.NoError(t, err)
	// Used to query sequencer Block
	var secret [32]byte
	jwtSecret := common.FromHex(strings.TrimSpace("688f5d737bad920bdfb2fc2f488d6b6209eebda1dae949a8de91398d932c517a"))
	if len(jwtSecret) != 32 {
		return
	}
	copy(secret[:], jwtSecret)
	aClient, err := authclient.DialContext(context.Background(), "http://localhost:7551", secret)
	if err != nil {
		fmt.Println("authclient.DialContext:", err)
		return
	}
	eClient, err := ethclient.Dial("http://localhost:7545")
	if err != nil {
		fmt.Println("ethclient.Dial:", err)
		return
	}
	l2Client, err := ethclient.Dial("http://localhost:8545")
	require.NoError(t, err)
	//block, err := l2Client.BlockByNumber(context.Background(), big.NewInt(1))
	//dBlock, err := eClient.BlockByNumber(context.Background(), big.NewInt(1))
	//fmt.Printf("block header:%+v\n", blo
	for i := 0; i < 1950; i++ {
		block, err := l2Client.BlockByNumber(context.Background(), big.NewInt(int64(i)))
		require.NoError(t, err)
		dBlock, err := eClient.BlockByNumber(context.Background(), big.NewInt(int64(i)))
		fmt.Printf("block header:%+v\n", block.Header())
		fmt.Printf("d block header:%+v\n", dBlock.Header())
		fmt.Printf("is eq：%v\n", reflect.DeepEqual(block.Header(), dBlock.Header()))
		require.True(t, reflect.DeepEqual(block.Header().Hash(), dBlock.Header().Hash()))
	}
	d := Derivation{
		ctx:                  ctx,
		l1Client:             l1Client,
		ZKEvmContractAddress: &addr,
		confirmations:        rpc.BlockNumber(5),
		l2Client:             types.NewRetryableClient(aClient, eClient, tmlog.NewTMLogger(tmlog.NewSyncWriter(os.Stdout))),
		validator:            nil,
		latestDerivation:     9,
		//db:                   store,
		fetchBlockRange: 100,
		pollInterval:    1,
	}
	ZKEvmEventTopic = "SubmitBatches(uint64,uint64)"
	ZKEvmEventTopicHash = crypto.Keccak256Hash([]byte(ZKEvmEventTopic))
	//for i := 0; i < 100; i++ {
	batchs, err := d.fetchZkEvmData(context.Background(), 1, 1000)
	if err != nil {
		panic(err)
	}
	for _, batch := range batchs {
		for _, blockData := range batch.BlockDatas {
			latestBlockNumber, err := d.l2Client.BlockNumber(ctx)
			if err != nil {
				return
			}
			if blockData.SafeL2Data.Number <= latestBlockNumber {
				continue
			}
			header, err := d.l2Client.NewSafeL2Block(ctx, blockData.SafeL2Data, blockData.blsData)
			if err != nil {
				fmt.Println("NewL2Block failed", "error", err)
				return
			}
			if !bytes.Equal(header.Hash().Bytes(), blockData.Root.Bytes()) && d.validator != nil && d.validator.ChallengeEnable() {
				fmt.Println("block hash is not equal", "l1", blockData.Root, "l2", header.Hash())
				err := d.validator.ChallengeState(1)
				require.NoError(t, err)
				return
			}
		}
	}
	//}
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
