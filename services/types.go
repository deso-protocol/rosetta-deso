package services

import "github.com/deso-protocol/core/lib"

type preprocessOptions struct {
	// Used to provide a suggested fee to clients. Tells us how many UTXOs to select
	InputAmount uint64 `json:"input_amount"`
	NumOutputs  uint64 `json:"num_outputs"`
}

type constructionMetadata struct {
	FeePerKB uint64           `json:"fee_per_kb"`
	UTXOs    []*lib.UtxoEntry `json:"utxos"`
	Change   uint64           `json:"change"`
}

type transactionMetadata struct {
	Transaction []byte `json:"transaction"`
	InputAmount string `json:"input_amount"`
}

type amountMetadata struct {
	Confirmations uint64 `json:"confirmations"`
}
