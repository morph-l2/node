package e2e

import (
	"context"
	"math/big"
	"testing"

	nodetypes "github.com/morphism-labs/node/core"
	"github.com/morphism-labs/node/db"
	"github.com/morphism-labs/node/types"
	"github.com/scroll-tech/go-ethereum/accounts/abi/bind"
	"github.com/scroll-tech/go-ethereum/common"
	eth "github.com/scroll-tech/go-ethereum/core/types"
	"github.com/stretchr/testify/require"
)

func TestNodeGeth_BasicProduceBlocks(t *testing.T) {
	geth, node := NewGethAndNode(t, db.NewMemoryStore(), nil, nil)
	genesisConfig := geth.Backend.BlockChain().Config()

	feeReceiver, err := geth.Backend.Etherbase()
	require.NoError(t, err)
	if geth.Backend.BlockChain().Config().Scroll.FeeVaultEnabled() {
		feeReceiver = *geth.Backend.BlockChain().Config().Scroll.FeeVaultAddress
	}
	feeReceiverBeforeBalance, err := geth.EthClient.BalanceAt(context.Background(), feeReceiver, nil)
	require.NoError(t, err)
	senderBeforeBalance, err := geth.EthClient.BalanceAt(context.Background(), testingAddress, nil)
	require.NoError(t, err)
	receiverBeforeBalance, err := geth.EthClient.BalanceAt(context.Background(), testingAddress2, nil)
	require.NoError(t, err)

	//send tx
	sendValue := big.NewInt(1e18)
	transactOpts, err := bind.NewKeyedTransactorWithChainID(testingPrivKey, genesisConfig.ChainID)
	require.NoError(t, err)
	transactOpts.Value = sendValue
	transferTx, err := geth.Transfer(transactOpts, testingAddress2)
	require.NoError(t, err)
	transferTxBytes, err := transferTx.MarshalBinary()
	require.NoError(t, err)
	pendings := geth.Backend.TxPool().Pending(true)
	require.EqualValues(t, 1, len(pendings))

	// block 1 producing
	txs, restBytes, blsBytes, err := node.RequestBlockData(1)
	require.NoError(t, err)
	require.EqualValues(t, 1, len(txs))
	converter := nodetypes.Version1Converter{}
	l2Data, l1Msgs, err := converter.Recover(blsBytes, restBytes, txs)
	require.NoError(t, err)
	require.EqualValues(t, 0, len(l1Msgs))
	require.EqualValues(t, 1, l2Data.Number)
	require.EqualValues(t, eth.EmptyAddress, l2Data.Miner)
	require.EqualValues(t, 1, len(l2Data.Transactions))
	require.EqualValues(t, transferTxBytes, l2Data.Transactions[0])

	valid, err := node.CheckBlockData(txs, restBytes, blsBytes)
	require.NoError(t, err)
	require.True(t, valid)

	require.NoError(t, node.DeliverBlock(txs, restBytes, blsBytes, nil, nil))

	// after block 1 produced
	currentBlock := geth.Backend.BlockChain().CurrentBlock()
	require.EqualValues(t, 1, currentBlock.Number().Uint64())
	curTxBytes, _ := currentBlock.Transactions()[0].MarshalBinary()
	require.EqualValues(t, transferTxBytes, curTxBytes)
	if l2Data.Hash != eth.EmptyHash {
		require.EqualValues(t, l2Data.Hash.String(), currentBlock.Hash().String())
	}
	pendings = geth.Backend.TxPool().Pending(true)
	require.EqualValues(t, 0, len(pendings))

	feeReceiverAfterBalance, err := geth.EthClient.BalanceAt(context.Background(), feeReceiver, nil)
	require.NoError(t, err)
	senderAfterBalance, err := geth.EthClient.BalanceAt(context.Background(), testingAddress, nil)
	require.NoError(t, err)
	receiverAfterBalance, err := geth.EthClient.BalanceAt(context.Background(), testingAddress2, nil)
	require.NoError(t, err)

	senderReduced := new(big.Int).Sub(senderBeforeBalance, senderAfterBalance)
	receiverGain := new(big.Int).Sub(receiverAfterBalance, receiverBeforeBalance)
	feeReceiverGain := new(big.Int).Sub(feeReceiverAfterBalance, feeReceiverBeforeBalance)
	require.EqualValues(t, 1, senderReduced.Cmp(big.NewInt(0)))
	require.EqualValues(t, 1, feeReceiverGain.Cmp(big.NewInt(0)))
	require.EqualValues(t, sendValue.Int64(), receiverGain.Int64())
	require.EqualValues(t, senderReduced.String(), new(big.Int).Add(receiverGain, feeReceiverGain).String())

	// block 2 producing
	txs, restBytes, blsBytes, err = node.RequestBlockData(2)
	require.NoError(t, err)
	require.EqualValues(t, 0, len(txs))
	l2Data, l1Msgs, err = converter.Recover(blsBytes, restBytes, txs)
	require.NoError(t, err)
	require.EqualValues(t, 0, len(l1Msgs))
	require.EqualValues(t, 2, l2Data.Number)
	require.EqualValues(t, eth.EmptyAddress, l2Data.Miner)
	require.EqualValues(t, 0, len(l2Data.Transactions))

	valid, err = node.CheckBlockData(txs, restBytes, blsBytes)
	require.NoError(t, err)
	require.True(t, valid)
	require.NoError(t, node.DeliverBlock(txs, restBytes, blsBytes, nil, nil))

	currentBlock = geth.Backend.BlockChain().CurrentBlock()
	require.EqualValues(t, 2, currentBlock.Number().Uint64())
	require.EqualValues(t, l2Data.Hash, currentBlock.Hash())
}

func TestNodeGeth_FullBlock(t *testing.T) {
	t.Run("TestMaxTxPerBlock", func(t *testing.T) {
		maxTxPerBlock := 5
		syncerDB := db.NewMemoryStore()
		geth, node := NewGethAndNode(t, syncerDB, func(config *SystemConfig) error {
			genesis, err := GenesisFromPath(FullGenesisPath)
			config.Genesis = genesis
			if err != nil {
				return err
			}
			config.Genesis.Config.Scroll.MaxTxPerBlock = &maxTxPerBlock
			return nil
		}, nil)

		// maxTxPerBlock+1 L2 transfer txs
		for i := 0; i < maxTxPerBlock+1; i++ {
			_, err := SimpleTransfer(geth)
			require.NoError(t, err)
		}

		// one l1 message
		l1Message, _, err := MockL1Message(testingAddress2, testingAddress2, uint64(0), big.NewInt(1e18))
		require.NoError(t, err)
		require.NoError(t, syncerDB.WriteSyncedL1Messages([]types.L1Message{*l1Message}, 0))

		txs, restBytes, blsBytes, err := node.RequestBlockData(1)
		require.NoError(t, err)
		valid, err := node.CheckBlockData(txs, restBytes, blsBytes)
		require.NoError(t, err)
		require.True(t, valid)
		require.NoError(t, node.DeliverBlock(txs, restBytes, blsBytes, nil, nil))

		currentBlock := geth.Backend.BlockChain().CurrentBlock()
		require.EqualValues(t, 1, currentBlock.Number().Uint64())
		require.EqualValues(t, maxTxPerBlock+1, currentBlock.Transactions().Len()) // l1 message will be not counted to maxTxPerBlock
		require.EqualValues(t, eth.L1MessageTxType, currentBlock.Transactions()[0].Type())
		// left one tx
		pendings := geth.Backend.TxPool().Pending(true)
		require.EqualValues(t, 1, len(pendings))
	})

	t.Run("TestMaxTxPayloadBytesPerBlock", func(t *testing.T) {
		syncerDB := db.NewMemoryStore()
		maxTxPayloadBytesPerBlock := 1000
		geth, node := NewGethAndNode(t, syncerDB, func(config *SystemConfig) error {
			genesis, err := GenesisFromPath(FullGenesisPath)
			config.Genesis = genesis
			if err != nil {
				return err
			}
			config.Genesis.Config.Scroll.MaxTxPayloadBytesPerBlock = &maxTxPayloadBytesPerBlock

			return nil
		}, nil)

		l1Message, _, err := MockL1Message(testingAddress2, testingAddress2, uint64(0), big.NewInt(1e18))
		require.NoError(t, err)
		require.NoError(t, syncerDB.WriteSyncedL1Messages([]types.L1Message{*l1Message}, 0))

		totalSize := eth.NewTx(&l1Message.L1MessageTx).Size()
		var l2TxCount int
		for totalSize < common.StorageSize(maxTxPayloadBytesPerBlock) {
			l2TxCount++
			tx, err := SimpleTransfer(geth)
			require.NoError(t, err)
			totalSize += tx.Size()
		}
		if totalSize == common.StorageSize(maxTxPayloadBytesPerBlock) {
			l2TxCount++
			_, err = SimpleTransfer(geth)
			require.NoError(t, err)
		}
		totalTxCount := l2TxCount + 1

		// block 1 producing
		txs, restBytes, blsBytes, err := node.RequestBlockData(1)
		require.NoError(t, err)
		valid, err := node.CheckBlockData(txs, restBytes, blsBytes)
		require.NoError(t, err)
		require.True(t, valid)
		require.NoError(t, node.DeliverBlock(txs, restBytes, blsBytes, nil, nil))

		// after block produced
		currentBlock := geth.Backend.BlockChain().CurrentBlock()
		require.EqualValues(t, 1, currentBlock.Number().Uint64())
		require.EqualValues(t, totalTxCount-1, currentBlock.Transactions().Len())
		// left one tx
		pendings := geth.Backend.TxPool().Pending(true)
		require.EqualValues(t, 1, len(pendings))
	})
}
