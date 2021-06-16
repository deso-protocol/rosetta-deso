package services

type preprocessOptions struct {
	// Maximum number of bytes the transaction can be with a signature. Used for fee calculation
	MaxTransactionSize uint64       `json:"max_transaction_size"`
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