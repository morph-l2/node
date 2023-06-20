package derivation

import (
	"errors"
	"github.com/morphism-labs/node/flags"
	"github.com/morphism-labs/node/types"
	"github.com/scroll-tech/go-ethereum/common"
	"github.com/scroll-tech/go-ethereum/rpc"
	"github.com/urfave/cli"
	"time"
)

const (
	// DefaultFetchBlockRange is the number of blocks that we collect in a single eth_getLogs query.
	DefaultFetchBlockRange = uint64(100)

	// DefaultPollInterval is the frequency at which we query for new L1 messages.
	DefaultPollInterval = time.Second * 15

	// DefaultLogProgressInterval is the frequency at which we log progress.
	DefaultLogProgressInterval = time.Second * 10
)

type Config struct {
	L1                   *types.L1Config `json:"l1"`
	L2                   *types.L2Config `json:"l2"`
	ZKEvmContractAddress *common.Address `json:"deposit_contract_address"`
	StartHeight          uint64          `json:"start_height"`
	PollInterval         time.Duration   `json:"poll_interval"`
	LogProgressInterval  time.Duration   `json:"log_progress_interval"`
	FetchBlockRange      uint64          `json:"fetch_block_range"`
}

func DefaultConfig() *Config {
	return &Config{
		L1: &types.L1Config{
			Confirmations: rpc.FinalizedBlockNumber,
		},
		PollInterval:        DefaultPollInterval,
		LogProgressInterval: DefaultLogProgressInterval,
		FetchBlockRange:     DefaultFetchBlockRange,
	}
}

func (c *Config) SetCliContext(ctx *cli.Context) error {
	c.L1.Addr = ctx.GlobalString(flags.L1NodeAddr.Name)
	if ctx.GlobalIsSet(flags.L1Confirmations.Name) {
		c.L1.Confirmations = rpc.BlockNumber(ctx.GlobalInt64(flags.L1Confirmations.Name))
	}

	if ctx.GlobalIsSet(flags.ZKEvmContractAddress.Name) {
		addr := common.HexToAddress(ctx.GlobalString(flags.ZKEvmContractAddress.Name))
		c.ZKEvmContractAddress = &addr
		if len(c.ZKEvmContractAddress.Bytes()) == 0 {
			return errors.New("invalid DerivationDepositContractAddr")
		}
	}

	if ctx.GlobalIsSet(flags.DerivationStartHeight.Name) {
		c.StartHeight = ctx.GlobalUint64(flags.DerivationStartHeight.Name)
		if c.StartHeight == 0 {
			return errors.New("invalid DerivationStartHeight")
		}
	}

	if ctx.GlobalIsSet(flags.DerivationPollInterval.Name) {
		c.PollInterval = ctx.GlobalDuration(flags.DerivationPollInterval.Name)
		if c.PollInterval == 0 {
			return errors.New("invalid pollInterval")
		}
	}
	if ctx.GlobalIsSet(flags.DerivationLogProgressInterval.Name) {
		c.LogProgressInterval = ctx.GlobalDuration(flags.DerivationLogProgressInterval.Name)
		if c.LogProgressInterval == 0 {
			return errors.New("invalid logProgressInterval")
		}
	}
	if ctx.GlobalIsSet(flags.DerivationFetchBlockRange.Name) {
		c.FetchBlockRange = ctx.GlobalUint64(flags.DerivationFetchBlockRange.Name)
		if c.FetchBlockRange == 0 {
			return errors.New("invalid fetchBlockRange")
		}
	}

	return nil
}
