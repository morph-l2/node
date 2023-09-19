package main

import (
	"context"
	"fmt"
	"github.com/tendermint/tendermint/privval"

	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/morphism-labs/morphism-bindings/bindings"
	"github.com/morphism-labs/node/core"
	"github.com/morphism-labs/node/db"
	"github.com/morphism-labs/node/derivation"
	"github.com/morphism-labs/node/flags"
	"github.com/morphism-labs/node/sequencer"
	"github.com/morphism-labs/node/sequencer/mock"
	"github.com/morphism-labs/node/sync"
	"github.com/morphism-labs/node/types"
	"github.com/morphism-labs/node/validator"
	"github.com/scroll-tech/go-ethereum/ethclient"
	tmnode "github.com/tendermint/tendermint/node"
	"github.com/urfave/cli"
)

func main() {
	app := cli.NewApp()
	app.Flags = flags.Flags
	app.Name = "morphnode"
	app.Action = L2NodeMain
	err := app.Run(os.Args)
	if err != nil {
		fmt.Println("Application failed, message: ", err)
		os.Exit(1)
	}
}

func L2NodeMain(ctx *cli.Context) error {
	var (
		err      error
		executor *node.Executor
		syncer   *sync.Syncer
		ms       *mock.Sequencer
		tmNode   *tmnode.Node
		dvNode   *derivation.Derivation

		nodeConfig = node.DefaultConfig()
	)
	isMockSequencer := ctx.GlobalBool(flags.MockEnabled.Name)
	isValidator := ctx.GlobalBool(flags.ValidatorEnable.Name)

	if err = nodeConfig.SetCliContext(ctx); err != nil {
		return err
	}
	home, err := homeDir(ctx)
	if err != nil {
		return err
	}

	if isValidator {
		// configure store
		dbConfig := db.DefaultConfig()
		dbConfig.SetCliContext(ctx)
		store, err := db.NewStore(dbConfig, home)
		if err != nil {
			return err
		}
		derivationCfg := derivation.DefaultConfig()
		if err := derivationCfg.SetCliContext(ctx); err != nil {
			return fmt.Errorf("derivation set cli context error: %v", err)
		}
		validatorCfg := validator.NewConfig()
		if err := validatorCfg.SetCliContext(ctx); err != nil {
			return fmt.Errorf("validator set cli context error: %v", err)
		}
		l1Client, err := ethclient.Dial(derivationCfg.L1.Addr)
		if err != nil {
			return fmt.Errorf("dial l1 node error:%v", err)
		}
		rollup, err := bindings.NewRollup(derivationCfg.RollupContractAddress, l1Client)
		if err != nil {
			return fmt.Errorf("NewRollup error:%v", err)
		}
		vt, err := validator.NewValidator(validatorCfg, rollup, nodeConfig.Logger)
		if err != nil {
			return fmt.Errorf("new validator client error: %v", err)
		}

		dvNode, err = derivation.NewDerivationClient(context.Background(), derivationCfg, store, vt, rollup, nodeConfig.Logger)
		if err != nil {
			return fmt.Errorf("new derivation client error: %v", err)
		}
		dvNode.Start()
		nodeConfig.Logger.Info("derivation node starting")
	} else {
		// launch tendermint node
		tmCfg, err := sequencer.LoadTmConfig(ctx, home)
		if err != nil {
			return err
		}
		tmVal := privval.LoadOrGenFilePV(tmCfg.PrivValidatorKeyFile(), tmCfg.PrivValidatorStateFile())
		pubKey, err := tmVal.GetPubKey()
		if err != nil {

		}
		executor, err = node.NewExecutor(ctx, home, nodeConfig, pubKey)
		if isMockSequencer {
			ms, err = mock.NewSequencer(executor)
			if err != nil {
				return err
			}
			go ms.Start()
		} else {
			if tmNode, err = sequencer.SetupNode(tmCfg, tmVal, executor, nodeConfig.Logger); err != nil {
				return fmt.Errorf("failed to setup consensus node, error: %v", err)
			}
			if err = tmNode.Start(); err != nil {
				return fmt.Errorf("failed to start consensus node, error: %v", err)
			}
		}
	}

	interruptChannel := make(chan os.Signal, 1)
	signal.Notify(interruptChannel, []os.Signal{
		os.Interrupt,
		os.Kill,
		syscall.SIGTERM,
		syscall.SIGQUIT,
	}...)
	<-interruptChannel

	if ms != nil {
		ms.Stop()
	}
	if tmNode != nil {
		tmNode.Stop()
	}
	if syncer != nil {
		syncer.Stop()
	}
	if dvNode != nil {
		dvNode.Stop()
	}

	return nil
}

func homeDir(ctx *cli.Context) (string, error) {
	home := ctx.GlobalString(flags.Home.Name)
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		home = filepath.Join(userHome, types.DefaultHomeDir)
	}
	return home, nil
}
