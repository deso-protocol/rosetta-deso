package services

import "github.com/deso-protocol/core/lib"

type preprocessOptions struct {
	// Used to provide a suggested fee to clients. Tells us how many UTXOs to select
	InputAmount uint64 `json:"input_amount"`
}

type constructionMetadata struct {
	FeePerKB uint64           `json:"fee_per_kb"`
	UTXOs    []*lib.UtxoEntry `json:"utxos"`
}

type transactionMetadata struct {
	Transaction  string   `json:"transaction"`
	InputAmounts []string `json:"input_amounts"`
}

type amountMetadata struct {
	Confirmations uint64 `json:"confirmations"`
}
