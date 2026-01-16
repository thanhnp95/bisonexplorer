package dcrpg

import (
	"github.com/btcsuite/btcd/btcutil"
	"github.com/decred/dcrd/dcrutil/v4"
	"github.com/decred/dcrdata/v8/mutilchain"
	"github.com/decred/dcrdata/v8/utils"
	"github.com/ltcsuite/ltcd/ltcutil"
)

func AmountToCoin(chainType string, amount int64) float64 {
	switch chainType {
	case mutilchain.TYPEBTC:
		return btcutil.Amount(amount).ToBTC()
	case mutilchain.TYPELTC:
		return ltcutil.Amount(amount).ToBTC()
	case mutilchain.TYPEDCR:
		return dcrutil.Amount(amount).ToCoin()
	case mutilchain.TYPEXMR:
		return utils.AtomicToXMR(uint64(amount))
	default:
		return 0
	}
}
