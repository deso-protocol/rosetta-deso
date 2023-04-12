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

	// Values used to set TxnNonce. Note that only one of
	// NonceExpirationBlockHeight and NonceExpirationBlockBuffer
	// should be specified. If both are specified,
	// NonceExpirationBlockBuffer is used.
	NoncePartialID             uint64 `json:"nonce_partial_id"`
	NonceExpirationBlockHeight uint64 `json:"nonce_expiration_block_height"`
	NonceExpirationBlockBuffer uint64 `json:"nonce_expiration_block_buffer"`

	// Values used to specify the fee for a transaction. Note that
	// only one of FeeRateNanosPerKB and TxnFeeNanos should be specified.
	// If both are specified, FeeRateNanosPerKB is used.
	FeeRateNanosPerKB uint64 `json:"fee_rate_nanos_per_kb"`
	TxnFeeNanos       uint64 `json:"txn_fee_nanos"`
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

	// Values used to set TxnNonce. Note that only one of
	// NonceExpirationBlockHeight and NonceExpirationBlockBuffer
	// should be specified. If both are specified,
	// NonceExpirationBlockBuffer is used.
	NoncePartialID             uint64 `json:"nonce_partial_id"`
	NonceExpirationBlockHeight uint64 `json:"nonce_expiration_block_height"`
	NonceExpirationBlockBuffer uint64 `json:"nonce_expiration_block_buffer"`

	// Values used to specify the fee for a transaction. Note that
	// only one of FeeRateNanosPerKB and TxnFeeNanos should be specified.
	// If both are specified, FeeRateNanosPerKB is used.
	FeeRateNanosPerKB uint64 `json:"fee_rate_nanos_per_kb"`
	TxnFeeNanos       uint64 `json:"txn_fee_nanos"`
}

type transactionMetadata struct {
	Transaction  []byte   `json:"transaction"`
	InputAmounts []string `json:"input_amount"`

	// Allow legacy manual selection of UTXOs
	LegacyUTXOSelection bool `json:"legacy_utxo_selection"` // Deprecated

	// Values used to set TxnNonce. Note that only one of
	// NonceExpirationBlockHeight and NonceExpirationBlockBuffer
	// should be specified. If both are specified,
	// NonceExpirationBlockBuffer is used.
	NoncePartialID             uint64 `json:"nonce_partial_id"`
	NonceExpirationBlockHeight uint64 `json:"nonce_expiration_block_height"`
	NonceExpirationBlockBuffer uint64 `json:"nonce_expiration_block_buffer"`

	// Values used to specify the fee for a transaction. Note that
	// only one of FeeRateNanosPerKB and TxnFeeNanos should be specified.
	// If both are specified, FeeRateNanosPerKB is used.
	FeeRateNanosPerKB uint64 `json:"fee_rate_nanos_per_kb"`
	TxnFeeNanos       uint64 `json:"txn_fee_nanos"`
}

type amountMetadata struct {
	Confirmations uint64 `json:"confirmations"`
}
