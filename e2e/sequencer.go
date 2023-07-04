package e2e

import (
	"github.com/morphism-labs/node/db"
	"github.com/scroll-tech/go-ethereum/eth/ethconfig"
	"github.com/scroll-tech/go-ethereum/node"
	"github.com/scroll-tech/go-ethereum/p2p"
	"github.com/scroll-tech/go-ethereum/p2p/enode"
	"testing"
	"time"

	"github.com/morphism-labs/node/sync"
	"github.com/stretchr/testify/require"
	"github.com/tendermint/tendermint/l2node"
)

func NewGethAndNode(t *testing.T, syncerDB sync.Database, systemConfigOption Option, gethOption GethOption) (Geth, l2node.L2Node) {
	sysConfig := NewSystemConfig(t, systemConfigOption)
	geth, err := NewGeth(sysConfig, gethOption)
	require.NoError(t, err)
	require.NoError(t, geth.Backend.StartMining(-1))

	node, err := NewSequencerNode(geth, sync.NewFakeSyncer(syncerDB))
	require.NoError(t, err)

	return geth, node
}

type GethAndNode struct {
	Geth Geth
	Node l2node.L2Node
}

func NewMultipleGethNodes(t *testing.T, nodesNum int) ([]l2node.L2Node, []Geth) {
	l2Nodes := make([]l2node.L2Node, nodesNum)
	geths := make([]Geth, nodesNum)
	for i := 0; i < nodesNum; i++ {
		geth, node := NewGethAndNode(t, db.NewMemoryStore(), nil, func(_ *ethconfig.Config, n *node.Config) error {
			n.P2P = p2p.Config{
				ListenAddr: ":0",
				MaxPeers:   10,
			}
			return nil
		})
		geths[i] = geth
		l2Nodes[i] = node
	}
	geth0 := geths[0]
	for i, geth := range geths {
		if i == 0 {
			continue
		}
		ethNode, err := enode.Parse(enode.ValidSchemes, geth.Node.Server().NodeInfo().Enode)
		require.NoError(t, err)
		geth0.Node.Server().AddPeer(ethNode)
	}

	peersEstablishTimeout := time.NewTimer(3 * time.Second)

loop:
	for {
		select {
		case <-peersEstablishTimeout.C:
			require.FailNow(t, "timeout to connect to peers")
		default:
			if len(geth0.Node.Server().Peers()) == nodesNum-1 {
				break loop
			}
		}
	}
	return l2Nodes, geths
}
