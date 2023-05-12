package node

import (
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/morphism-labs/node/flags"
	"github.com/morphism-labs/node/types"
	"github.com/scroll-tech/go-ethereum/common"
	"github.com/scroll-tech/go-ethereum/common/hexutil"
	"github.com/scroll-tech/go-ethereum/log"
	"github.com/urfave/cli"
)

type Config struct {
	L2                      *types.L2Config `json:"l2"`
	MaxL1MessageNumPerBlock uint64          `json:"max_l1_message_num_per_block"`
}

func DefaultConfig() *Config {
	return &Config{
		L2:                      new(types.L2Config),
		MaxL1MessageNumPerBlock: 100,
	}
}

func (c *Config) SetCliContext(ctx *cli.Context) error {
	l2EthAddr := ctx.GlobalString(flags.L2EthAddr.Name)
	l2EngineAddr := ctx.GlobalString(flags.L2EngineAddr.Name)
	fileName := ctx.GlobalString(flags.L2EngineJWTSecret.Name)
	var secret [32]byte
	fileName = strings.TrimSpace(fileName)
	if fileName == "" {
		return fmt.Errorf("file-name of jwt secret is empty")
	}
	if data, err := os.ReadFile(fileName); err == nil {
		jwtSecret := common.FromHex(strings.TrimSpace(string(data)))
		if len(jwtSecret) != 32 {
			return fmt.Errorf("invalid jwt secret in path %s, not 32 hex-formatted bytes", fileName)
		}
		copy(secret[:], jwtSecret)
	} else {
		log.Warn("Failed to read JWT secret from file, generating a new one now. Configure L2 geth with --authrpc.jwt-secret=" + fmt.Sprintf("%q", fileName))
		if _, err := io.ReadFull(rand.Reader, secret[:]); err != nil {
			return fmt.Errorf("failed to generate jwt secret: %w", err)
		}
		if err := os.WriteFile(fileName, []byte(hexutil.Encode(secret[:])), 0600); err != nil {
			return err
		}
	}
	c.L2.EthAddr = l2EthAddr
	c.L2.EngineAddr = l2EngineAddr
	c.L2.JwtSecret = secret

	if ctx.GlobalIsSet(flags.MaxL1MessageNumPerBlock.Name) {
		c.MaxL1MessageNumPerBlock = ctx.GlobalUint64(flags.MaxL1MessageNumPerBlock.Name)
		if c.MaxL1MessageNumPerBlock == 0 {
			return fmt.Errorf("MaxL1MessageNumPerBlock must be above 0")
		}
	}
	return nil
}
