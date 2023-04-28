package types

import "errors"

var (
	// ErrInvalidL1MessageOrder is returned if a block contains L1 messages in the wrong
	// order. Possible scenarios are: (1) L1 messages do not follow their QueueIndex order,
	// (2) the block skipped once or more L1 message, (3) L1 messages are not included in
	// a contiguous block at the front of the block.
	ErrInvalidL1MessageOrder = errors.New("invalid L1 message order")

	// ErrUnknownL1Message is returned if a block contains an L1 message that does not
	// match the corresponding message in the node's local database.
	ErrUnknownL1Message = errors.New("unknown L1 message")

	ErrNotConfirmedBlock = errors.New("l1 block has not been considered to be confirmed")

	ErrInvalidL1Message = errors.New("invalid L1 message")

	ErrWrongBlockNumber = errors.New("wrong block number")

	ErrMemoryDBNotFound = errors.New("not found")
)
