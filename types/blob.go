package types

import (
	"errors"
	eth "github.com/scroll-tech/go-ethereum/core/types"
	"github.com/scroll-tech/go-ethereum/crypto/kzg4844"
)

func makeBCP(bz []byte) (b kzg4844.Blob, c kzg4844.Commitment, p kzg4844.Proof) {
	copy(b[:], bz)
	c, _ = kzg4844.BlobToCommitment(b)
	p, _ = kzg4844.ComputeBlobProof(b, c)
	return
}

func MakeBlobTxSidecar(blobBytes []byte) (*eth.BlobTxSidecar, error) {
	if len(blobBytes) == 0 {
		return nil, nil
	}
	if len(blobBytes) > 2*131072 {
		return nil, errors.New("only 2 blobs at most is allowed")
	}
	blobCount := len(blobBytes)/(131072+1) + 1
	var (
		blobs       = make([]kzg4844.Blob, blobCount)
		commitments = make([]kzg4844.Commitment, blobCount)
		proofs      = make([]kzg4844.Proof, blobCount)
	)
	switch blobCount {
	case 1:
		blobs[0], commitments[0], proofs[0] = makeBCP(blobBytes)
	case 2:
		blobs[0], commitments[0], proofs[0] = makeBCP(blobBytes[:131072])
		blobs[1], commitments[1], proofs[1] = makeBCP(blobBytes[131072:])
	}
	return &eth.BlobTxSidecar{
		Blobs:       blobs,
		Commitments: commitments,
		Proofs:      proofs,
	}, nil
}
