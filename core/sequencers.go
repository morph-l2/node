package node

import (
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/scroll-tech/go-ethereum/common/hexutil"
	"github.com/scroll-tech/go-ethereum/crypto"
	"github.com/scroll-tech/go-ethereum/crypto/bls12381"
	"github.com/tendermint/tendermint/blssignatures"
	"github.com/tendermint/tendermint/crypto/ed25519"
	tmproto "github.com/tendermint/tendermint/proto/tendermint/types"
)

const tmKeySize = ed25519.PubKeySize

type sequencerKey struct {
	index     uint64
	blsPubKey blssignatures.PublicKey
}

func (e *Executor) VerifySignature(tmPubKey []byte, message []byte, blsSig []byte) (bool, error) {
	if e.devSequencer {
		e.logger.Info("we are in dev mode, do not verify the bls signature")
		return true, nil
	}
	if len(e.sequencerSet) == 0 {
		return false, errors.New("no available sequencers found in layer2")
	}
	var pk [tmKeySize]byte
	copy(pk[:], tmPubKey)

	seqKey, ok := e.sequencerSet[pk]
	if !ok {
		return false, errors.New("it is not a valid sequencer")
	}

	sig, err := blssignatures.SignatureFromBytes(blsSig)
	if err != nil {
		e.logger.Error("failed to recover bytes to signature", "error", err)
		return false, fmt.Errorf("failed to recover bytes to signature, error: %v", err)
	}
	messageHash := crypto.Keccak256(message)
	return blssignatures.VerifySignature(sig, messageHash, seqKey.blsPubKey)
}

func (e *Executor) sequencerSetUpdates() ([][]byte, error) {
	currentVersion, err := e.sequencerContract.CurrentVersion(nil)
	if err != nil {
		return nil, err
	}
	if e.currentVersion != nil && *e.currentVersion == currentVersion.Uint64() {
		return e.validators, nil
	}

	var beforeVersion uint64
	if e.currentVersion != nil {
		beforeVersion = *e.currentVersion
	}
	e.logger.Info("sequencers updates, version changed", "before", beforeVersion, "now", currentVersion.Uint64())
	sequencersInfo, err := e.sequencerContract.GetSequencerInfos(nil)
	if err != nil {
		return nil, err
	}
	newValidators := make([][]byte, 0)
	newSequencerSet := make(map[[tmKeySize]byte]sequencerKey)
	for i, sequencerInfo := range sequencersInfo {
		blsPK, err := decodeBlsPubKey(sequencerInfo.BlsKey)
		if err != nil {
			e.logger.Error("failed to decode bls key", "key bytes", hexutil.Encode(sequencerInfo.BlsKey), "error", err)
			return nil, err
		}
		newSequencerSet[sequencerInfo.TmKey] = sequencerKey{
			index:     uint64(i),
			blsPubKey: blsPK,
		}
		newValidators = append(newValidators, sequencerInfo.TmKey[:])
	}

	e.sequencerSet = newSequencerSet
	cvUint64 := currentVersion.Uint64()
	e.currentVersion = &cvUint64
	e.validators = newValidators
	return newValidators, nil
}

func (e *Executor) batchParamsUpdates(height uint64) (*tmproto.BatchParams, error) {
	var (
		batchBlockInterval, batchMaxBytes, batchTimeout *big.Int
		err                                             error
	)

	if batchBlockInterval, err = e.govContract.BatchBlockInterval(nil); err != nil {
		return nil, err
	}
	if batchMaxBytes, err = e.govContract.BatchMaxBytes(nil); err != nil {
		return nil, err
	}
	if batchTimeout, err = e.govContract.BatchTimeout(nil); err != nil {
		return nil, err
	}

	changed := e.batchParams.BlocksInterval != batchBlockInterval.Int64() ||
		e.batchParams.MaxBytes != batchMaxBytes.Int64() ||
		int64(e.batchParams.Timeout.Seconds()) != batchTimeout.Int64()

	if changed {
		e.batchParams.BlocksInterval = batchBlockInterval.Int64()
		e.batchParams.MaxBytes = batchMaxBytes.Int64()
		e.batchParams.Timeout = time.Duration(batchTimeout.Int64() * int64(time.Second))
		e.logger.Info("batch params changed", "height", height,
			"batchBlockInterval", batchBlockInterval.Int64(),
			"batchMaxBytes", batchMaxBytes.Int64(),
			"batchTimeout", batchTimeout.Int64())
		return &tmproto.BatchParams{
			BlocksInterval: batchBlockInterval.Int64(),
			MaxBytes:       batchMaxBytes.Int64(),
			Timeout:        time.Duration(batchTimeout.Int64() * int64(time.Second)),
		}, nil
	}
	return nil, nil
}

func (e *Executor) updateSequencerSet() ([][]byte, error) {
	validatorUpdates, err := e.sequencerSetUpdates()
	if err != nil {
		e.logger.Error("failed to get sequencer set from geth")
		return nil, err
	}
	var tmPKBz [tmKeySize]byte
	copy(tmPKBz[:], e.tmPubKey)
	_, isSequencer := e.sequencerSet[tmPKBz]
	if !e.isSequencer && isSequencer {
		e.logger.Info("I am a sequencer, start to launch syncer")
		if e.syncer == nil {
			syncer, err := e.newSyncerFunc()
			if err != nil {
				e.logger.Error("failed to create syncer", "error", err)
				return nil, err
			}
			e.syncer = syncer
			e.l1MsgReader = syncer // syncer works as l1MsgReader
			e.syncer.Start()
		} else {
			go e.syncer.Start()
		}
	} else if e.isSequencer && !isSequencer {
		e.logger.Info("I am not a sequencer, stop syncing")
		e.syncer.Stop()
	}
	e.isSequencer = isSequencer
	return validatorUpdates, nil
}

func decodeBlsPubKey(in []byte) (blssignatures.PublicKey, error) {
	g2P, err := bls12381.NewG2().DecodePoint(in)
	if err != nil {
		return blssignatures.PublicKey{}, err
	}
	return blssignatures.NewTrustedPublicKey(g2P), nil
}
