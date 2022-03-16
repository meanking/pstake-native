package types_test

import (
	"testing"

	"github.com/stretchr/testify/suite"

	sdk "github.com/cosmos/cosmos-sdk/types"
	farmingtypes "github.com/crescent-network/crescent/x/farming/types"

	"github.com/crescent-network/crescent/x/liquidstaking/types"
)

type keysTestSuite struct {
	suite.Suite
}

func TestKeysTestSuite(t *testing.T) {
	suite.Run(t, new(keysTestSuite))
}

func (s *keysTestSuite) TestGetLiquidValidatorKey() {
	// 20 bytes length addr
	valOper1 := farmingtypes.DeriveAddress(farmingtypes.AddressType20Bytes, types.ModuleName, "valoper-tc1")
	valAddr1 := sdk.ValAddress(valOper1.Bytes())
	lv1 := types.LiquidValidator{
		OperatorAddress: valAddr1.String(),
	}

	// 32 bytes length addr
	valOper2 := farmingtypes.DeriveAddress(farmingtypes.AddressType32Bytes, types.ModuleName, "valoper-tc2")
	valAddr2 := sdk.ValAddress(valOper2.Bytes())
	lv2 := types.LiquidValidator{
		OperatorAddress: valAddr2.String(),
	}

	s.Require().Equal([]byte{0xc0, 0x14, 0xa5, 0x9a, 0x97, 0x60, 0x57, 0xe, 0xf1, 0x80, 0x8f, 0x36, 0xbf, 0xc4, 0x36, 0x5a, 0xe2, 0x71, 0x54, 0x5a, 0xbf, 0xf1}, types.GetLiquidValidatorKey(lv1.GetOperator()))
	s.Require().Equal([]byte{0xc0, 0x14, 0xa5, 0x9a, 0x97, 0x60, 0x57, 0xe, 0xf1, 0x80, 0x8f, 0x36, 0xbf, 0xc4, 0x36, 0x5a, 0xe2, 0x71, 0x54, 0x5a, 0xbf, 0xf1}, types.GetLiquidValidatorKey(valAddr1))
	s.Require().Equal([]byte{0xc0, 0x20, 0x37, 0xa2, 0x82, 0x32, 0xfe, 0xaf, 0x6f, 0x5, 0xd, 0x65, 0xc0, 0x6, 0x19, 0x5a, 0xd6, 0xf5, 0x67, 0x81, 0x39, 0x21, 0x9c, 0x2c, 0xc8, 0x8f, 0x2, 0xdc, 0x12, 0xfd, 0xeb, 0xb2, 0xa3, 0x6d}, types.GetLiquidValidatorKey(lv2.GetOperator()))
	s.Require().Equal([]byte{0xc0, 0x20, 0x37, 0xa2, 0x82, 0x32, 0xfe, 0xaf, 0x6f, 0x5, 0xd, 0x65, 0xc0, 0x6, 0x19, 0x5a, 0xd6, 0xf5, 0x67, 0x81, 0x39, 0x21, 0x9c, 0x2c, 0xc8, 0x8f, 0x2, 0xdc, 0x12, 0xfd, 0xeb, 0xb2, 0xa3, 0x6d}, types.GetLiquidValidatorKey(valAddr2))
}
