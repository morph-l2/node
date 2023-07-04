package e2e

import (
	"errors"
	node "github.com/morphism-labs/node/core"
	"github.com/morphism-labs/node/sync"
	"github.com/tendermint/tendermint/l2node"
	"github.com/tendermint/tendermint/types"
	tdm "github.com/tendermint/tendermint/types"
)

func NewSequencerNode(geth Geth, syncer *sync.Syncer) (l2node.L2Node, error) {
	nodeConfig := node.DefaultConfig()
	nodeConfig.L2.EthAddr = geth.Node.HTTPEndpoint()
	nodeConfig.L2.EngineAddr = geth.Node.HTTPAuthEndpoint()
	nodeConfig.L2.JwtSecret = testingJWTSecret
	return node.NewSequencerExecutor(nodeConfig, syncer)
}

func ManualCreateBlock(node l2node.L2Node, blockNumber int64) error {
	txs, restBytes, blsBytes, err := node.RequestBlockData(blockNumber)
	if err != nil {
		return err
	}
	valid, err := node.CheckBlockData(txs, restBytes, blsBytes)
	if err != nil {
		return err
	}
	if !valid {
		return errors.New("check block data false")
	}
	return node.DeliverBlock(txs, restBytes, blsBytes, nil, nil)
}

/**
 * Custom node, customize the actions to test different cases
 */

type CustomNode struct {
	origin l2node.L2Node

	CustomFuncRequestBlockData FuncRequestBlockData
	CustomFuncCheckBlockData   FuncCheckBlockData
	CustomFuncDeliverBlock     FuncDeliverBlock
}

type FuncRequestBlockData func(height int64) (txs [][]byte, l2Config []byte, zkConfig []byte, err error)
type FuncCheckBlockData func(txs [][]byte, l2Config []byte, zkConfig []byte) (valid bool, err error)
type FuncDeliverBlock func(txs [][]byte, l2Config []byte, zkConfig []byte, validators []types.Address, blsSignatures [][]byte) (err error)

func NewCustomNode(origin l2node.L2Node) *CustomNode {
	return &CustomNode{
		origin: origin,
	}
}
func (cn *CustomNode) WithCustomRequestBlockData(rbdFunc FuncRequestBlockData) *CustomNode {
	cn.CustomFuncRequestBlockData = rbdFunc
	return cn
}
func (cn *CustomNode) WithCustomFuncCheckBlockData(cbdFunc FuncCheckBlockData) *CustomNode {
	cn.CustomFuncCheckBlockData = cbdFunc
	return cn
}
func (cn *CustomNode) WithCustomFuncDeliverBlock(dbFunc FuncDeliverBlock) *CustomNode {
	cn.CustomFuncDeliverBlock = dbFunc
	return cn
}

func (cn *CustomNode) RequestBlockData(height int64) (txs [][]byte, l2Config, zkConfig []byte, err error) {
	if cn.CustomFuncRequestBlockData != nil {
		return cn.CustomFuncRequestBlockData(height)
	}
	return cn.origin.RequestBlockData(height)
}

func (cn *CustomNode) CheckBlockData(txs [][]byte, l2Config, zkConfig []byte) (valid bool, err error) {
	if cn.CustomFuncCheckBlockData != nil {
		return cn.CustomFuncCheckBlockData(txs, l2Config, zkConfig)
	}
	return cn.origin.CheckBlockData(txs, l2Config, zkConfig)
}

func (cn *CustomNode) DeliverBlock(txs [][]byte, l2Config, zkConfig []byte, validators []tdm.Address, blsSignatures [][]byte) error {
	if cn.CustomFuncDeliverBlock != nil {
		return cn.CustomFuncDeliverBlock(txs, l2Config, zkConfig, validators, blsSignatures)
	}
	return cn.origin.DeliverBlock(txs, l2Config, zkConfig, validators, blsSignatures)
}
