package sequencer

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/morphism-labs/node/core"
	"github.com/morphism-labs/node/flags"
	"github.com/spf13/viper"
	tmtypes "github.com/tendermint/tendermint/abci/types"
	"github.com/tendermint/tendermint/blssignatures"
	"github.com/tendermint/tendermint/config"
	tmflags "github.com/tendermint/tendermint/libs/cli/flags"
	tmlog "github.com/tendermint/tendermint/libs/log"
	tmos "github.com/tendermint/tendermint/libs/os"
	tmnode "github.com/tendermint/tendermint/node"
	"github.com/tendermint/tendermint/p2p"
	"github.com/tendermint/tendermint/privval"
	"github.com/tendermint/tendermint/proxy"
	"github.com/urfave/cli"
)

func SetupNode(ctx *cli.Context, home string, executor *node.Executor, logger tmlog.Logger) (*tmnode.Node, error) {
	var (
		tmCfg      *config.Config
		configPath = ctx.GlobalString(flags.TendermintConfigPath.Name)
	)
	if configPath == "" {
		if home == "" {
			return nil, fmt.Errorf("either Home or Config Path has to be provided")
		}
		configPath = filepath.Join(home, "config")
	}
	viper.AddConfigPath(configPath)
	viper.SetConfigName("config")
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
		logger = tmlog.NewTMJSONLogger(tmlog.NewSyncWriter(os.Stdout))
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

	if !tmos.FileExists(tmCfg.BLSKey) {
		blssignatures.GenFileBLSKey().Save(tmCfg.BLSKeyFile())
	}
	blsPrivKey, err := blssignatures.PrivateKeyFromBytes(blssignatures.LoadBLSKey(tmCfg.BLSKeyFile()).PrivKey)
	if err != nil {
		return nil, fmt.Errorf("failed to load bls priv key")
	}

	//var app types.Application
	n, err := tmnode.NewNode(
		tmCfg,
		executor,
		privval.LoadOrGenFilePV(tmCfg.PrivValidatorKeyFile(), tmCfg.PrivValidatorStateFile()),
		&blsPrivKey,
		nodeKey,
		proxy.NewLocalClientCreator(tmtypes.NewBaseApplication()),
		tmnode.DefaultGenesisDocProviderFunc(tmCfg),
		tmnode.DefaultDBProvider,
		tmnode.DefaultMetricsProvider(tmCfg.Instrumentation),
		nodeLogger,
	)
	return n, err
}
