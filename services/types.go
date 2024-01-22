package services

type preprocessOptions struct {
	// The from public key is used to gather UTXOs in the metadata portion. This
	// obviates the need to rely on UTXOs in the Rosetta API, and supports a future
	// shift away from UTXOs to balance model.
	FromPublicKey string `json:"from_public_key"`

	// The outputs are used to compute the fee estimate in the metadata portion.
	DeSoOutputs []*desoOutput `json:"deso_outputs"`

	// Values used to set TxnNonce. Note that only one of
	// NonceExpirationBlockHeight and NonceExpirationBlockHeightOffset
	// should be specified. If both are specified,

	// NonceExpirationBlockHeightOffset is used.
	// NoncePartialID allows for specifying the partial ID used in the transaction's nonce.
	NoncePartialID uint64 `json:"nonce_partial_id"`
	// NonceExpirationBlockHeight allows for specifying the expiration block height used in
	// the transaction's nonce. Must be greater than the current block height and less than
	// the current block height plus the current network
	NonceExpirationBlockHeight uint64 `json:"nonce_expiration_block_height"`
	// NonceExpirationBlockHeightOffset allows for specifying the offset used to compute the
	// expiration block height in the transaction's nonce. This takes priority over
	// nonce_expiration_block_height. The expiration block height of the nonce will be set to
	// the current block height plus this value. Must be less than the current network
	// MaxNonceExpirationBlockHeightOffset value.
	NonceExpirationBlockHeightOffset uint64 `json:"nonce_expiration_block_height_offset"`

	// Values used to specify the fee for a transaction. Note that
	// only one of FeeRateNanosPerKB and TxnFeeNanos should be specified.

	// FeeRateNanosPerKB allows for specifying a fee rate that differs from the network minimum.
	// Must be greater than the network minimum.
	FeeRateNanosPerKB uint64 `json:"fee_rate_nanos_per_kb"`
	// TxnFeeNanos allows for explicitly specify the fee nanos field on a transaction.
	// Must be greater than the fee nanos values computed when constructing the transaction.
	TxnFeeNanos uint64 `json:"txn_fee_nanos"`
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

	// Values used to set TxnNonce. Note that only one of
	// NonceExpirationBlockHeight and NonceExpirationBlockHeightOffset
	// should be specified. If both are specified,
	// NonceExpirationBlockHeightOffset is used.
	NoncePartialID                   uint64 `json:"nonce_partial_id"`
	NonceExpirationBlockHeight       uint64 `json:"nonce_expiration_block_height"`
	NonceExpirationBlockHeightOffset uint64 `json:"nonce_expiration_block_height_offset"`

	// Values used to specify the fee for a transaction. Note that
	// only one of FeeRateNanosPerKB and TxnFeeNanos should be specified.
	// If both are specified, FeeRateNanosPerKB is used.
	FeeRateNanosPerKB uint64 `json:"fee_rate_nanos_per_kb"`
	TxnFeeNanos       uint64 `json:"txn_fee_nanos"`
}

type amountMetadata struct {
	Confirmations uint64 `json:"confirmations"`
}
