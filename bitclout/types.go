package bitclout

import "github.com/coinbase/rosetta-sdk-go/types"

const (
	InputOpType  = "INPUT"
	OutputOpType = "OUTPUT"
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
)
