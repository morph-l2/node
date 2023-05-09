package main

import (
	"context"
	"fmt"
	"github.com/bebop-labs/l2-node/types"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/bebop-labs/l2-node/db"
	"github.com/bebop-labs/l2-node/flags"
	"github.com/bebop-labs/l2-node/node"
	"github.com/bebop-labs/l2-node/sequencer"
	"github.com/bebop-labs/l2-node/sequencer/mock"
	"github.com/bebop-labs/l2-node/sync"
	"github.com/scroll-tech/go-ethereum/log"
	tmnode "github.com/tendermint/tendermint/node"
	"github.com/urfave/cli"
)

func main() {
	log.Root().SetHandler(
		log.LvlFilterHandler(
			log.LvlInfo,
			log.StreamHandler(os.Stdout, log.LogfmtFormat()),
		),
	)
	app := cli.NewApp()
	app.Flags = flags.Flags
	app.Name = "l2node"
	app.Action = L2NodeMain
	err := app.Run(os.Args)
	if err != nil {
		log.Crit("Application failed", "message", err)
	}
}

func L2NodeMain(ctx *cli.Context) error {
	log.Info("Initializing L2 Node")

	var (
		err      error
		executor *node.Executor
		syncer   *sync.Syncer
		ms       *mock.Sequencer
		tmNode   *tmnode.Node

		nodeConfig = node.DefaultConfig()
	)
	isSequencer := ctx.GlobalBool(flags.SequencerEnabled.Name)
	isMockSequencer := ctx.GlobalBool(flags.MockEnabled.Name)
	if isSequencer && isMockSequencer {
		return fmt.Errorf("the sequencer and mockSequencer can not be enabled both")
	}
	if err = nodeConfig.SetCliContext(ctx); err != nil {
		return err
	}
	if isSequencer {
		home, err := homeDir(ctx)
		if err != nil {
			return err
		}

		// configure store
		dbConfig := db.DefaultConfig()
		dbConfig.SetCliContext(ctx)
		store, err := db.NewStore(dbConfig, home)
		if err != nil {
			return err
		}

		// launch syncer
		syncConfig := sync.DefaultConfig()
		if err = syncConfig.SetCliContext(ctx); err != nil {
			return err
		}
		syncer, err = sync.NewSyncer(context.Background(), store, syncConfig)
		if err != nil {
			return fmt.Errorf("failed to create syncer, error: %v", err)
		}
		syncer.Start()

		// create executor
		executor, err = node.NewSequencerExecutor(nodeConfig, syncer)
		if err != nil {
			return fmt.Errorf("failed to create executor, error: %v", err)
		}

		// launch tendermint node
		tmNode, err = sequencer.SetupNode(ctx, home, executor)
		if err != nil {
			return fmt.Errorf("failed to setup consensus node, error: %v", err)
		}
		if err := tmNode.Start(); err != nil {
			return fmt.Errorf("failed to start consensus node, error: %v", err)
		}
	} else {
		executor, err = node.NewExecutor(nodeConfig)
		if err != nil {
			return fmt.Errorf("failed to create executor, error: %v", err)
		}
	}

	if isMockSequencer {
		ms, err = mock.NewSequencer(executor)
		if err != nil {
			return err
		}
		go ms.Start()
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
