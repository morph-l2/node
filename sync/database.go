package sync

type Database interface {
	Reader
	Writer
}

type Reader interface {
	ReadLatestSyncedL1Height() *uint64
	ReadL1MessagesInRange(start, end uint64) []L1Message
	ReadL1MessageByIndex(index uint64) *L1Message
}

type Writer interface {
	WriteLatestSyncedL1Height(latest uint64)
	WriteSyncedL1Messages(messages []L1Message, latest uint64) error
}
