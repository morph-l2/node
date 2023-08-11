package derivation

import "github.com/morphism-labs/node/types"

type Database interface {
	Reader
	Writer
}

type Reader interface {
	ReadLatestDerivationL1Height() *uint64
	ReadLatestBatchBls() types.BatchBls
}

type Writer interface {
	WriteLatestDerivationL1Height(latest uint64)
	WriteLatestBatchBls(batchBls types.BatchBls)
}