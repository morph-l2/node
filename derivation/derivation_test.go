package derivation

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"github.com/scroll-tech/go-ethereum/common/hexutil"
	"math/big"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/morphism-labs/morphism-bindings/bindings"
	"github.com/morphism-labs/node/types"
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
	logs, err := d.fetchZkEvmLog(context.Background(), 1, 1000)
	require.NoError(t, err)
	for _, lg := range logs {
		batchBls := d.db.ReadLatestBatchBls()
		rollupData, err := d.fetchRollupDataByTxHash(lg.TxHash, lg.BlockNumber, &batchBls)
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

func TestNewDerivationClient(t *testing.T) {
	firstInput := "0xfabef37f000000000000000000000000000000000000000000000000000000000000002000000000000000000000000000000000000000000000000000000000000000010000000000000000000000000000000000000000000000000000000000000020000000000000000000000000000000000000000000000000000000000000006500000000000000000000000000000000000000000000000000000000000000e0000000000000000000000000000000000000000000000000000000000000012000000000000000000000000000000000000000000000000000000000000000002425be944cc380f8d876b3961d0ca200822491d73502ac46e158328ae67f0af227ae5ba08d7291c96c8cbddcc148bf48a6d68c7974b94356f53754ef6171d75700000000000000000000000000000000000000000000000000000000000023600000000000000000000000000000000000000000000000000000000000000001c000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000220800000000000000000000000000000000000000000000000000000000000000030000000064d5c10c00000000000000000000000000000000000000000000000000000000000000000000000001c86c88000000000000000000000000000000000000000000000000000000000000000000000000000000040000000064d5c11200000000000000000000000000000000000000000000000000000000000000000000000001c7fa6e000000000000000000000000000000000000000000000000000000000000000000000000000000050000000064d5c11800000000000000000000000000000000000000000000000000000000000000000000000001c78871000000000000000000000000000000000000000000000000000000000000000000000000000000060000000064d5c11e00000000000000000000000000000000000000000000000000000000000000000000000001c71690000000000000000000000000000000000000000000000000000000000000000000000000000000070000000064d5c12400000000000000000000000000000000000000000000000000000000000000000000000001c6a4cc000000000000000000000000000000000000000000000000000000000000000000000000000000080000000064d5c12a00000000000000000000000000000000000000000000000000000000000000000000000001c63324000000000000000000000000000000000000000000000000000000000000000000000000000000090000000064d5c13000000000000000000000000000000000000000000000000000000000000000000000000001c5c1990000000000000000000000000000000000000000000000000000000000000000000000000000000a0000000064d5c13600000000000000000000000000000000000000000000000000000000000000000000000001c5502a0000000000000000000000000000000000000000000000000000000000000000000000000000000b0000000064d5c13c00000000000000000000000000000000000000000000000000000000000000000000000001c4ded70000000000000000000000000000000000000000000000000000000000000000000000000000000c0000000064d5c14200000000000000000000000000000000000000000000000000000000000000000000000001c46da10000000000000000000000000000000000000000000000000000000000000000000000000000000d0000000064d5c14800000000000000000000000000000000000000000000000000000000000000000000000001c3fc870000000000000000000000000000000000000000000000000000000000000000000000000000000e0000000064d5c14e00000000000000000000000000000000000000000000000000000000000000000000000001c38b890000000000000000000000000000000000000000000000000000000000000000000000000000000f0000000064d5c15400000000000000000000000000000000000000000000000000000000000000000000000001c31aa8000000000000000000000000000000000000000000000000000000000000000000000000000000100000000064d5c15a00000000000000000000000000000000000000000000000000000000000000000000000001c2a9e3000000000000000000000000000000000000000000000000000000000000000000000000000000110000000064d5c16000000000000000000000000000000000000000000000000000000000000000000000000001c2393a000000000000000000000000000000000000000000000000000000000000000000000000000000120000000064d5c16600000000000000000000000000000000000000000000000000000000000000000000000001c1c8ad000000000000000000000000000000000000000000000000000000000000000000000000000000130000000064d5c16c00000000000000000000000000000000000000000000000000000000000000000000000001c1583c000000000000000000000000000000000000000000000000000000000000000000000000000000140000000064d5c17300000000000000000000000000000000000000000000000000000000000000000000000001c0e7e7000000000000000000000000000000000000000000000000000000000000000000000000000000150000000064d5c17900000000000000000000000000000000000000000000000000000000000000000000000001c077af000000000000000000000000000000000000000000000000000000000000000000000000000000160000000064d5c17f00000000000000000000000000000000000000000000000000000000000000000000000001c00793000000000000000000000000000000000000000000000000000000000000000000000000000000170000000064d5c18500000000000000000000000000000000000000000000000000000000000000000000000001bf9793000000000000000000000000000000000000000000000000000000000000000000000000000000180000000064d5c18b00000000000000000000000000000000000000000000000000000000000000000000000001bf27af000000000000000000000000000000000000000000000000000000000000000000000000000000190000000064d5c19100000000000000000000000000000000000000000000000000000000000000000000000001beb7e70000000000000000000000000000000000000000000000000000000000000000000000000000001a0000000064d5c19700000000000000000000000000000000000000000000000000000000000000000000000001be483b0000000000000000000000000000000000000000000000000000000000000000000000000000001b0000000064d5c19d00000000000000000000000000000000000000000000000000000000000000000000000001bdd8aa0000000000000000000000000000000000000000000000000000000000000000000000000000001c0000000064d5c1a300000000000000000000000000000000000000000000000000000000000000000000000001bd69350000000000000000000000000000000000000000000000000000000000000000000000000000001d0000000064d5c1a900000000000000000000000000000000000000000000000000000000000000000000000001bcf9dc0000000000000000000000000000000000000000000000000000000000000000000000000000001e0000000064d5c1af00000000000000000000000000000000000000000000000000000000000000000000000001bc8a9f0000000000000000000000000000000000000000000000000000000000000000000000000000001f0000000064d5c1b500000000000000000000000000000000000000000000000000000000000000000000000001bc1b7e000000000000000000000000000000000000000000000000000000000000000000000000000000200000000064d5c1bb00000000000000000000000000000000000000000000000000000000000000000000000001bbac79000000000000000000000000000000000000000000000000000000000000000000000000000000210000000064d5c1c100000000000000000000000000000000000000000000000000000000000000000000000001bb3d8f000000000000000000000000000000000000000000000000000000000000000000000000000000220000000064d5c1c700000000000000000000000000000000000000000000000000000000000000000000000001bacec1000000000000000000000000000000000000000000000000000000000000000000000000000000230000000064d5c1cd00000000000000000000000000000000000000000000000000000000000000000000000001ba600f000000000000000000000000000000000000000000000000000000000000000000000000000000240000000064d5c1d300000000000000000000000000000000000000000000000000000000000000000000000001b9f178000000000000000000000000000000000000000000000000000000000000000000000000000000250000000064d5c1d900000000000000000000000000000000000000000000000000000000000000000000000001b982fd000000000000000000000000000000000000000000000000000000000000000000000000000000260000000064d5c1df00000000000000000000000000000000000000000000000000000000000000000000000001b9149e000000000000000000000000000000000000000000000000000000000000000000000000000000270000000064d5c1e500000000000000000000000000000000000000000000000000000000000000000000000001b8a65a000000000000000000000000000000000000000000000000000000000000000000000000000000280000000064d5c1eb00000000000000000000000000000000000000000000000000000000000000000000000001b83832000000000000000000000000000000000000000000000000000000000000000000000000000000290000000064d5c1f100000000000000000000000000000000000000000000000000000000000000000000000001b7ca250000000000000000000000000000000000000000000000000000000000000000000000000000002a0000000064d5c1f700000000000000000000000000000000000000000000000000000000000000000000000001b75c340000000000000000000000000000000000000000000000000000000000000000000000000000002b0000000064d5c1fd00000000000000000000000000000000000000000000000000000000000000000000000001b6ee5e0000000000000000000000000000000000000000000000000000000000000000000000000000002c0000000064d5c20300000000000000000000000000000000000000000000000000000000000000000000000001b680a40000000000000000000000000000000000000000000000000000000000000000000000000000002d0000000064d5c20900000000000000000000000000000000000000000000000000000000000000000000000001b613050000000000000000000000000000000000000000000000000000000000000000000000000000002e0000000064d5c20f00000000000000000000000000000000000000000000000000000000000000000000000001b5a5820000000000000000000000000000000000000000000000000000000000000000000000000000002f0000000064d5c21500000000000000000000000000000000000000000000000000000000000000000000000001b5381a000000000000000000000000000000000000000000000000000000000000000000000000000000300000000064d5c21b00000000000000000000000000000000000000000000000000000000000000000000000001b4cacd000000000000000000000000000000000000000000000000000000000000000000000000000000310000000064d5c22100000000000000000000000000000000000000000000000000000000000000000000000001b45d9c000000000000000000000000000000000000000000000000000000000000000000000000000000320000000064d5c22700000000000000000000000000000000000000000000000000000000000000000000000001b3f086000000000000000000000000000000000000000000000000000000000000000000000000000000330000000064d5c22d00000000000000000000000000000000000000000000000000000000000000000000000001b3838b000000000000000000000000000000000000000000000000000000000000000000000000000000340000000064d5c23300000000000000000000000000000000000000000000000000000000000000000000000001b316ac000000000000000000000000000000000000000000000000000000000000000000000000000000350000000064d5c23900000000000000000000000000000000000000000000000000000000000000000000000001b2a9e8000000000000000000000000000000000000000000000000000000000000000000000000000000360000000064d5c23f00000000000000000000000000000000000000000000000000000000000000000000000001b23d3f000000000000000000000000000000000000000000000000000000000000000000000000000000370000000064d5c24500000000000000000000000000000000000000000000000000000000000000000000000001b1d0b1000000000000000000000000000000000000000000000000000000000000000000000000000000380000000064d5c24b00000000000000000000000000000000000000000000000000000000000000000000000001b1643e000000000000000000000000000000000000000000000000000000000000000000000000000000390000000064d5c25100000000000000000000000000000000000000000000000000000000000000000000000001b0f7e60000000000000000000000000000000000000000000000000000000000000000000000000000003a0000000064d5c25700000000000000000000000000000000000000000000000000000000000000000000000001b08baa0000000000000000000000000000000000000000000000000000000000000000000000000000003b0000000064d5c25d00000000000000000000000000000000000000000000000000000000000000000000000001b01f890000000000000000000000000000000000000000000000000000000000000000000000000000003c0000000064d5c26300000000000000000000000000000000000000000000000000000000000000000000000001afb3830000000000000000000000000000000000000000000000000000000000000000000000000000003d0000000064d5c26900000000000000000000000000000000000000000000000000000000000000000000000001af47980000000000000000000000000000000000000000000000000000000000000000000000000000003e0000000064d5c26f00000000000000000000000000000000000000000000000000000000000000000000000001aedbc80000000000000000000000000000000000000000000000000000000000000000000000000000003f0000000064d5c27600000000000000000000000000000000000000000000000000000000000000000000000001ae7013000000000000000000000000000000000000000000000000000000000000000000000000000000400000000064d5c27c00000000000000000000000000000000000000000000000000000000000000000000000001ae0478000000000000000000000000000000000000000000000000000000000000000000000000000000410000000064d5c28200000000000000000000000000000000000000000000000000000000000000000000000001ad98f8000000000000000000000000000000000000000000000000000000000000000000000000000000420000000064d5c28800000000000000000000000000000000000000000000000000000000000000000000000001ad2d93000000000000000000000000000000000000000000000000000000000000000000000000000000430000000064d5c28e00000000000000000000000000000000000000000000000000000000000000000000000001acc249000000000000000000000000000000000000000000000000000000000000000000000000000000440000000064d5c29400000000000000000000000000000000000000000000000000000000000000000000000001ac571a000000000000000000000000000000000000000000000000000000000000000000000000000000450000000064d5c29a00000000000000000000000000000000000000000000000000000000000000000000000001abec06000000000000000000000000000000000000000000000000000000000000000000000000000000460000000064d5c2a000000000000000000000000000000000000000000000000000000000000000000000000001ab810c000000000000000000000000000000000000000000000000000000000000000000000000000000470000000064d5c2a600000000000000000000000000000000000000000000000000000000000000000000000001ab162d000000000000000000000000000000000000000000000000000000000000000000000000000000480000000064d5c2ac00000000000000000000000000000000000000000000000000000000000000000000000001aaab69000000000000000000000000000000000000000000000000000000000000000000000000000000490000000064d5c2b200000000000000000000000000000000000000000000000000000000000000000000000001aa40c00000000000000000000000000000000000000000000000000000000000000000000000000000004a0000000064d5c2b800000000000000000000000000000000000000000000000000000000000000000000000001a9d6310000000000000000000000000000000000000000000000000000000000000000000000000000004b0000000064d5c2be00000000000000000000000000000000000000000000000000000000000000000000000001a96bbd0000000000000000000000000000000000000000000000000000000000000000000000000000004c0000000064d5c2c400000000000000000000000000000000000000000000000000000000000000000000000001a901640000000000000000000000000000000000000000000000000000000000000000000000000000004d0000000064d5c2ca00000000000000000000000000000000000000000000000000000000000000000000000001a897250000000000000000000000000000000000000000000000000000000000000000000000000000004e0000000064d5c2d000000000000000000000000000000000000000000000000000000000000000000000000001a82d010000000000000000000000000000000000000000000000000000000000000000000000000000004f0000000064d5c2d600000000000000000000000000000000000000000000000000000000000000000000000001a7c2f7000000000000000000000000000000000000000000000000000000000000000000000000000000500000000064d5c2dc00000000000000000000000000000000000000000000000000000000000000000000000001a75908000000000000000000000000000000000000000000000000000000000000000000000000000000510000000064d5c2e200000000000000000000000000000000000000000000000000000000000000000000000001a6ef33000000000000000000000000000000000000000000000000000000000000000000000000000000520000000064d5c2e800000000000000000000000000000000000000000000000000000000000000000000000001a68579000000000000000000000000000000000000000000000000000000000000000000000000000000530000000064d5c2ee00000000000000000000000000000000000000000000000000000000000000000000000001a61bd9000000000000000000000000000000000000000000000000000000000000000000000000000000540000000064d5c2f400000000000000000000000000000000000000000000000000000000000000000000000001a5b254000000000000000000000000000000000000000000000000000000000000000000000000000000550000000064d5c2fa00000000000000000000000000000000000000000000000000000000000000000000000001a548e9000000000000000000000000000000000000000000000000000000000000000000000000000000560000000064d5c30000000000000000000000000000000000000000000000000000000000000000000000000001a4df98000000000000000000000000000000000000000000000000000000000000000000000000000000570000000064d5c30600000000000000000000000000000000000000000000000000000000000000000000000001a47662000000000000000000000000000000000000000000000000000000000000000000000000000000580000000064d5c30c00000000000000000000000000000000000000000000000000000000000000000000000001a40d46000000000000000000000000000000000000000000000000000000000000000000000000000000590000000064d5c31200000000000000000000000000000000000000000000000000000000000000000000000001a3a4440000000000000000000000000000000000000000000000000000000000000000000000000000005a0000000064d5c31800000000000000000000000000000000000000000000000000000000000000000000000001a33b5c0000000000000000000000000000000000000000000000000000000000000000000000000000005b0000000064d5c31e00000000000000000000000000000000000000000000000000000000000000000000000001a2d28f0000000000000000000000000000000000000000000000000000000000000000000000000000005c0000000064d5c32400000000000000000000000000000000000000000000000000000000000000000000000001a269dc0000000000000000000000000000000000000000000000000000000000000000000000000000005d0000000064d5c32a00000000000000000000000000000000000000000000000000000000000000000000000001a201430000000000000000000000000000000000000000000000000000000000000000000000000000005e0000000064d5c33000000000000000000000000000000000000000000000000000000000000000000000000001a198c40000000000000000000000000000000000000000000000000000000000000000000000000000005f0000000064d5c33600000000000000000000000000000000000000000000000000000000000000000000000001a1305f000000000000000000000000000000000000000000000000000000000000000000000000000000600000000064d5c33c00000000000000000000000000000000000000000000000000000000000000000000000001a0c814000000000000000000000000000000000000000000000000000000000000000000000000000000610000000064d5c34200000000000000000000000000000000000000000000000000000000000000000000000001a05fe3000000000000000000000000000000000000000000000000000000000000000000000000000000620000000064d5c348000000000000000000000000000000000000000000000000000000000000000000000000019ff7cd000000000000000000000000000000000000000000000000000000000000000000000000000000630000000064d5c34e000000000000000000000000000000000000000000000000000000000000000000000000019f8fd1000000000000000000000000000000000000000000000000000000000000000000000000000000640000000064d5c355000000000000000000000000000000000000000000000000000000000000000000000000019f27ef000000000000000000000000000000000000000000000000000000000000000000000000000000650000000064d5c35b000000000000000000000000000000000000000000000000000000000000000000000000019ec0270000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000004000000000000000000000000000000000000000000000000000000000000000c0000000000000000000000000000000000000000000000000000000000000000100000000000000000000000000000000000000000000000000000000000000200000000000000000000000000000000000000000000000000000000000000014c3e1ce0d9c3a3da1b9cb04d133ad4e3bbe1fab7e000000000000000000000000000000000000000000000000000000000000000000000000000000000000008000000000000000000000000000000000050382b8fc794a9d02b01a05aab983d8bad95f36cc8f196fb0160aa0b1533ce8f8f3c7c375008ce701a75c7e788db8d8000000000000000000000000000000000c6a23cfbd5943b5afbdf504a5228ea920c2570140c8314310b712f65254efb8436aa6949100dea26055c12eb1e30e74"
	decodefirstInput, err := hexutil.Decode(firstInput)
	abi, err := bindings.ZKEVMMetaData.GetAbi()
	require.NoError(t, err)
	args, err := abi.Methods["submitBatches"].Inputs.Unpack(decodefirstInput[4:])
	require.NoError(t, err)
	// parse calldata to zkevm batch data
	fetchBatch := newRollupData(58, common.HexToHash("0x6f74f717059c77203c6518ab345f60757f3a9903f6331bb2c8ebcba02dab6735"), 1)
	d := testNewDerivationClient(t)
	err = d.parseArgs(args, fetchBatch, nil)
	require.NoError(t, err)
}
