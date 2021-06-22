package services

type preprocessOptions struct {
	// Estimated number of bytes the transaction can be with a signature and change address.
	// Used to provide a suggested fee to clients
	TransactionSizeEstimate uint64       `json:"transaction_size_estimate"`
}

type constructionMetadata struct {
	FeePerKB uint64       `json:"fee_per_kb"`
}

type transactionMetadata struct {
	Transaction    string                  `json:"transaction"`
	InputAmounts   []string                `json:"input_amounts"`
}

type amountMetadata struct {
	Confirmations uint64 `json:"confirmations"`
}