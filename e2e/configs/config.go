package configs

import (
	"github.com/scroll-tech/go-ethereum/common"
	"math/big"
)

func init() {
	alias := new(big.Int).Add(new(big.Int).SetBytes(L1CrossDomainMessengerAddress.Bytes()), new(big.Int).SetBytes(AliasOffset.Bytes()))
	L1CrossDomainMessengerAddressAlias = common.BytesToAddress(alias.Bytes())
}

var (
	AliasOffset                        = common.HexToAddress("0x1111000000000000000000000000000000001111")
	L1StandardBridgeAddress            = common.HexToAddress("0x6900000000000000000000000000000000000003")
	L1CrossDomainMessengerAddress      = common.HexToAddress("0x6900000000000000000000000000000000000002")
	L1CrossDomainMessengerAddressAlias common.Address

	L2StandardBridgeAddress       = common.HexToAddress("0x4200000000000000000000000000000000000010")
	L2CrossDomainMessengerAddress = common.HexToAddress("0x4200000000000000000000000000000000000007")

	DepositETHGasLimit = big.NewInt(200_000)
)
