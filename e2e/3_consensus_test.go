package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	nodetypes "github.com/morphism-labs/node/core"
	"github.com/morphism-labs/node/db"
	"github.com/scroll-tech/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
	"github.com/tendermint/tendermint/blssignatures"
	"github.com/tendermint/tendermint/config"
	tdm "github.com/tendermint/tendermint/types"
)

func TestSingleTendermint_BasicProduceBlocks(t *testing.T) {
	geth, node := NewSequencer(t, db.NewMemoryStore(), nil, nil)
	tendermint, err := NewDefaultTendermintNode(node)
	require.NoError(t, err)
	require.NoError(t, tendermint.Start())

	tx, err := SimpleTransfer(geth)
	require.NoError(t, err)

	timeoutCommit := tendermint.Config().Consensus.TimeoutCommit
	time.Sleep(timeoutCommit + time.Second)
	receipt, err := geth.EthClient.TransactionReceipt(context.Background(), tx.Hash())
	require.NoError(t, err)
	require.NotNil(t, receipt)
	require.NotNil(t, receipt.BlockNumber, "has not been involved in block after (timeoutCommit + 1) sec passed")
	require.EqualValues(t, 1, receipt.Status)
}

func TestSingleTendermint_VerifyBLS(t *testing.T) {
	geth, node := NewSequencer(t, db.NewMemoryStore(), nil, nil)
	blsKey := blssignatures.GenFileBLSKey()

	txsContext := make([]byte, 0)
	zkContext := make([]byte, 0)
	stop := make(chan struct{})
	customNode := NewCustomNode(node).WithCustomFuncDeliverBlock(func(txs [][]byte, l2Config []byte, zkConfig []byte, validators []tdm.Address, blsSignatures [][]byte) (err error) {
		for _, tx := range txs {
			txsContext = append(txsContext, tx...)
		}
		zkContext = append(zkContext, zkConfig...)
		if len(blsSignatures) > 0 && len(blsSignatures[0]) > 0 {
			require.EqualValues(t, 1, len(blsSignatures))
			require.EqualValues(t, 1, len(validators))
			sig, err := blssignatures.SignatureFromBytes(blsSignatures[0])
			require.NoError(t, err)
			pk, err := blssignatures.PublicKeyFromBytes(blsKey.PubKey, false)
			require.NoError(t, err)

			valid, err := blssignatures.VerifySignature(sig, crypto.Keccak256(append(zkContext, txsContext...)), pk)
			require.NoError(t, err)
			require.True(t, valid)
			close(stop)
		}
		return node.DeliverBlock(txs, l2Config, zkConfig, validators, blsSignatures)
	})
	batchTimeout := 3 * time.Second
	tendermint, err := NewTendermintNode(customNode, blsKey, func(config *config.Config) {
		config.Consensus.BatchTimeout = batchTimeout
	})
	require.NoError(t, err)
	require.NoError(t, tendermint.Start())

	_, err = SimpleTransfer(geth)
	require.NoError(t, err)

	timer := time.NewTimer(batchTimeout + time.Second)
Loop:
	for {
		select {
		case <-stop:
			t.Log("successfully verified BLS")
			geth.Node.Close()
			tendermint.Stop()
			break Loop
		case <-timer.C:
			require.Fail(t, "timeout")
			break Loop
		}
	}
}

func TestSingleTendermint_BatchPoint(t *testing.T) {
	var (
		configBatchBlocksInterval = 3
		configBatchMaxBytes       = 10000
		configBatchTimeout        = 3 * time.Second
	)

	t.Run("TestBlockInterval", func(t *testing.T) {
		_, node := NewSequencer(t, db.NewMemoryStore(), nil, nil)
		stop := make(chan struct{})
		errChan := make(chan struct{})
		converter := nodetypes.Version1Converter{}
		customNode := NewCustomNode(node).WithCustomFuncDeliverBlock(func(txs [][]byte, l2Config []byte, zkConfig []byte, validators []tdm.Address, blsSignatures [][]byte) (err error) {
			l2Data, _, err := converter.Recover(zkConfig, l2Config, txs)
			if l2Data.Number%uint64(configBatchBlocksInterval) == 0 {
				if !hasSig(blsSignatures) {
					close(errChan)
					require.FailNow(t, fmt.Sprintf("should has signature on the block number of %d, but not found", l2Data.Number))
				}
			} else if hasSig(blsSignatures) {
				close(errChan)
				require.FailNow(t, fmt.Sprintf("signature on the wrong point, current block number: %d", l2Data.Number))
			}
			if l2Data.Number/uint64(configBatchBlocksInterval) == 2 { // tested 2 batch point, stop testing
				close(stop)
			}
			return node.DeliverBlock(txs, l2Config, zkConfig, validators, blsSignatures)
		})
		tendermint, err := NewTendermintNode(customNode, nil, func(config *config.Config) {
			config.Consensus.BatchBlocksInterval = int64(configBatchBlocksInterval)
		})
		require.NoError(t, err)
		require.NoError(t, tendermint.Start())

		timer := time.NewTimer(10 * time.Second)
	Loop:
		for {
			select {
			case <-stop:
				t.Log("successfully checked the batch point")
				break Loop
			case <-errChan:
				t.Log("failed the testcase")
				time.Sleep(500 * time.Millisecond)
				break Loop
			case <-timer.C:
				require.Fail(t, "not reach the stop block, timeout")
				break Loop
			}
		}
	})

	t.Run("TestBatchMaxBytes", func(t *testing.T) {
		_, node := NewSequencer(t, db.NewMemoryStore(), nil, nil)
		stop := make(chan struct{})
		errChan := make(chan struct{})
		txsContext := make([]byte, 0)
		zkContext := make([]byte, 0)
		var batchNum int
		customNode := NewCustomNode(node).WithCustomFuncDeliverBlock(func(txs [][]byte, l2Config []byte, zkConfig []byte, validators []tdm.Address, blsSignatures [][]byte) (err error) {
			// reached 2 round batch, stop testing
			if batchNum == 2 {
				close(stop)
			}
			for _, tx := range txs {
				txsContext = append(txsContext, tx...)
			}
			zkContext = append(zkContext, zkConfig...)
			currentBytes := append(zkContext, txsContext...)
			if len(currentBytes) >= configBatchMaxBytes {
				if !hasSig(blsSignatures) {
					close(errChan)
					require.FailNow(t, "no signature found when bytes reached configBatchMaxBytes")
				}
				// clean the context
				txsContext = make([]byte, 0)
				zkContext = make([]byte, 0)
				batchNum++
			} else if hasSig(blsSignatures) {
				close(errChan)
				require.FailNow(t, "signature on the wrong point, bytes haven't reached configBatchMaxBytes")
			}
			return node.DeliverBlock(txs, l2Config, zkConfig, validators, blsSignatures)
		})
		tendermint, err := NewTendermintNode(customNode, nil, func(config *config.Config) {
			config.Consensus.BatchMaxBytes = int64(configBatchMaxBytes)
		})
		require.NoError(t, err)
		require.NoError(t, tendermint.Start())
		timer := time.NewTimer(10 * time.Second)

	Loop:
		for {
			select {
			case <-stop:
				t.Log("successfully checked the batch point")
				break Loop
			case <-errChan:
				t.Log("failed the testcase")
				time.Sleep(500 * time.Millisecond)
				break Loop
			case <-timer.C:
				require.Fail(t, "timeout")
				break Loop
			}
		}
	})

	t.Run("TestBatchTimeout", func(t *testing.T) {
		_, node := NewSequencer(t, db.NewMemoryStore(), nil, nil)
		stop := make(chan struct{})
		errChan := make(chan struct{})
		converter := nodetypes.Version1Converter{}
		var startTime time.Time
		var batchNum int
		customNode := NewCustomNode(node).WithCustomFuncDeliverBlock(func(txs [][]byte, l2Config []byte, zkConfig []byte, validators []tdm.Address, blsSignatures [][]byte) (err error) {
			if batchNum == 2 {
				close(stop)
			}
			l2Data, _, err := converter.Recover(zkConfig, l2Config, txs)
			currentBlockTime := time.Unix(int64(l2Data.Timestamp), 0)
			if l2Data.Number == 1 {
				startTime = currentBlockTime
			} else {
				if currentBlockTime.Sub(startTime) >= configBatchTimeout {
					if !hasSig(blsSignatures) {
						close(errChan)
						require.FailNow(t, "no signature found when reached configBatchTimeout")
					}
					startTime = currentBlockTime
					batchNum++
				} else if hasSig(blsSignatures) {
					close(errChan)
					require.FailNow(t, "signature on the wrong point, haven't reached configBatchTimeout", "current block number: %d", l2Data.Number)
				}
			}
			return node.DeliverBlock(txs, l2Config, zkConfig, validators, blsSignatures)
		})
		tendermint, err := NewTendermintNode(customNode, nil, func(config *config.Config) {
			config.Consensus.BatchTimeout = configBatchTimeout
		})
		require.NoError(t, err)
		require.NoError(t, tendermint.Start())
		timer := time.NewTimer(10 * time.Second)
	Loop:
		for {
			select {
			case <-stop:
				t.Log("successfully checked the batch point")
				break Loop
			case <-errChan:
				t.Log("failed the testcase")
				time.Sleep(500 * time.Millisecond)
				break Loop
			case <-timer.C:
				require.Fail(t, "timeout")
				break Loop
			}
		}
	})
}

func hasSig(blsSignatures [][]byte) bool {
	return len(blsSignatures) > 0 && len(blsSignatures[0]) > 0
}
