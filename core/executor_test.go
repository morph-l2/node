package node

import (
	"context"
	"flag"
	tmlog "github.com/tendermint/tendermint/libs/log"
	"os"
	"testing"

	"github.com/morphism-labs/node/db"
	"github.com/morphism-labs/node/sync"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli"
)

func TestNewSequencerExecutor(t *testing.T) {
	//prepare context
	ctx := PrepareContext()

	//syncer
	syncConfig := sync.DefaultConfig()
	syncConfig.SetCliContext(ctx)
	store := db.NewMemoryStore()
	store.WriteLatestSyncedL1Height(100)
	syncer, err := sync.NewSyncer(context.Background(), store, syncConfig, tmlog.NewTMLogger(tmlog.NewSyncWriter(os.Stdout)))
	require.NotNil(t, syncer)
	require.NoError(t, err)
	//SequencerExecutor
	nodeConfig := DefaultConfig()
	nodeConfig.SetCliContext(ctx)
	executor, err := NewSequencerExecutor(nodeConfig, syncer)
	require.NotNil(t, executor)
	require.NoError(t, err)

}

func TestNewExecutor(t *testing.T) {
	//prepare context
	ctx := PrepareContext()
	//executor
	nodeConfig := DefaultConfig()
	nodeConfig.SetCliContext(ctx)
	executor, err := NewExecutor(nodeConfig)
	require.NotNil(t, executor)
	require.NoError(t, err)
}

func PrepareContext() *cli.Context {
	env := map[string]string{
		"l1.rpc":                   "https://arb1.arbitrum.io/rpc",
		"sync.depositContractAddr": "0xFd086bC7CD5C481DCC9C85ebE478A1C0b69FCbb9",
		"l2.engine":                "http://127.0.0.1:8551",
		"l2.eth":                   "http://127.0.0.1:8545",
		"l2.jwt-secret":            "../jwt-secret.txt",
	}
	flagSet := flag.NewFlagSet("testApp", flag.ContinueOnError)
	for k, v := range env {
		flagSet.String(k, v, "param")
		flagSet.Set(k, v)
	}
	ctx := cli.NewContext(nil, flagSet, nil)
	return ctx
}
