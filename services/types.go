package services

type preprocessOptions struct {
	// The from public key is used to gather UTXOs in the metadata portion. This
	// obviates the need to rely on UTXOs in the Rosetta API, and supports a future
	// shift away from UTXOs to balance model.
	FromPublicKey string `json:"from_public_key"`

	// The outputs are used to compute the fee estimate in the metadata portion.
	DeSoOutputs []*desoOutput `json:"deso_outputs"`

	// Allow legacy manual selection of UTXOs
	LegacyUTXOSelection bool         `json:"legacy_utxo_selection"` // Deprecated
	DeSoInputs          []*desoInput `json:"deso_inputs"`           // Deprecated
}

type desoOutput struct {
	PublicKey   string
	AmountNanos uint64
}

type desoInput struct {
	// The 32-byte transaction id where the unspent output occurs.
	TxHex string
	// The index within the txn where the unspent output occurs.
	Index uint32
}

type constructionMetadata struct {
	FeePerKB uint64 `json:"fee_per_kb"`

	// This transaction is used to estimate the fee in the metadata portion. The inputs
	// on this transaction are also used in the offline portion in order to construct the
	// final transaction.
	DeSoSampleTxnHex string `json:"deso_sample_txn_hex"`

	// Allow legacy manual selection of UTXOs
	LegacyUTXOSelection bool `json:"legacy_utxo_selection"` // Deprecated
}

type transactionMetadata struct {
	Transaction  []byte   `json:"transaction"`
	InputAmounts []string `json:"input_amount"`

	// Allow legacy manual selection of UTXOs
	LegacyUTXOSelection bool `json:"legacy_utxo_selection"` // Deprecated
}

type amountMetadata struct {
	Confirmations uint64 `json:"confirmations"`
}

type amountMetadata struct {
	Confirmations uint64 `json:"confirmations"`
}
