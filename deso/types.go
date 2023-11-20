package deso

import "github.com/coinbase/rosetta-sdk-go/types"

type Mode string
type Network string

const (
	InputOpType  = "INPUT"
	OutputOpType = "OUTPUT"

	Online  Mode = "ONLINE"
	Offline Mode = "OFFLINE"

	Mainnet Network = "MAINNET"
	Testnet Network = "TESTNET"

	// CreatorCoin is the SubAccount address for a public key's
	// total DESO locked in their creator coin.
	CreatorCoin = "CREATOR_COIN"

	// StakeEntry is the SubAccount address for a public key's
	// total DESO locked in their stake entry.
	StakeEntry = "STAKE_ENTRY"
)

var (
	Currency = types.Currency{
		Symbol:   "DESO",
		Decimals: 9,
	}

	OperationTypes = []string{
		InputOpType,
		OutputOpType,
	}

	SuccessStatus  = "SUCCESS"
	RevertedStatus = "REVERTED"

	MinFeeRateNanosPerKB = uint64(1000)
)
