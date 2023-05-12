package sync

import (
	"context"
	"errors"
	"time"

	"github.com/morphism-labs/node/types"
	"github.com/scroll-tech/go-ethereum/common"
	"github.com/scroll-tech/go-ethereum/ethclient"
	"github.com/scroll-tech/go-ethereum/log"
)

type Syncer struct {
	ctx          context.Context
	cancel       context.CancelFunc
	bridgeClient *BridgeClient
	latestSynced uint64
	db           Database

	fetchBlockRange     uint64
	pollInterval        time.Duration
	logProgressInterval time.Duration
	stop                chan struct{}
}

func NewSyncer(ctx context.Context, db Database, config *Config) (*Syncer, error) {
	l1Client, err := ethclient.Dial(config.L1.Addr)
	if err != nil {
		return nil, err
	}

	if config.DepositContractAddress == nil {
		return nil, errors.New("deposit contract address cannot be nil")
	}

	bridgeClient := NewBridgeClient(l1Client, *config.DepositContractAddress, config.L1.Confirmations)

	latestSynced := db.ReadLatestSyncedL1Height()
	if latestSynced == nil {
		if config.StartHeight == 0 {
			return nil, errors.New("sync start height cannot be nil")
		}
		h := config.StartHeight - 1
		latestSynced = &h
	}
	ctx, cancel := context.WithCancel(ctx)
	return &Syncer{
		ctx:          ctx,
		cancel:       cancel,
		bridgeClient: bridgeClient,
		latestSynced: *latestSynced,
		db:           db,
		stop:         make(chan struct{}),

		fetchBlockRange:     config.FetchBlockRange,
		pollInterval:        config.PollInterval,
		logProgressInterval: config.LogProgressInterval,
	}, nil
}

func (s *Syncer) Start() {
	// block node startup during initial sync and print some helpful logs
	log.Warn("Running initial sync of L1 messages before starting sequencer, this might take a while...")
	s.fetchL1Messages()
	log.Info("L1 message initial sync completed", "latestSyncedBlock", s.latestSynced)

	go func() {
		t := time.NewTicker(s.pollInterval)
		defer t.Stop()

		for {
			// don't wait for ticker during startup
			s.fetchL1Messages()

			select {
			case <-s.ctx.Done():
				close(s.stop)
				return
			case <-t.C:
				continue
			}
		}
	}()
}

func (s *Syncer) Stop() {
	if s == nil {
		return
	}

	log.Info("Stopping sync service")

	if s.cancel != nil {
		s.cancel()
	}
	<-s.stop
	log.Info("Sync service is stopped")
}

func (s *Syncer) fetchL1Messages() {
	latestConfirmed, err := s.bridgeClient.getLatestConfirmedBlockNumber(s.ctx)
	if err != nil {
		log.Warn("failed to get latest confirmed block number", "err", err)
		return
	}

	// ticker for logging progress
	t := time.NewTicker(s.logProgressInterval)
	numMessagesCollected := 0

	// query in batches
	for from := s.latestSynced + 1; from <= latestConfirmed; from += s.fetchBlockRange {
		select {
		case <-s.ctx.Done():
			return
		case <-t.C:
			progress := 100 * float64(s.latestSynced) / float64(latestConfirmed)
			log.Info("Syncing L1 messages", "synced", s.latestSynced, "confirmed", latestConfirmed, "collected", numMessagesCollected, "progress(%)", progress)
		default:
		}

		to := from + s.fetchBlockRange - 1

		if to > latestConfirmed {
			to = latestConfirmed
		}

		l1Messages, err := s.bridgeClient.L1Messages(s.ctx, from, to)
		if err != nil {
			log.Warn("failed to fetch L1 messages", "fromBlock", from, "toBlock", to, "err", err)
			return
		}

		if len(l1Messages) > 0 {
			log.Debug("Received new L1 events", "fromBlock", from, "toBlock", to, "count", len(l1Messages))
			if err = s.db.WriteSyncedL1Messages(l1Messages, to); err != nil {
				// crash on database error
				log.Crit("failed to write L1 messages to database", "err", err)
			}
			numMessagesCollected += len(l1Messages)
		} else {
			s.db.WriteLatestSyncedL1Height(to)
		}
		s.latestSynced = to
	}
}

func (s *Syncer) GetL1Message(index uint64, txHash common.Hash) (*types.L1Message, error) {
	l1Message := s.db.ReadL1MessageByIndex(index)
	if l1Message != nil {
		return l1Message, nil
	}

	l1Messages, err := s.bridgeClient.L1MessagesFromTxHash(s.ctx, txHash)
	if err != nil {
		return nil, err
	}

	for _, msg := range l1Messages {
		if msg.QueueIndex == index {
			return &msg, nil
		}
	}
	return nil, nil
}

func (s *Syncer) ReadL1MessagesInRange(start, end uint64) []types.L1Message {
	return s.db.ReadL1MessagesInRange(start, end)
}
