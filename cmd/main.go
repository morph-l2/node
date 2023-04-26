package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/bebop-labs/l2-node/db"
	"github.com/bebop-labs/l2-node/flags"
	"github.com/bebop-labs/l2-node/mock"
	"github.com/bebop-labs/l2-node/node"
	"github.com/bebop-labs/l2-node/sync"
	"github.com/scroll-tech/go-ethereum/log"
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
	app.Name = "l2-node"
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

		nodeConfig = node.DefaultConfig()
	)
	sequencer := ctx.GlobalBool(flags.SequencerEnabledFlag.Name)
	mockSequencer := ctx.GlobalBool(flags.MockEnabledFlag.Name)
	if sequencer && mockSequencer {
		return fmt.Errorf("the sequencer and mockSequencer can not be enabled both")
	}
	if err = nodeConfig.SetCliContext(ctx); err != nil {
		return err
	}
	if sequencer {
		dbConfig := db.DefaultConfig()
		dbConfig.SetCliContext(ctx)
		store, err := db.NewStore(dbConfig)
		if err != nil {
			return err
		}

		syncConfig := sync.DefaultConfig()
		if err = syncConfig.SetCliContext(ctx); err != nil {
			return err
		}
		syncer, err = sync.NewSyncer(context.Background(), store, syncConfig)
		if err != nil {
			return fmt.Errorf("failed to create syncer, error: %v", err)
		}
		syncer.Start()

		executor, err = node.NewSequencerExecutor(nodeConfig, syncer)
		if err != nil {
			return fmt.Errorf("failed to create executor, error: %v", err)
		}
	} else {
		executor, err = node.NewExecutor(nodeConfig)
		if err != nil {
			return fmt.Errorf("failed to create executor, error: %v", err)
		}
	}

	if mockSequencer {
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

	if syncer != nil {
		syncer.Stop()
	}
	if ms != nil {
		ms.Stop()
	}

	return nil
}
