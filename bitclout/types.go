package bitclout

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
)

var (
	Currency = types.Currency{
		Symbol:   "CLOUT",
		Decimals: 9,
	}

	OperationTypes = []string{
		InputOpType,
		OutputOpType,
	}

	SuccessStatus = "SUCCESS"
	RevertedStatus = "REVERTED"

	MinFeeRateNanosPerKB = uint64(1000)
)
