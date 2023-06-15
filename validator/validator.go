package validator

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"math/big"
	"time"

	"github.com/morphism-labs/node/types/bindings"
	"github.com/scroll-tech/go-ethereum"
	"github.com/scroll-tech/go-ethereum/accounts/abi/bind"
	ethtypes "github.com/scroll-tech/go-ethereum/core/types"
	"github.com/scroll-tech/go-ethereum/ethclient"
	"github.com/scroll-tech/go-ethereum/log"
)

type Validator struct {
	cli        DeployContractBackend
	privateKey *ecdsa.PrivateKey
	l1ChainID  *big.Int
	contract   *bindings.ZKEVMTransactor
}

type DeployContractBackend interface {
	bind.DeployBackend
	bind.ContractBackend
}

func NewValidator(cfg *Config) (*Validator, error) {
	cli, err := ethclient.Dial(cfg.l1RPC)
	if err != nil {
		return nil, err
	}
	zkEVMTransactor, err := bindings.NewZKEVMTransactor(*cfg.zkEvmContract, cli)
	if err != nil {
		return nil, err
	}
	return &Validator{
		cli:        cli,
		contract:   zkEVMTransactor,
		privateKey: cfg.PrivateKey,
		l1ChainID:  cfg.L1ChainID,
	}, nil
}

func (v *Validator) ChallengeState(batchIndex uint64) error {
	opts, err := bind.NewKeyedTransactorWithChainID(v.privateKey, v.l1ChainID)
	if err != nil {
		return err
	}
	gasPrice, err := v.cli.SuggestGasPrice(opts.Context)
	if err != nil {
		return err
	}
	opts.GasPrice = gasPrice
	tx, err := v.contract.ChallengeState(opts, batchIndex)
	if err != nil {
		return err
	}
	log.Info("send ChallengeState transaction ", "txHash", tx.Hash().Hex())
	if err := v.cli.SendTransaction(context.Background(), tx); err != nil {
		return err
	}
	// Wait for the receipt
	receipt, err := waitForReceipt(v.cli, tx)
	if err != nil {
		return err
	}
	log.Info("L1 transaction confirmed", "hash", tx.Hash().Hex(),
		"gas-used", receipt.GasUsed, "blocknumber", receipt.BlockNumber)
	return nil
}

func waitForReceipt(backend DeployContractBackend, tx *ethtypes.Transaction) (*ethtypes.Receipt, error) {
	t := time.NewTicker(300 * time.Millisecond)
	receipt := new(ethtypes.Receipt)
	var err error
	for range t.C {
		receipt, err = backend.TransactionReceipt(context.Background(), tx.Hash())
		if errors.Is(err, ethereum.NotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if receipt != nil {
			t.Stop()
			break
		}
	}
	return receipt, nil
}
