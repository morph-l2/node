package sequencer

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/bebop-labs/l2-node/flags"
	"github.com/bebop-labs/l2-node/node"
	"github.com/spf13/viper"
	"github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/blssignatures"
	"github.com/tendermint/tendermint/config"
	tmflags "github.com/tendermint/tendermint/libs/cli/flags"
	"github.com/tendermint/tendermint/libs/log"
	tmnode "github.com/tendermint/tendermint/node"
	"github.com/tendermint/tendermint/p2p"
	"github.com/tendermint/tendermint/privval"
	"github.com/tendermint/tendermint/proxy"
	"github.com/urfave/cli"
)

var logger = log.NewTMLogger(log.NewSyncWriter(os.Stdout))

func SetupNode(ctx *cli.Context, home string, executor *node.Executor) (*tmnode.Node, error) {
	var (
		tmCfg      *config.Config
		configPath = ctx.GlobalString(flags.TendermintConfigPathFlag.Name)
	)
	if configPath == "" {
		if home == "" {
			return nil, fmt.Errorf("either Home or Config Path has to be provided")
		}
		configPath = filepath.Join(home, "config")
	}
	viper.AddConfigPath(configPath)
	viper.SetConfigName("tm-config")
	if err := viper.ReadInConfig(); err != nil {
		return nil, err
	}
	tmCfg = config.DefaultConfig()

	if err := viper.Unmarshal(tmCfg); err != nil {
		return nil, err
	}

	tmCfg.SetRoot(home)
	if err := tmCfg.ValidateBasic(); err != nil {
		return nil, fmt.Errorf("error in config file: %w", err)
	}

	if tmCfg.LogFormat == config.LogFormatJSON {
		logger = log.NewTMJSONLogger(log.NewSyncWriter(os.Stdout))
	}
	nodeLogger, err := tmflags.ParseLogLevel(tmCfg.LogLevel, logger, config.DefaultLogLevel)
	if err != nil {
		return nil, err
	}

	nodeLogger = nodeLogger.With("module", "main")
	nodeKey, err := p2p.LoadOrGenNodeKey(tmCfg.NodeKeyFile())
	if err != nil {
		return nil, err
	}

	blsPrivKey, err := blssignatures.PrivateKeyFromBytes(blssignatures.LoadBLSKey(tmCfg.BLSKeyFile()).PrivKey)
	if err != nil {
		return nil, fmt.Errorf("failed to load bls priv key")
	}

	var app types.Application
	n, err := tmnode.NewNode(
		tmCfg,
		privval.LoadOrGenFilePV(tmCfg.PrivValidatorKeyFile(), tmCfg.PrivValidatorStateFile()),
		&blsPrivKey,
		nodeKey,
		proxy.NewLocalClientCreator(app),
		tmnode.DefaultGenesisDocProviderFunc(tmCfg),
		tmnode.DefaultDBProvider,
		tmnode.DefaultMetricsProvider(tmCfg.Instrumentation),
		nodeLogger,
	)
	return n, err
}
