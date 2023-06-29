package e2e

import (
	"testing"

	"github.com/morphism-labs/node/sync"
	"github.com/stretchr/testify/require"
	"github.com/tendermint/tendermint/l2node"
)

func NewSequencer(t *testing.T, syncerDB sync.Database, systemConfigOption Option, gethOption GethOption) (Geth, l2node.L2Node) {
	sysConfig := NewSystemConfig(t, systemConfigOption)
	geth, err := NewGeth(sysConfig, gethOption)
	require.NoError(t, err)
	require.NoError(t, geth.Backend.StartMining(-1))

	node, err := NewSequencerNode(geth, sync.NewFakeSyncer(syncerDB))
	require.NoError(t, err)

	return geth, node
}
