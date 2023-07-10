package derivation

import (
	"context"
	"crypto/ecdsa"
	"math/big"
	"testing"

	"github.com/morphism-labs/node/db"
	"github.com/morphism-labs/node/types/bindings"
	"github.com/scroll-tech/go-ethereum/accounts/abi/bind"
	"github.com/scroll-tech/go-ethereum/accounts/abi/bind/backends"
	"github.com/scroll-tech/go-ethereum/common"
	"github.com/scroll-tech/go-ethereum/core"
	"github.com/scroll-tech/go-ethereum/core/rawdb"
	"github.com/scroll-tech/go-ethereum/crypto"
	"github.com/scroll-tech/go-ethereum/ethdb"
	"github.com/scroll-tech/go-ethereum/rpc"
	"github.com/stretchr/testify/require"
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

func newSimulatedBackend(key *ecdsa.PrivateKey) (*backends.SimulatedBackend, ethdb.Database) {
	var gasLimit uint64 = 9_000_000
	auth, _ := bind.NewKeyedTransactorWithChainID(key, big.NewInt(1337))
	genAlloc := make(core.GenesisAlloc)
	genAlloc[auth.From] = core.GenesisAccount{Balance: big.NewInt(9223372036854775807)}
	db := rawdb.NewMemoryDatabase()
	sim := backends.NewSimulatedBackendWithDatabase(db, genAlloc, gasLimit)
	return sim, db
}
