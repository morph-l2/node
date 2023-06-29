package e2e

import (
	"encoding/json"
	"github.com/scroll-tech/go-ethereum/accounts/abi/bind"
	"github.com/scroll-tech/go-ethereum/common"
	"github.com/scroll-tech/go-ethereum/common/hexutil"
	"github.com/scroll-tech/go-ethereum/core"
	eth "github.com/scroll-tech/go-ethereum/core/types"
	"github.com/scroll-tech/go-ethereum/crypto"
	"github.com/scroll-tech/go-ethereum/params"
	"github.com/stretchr/testify/require"
	"math/big"
	"os"
	"path"
	"testing"
)

var (
	testingJWTSecret   = [32]byte{123}
	defaultGenesisPath = "configs/genesis_tiny.json"
	FullGenesisPath    = "configs/genesis.json"
	defaultEtherbase   = common.HexToAddress("0xca062B0Fd91172d89BcD4Bb084ac4e21972CC467")

	testingAddress    = common.HexToAddress("0x2f607b4bFd09a205531059de0Ee29fa9F6C6E012")
	testingPrivKey    = crypto.ToECDSAUnsafe(hexutil.MustDecode("0xc716c90304d615e9280e79c2ed9bb0fc5ac435fba331a4565038462c2566e2d7"))
	genesisBalance, _ = new(big.Int).SetString("2000000000000000000000", 10)

	testingAddress2 = common.HexToAddress("0x3784D658390e5331ba52A2bF92503b5B3C84B6cB")
)

func writeDefaultJWT(t *testing.T) string {
	// Sadly the geth node config cannot load JWT secret from memory, it has to be a file
	jwtPath := path.Join(t.TempDir(), "jwt_secret")
	if err := os.WriteFile(jwtPath, []byte(hexutil.Encode(testingJWTSecret[:])), 0600); err != nil {
		t.Fatalf("failed to prepare jwt file for geth: %v", err)
	}
	return jwtPath
}

type SystemConfig struct {
	JwtPath  string
	JwtToken [32]byte
	Genesis  *core.Genesis
}

type Option func(config *SystemConfig) error

func NewSystemConfig(t *testing.T, opts ...Option) *SystemConfig {
	config := DefaultSystemConfig(t)
	for _, opt := range opts {
		if opt != nil {
			require.NoError(t, opt(config))
		}
	}
	// always initializes testingAddress balance
	config.Genesis.Alloc[testingAddress] = core.GenesisAccount{Balance: genesisBalance}

	return config
}

func GenesisFromPath(path string) (*core.Genesis, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	genesis := new(core.Genesis)
	if err = json.NewDecoder(file).Decode(genesis); err != nil {
		return nil, err
	}
	return genesis, nil
}

func DefaultSystemConfig(t *testing.T) *SystemConfig {
	genesis, err := GenesisFromPath(defaultGenesisPath)
	require.NoError(t, err)

	if genesis.Config.Scroll.MaxTxPerBlock == nil {
		genesis.Config.Scroll.MaxTxPerBlock = &params.ScrollMaxTxPerBlock
	}
	if genesis.Config.Scroll.MaxTxPayloadBytesPerBlock == nil {
		genesis.Config.Scroll.MaxTxPayloadBytesPerBlock = &params.ScrollMaxTxPayloadBytesPerBlock
	}
	return &SystemConfig{
		JwtPath:  writeDefaultJWT(t),
		JwtToken: testingJWTSecret,
		Genesis:  genesis,
	}
}

func SimpleTransfer(geth Geth) (*eth.Transaction, error) {
	transactOpts, err := bind.NewKeyedTransactorWithChainID(testingPrivKey, geth.Backend.BlockChain().Config().ChainID)
	if err != nil {
		return nil, err
	}
	transactOpts.Value = big.NewInt(1e18)
	return geth.Transfer(transactOpts, testingAddress2)
}
