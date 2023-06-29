package e2e

import (
	tmtypes "github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/blssignatures"
	cfg "github.com/tendermint/tendermint/config"
	"github.com/tendermint/tendermint/l2node"
	"github.com/tendermint/tendermint/libs/log"
	nm "github.com/tendermint/tendermint/node"
	"github.com/tendermint/tendermint/p2p"
	"github.com/tendermint/tendermint/privval"
	"github.com/tendermint/tendermint/proxy"
	rpctest "github.com/tendermint/tendermint/rpc/test"
)

type TdmConfigOption func(*cfg.Config)

func NewDefaultTendermintNode(l2Node l2node.L2Node, opts ...TdmConfigOption) (*nm.Node, error) {
	return NewTendermintNode(l2Node, nil, opts...)
}

func NewTendermintNode(l2Node l2node.L2Node, blsKey *blssignatures.FileBLSKey, opts ...TdmConfigOption) (*nm.Node, error) {
	// Create & start node
	config := rpctest.GetConfig(true)
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
