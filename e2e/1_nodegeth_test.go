package e2e

import (
	"context"
	"math/big"
	"testing"

	"github.com/morphism-labs/morphism-bindings/bindings"
	nodetypes "github.com/morphism-labs/node/core"
	"github.com/morphism-labs/node/db"
	"github.com/morphism-labs/node/types"
	"github.com/scroll-tech/go-ethereum/accounts/abi/bind"
	"github.com/scroll-tech/go-ethereum/common"
	eth "github.com/scroll-tech/go-ethereum/core/types"
	"github.com/scroll-tech/go-ethereum/rollup/fees"
	"github.com/scroll-tech/go-ethereum/rollup/rcfg"
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
	txs, restBytes, blsBytes, root, err := node.RequestBlockData(1)
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

	valid, err := node.CheckBlockData(txs, restBytes, blsBytes, root)
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
	txs, restBytes, blsBytes, root, err = node.RequestBlockData(2)
	require.NoError(t, err)
	require.EqualValues(t, 0, len(txs))
	l2Data, l1Msgs, err = converter.Recover(blsBytes, restBytes, txs)
	require.NoError(t, err)
	require.EqualValues(t, 0, len(l1Msgs))
	require.EqualValues(t, 2, l2Data.Number)
	require.EqualValues(t, eth.EmptyAddress, l2Data.Miner)
	require.EqualValues(t, 0, len(l2Data.Transactions))

	valid, err = node.CheckBlockData(txs, restBytes, blsBytes, root)
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

		require.NoError(t, ManualCreateBlock(node, 1))

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
		require.NoError(t, ManualCreateBlock(node, 1))

		// after block produced
		currentBlock := geth.Backend.BlockChain().CurrentBlock()
		require.EqualValues(t, 1, currentBlock.Number().Uint64())
		require.EqualValues(t, totalTxCount-1, currentBlock.Transactions().Len())
		// left one tx
		pendings := geth.Backend.TxPool().Pending(true)
		require.EqualValues(t, 1, len(pendings))
	})
}

func TestL1Fee(t *testing.T) {
	geth, node := NewGethAndNode(t, db.NewMemoryStore(), func(config *SystemConfig) error {
		genesis, err := GenesisFromPath(FullGenesisPath)
		config.Genesis = genesis
		if err != nil {
			return err
		}
		return nil
	}, nil)

	// block 1
	require.NoError(t, ManualCreateBlock(node, 1))

	gasPriceOracle, err := bindings.NewGasPriceOracle(GasPriceOracleAddress, geth.EthClient)
	require.NoError(t, err)
	l1BaseFee, err := gasPriceOracle.L1BaseFee(nil)
	require.NoError(t, err)
	require.EqualValues(t, 0, l1BaseFee.Int64())
	overhead, err := gasPriceOracle.Overhead(nil)
	require.NoError(t, err)
	require.EqualValues(t, 0, overhead.Int64())
	scalar, err := gasPriceOracle.Scalar(nil)
	require.NoError(t, err)
	require.EqualValues(t, 0, scalar.Int64())
	owner, err := gasPriceOracle.Owner(nil)
	require.NoError(t, err)
	require.EqualValues(t, defaultOwnerAddress.Bytes(), owner.Bytes())

	// update configs
	transactor, err := bind.NewKeyedTransactorWithChainID(defaultOwnerPrivKey, geth.Backend.BlockChain().Config().ChainID)
	require.NoError(t, err)
	setScalarTx, err := gasPriceOracle.SetScalar(transactor, big.NewInt(1e9))
	require.NoError(t, err)
	setL1BaseFeeTx, err := gasPriceOracle.SetL1BaseFee(transactor, big.NewInt(2e9)) // 2Gwei
	require.NoError(t, err)
	// block 2
	require.NoError(t, ManualCreateBlock(node, 2))
	// check the updates
	setScalarTxReceipt, err := geth.EthClient.TransactionReceipt(context.Background(), setScalarTx.Hash())
	require.NoError(t, err)
	require.EqualValues(t, 1, setScalarTxReceipt.Status)
	setL1BaseFeeReceipt, err := geth.EthClient.TransactionReceipt(context.Background(), setL1BaseFeeTx.Hash())
	require.NoError(t, err)
	require.EqualValues(t, 1, setL1BaseFeeReceipt.Status)
	l1BaseFee, err = gasPriceOracle.L1BaseFee(nil)
	require.NoError(t, err)
	require.EqualValues(t, 2e9, l1BaseFee.Int64())
	scalar, err = gasPriceOracle.Scalar(nil)
	require.NoError(t, err)
	require.EqualValues(t, 1e9, scalar.Int64())

	state, err := geth.Backend.BlockChain().State()
	require.NoError(t, err)
	l1BaseFeeFromSlot := state.GetState(GasPriceOracleAddress, rcfg.L1BaseFeeSlot)
	overheadFromSlot := state.GetState(GasPriceOracleAddress, rcfg.OverheadSlot)
	scalarFromSlot := state.GetState(GasPriceOracleAddress, rcfg.ScalarSlot)
	require.EqualValues(t, l1BaseFee.Int64(), l1BaseFeeFromSlot.Big().Int64())
	require.EqualValues(t, overhead.Int64(), overheadFromSlot.Big().Int64())
	require.EqualValues(t, scalar.Int64(), scalarFromSlot.Big().Int64())
	// transfer
	tx, err := SimpleTransfer(geth)
	require.NoError(t, err)
	txBytes, err := tx.MarshalBinary()
	require.NoError(t, err)

	l1GasUsed := fees.CalculateL1GasUsed(txBytes, overhead)
	l1DataFee := new(big.Int).Mul(l1GasUsed, l1BaseFee)
	expectedL1Fee := mulAndScale(l1DataFee, scalar, new(big.Int).SetUint64(1e9))

	//block 3
	require.NoError(t, ManualCreateBlock(node, 3))

	txReceipt, err := geth.EthClient.TransactionReceipt(context.Background(), tx.Hash())
	require.NoError(t, err)
	require.EqualValues(t, 1, txReceipt.Status)
	require.EqualValues(t, expectedL1Fee.Int64(), txReceipt.L1Fee.Int64())

}

func TestWithdrawRootSlot(t *testing.T) {
	geth, node := NewGethAndNode(t, db.NewMemoryStore(), func(config *SystemConfig) error {
		genesis, err := GenesisFromPath(FullGenesisPath)
		config.Genesis = genesis
		if err != nil {
			return err
		}
		return nil
	}, nil)

	// block 1
	require.NoError(t, ManualCreateBlock(node, 1))

	l2ToL1MessagePasser, err := bindings.NewL2ToL1MessagePasserCaller(rcfg.L2MessageQueueAddress, geth.EthClient)
	require.NoError(t, err)
	expectedRoot, err := l2ToL1MessagePasser.GetTreeRoot(nil)
	require.NoError(t, err)
	messageRoot, err := l2ToL1MessagePasser.MessageRoot(nil)
	require.NoError(t, err)
	require.EqualValues(t, expectedRoot, messageRoot)
	state, err := geth.Backend.BlockChain().State()
	require.NoError(t, err)
	result := state.GetState(rcfg.L2MessageQueueAddress, rcfg.WithdrawTrieRootSlot)
	require.EqualValues(t, expectedRoot, result)
}

// mulAndScale multiplies a big.Int by a big.Int and then scale it by precision,
// rounded towards zero
func mulAndScale(x *big.Int, y *big.Int, precision *big.Int) *big.Int {
	z := new(big.Int).Mul(x, y)
	return new(big.Int).Quo(z, precision)
}
