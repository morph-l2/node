package validator

import (
	"crypto/ecdsa"
	"math/big"

	"github.com/morphism-labs/node/flags"
	"github.com/scroll-tech/go-ethereum/common"
	"github.com/scroll-tech/go-ethereum/crypto"
	"github.com/urfave/cli"
)

type Config struct {
	l1RPC           string
	PrivateKey      *ecdsa.PrivateKey
	L1ChainID       *big.Int
	zkEvmContract   *common.Address
	challengeEnable bool
}

func NewConfig() *Config {
	return &Config{}
}

func (c *Config) SetCliContext(ctx *cli.Context) error {
	l1NodeAddr := ctx.GlobalString(flags.L1NodeAddr.Name)
	l1ChainID := ctx.GlobalUint64(flags.L1ChainID.Name)
	hex := ctx.GlobalString(flags.ValidatorPrivateKey.Name)
	privateKey, err := crypto.HexToECDSA(hex)
	if err != nil {
		return err
	}
	c.challengeEnable = ctx.GlobalIsSet(flags.ValidatorEnable.Name)
	addrHex := ctx.GlobalString(flags.ZKEvmContractAddress.Name)
	zkEvmContract := common.HexToAddress(addrHex)
	c.l1RPC = l1NodeAddr
	c.L1ChainID = big.NewInt(int64(l1ChainID))
	c.PrivateKey = privateKey
	c.zkEvmContract = &zkEvmContract
	return nil
}