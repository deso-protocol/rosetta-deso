package deso

import "github.com/coinbase/rosetta-sdk-go/types"

// Errors comprises all the errors returned by the deso package in
// this rosetta implementation. Codes are reserved in the range 100-200.
var (
	Errors = []*types.Error{
		ErrBlockHeightTooHigh,
		ErrBlockHeightMismatch,
		ErrCannotDecodeBlockHash,
		ErrBlockNodeNotFound,
		ErrProblemFetchingUtxoOpsForBlock,
		ErrCannotHashBlock,
	}
	ErrBlockHeightTooHigh = &types.Error{
		Code:    100,
		Message: "Block height is higher than the current tip",
	}

	ErrBlockHeightMismatch = &types.Error{
		Code:    101,
		Message: "Block at index does not have the expected height",
	}

	ErrCannotDecodeBlockHash = &types.Error{
		Code:    102,
		Message: "Cannot decode block hash",
	}

	ErrBlockNodeNotFound = &types.Error{
		Code:    103,
		Message: "Block node not found",
	}

	ErrProblemFetchingUtxoOpsForBlock = &types.Error{
		Code:    104,
		Message: "Problem fetching UTXO operations for block",
	}

	ErrCannotHashBlock = &types.Error{
		Code:    105,
		Message: "Cannot hash block",
	}

	ErrMsgDesoBlockNotFound = &types.Error{
		Code:    106,
		Message: "Msg DeSo Block not found",
	}

	ErrCommittedTipNotFound = &types.Error{
		Code:    107,
		Message: "Committed tip not found",
	}

	ErrBlockIsNotCommitted = &types.Error{
		Code:    108,
		Message: "Block is not committed",
	}
)
