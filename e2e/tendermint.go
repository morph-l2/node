package e2e

import (
	"fmt"
	"strings"
	"time"

	tmtypes "github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/blssignatures"
	cfg "github.com/tendermint/tendermint/config"
	"github.com/tendermint/tendermint/crypto/ed25519"
	"github.com/tendermint/tendermint/l2node"
	"github.com/tendermint/tendermint/libs/log"
	tmnet "github.com/tendermint/tendermint/libs/net"
	tmrand "github.com/tendermint/tendermint/libs/rand"
	nm "github.com/tendermint/tendermint/node"
	"github.com/tendermint/tendermint/p2p"
	"github.com/tendermint/tendermint/privval"
	"github.com/tendermint/tendermint/proxy"
	rpctest "github.com/tendermint/tendermint/rpc/test"
	"github.com/tendermint/tendermint/types"
	tdm "github.com/tendermint/tendermint/types"
)

type TdmConfigOption func(*cfg.Config)

func NewDefaultTendermintNode(l2Node l2node.L2Node, opts ...TdmConfigOption) (*nm.Node, error) {
	return NewTendermintNode(l2Node, nil, opts...)
}

func NewTendermintNode(l2Node l2node.L2Node, blsKey *blssignatures.FileBLSKey, opts ...TdmConfigOption) (*nm.Node, error) {
	// Create & start node
	config := rpctest.GetConfig(true)
	genDoc, err := tdm.GenesisDocFromFile(config.GenesisFile())
	if err != nil {
		return nil, err
	}
	genDoc.GenesisTime = time.Now()
	if err = genDoc.SaveAs(config.GenesisFile()); err != nil {
		return nil, err
	}

	if opts != nil {
		for _, opt := range opts {
			opt(config)
		}
	}
	pvKeyFile := config.PrivValidatorKeyFile()
	pvKeyStateFile := config.PrivValidatorStateFile()
	pv := privval.LoadOrGenFilePV(pvKeyFile, pvKeyStateFile)

	if blsKey == nil {
		blsKey = blssignatures.GenFileBLSKey()
	}
	blsPrivKey, err := blssignatures.PrivateKeyFromBytes(blsKey.PrivKey)
	if err != nil {
		return nil, err
	}
	nodeKey, err := p2p.LoadOrGenNodeKey(config.NodeKeyFile())
	if err != nil {
		return nil, err
	}

	logger := log.NewNopLogger()

	return nm.NewNode(
		config,
		l2Node,
		pv,
		&blsPrivKey,
		nodeKey,
		proxy.NewLocalClientCreator(tmtypes.NewBaseApplication()),
		nm.DefaultGenesisDocProviderFunc(config),
		nm.DefaultDBProvider,
		nm.DefaultMetricsProvider(config.Instrumentation),
		logger,
	)
}

func NewMultipleTendermintNodes(l2Nodes []l2node.L2Node, opts ...TdmConfigOption) ([]*nm.Node, error) {
	// preparation for this tendermint network
	num := len(l2Nodes)
	privKeys := make([]ed25519.PrivKey, num)
	nodeKeys := make([]*p2p.NodeKey, num)
	genVals := make([]types.GenesisValidator, num)
	p2pPorts := make([]int, num)

	for i := 0; i < num; i++ {
		privKey := ed25519.GenPrivKey()
		privKeys[i] = privKey
		nodeKeys[i] = &p2p.NodeKey{
			PrivKey: ed25519.GenPrivKey(),
		}
		privKey.PubKey()
		genVals[i] = types.GenesisValidator{
			Address: privKey.PubKey().Address(),
			PubKey:  privKey.PubKey(),
			Power:   1,
			Name:    fmt.Sprintf("validator%d", i),
		}

		port, err := tmnet.GetFreePort()
		if err != nil {
			panic(err)
		}
		p2pPorts[i] = port
	}
	// Generate genesis doc from generated validators
	genDoc := &types.GenesisDoc{
		ChainID:         "chain-" + tmrand.Str(6),
		ConsensusParams: types.DefaultConsensusParams(),
		GenesisTime:     time.Now(),
		InitialHeight:   0,
		Validators:      genVals,
	}

	tmNodes := make([]*nm.Node, num)
	for i, l2Node := range l2Nodes {
		tendermintNode, err := NewTendermintNode(l2Node, nil, func(config *cfg.Config) {
			// overwrite privValidatorKeyFile & privValidatorStateFile
			pv := privval.NewFilePV(privKeys[i], config.PrivValidatorKeyFile(), config.PrivValidatorStateFile())
			pv.Save()
			fmt.Println("Generated private validator", "keyFile", config.PrivValidatorKeyFile(),
				"stateFile", config.PrivValidatorStateFile())

			// overwrite nodeKeyFile
			if err := nodeKeys[i].SaveAs(config.NodeKeyFile()); err != nil {
				panic(err)
			}

			// overwrite genesisFile
			if err := genDoc.SaveAs(config.GenesisFile()); err != nil {
				panic(err)
			}

			// overwrite p2p listen address
			config.P2P.ListenAddress = fmt.Sprintf("tcp://127.0.0.1:%d", p2pPorts[i])

			// overwrite persistent peers
			persistentPeers := make([]string, 0) // concluded itself
			for nodeIndex, nodeKey := range nodeKeys {
				// skip if it is self nodeKey
				if i == nodeIndex {
					continue
				}
				persistentPeers = append(persistentPeers, p2p.IDAddressString(nodeKey.ID(), fmt.Sprintf("%s:%d", "127.0.0.1", p2pPorts[nodeIndex])))
				//persistentPeers[i] = p2p.IDAddressString(nodeKey.ID(), fmt.Sprintf("%s:%d", "127.0.0.1", p2pPorts[nodeIndex]))
			}

			config.P2P.PersistentPeers = strings.Join(persistentPeers, ",")

			for _, opt := range opts {
				opt(config)
			}
		})
		if err != nil {
			return nil, err
		}
		tmNodes[i] = tendermintNode
	}

	return tmNodes, nil

}
