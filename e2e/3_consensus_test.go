package e2e

import (
	"context"
	"fmt"
	"math/big"
	"testing"
	"time"

	nodetypes "github.com/morphism-labs/node/core"
	"github.com/morphism-labs/node/db"
	"github.com/scroll-tech/go-ethereum/accounts/abi/bind"
	"github.com/scroll-tech/go-ethereum/common/hexutil"
	"github.com/scroll-tech/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
	"github.com/tendermint/tendermint/blssignatures"
	"github.com/tendermint/tendermint/config"
	rpctest "github.com/tendermint/tendermint/rpc/test"
	tdm "github.com/tendermint/tendermint/types"
)

func TestSingleTendermint_BasicProduceBlocks(t *testing.T) {
	t.Run("TestDefaultConfig", func(t *testing.T) {
		geth, node := NewGethAndNode(t, db.NewMemoryStore(), nil, nil)
		tendermint, err := NewDefaultTendermintNode(node)
		require.NoError(t, err)
		require.NoError(t, tendermint.Start())
		defer func() {
			rpctest.StopTendermint(tendermint)
		}()
		tx, err := SimpleTransfer(geth)
		require.NoError(t, err)

		timeoutCommit := tendermint.Config().Consensus.TimeoutCommit
		time.Sleep(timeoutCommit + time.Second)
		receipt, err := geth.EthClient.TransactionReceipt(context.Background(), tx.Hash())
		require.NoError(t, err)
		require.NotNil(t, receipt)
		require.NotNil(t, receipt.BlockNumber, "has not been involved in block after (timeoutCommit + 1) sec passed")
		require.EqualValues(t, 1, receipt.Status)
	})

	t.Run("TestDelayEmptyBlocks", func(t *testing.T) {
		geth, node := NewGethAndNode(t, db.NewMemoryStore(), nil, nil)
		tendermint, err := NewTendermintNode(node, nil, func(c *config.Config) {
			c.Consensus.TimeoutCommit = time.Second
			c.Consensus.CreateEmptyBlocks = true
			c.Consensus.CreateEmptyBlocksInterval = 5 * time.Second
		})
		require.NoError(t, err)
		require.NoError(t, tendermint.Start())
		defer func() {
			rpctest.StopTendermint(tendermint)
		}()

		time.Sleep(3 * time.Second)
		tx, err := SimpleTransfer(geth)
		require.NoError(t, err)

		timeoutCommit := tendermint.Config().Consensus.TimeoutCommit
		time.Sleep(timeoutCommit)
		receipt, err := geth.EthClient.TransactionReceipt(context.Background(), tx.Hash())
		require.NoError(t, err)
		require.NotNil(t, receipt)
		require.NotNil(t, receipt.BlockNumber, "has not been involved in block after (timeoutCommit + 1) sec passed")
		require.EqualValues(t, 1, receipt.Status)
	})

}

func TestSingleTendermint_VerifyBLS(t *testing.T) {
	geth, node := NewGethAndNode(t, db.NewMemoryStore(), nil, nil)
	blsKey := blssignatures.GenFileBLSKey()

	txsContext := make([]byte, 0)
	zkContext := make([]byte, 0)
	stop := make(chan struct{})
	var lastRoot []byte
	customNode := NewCustomNode(node).
		WithCustomRequestBlockData(func(height int64) (txs [][]byte, l2Config []byte, zkConfig, root []byte, err error) {
			txs, l2Config, zkConfig, root, err = node.RequestBlockData(height)
			fmt.Printf("height: %d, root: %s \n", height, hexutil.Encode(root))
			lastRoot = root
			return
		}).
		WithCustomFuncDeliverBlock(func(txs [][]byte, l2Config []byte, zkConfig []byte, validators []tdm.Address, blsSignatures [][]byte) (err error) {
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

				valid, err := blssignatures.VerifySignature(sig, crypto.Keccak256(append(append(zkContext, txsContext...), lastRoot...)), pk)
				require.NoError(t, err)
				require.True(t, valid)
				close(stop)
			}
			return node.DeliverBlock(txs, l2Config, zkConfig, validators, blsSignatures)
		})
	tendermint, err := NewTendermintNode(customNode, blsKey, func(config *config.Config) {
		config.Consensus.BatchBlocksInterval = 2
	})
	require.NoError(t, err)
	require.NoError(t, tendermint.Start())
	defer func() {
		rpctest.StopTendermint(tendermint)
		geth.Node.Close()
	}()
	_, err = SimpleTransfer(geth)
	require.NoError(t, err)

	timer := time.NewTimer(2 * time.Second)
Loop:
	for {
		select {
		case <-stop:
			t.Log("successfully verified BLS")
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
		configBatchMaxBytes       = 5000
		//configBatchTimeout      = 0 * time.Second
	)

	t.Run("TestBlockInterval", func(t *testing.T) {
		_, node := NewGethAndNode(t, db.NewMemoryStore(), nil, nil)
		stop := make(chan struct{})
		errChan := make(chan struct{})
		converter := nodetypes.Version1Converter{}
		customNode := NewCustomNode(node).WithCustomFuncDeliverBlock(func(txs [][]byte, l2Config []byte, zkConfig []byte, validators []tdm.Address, blsSignatures [][]byte) (err error) {
			l2Data, _, err := converter.Recover(zkConfig, l2Config, txs)
			require.NoError(t, err)
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
			config.Consensus.BatchMaxBytes = 0
		})
		defer func() {
			if tendermint != nil {
				rpctest.StopTendermint(tendermint)
			}
		}()
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
		_, node := NewGethAndNode(t, db.NewMemoryStore(), nil, nil)
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
			config.Consensus.BatchBlocksInterval = 0
		})
		defer func() {
			if tendermint != nil {
				rpctest.StopTendermint(tendermint)
			}
		}()
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
				break Loop
			case <-timer.C:
				require.Fail(t, "timeout")
				break Loop
			}
		}
	})

	//t.Run("TestBatchTimeout", func(t *testing.T) {
	//	_, node := NewGethAndNode(t, db.NewMemoryStore(), nil, nil)
	//	stop := make(chan struct{})
	//	errChan := make(chan struct{})
	//	converter := nodetypes.Version1Converter{}
	//	var startTime time.Time
	//	var batchNum int
	//	customNode := NewCustomNode(node).WithCustomFuncDeliverBlock(func(txs [][]byte, l2Config []byte, zkConfig []byte, validators []tdm.Address, blsSignatures [][]byte) (err error) {
	//		if batchNum == 2 {
	//			close(stop)
	//		}
	//		l2Data, _, err := converter.Recover(zkConfig, l2Config, txs)
	//		currentBlockTime := time.Unix(int64(l2Data.Timestamp), 0)
	//		if l2Data.Number == 1 {
	//			startTime = currentBlockTime
	//		} else {
	//			if currentBlockTime.Sub(startTime) >= configBatchTimeout {
	//				if !hasSig(blsSignatures) {
	//					close(errChan)
	//					require.FailNow(t, "no signature found when reached configBatchTimeout")
	//				}
	//				startTime = currentBlockTime
	//				batchNum++
	//			} else if hasSig(blsSignatures) {
	//				close(errChan)
	//				require.FailNow(t, "signature on the wrong point, haven't reached configBatchTimeout", "current block number: %d", l2Data.Number)
	//			}
	//		}
	//		return node.DeliverBlock(txs, l2Config, zkConfig, validators, blsSignatures)
	//	})
	//	tendermint, err := NewTendermintNode(customNode, nil, func(config *config.Config) {
	//		config.Consensus.BatchTimeout = configBatchTimeout
	//		genDoc, err := tdm.GenesisDocFromFile(config.GenesisFile())
	//		require.NoError(t, err)
	//		genDoc.GenesisTime = time.Now()
	//		require.NoError(t, genDoc.SaveAs(config.GenesisFile()))
	//	})
	//	defer func() {
	//		if tendermint != nil {
	//			rpctest.StopTendermint(tendermint)
	//		}
	//	}()
	//	require.NoError(t, err)
	//	require.NoError(t, tendermint.Start())
	//	timer := time.NewTimer(10 * time.Second)
	//Loop:
	//	for {
	//		select {
	//		case <-stop:
	//			t.Log("successfully checked the batch point")
	//			break Loop
	//		case <-errChan:
	//			t.Log("failed the testcase")
	//			break Loop
	//		case <-timer.C:
	//			require.Fail(t, "timeout")
	//			break Loop
	//		}
	//	}
	//})
}

func hasSig(blsSignatures [][]byte) bool {
	return len(blsSignatures) > 0 && len(blsSignatures[0]) > 0
}

/******************************************************/
/*                 multiple nodes testing             */
/******************************************************/

func TestMultipleTendermint_BasicProduceBlocks(t *testing.T) {
	nodesNum := 4
	l2Nodes, geths := NewMultipleGethNodes(t, nodesNum)

	sendValue := big.NewInt(1e18)
	transactOpts, err := bind.NewKeyedTransactorWithChainID(testingPrivKey, geths[0].Backend.BlockChain().Config().ChainID)
	require.NoError(t, err)
	transactOpts.Value = sendValue
	transferTx, err := geths[0].Transfer(transactOpts, testingAddress2)
	require.NoError(t, err)
	pendings := geths[0].Backend.TxPool().Pending(true)
	require.EqualValues(t, 1, len(pendings))

	// testing the transactions broadcasting
	time.Sleep(1000 * time.Millisecond) // give the time for broadcasting
	for i := 1; i < nodesNum; i++ {
		pendings = geths[i].Backend.TxPool().Pending(true)
		require.EqualValues(t, 1, len(pendings), "geth%d has not received this transaction", i)
	}

	tmNodes, err := NewMultipleTendermintNodes(l2Nodes)
	defer func() {
		for _, tmNode := range tmNodes {
			if tmNode != nil {
				rpctest.StopTendermint(tmNode)
			}
		}
	}()
	require.NoError(t, err)
	for _, tmNode := range tmNodes {
		go tmNode.Start()
		// sleep for a while to start the next tendermint node, in case the conflicts during concurrent operations
		time.Sleep(100 * time.Millisecond)
	}

	// testing the block producing
	time.Sleep(tmNodes[0].Config().Consensus.TimeoutCommit + 2*time.Second)
	receipt, err := geths[0].EthClient.TransactionReceipt(context.Background(), transferTx.Hash())
	require.NoError(t, err)
	require.NotNil(t, receipt.BlockNumber, "the transaction has not been involved in block")
	require.EqualValues(t, 1, receipt.Status)

	receipt, err = geths[1].EthClient.TransactionReceipt(context.Background(), transferTx.Hash())
	require.NoError(t, err)
	require.NotNil(t, receipt.BlockNumber, "the transaction has not been involved in block")
	require.EqualValues(t, 1, receipt.Status)

	receipt, err = geths[2].EthClient.TransactionReceipt(context.Background(), transferTx.Hash())
	require.NoError(t, err)
	require.NotNil(t, receipt.BlockNumber, "the transaction has not been involved in block")
	require.EqualValues(t, 1, receipt.Status)

	receipt, err = geths[3].EthClient.TransactionReceipt(context.Background(), transferTx.Hash())
	require.NoError(t, err)
	require.NotNil(t, receipt.BlockNumber, "the transaction has not been involved in block")
	require.EqualValues(t, 1, receipt.Status)
}

func TestMultipleTendermint_AddNonSequencer(t *testing.T) {
	nodesNum := 5
	l2Nodes, geths := NewMultipleGethNodes(t, nodesNum)

	nonSeqL2Node, nonSeqGeth := l2Nodes[4], geths[4]
	l2Nodes = l2Nodes[:4]

	//timeoutCommit := time.Second
	tmNodes, err := NewMultipleTendermintNodes(l2Nodes)
	defer func() {
		for _, tmNode := range tmNodes {
			if tmNode != nil {
				rpctest.StopTendermint(tmNode)
			}
		}
	}()
	require.NoError(t, err)

	// send transfer tx
	sendValue := big.NewInt(1e18)
	transactOpts, err := bind.NewKeyedTransactorWithChainID(testingPrivKey, geths[0].Backend.BlockChain().Config().ChainID)
	require.NoError(t, err)
	transactOpts.Value = sendValue
	transferTx, err := geths[0].Transfer(transactOpts, testingAddress2)
	require.NoError(t, err)

	for _, tmNode := range tmNodes {
		go tmNode.Start()
		// sleep for a while to start the next tendermint node, in case the conflicts during concurrent operations
		time.Sleep(100 * time.Millisecond)
	}
	time.Sleep(time.Second)

	// new a non-sequencer
	genDoc, err := tdm.GenesisDocFromFile(tmNodes[0].Config().GenesisFile())
	require.NoError(t, err)
	nonSeqTendermint, err := NewDefaultTendermintNode(nonSeqL2Node, func(c *config.Config) {
		if err = genDoc.SaveAs(c.GenesisFile()); err != nil {
			require.NoError(t, err)
		}
		c.P2P.PersistentPeers = tmNodes[0].Config().P2P.PersistentPeers
	})
	defer func() {
		if nonSeqTendermint != nil {
			rpctest.StopTendermint(nonSeqTendermint)
		}
	}()
	require.NoError(t, err)
	go nonSeqTendermint.Start()
	time.Sleep(2 * time.Second)
	require.True(t, nonSeqGeth.Backend.BlockChain().CurrentHeader().Number.Uint64() > 0)
	receipt, err := nonSeqGeth.EthClient.TransactionReceipt(context.Background(), transferTx.Hash())
	require.NoError(t, err)
	require.EqualValues(t, 1, receipt.Status)

	balance, err := nonSeqGeth.EthClient.BalanceAt(context.Background(), testingAddress2, nil)
	require.NoError(t, err)
	require.EqualValues(t, sendValue.Int64(), balance.Int64())
}

func TestMultipleTendermint_NodeOffline(t *testing.T) {
	nodesNum := 4
	l2Nodes, geths := NewMultipleGethNodes(t, nodesNum)

	timeoutCommit := time.Second
	tmNodes, err := NewMultipleTendermintNodes(l2Nodes, func(c *config.Config) {
		c.Consensus.SkipTimeoutCommit = false
		c.Consensus.TimeoutCommit = timeoutCommit
	})
	defer func() {
		for _, tmNode := range tmNodes {
			if tmNode != nil {
				rpctest.StopTendermint(tmNode)
			}
		}
	}()
	require.NoError(t, err)
	for _, tmNode := range tmNodes {
		go tmNode.Start()
		// sleep for a while to start the next tendermint node, in case the conflicts during concurrent operations
		time.Sleep(100 * time.Millisecond)
	}

	theAlwaysActiveGeth := geths[nodesNum-1]
	startedHeight := theAlwaysActiveGeth.Backend.BlockChain().CurrentHeader().Number.Uint64()
	time.Sleep(timeoutCommit * 3)
	laterHeight := theAlwaysActiveGeth.Backend.BlockChain().CurrentHeader().Number.Uint64()
	require.True(t, laterHeight > startedHeight)
	// one node offline
	require.NoError(t, tmNodes[0].Stop())
	afterNode0StoppedHeight := theAlwaysActiveGeth.Backend.BlockChain().CurrentHeader().Number.Uint64()
	time.Sleep(timeoutCommit * 3)
	laterHeight = theAlwaysActiveGeth.Backend.BlockChain().CurrentHeader().Number.Uint64()
	require.True(t, laterHeight > afterNode0StoppedHeight, "stop producing blocks after one node offline")
	// two nodes offline
	require.NoError(t, tmNodes[1].Stop())
	afterNode1StoppedHeight := theAlwaysActiveGeth.Backend.BlockChain().CurrentHeader().Number.Uint64()
	time.Sleep(timeoutCommit * 3)
	laterHeight = theAlwaysActiveGeth.Backend.BlockChain().CurrentHeader().Number.Uint64()
	require.True(t, laterHeight == afterNode1StoppedHeight, "producing blocks even 2 nodes offline")
}
