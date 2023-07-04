package e2e

import (
	"context"
	"fmt"
	"github.com/scroll-tech/go-ethereum/accounts/abi"
	"github.com/scroll-tech/go-ethereum/accounts/abi/bind"
	"github.com/scroll-tech/go-ethereum/common"
	"github.com/scroll-tech/go-ethereum/core/types"
	"github.com/scroll-tech/go-ethereum/eth"
	"github.com/scroll-tech/go-ethereum/eth/catalyst"
	"github.com/scroll-tech/go-ethereum/eth/ethconfig"
	"github.com/scroll-tech/go-ethereum/eth/tracers"
	"github.com/scroll-tech/go-ethereum/ethclient"
	"github.com/scroll-tech/go-ethereum/ethclient/authclient"
	"github.com/scroll-tech/go-ethereum/node"
)

type GethOption func(*ethconfig.Config, *node.Config) error

type Geth struct {
	Node       *node.Node
	Backend    *eth.Ethereum
	AuthClient *authclient.Client
	EthClient  *ethclient.Client
}

func NewGeth(systemConfig *SystemConfig, opts ...GethOption) (Geth, error) {
	ethConfig := ethconfig.Defaults
	ethConfig.NetworkId = systemConfig.Genesis.Config.ChainID.Uint64()
	ethConfig.Genesis = systemConfig.Genesis

	nodeConfig := node.Config{
		Name:        "morph-geth",
		WSHost:      "127.0.0.1",
		WSPort:      0,
		AuthAddr:    "127.0.0.1",
		AuthPort:    0,
		HTTPHost:    "127.0.0.1",
		HTTPPort:    0,
		WSModules:   []string{"debug", "admin", "eth", "txpool", "net", "rpc", "web3", "personal", "engine", "scroll"},
		HTTPModules: []string{"debug", "admin", "eth", "txpool", "net", "rpc", "web3", "personal", "engine", "scroll"},
		JWTSecret:   systemConfig.JwtPath,
	}

	for _, opt := range opts {
		if opt != nil {
			if err := opt(&ethConfig, &nodeConfig); err != nil {
				return Geth{}, err
			}
		}
	}

	stack, err := node.New(&nodeConfig)
	if err != nil {
		_ = stack.Close()
		return Geth{}, err
	}

	backend, err := eth.New(stack, &ethConfig)
	if err != nil {
		_ = stack.Close()
		return Geth{}, err
	}
	if eb, err := backend.Etherbase(); err != nil || eb == (common.Address{}) {
		backend.SetEtherbase(defaultEtherbase)
	}

	if err = catalyst.RegisterL2Engine(stack, backend); err != nil {
		return Geth{}, fmt.Errorf("failed to register the Engine API service: %v", err)
	}
	stack.RegisterAPIs(tracers.APIs(backend.APIBackend))

	if err := stack.Start(); err != nil {
		return Geth{}, err
	}

	authClient, err := authclient.DialContext(context.Background(), stack.HTTPAuthEndpoint(), testingJWTSecret)
	if err != nil {
		return Geth{}, err
	}

	ethClient, err := ethclient.Dial(stack.HTTPEndpoint())
	if err != nil {
		return Geth{}, err
	}
	return Geth{
		Node:       stack,
		Backend:    backend,
		AuthClient: authClient,
		EthClient:  ethClient,
	}, nil
}

func (geth Geth) Transfer(opts *bind.TransactOpts, to common.Address) (*types.Transaction, error) {
	bc := bind.NewBoundContract(to, abi.ABI{}, geth.EthClient, geth.EthClient, nil)
	opts.GasLimit = 21000
	return bc.Transfer(opts)
}
