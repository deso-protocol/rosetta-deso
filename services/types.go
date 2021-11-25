package services

import "github.com/deso-protocol/core/lib"

type preprocessOptions struct {
	// The from public key is used to gather UTXOs in the metadata portion. This
	// obviates the need to rely on UTXOs in the Rosetta API, and supports a future
	// shift away from UTXOs to balance model.
	FromPublicKey string `json:"from_public_key"`
	// The outputs are used to compute the fee estimate in the metadata portion.
	DeSoOutputs []*lib.DeSoOutput `json:"deso_outputs"`
}

type constructionMetadata struct {
	FeePerKB uint64       `json:"fee_per_kb"`
	// This transaction is used to estimate the fee in the metadata portion. The inputs
	// on this transaction are also used in the offline portion in order to construct the
	// final transaction.
	DeSoSampleTxn *lib.MsgDeSoTxn `json:"deso_sample_txn"`
}

type transactionMetadata struct {
	Transaction    string                  `json:"transaction"`
	InputAmounts   []string                `json:"input_amounts"`
}

type amountMetadata struct {
	Confirmations uint64 `json:"confirmations"`
}
