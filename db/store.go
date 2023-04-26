package db

import (
	"math/big"
	"path/filepath"

	"github.com/bebop-labs/l2-node/sync"
	"github.com/scroll-tech/go-ethereum/core/rawdb"
	"github.com/scroll-tech/go-ethereum/ethdb"
	"github.com/scroll-tech/go-ethereum/log"
	"github.com/scroll-tech/go-ethereum/rlp"
	"github.com/syndtr/goleveldb/leveldb/errors"
)

type Store struct {
	db ethdb.Database
}

func NewStore(config *Config) (*Store, error) {
	var db ethdb.Database
	var err error
	if config.FileName == "" {
		db = rawdb.NewMemoryDatabase()
	} else {
		freezer := config.DatabaseFreezer
		if config.DatabaseFreezer == "" {
			freezer = filepath.Join(config.FileName, "ancient")
		}
		db, err = rawdb.NewLevelDBDatabaseWithFreezer(config.FileName, config.DatabaseCache, config.DatabaseHandles, freezer, config.Namespace, false)
		if err != nil {
			return nil, err
		}
	}
	return &Store{
		db: db,
	}, nil
}

func (s *Store) ReadLatestSyncedL1Height() *uint64 {
	data, err := s.db.Get(syncedL1HeightKey)
	if err != nil && err != errors.ErrNotFound {
		log.Crit("Failed to read synced L1 block number from database", "err", err)
	}
	if len(data) == 0 {
		return nil
	}

	number := new(big.Int).SetBytes(data)
	if !number.IsUint64() {
		log.Crit("Unexpected synced L1 block number in database", "number", number)
	}

	value := number.Uint64()
	return &value
}

func (s *Store) ReadL1MessagesInRange(start, end uint64) []sync.L1Message {
	if start > end {
		return nil
	}
	expectedCount := start - end + 1
	messages := make([]sync.L1Message, 0, expectedCount)
	it := IterateL1MessagesFrom(s.db, start)
	defer it.Release()

	for it.Next() {
		if it.EnqueueIndex() > end {
			break
		}
		messages = append(messages, it.L1Message())
	}

	return messages
}

func (s *Store) ReadL1MessageByIndex(index uint64) *sync.L1Message {
	data, err := s.db.Get(L1MessageKey(index))
	if err != nil && err != errors.ErrNotFound {
		log.Crit("Failed to read L1 message from database", "err", err)
	}
	if len(data) == 0 {
		return nil
	}
	var l1Msg sync.L1Message
	if err := rlp.DecodeBytes(data, &l1Msg); err != nil {
		log.Crit("Invalid L1 message RLP", "data", data, "err", err)
	}
	return &l1Msg

}

func (s *Store) WriteLatestSyncedL1Height(latest uint64) {
	if err := s.db.Put(syncedL1HeightKey, new(big.Int).SetUint64(latest).Bytes()); err != nil {
		log.Crit("Failed to update synced L1 height", "err", err)
	}
}

func (s *Store) WriteSyncedL1Messages(messages []sync.L1Message, latest uint64) error {
	if len(messages) == 0 {
		return nil
	}
	batch := s.db.NewBatch()
	for _, msg := range messages {
		bytes, err := rlp.EncodeToBytes(msg)
		if err != nil {
			log.Crit("Failed to RLP encode L1 message", "err", err)
		}
		enqueueIndex := msg.QueueIndex
		if err := batch.Put(L1MessageKey(enqueueIndex), bytes); err != nil {
			log.Crit("Failed to store L1 message", "err", err)
		}
		latest = msg.L1Height
	}
	if err := batch.Put(syncedL1HeightKey, new(big.Int).SetUint64(latest).Bytes()); err != nil {
		log.Crit("Failed to update synced L1 height", "err", err)
	}
	return batch.Write()
}
