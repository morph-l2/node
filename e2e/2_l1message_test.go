package e2e

import (
	"context"
	"math/big"
	"testing"

	"github.com/morphism-labs/morphism-bindings/bindings"
	nodetypes "github.com/morphism-labs/node/core"
	"github.com/morphism-labs/node/db"
	"github.com/morphism-labs/node/e2e/configs"
	"github.com/morphism-labs/node/sync"
	"github.com/morphism-labs/node/types"
	"github.com/scroll-tech/go-ethereum/accounts/abi/bind"
	eth "github.com/scroll-tech/go-ethereum/core/types"
	"github.com/scroll-tech/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
)

func TestL1DepositMessage_ConstantBlocksWithL1Message(t *testing.T) {
	syncerDB := db.NewMemoryStore()
	geth, node := NewGethAndNode(t, syncerDB, func(config *SystemConfig) error {
		genesis, err := GenesisFromPath(FullGenesisPath)
		config.Genesis = genesis
		return err
	}, nil)

	beforeDepositorBalance, err := geth.EthClient.BalanceAt(context.Background(), testingAddress2, nil)
	require.NoError(t, err)
	i := 0
	for i = 0; i < 5; i++ {
		l1Message, relayMessageInput, err := MockL1Message(testingAddress2, testingAddress2, uint64(i), big.NewInt(1e18))
		require.NoError(t, err)
		require.NoError(t, syncerDB.WriteSyncedL1Messages([]types.L1Message{*l1Message}, 0))
		transaction := eth.NewTx(&l1Message.L1MessageTx)
		txBytes, err := transaction.MarshalBinary()
		require.NoError(t, err)

		senderVaultBalanceBefore, err := geth.EthClient.BalanceAt(context.Background(), l1Message.Sender, nil)
		require.NoError(t, err)
		depositorBalanceBefore, err := geth.EthClient.BalanceAt(context.Background(), testingAddress2, nil)
		require.NoError(t, err)
		L2CrossDomainMessengerBalanceBefore, err := geth.EthClient.BalanceAt(context.Background(), configs.L2CrossDomainMessengerAddress, nil)
		require.NoError(t, err)
		require.EqualValues(t, 0, L2CrossDomainMessengerBalanceBefore.Uint64())

		// block producing
		txs, restBytes, blsBytes, err := node.RequestBlockData(int64(i + 1))
		require.NoError(t, err)
		require.EqualValues(t, 1, len(txs))
		converter := nodetypes.Version1Converter{}
		l2Data, l1Msgs, err := converter.Recover(blsBytes, restBytes, txs)
		require.NoError(t, err)
		require.EqualValues(t, 1, len(l1Msgs))
		require.EqualValues(t, i+1, l2Data.Number)
		require.EqualValues(t, eth.EmptyAddress, l2Data.Miner)
		require.EqualValues(t, 1, len(l2Data.Transactions))
		require.EqualValues(t, txBytes, l2Data.Transactions[0])
		valid, err := node.CheckBlockData(txs, restBytes, blsBytes)
		require.NoError(t, err)
		require.True(t, valid)
		require.NoError(t, node.DeliverBlock(txs, restBytes, blsBytes, nil, nil))

		// after block produced
		currentBlock := geth.Backend.BlockChain().CurrentBlock()
		require.EqualValues(t, i+1, currentBlock.Number().Uint64())
		senderVaultBalanceAfter, err := geth.EthClient.BalanceAt(context.Background(), l1Message.Sender, nil)
		require.NoError(t, err)
		depositorBalanceAfter, err := geth.EthClient.BalanceAt(context.Background(), testingAddress2, nil)
		require.NoError(t, err)
		L2CrossDomainMessengerBalanceAfter, err := geth.EthClient.BalanceAt(context.Background(), configs.L2CrossDomainMessengerAddress, nil)
		require.NoError(t, err)

		// check receipt success
		receipt, err := geth.EthClient.TransactionReceipt(context.Background(), transaction.Hash())
		require.NoError(t, err)
		require.EqualValues(t, 1, receipt.Status) // success
		// check if deposit success
		l2Messenger, err := bindings.NewL2CrossDomainMessenger(configs.L2CrossDomainMessengerAddress, geth.EthClient)
		require.NoError(t, err)
		versionedHash := crypto.Keccak256Hash(relayMessageInput)
		isSuccess, err := l2Messenger.SuccessfulMessages(nil, versionedHash)
		require.NoError(t, err)
		require.True(t, isSuccess)
		isFailed, err := l2Messenger.FailedMessages(nil, versionedHash)
		require.NoError(t, err)
		require.False(t, isFailed)
		receivedNonce, err := l2Messenger.ReceiveNonce(nil)
		require.NoError(t, err)
		require.EqualValues(t, types.EncodeNonce(uint64(i)).String(), receivedNonce.String())
		// check if balances correct
		vaultReduced := new(big.Int).Sub(senderVaultBalanceBefore, senderVaultBalanceAfter)
		depositorGain := new(big.Int).Sub(depositorBalanceAfter, depositorBalanceBefore)
		require.EqualValues(t, 0, L2CrossDomainMessengerBalanceAfter.Uint64())
		require.EqualValues(t, 1e18, vaultReduced.Uint64())
		require.EqualValues(t, vaultReduced.Uint64(), depositorGain.Uint64())
	}
	afterDepositorBalance, err := geth.EthClient.BalanceAt(context.Background(), testingAddress2, nil)
	require.NoError(t, err)
	allGain := new(big.Int).Sub(afterDepositorBalance, beforeDepositorBalance)
	require.EqualValues(t, new(big.Int).Mul(big.NewInt(1e18), big.NewInt(int64(i))).String(), allGain.String())
}

func TestL1DepositMessage_L1AndL2MessageInOneBlock(t *testing.T) {
	syncerDB := db.NewMemoryStore()
	geth, node := NewGethAndNode(t, syncerDB, func(config *SystemConfig) error {
		genesis, err := GenesisFromPath(FullGenesisPath)
		config.Genesis = genesis
		return err
	}, nil)
	genesisConfig := geth.Backend.BlockChain().Config()

	l1Message0, _, err := MockL1Message(testingAddress2, testingAddress2, uint64(0), big.NewInt(1e18))
	require.NoError(t, err)
	l1Message1, _, err := MockL1Message(testingAddress2, testingAddress2, uint64(1), big.NewInt(1e18))
	require.NoError(t, err)
	require.NoError(t, syncerDB.WriteSyncedL1Messages([]types.L1Message{*l1Message0, *l1Message1}, 0))

	transactOpts, err := bind.NewKeyedTransactorWithChainID(testingPrivKey, genesisConfig.ChainID)
	require.NoError(t, err)
	transactOpts.Value = big.NewInt(1e18)
	_, err = geth.Transfer(transactOpts, testingAddress2)
	require.NoError(t, err)

	txs, restBytes, blsBytes, err := node.RequestBlockData(1)
	require.NoError(t, err)
	valid, err := node.CheckBlockData(txs, restBytes, blsBytes)
	require.NoError(t, err)
	require.True(t, valid)
	require.NoError(t, node.DeliverBlock(txs, restBytes, blsBytes, nil, nil))

	currentBlock := geth.Backend.BlockChain().CurrentBlock()
	require.EqualValues(t, 1, currentBlock.Number().Uint64())
	require.EqualValues(t, 3, currentBlock.Transactions().Len())

	require.EqualValues(t, eth.L1MessageTxType, currentBlock.Transactions()[0].Type())
	require.EqualValues(t, eth.L1MessageTxType, currentBlock.Transactions()[1].Type())
	require.NotEqualValues(t, eth.L1MessageTxType, currentBlock.Transactions()[2].Type())
}

func TestL1DepositMessage_InvalidL1MessageOrder(t *testing.T) {
	syncerDB := db.NewMemoryStore()
	_, node := NewGethAndNode(t, syncerDB, nil, nil)

	l1Message0, _, err := MockL1Message(testingAddress2, testingAddress2, uint64(0), big.NewInt(1e18))
	require.NoError(t, err)
	l1Message2, _, err := MockL1Message(testingAddress2, testingAddress2, uint64(2), big.NewInt(1e18))
	require.NoError(t, err)
	require.NoError(t, syncerDB.WriteSyncedL1Messages([]types.L1Message{*l1Message0, *l1Message2}, 0))

	_, _, _, err = node.RequestBlockData(1)
	require.ErrorIs(t, err, types.ErrInvalidL1MessageOrder)
}

func TestL1DepositMessage_RetryFailure(t *testing.T) {
	syncerDB := db.NewMemoryStore()
	geth, node := NewGethAndNode(t, syncerDB, func(config *SystemConfig) error {
		genesis, err := GenesisFromPath(FullGenesisPath)
		config.Genesis = genesis
		return err
	}, nil)
	genesisConfig := geth.Backend.BlockChain().Config()

	// mock a message that will be failed
	l1Message, relayMessageInput, err := MockL1Message(testingAddress2, testingAddress2, uint64(0), big.NewInt(1e18))
	require.NoError(t, err)
	// set a small amount of gas limit to make the transaction failed
	l1Message.Gas = 200_000
	require.NoError(t, syncerDB.WriteSyncedL1Messages([]types.L1Message{*l1Message}, 0))

	// block 1
	require.NoError(t, ManualCreateBlock(node, 1))

	// check the result
	receipt, err := geth.EthClient.TransactionReceipt(context.Background(), eth.NewTx(&l1Message.L1MessageTx).Hash())
	require.NoError(t, err)
	require.EqualValues(t, 1, receipt.Status) // success
	// check if deposit success
	l2Messenger, err := bindings.NewL1CrossDomainMessenger(configs.L2CrossDomainMessengerAddress, geth.EthClient)
	require.NoError(t, err)
	versionedHash := crypto.Keccak256Hash(relayMessageInput)
	isSuccess, err := l2Messenger.SuccessfulMessages(nil, versionedHash)
	require.NoError(t, err)
	require.False(t, isSuccess, "the L1Message should be fail")
	isFailed, err := l2Messenger.FailedMessages(nil, versionedHash)
	require.NoError(t, err)
	require.True(t, isFailed, "the L1Message should be fail")
	receivedNonce, err := l2Messenger.ReceiveNonce(nil)
	require.NoError(t, err)
	require.EqualValues(t, types.EncodeNonce(uint64(0)).String(), receivedNonce.String())
	contractBalance, err := geth.EthClient.BalanceAt(context.Background(), configs.L2CrossDomainMessengerAddress, nil)
	require.NoError(t, err)
	require.EqualValues(t, l1Message.Value.String(), contractBalance.String())
	depositorBalance, err := geth.EthClient.BalanceAt(context.Background(), testingAddress2, nil)
	require.NoError(t, err)
	require.EqualValues(t, 0, depositorBalance.Uint64())

	// new 2nd message
	l1Message2, relayMessageInput2, err := MockL1Message(testingAddress2, testingAddress2, uint64(1), big.NewInt(1e18))
	require.NoError(t, err)
	require.NoError(t, syncerDB.WriteSyncedL1Messages([]types.L1Message{*l1Message2}, 1))
	// relay the last failed message
	bindContract := bind.NewBoundContract(configs.L2CrossDomainMessengerAddress, *sync.L2CrossDomainMessengerABI, geth.EthClient, geth.EthClient, geth.EthClient)
	transactOpts, err := bind.NewKeyedTransactorWithChainID(testingPrivKey, genesisConfig.ChainID)
	require.NoError(t, err)
	transactOpts.GasLimit = 500_000
	retryRelayMessageTx, err := bindContract.RawTransact(transactOpts, relayMessageInput)
	require.NoError(t, err)
	// block 2
	require.NoError(t, ManualCreateBlock(node, 2))
	// check the result
	receipt2, err := geth.EthClient.TransactionReceipt(context.Background(), eth.NewTx(&l1Message2.L1MessageTx).Hash())
	require.NoError(t, err)
	require.EqualValues(t, 2, receipt2.BlockNumber.Int64())
	require.EqualValues(t, 1, receipt2.Status)
	receipt, err = geth.EthClient.TransactionReceipt(context.Background(), retryRelayMessageTx.Hash())
	require.NoError(t, err)
	require.EqualValues(t, 2, receipt.BlockNumber.Int64())
	require.EqualValues(t, 1, receipt.Status) // success
	// check if deposit success
	versionedHash2 := crypto.Keccak256Hash(relayMessageInput2)
	isSuccess2, err := l2Messenger.SuccessfulMessages(nil, versionedHash2)
	require.NoError(t, err)
	require.True(t, isSuccess2, "the L1Message should be success")
	isSuccess, err = l2Messenger.SuccessfulMessages(nil, versionedHash)
	require.NoError(t, err)
	require.True(t, isSuccess, "the L1Message should be success")
	contractBalance, err = geth.EthClient.BalanceAt(context.Background(), configs.L2CrossDomainMessengerAddress, nil)
	require.NoError(t, err)
	require.EqualValues(t, 0, contractBalance.Int64())
	depositorBalance, err = geth.EthClient.BalanceAt(context.Background(), testingAddress2, nil)
	require.NoError(t, err)
	require.EqualValues(t, l1Message.Value.Uint64()+l1Message2.Value.Uint64(), depositorBalance.Uint64())
	receivedNonce, err = l2Messenger.ReceiveNonce(nil)
	require.NoError(t, err)
	require.EqualValues(t, types.EncodeNonce(uint64(1)).String(), receivedNonce.String())

	transactOpts.GasLimit = 0
	_, err = bindContract.RawTransact(transactOpts, relayMessageInput)
	require.ErrorContains(t, err, "message has already been relayed")
}
