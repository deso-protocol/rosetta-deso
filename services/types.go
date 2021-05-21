package services

type constructionMetadata struct {
	FeePerKB uint64       `json:"fee_per_kb"`
}

type transactionMetadata struct {
	Transaction    string                  `json:"transaction"`
	InputAmounts   []string                `json:"input_amounts"`
}
