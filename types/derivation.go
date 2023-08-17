package types

import eth "github.com/scroll-tech/go-ethereum/core/types"

type BatchBls struct {
	BlsData     *eth.BLSData
	BlockNumber uint64 // last blockNumber of batch
}
