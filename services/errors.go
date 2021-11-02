package services

import (
	"github.com/coinbase/rosetta-sdk-go/types"
)

var (
	Errors = []*types.Error{
		ErrUnimplemented,
		ErrUnavailableOffline,
		ErrNotReady,
		ErrDeSo,
		ErrUnableToParseIntermediateResult,
		ErrUnableToGetCoins,
	}

	ErrUnimplemented = &types.Error{
		Code:    0,
		Message: "Endpoint not implemented",
	}

	ErrUnavailableOffline = &types.Error{
		Code:    1,
		Message: "Endpoint unavailable offline",
	}

	ErrNotReady = &types.Error{
		Code:      2,
		Message:   "DeSo node is not ready",
		Retriable: true,
	}

	ErrDeSo = &types.Error{
		Code:    3,
		Message: "DeSo node error",
	}

	ErrUnableToParseIntermediateResult = &types.Error{
		Code:    4,
		Message: "Unable to parse intermediate result",
	}

	ErrUnableToGetCoins = &types.Error{
		Code:    5,
		Message: "Unable to get coins",
	}

	ErrBlockNotFound = &types.Error{
		Code:    6,
		Message: "Block not found",
	}

	ErrMultipleSigners = &types.Error{
		Code:    7,
		Message: "A transaction can only have one signer",
	}

	ErrInvalidPublicKey = &types.Error{
		Code:    8,
		Message: "Unable to parse public key",
	}

	ErrInvalidCoin = &types.Error{
		Code:    9,
		Message: "Unable to parse coin",
	}

	ErrInvalidTransaction = &types.Error{
		Code:    10,
		Message: "Unable to parse transaction",
	}
)

// wrapErr adds details to the types.Error provided. We use a function
// to do this so that we don't accidentially overrwrite the standard
// errors.
func wrapErr(rErr *types.Error, err error) *types.Error {
	newErr := &types.Error{
		Code:      rErr.Code,
		Message:   rErr.Message,
		Retriable: rErr.Retriable,
	}
	if err != nil {
		newErr.Details = map[string]interface{}{
			"context": err.Error(),
		}
	}

	return newErr
}
