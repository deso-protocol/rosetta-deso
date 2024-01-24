package services

import (
	"context"
	"encoding/hex"
	"fmt"
	"github.com/btcsuite/btcd/btcec"
	"github.com/coinbase/rosetta-sdk-go/server"
	"github.com/coinbase/rosetta-sdk-go/types"
	"github.com/deso-protocol/core/lib"
	merkletree "github.com/deso-protocol/go-merkle-tree"
	"github.com/deso-protocol/rosetta-deso/deso"
	"github.com/pkg/errors"
	"math/big"
	"strconv"
)

const (
	BytesPerKb   = 1000
	MaxDERSigLen = 74

	// FeeByteBuffer adds a byte buffer to the length of the transaction when calculating the suggested fee.
	// We need this buffer because the size of the transaction can increase by a few bytes after
	// the preprocess step and before the combine step
	FeeByteBuffer = 2
)

type ConstructionAPIService struct {
	config *deso.Config
	node   *deso.Node
}

func NewConstructionAPIService(config *deso.Config, node *deso.Node) server.ConstructionAPIServicer {
	return &ConstructionAPIService{
		config: config,
		node:   node,
	}
}

func (s *ConstructionAPIService) ConstructionDerive(ctx context.Context, request *types.ConstructionDeriveRequest) (*types.ConstructionDeriveResponse, *types.Error) {
	return &types.ConstructionDeriveResponse{
		AccountIdentifier: &types.AccountIdentifier{
			Address: lib.Base58CheckEncode(request.PublicKey.Bytes, false, s.config.Params),
		},
	}, nil
}

func (s *ConstructionAPIService) ConstructionPreprocess(ctx context.Context, request *types.ConstructionPreprocessRequest) (*types.ConstructionPreprocessResponse, *types.Error) {
	// Rack up the public keys from the inputs so that we can compute the
	// corresponding metadata.
	//
	// Also compute the total input so that we can compute a suggested fee
	// in the metadata portion.
	optionsObj := &preprocessOptions{}
	inputPubKeysFoundMap := make(map[string]bool)
	for _, op := range request.Operations {
		if op.Type == deso.InputOpType {
			// Add the account identifier to our map
			inputPubKeysFoundMap[op.Account.Address] = true
		} else if op.Type == deso.OutputOpType {
			// Parse the amount of this output
			amount, err := strconv.ParseUint(op.Amount.Value, 10, 64)
			if err != nil {
				return nil, wrapErr(ErrUnableToParseIntermediateResult, err)
			}
			optionsObj.DeSoOutputs = append(optionsObj.DeSoOutputs, &desoOutput{
				PublicKey:   op.Account.Address,
				AmountNanos: amount,
			})
		}
	}

	// Exactly one public key is required, otherwise that's an error.
	if len(inputPubKeysFoundMap) != 1 {
		return nil, wrapErr(ErrUnableToParseIntermediateResult, fmt.Errorf(
			"Exactly one input public key is required but instead found: %v",
			inputPubKeysFoundMap))
	}

	// Set the from public key on the options
	for publicKey := range inputPubKeysFoundMap {
		optionsObj.FromPublicKey = publicKey
		break
	}

	var err error
	// Parse nonce and fee fields from the metadata
	optionsObj.NoncePartialID, err = CheckMetadataForAttributeAndParseUint64(request.Metadata, "nonce_partial_id")
	if err != nil {
		return nil, wrapErr(ErrUnableToParseIntermediateResult, err)
	}
	optionsObj.NonceExpirationBlockHeight, err = CheckMetadataForAttributeAndParseUint64(request.Metadata, "nonce_expiration_block_height")
	if err != nil {
		return nil, wrapErr(ErrUnableToParseIntermediateResult, err)
	}
	optionsObj.NonceExpirationBlockHeightOffset, err = CheckMetadataForAttributeAndParseUint64(request.Metadata, "nonce_expiration_block_height_offset")
	if err != nil {
		return nil, wrapErr(ErrUnableToParseIntermediateResult, err)
	}
	optionsObj.FeeRateNanosPerKB, err = CheckMetadataForAttributeAndParseUint64(request.Metadata, "fee_rate_nanos_per_kb")
	if err != nil {
		return nil, wrapErr(ErrUnableToParseIntermediateResult, err)
	}
	optionsObj.TxnFeeNanos, err = CheckMetadataForAttributeAndParseUint64(request.Metadata, "txn_fee_nanos")
	if err != nil {
		return nil, wrapErr(ErrUnableToParseIntermediateResult, err)
	}

	options, err := types.MarshalMap(optionsObj)
	if err != nil {
		return nil, wrapErr(ErrUnableToParseIntermediateResult, err)
	}

	return &types.ConstructionPreprocessResponse{
		Options: options,
	}, nil
}

func CheckMetadataForAttributeAndParseUint64(metadata map[string]interface{}, key string) (uint64, error) {
	value, exists := metadata[key]
	if !exists {
		return 0, nil
	}
	valueStr, ok := value.(string)
	if !ok {
		return 0, fmt.Errorf("%s is not a string", key)
	}
	parsedValue, err := strconv.ParseUint(valueStr, 10, 64)
	if err != nil {
		return 0, errors.Wrapf(err, "%s: %v", key, valueStr)
	}
	return parsedValue, nil
}

func (s *ConstructionAPIService) ConstructionMetadata(ctx context.Context, request *types.ConstructionMetadataRequest) (*types.ConstructionMetadataResponse, *types.Error) {
	if s.config.Mode != deso.Online {
		return nil, ErrUnavailableOffline
	}

	mempoolView, err := s.node.GetMempool().GetAugmentedUniversalView()
	if err != nil {
		return nil, wrapErr(ErrDeSo, err)
	}

	var options preprocessOptions
	if err := types.UnmarshalMap(request.Options, &options); err != nil {
		return nil, wrapErr(ErrUnableToParseIntermediateResult, err)
	}

	// Determine the network-wide feePerKB rate
	feePerKB := mempoolView.GlobalParamsEntry.MinimumNetworkFeeNanosPerKB
	if feePerKB == 0 {
		feePerKB = deso.MinFeeRateNanosPerKB
	}
	// If the caller specified a fee rate that is too low, return an error
	if options.FeeRateNanosPerKB > 0 && options.FeeRateNanosPerKB < feePerKB {
		return nil, ErrFeeRateBelowNetworkMinimum
	}
	// We only want to use the fee rate if it's higher than the network-wide
	// fee rate.
	if options.FeeRateNanosPerKB > feePerKB {
		feePerKB = options.FeeRateNanosPerKB
	}

	fullDeSoOutputs := []*lib.DeSoOutput{}
	for _, output := range options.DeSoOutputs {
		pkBytes, _, err := lib.Base58CheckDecode(output.PublicKey)
		if err != nil {
			return nil, wrapErr(ErrUnableToParseIntermediateResult, err)
		}
		fullDeSoOutputs = append(fullDeSoOutputs, &lib.DeSoOutput{
			PublicKey:   pkBytes,
			AmountNanos: output.AmountNanos,
		})
	}

	// Use the input amount to compute how many UTXOs will be needed
	fromPubKeyBytes, _, err := lib.Base58CheckDecode(options.FromPublicKey)
	if err != nil {
		return nil, wrapErr(ErrDeSo, err)
	}
	txn := &lib.MsgDeSoTxn{
		// The inputs will be set below.
		TxInputs:  []*lib.DeSoInput{},
		TxOutputs: fullDeSoOutputs,
		PublicKey: fromPubKeyBytes,
		TxnMeta:   &lib.BasicTransferMetadata{},
	}

	var fee uint64
	_, _, _, fee, err = s.node.GetBlockchain().AddInputsAndChangeToTransaction(txn, feePerKB, s.node.GetMempool())
	if err != nil {
		return nil, wrapErr(ErrInvalidTransaction, err)
	}

	// If the caller specified a partial ID, apply it to the transaction's nonce (as long as the nonce exists).
	if options.NoncePartialID > 0 && txn.TxnNonce != nil {
		txn.TxnNonce.PartialID = options.NoncePartialID
	}
	// Get the current max nonce expiration block height offset and current block height
	currentMaxExpirationBlockHeightOffset := uint64(lib.DefaultMaxNonceExpirationBlockHeightOffset)
	if mempoolView.GlobalParamsEntry.MaxNonceExpirationBlockHeightOffset > 0 {
		currentMaxExpirationBlockHeightOffset = mempoolView.GlobalParamsEntry.MaxNonceExpirationBlockHeightOffset
	}
	currentBlockHeight := uint64(s.node.GetBlockchain().BlockTip().Height)
	// If the caller specified a expiration block height offset,
	// validate it and apply it to the transaction's nonce (as long as the nonce exists).
	if options.NonceExpirationBlockHeightOffset > 0 && txn.TxnNonce != nil {
		if options.NonceExpirationBlockHeightOffset > currentMaxExpirationBlockHeightOffset {
			return nil, ErrNonceExpirationBlockHeightOffsetTooLarge
		}
		txn.TxnNonce.ExpirationBlockHeight = currentBlockHeight + options.NonceExpirationBlockHeightOffset
	} else if options.NonceExpirationBlockHeight > 0 && txn.TxnNonce != nil {
		// If the caller specified a expiration block height,
		// validate it and apply it to the transaction's nonce (as long as the nonce exists).
		if options.NonceExpirationBlockHeight < currentBlockHeight {
			return nil, ErrNonceExpirationBlockHeightTooLow
		}
		if options.NonceExpirationBlockHeight > currentBlockHeight+currentMaxExpirationBlockHeightOffset {
			return nil, ErrNonceExpirationBlockHeightTooHigh
		}
		txn.TxnNonce.ExpirationBlockHeight = options.NonceExpirationBlockHeight
	}

	// If the caller specified a fee, validate it and apply it to the transaction.
	if options.TxnFeeNanos > 0 {
		if txn.TxnFeeNanos > options.TxnFeeNanos {
			return nil, ErrFeeTooLow
		}
		txn.TxnFeeNanos = options.TxnFeeNanos
		fee = options.TxnFeeNanos
	}

	desoTxnBytes, err := txn.ToBytes(true)
	if err != nil {
		return nil, wrapErr(ErrInvalidTransaction, err)
	}

	metadata, err := types.MarshalMap(&constructionMetadata{
		FeePerKB:                         feePerKB,
		DeSoSampleTxnHex:                 hex.EncodeToString(desoTxnBytes),
		NoncePartialID:                   options.NoncePartialID,
		NonceExpirationBlockHeight:       options.NonceExpirationBlockHeight,
		NonceExpirationBlockHeightOffset: options.NonceExpirationBlockHeightOffset,
		TxnFeeNanos:                      options.TxnFeeNanos,
		FeeRateNanosPerKB:                options.FeeRateNanosPerKB,
	})
	if err != nil {
		return nil, wrapErr(ErrUnableToParseIntermediateResult, err)
	}

	return &types.ConstructionMetadataResponse{
		Metadata: metadata,
		SuggestedFee: []*types.Amount{
			{
				Value:    strconv.FormatUint(fee, 10),
				Currency: &deso.Currency,
			},
		},
	}, nil
}

func (s *ConstructionAPIService) ConstructionPayloads(ctx context.Context, request *types.ConstructionPayloadsRequest) (*types.ConstructionPayloadsResponse, *types.Error) {
	var metadata constructionMetadata
	if err := types.UnmarshalMap(request.Metadata, &metadata); err != nil {
		return nil, wrapErr(ErrUnableToParseIntermediateResult, err)
	}

	var inputAmounts []string
	var signingAccount *types.AccountIdentifier
	for _, operation := range request.Operations {
		if operation.Type == deso.InputOpType {
			inputAmounts = append(inputAmounts, operation.Amount.Value)
			// Assigning multiple times is OK because all the inputs have
			// been checked to have the same account at this point.
			signingAccount = operation.Account
		}
	}

	desoTxnBytes, err := hex.DecodeString(metadata.DeSoSampleTxnHex)
	if err != nil {
		return nil, wrapErr(ErrInvalidTransaction, err)
	}

	// We should only have one input with the balance model
	if len(inputAmounts) != 1 {
		return nil, wrapErr(ErrInvalidTransaction, fmt.Errorf("Txn must have exactly one input but found %v", len(inputAmounts)))
	}

	unsignedBytes := merkletree.Sha256DoubleHash(desoTxnBytes)

	return &types.ConstructionPayloadsResponse{
		UnsignedTransaction: hex.EncodeToString(desoTxnBytes),
		Payloads: []*types.SigningPayload{
			{
				AccountIdentifier: signingAccount,
				Bytes:             unsignedBytes,
				SignatureType:     types.Ecdsa,
			},
		},
	}, nil
}

func (s *ConstructionAPIService) ConstructionCombine(ctx context.Context, request *types.ConstructionCombineRequest) (*types.ConstructionCombineResponse, *types.Error) {
	unsignedTxnBytes, err := hex.DecodeString(request.UnsignedTransaction)
	if err != nil {
		return nil, wrapErr(ErrInvalidTransaction, err)
	}

	desoTxn := &lib.MsgDeSoTxn{}
	if err = desoTxn.FromBytes(unsignedTxnBytes); err != nil {
		return nil, wrapErr(ErrInvalidTransaction, err)
	}

	// signature is in form of R || S
	signatureBytes := request.Signatures[0].Bytes
	desoTxn.Signature.SetSignature(&btcec.Signature{
		R: new(big.Int).SetBytes(signatureBytes[:32]),
		S: new(big.Int).SetBytes(signatureBytes[32:64]),
	})

	signedTxnBytes, err := desoTxn.ToBytes(false)
	if err != nil {
		return nil, wrapErr(ErrInvalidTransaction, err)
	}

	return &types.ConstructionCombineResponse{
		SignedTransaction: hex.EncodeToString(signedTxnBytes),
	}, nil
}

func (s *ConstructionAPIService) ConstructionHash(ctx context.Context, request *types.ConstructionHashRequest) (*types.TransactionIdentifierResponse, *types.Error) {
	txnBytes, err := hex.DecodeString(request.SignedTransaction)
	if err != nil {
		return nil, wrapErr(ErrInvalidTransaction, err)
	}

	txn := &lib.MsgDeSoTxn{}
	if err = txn.FromBytes(txnBytes); err != nil {
		return nil, wrapErr(ErrInvalidTransaction, err)
	}

	return &types.TransactionIdentifierResponse{
		TransactionIdentifier: &types.TransactionIdentifier{
			Hash: txn.Hash().String(),
		},
	}, nil
}

func (s *ConstructionAPIService) ConstructionParse(ctx context.Context, request *types.ConstructionParseRequest) (*types.ConstructionParseResponse, *types.Error) {
	txnBytes, err := hex.DecodeString(request.Transaction)
	if err != nil {
		return nil, wrapErr(ErrInvalidTransaction, err)
	}

	desoTxn := &lib.MsgDeSoTxn{}
	if err = desoTxn.FromBytes(txnBytes); err != nil {
		return nil, wrapErr(ErrInvalidTransaction, err)
	}

	signer := &types.AccountIdentifier{
		Address: lib.Base58CheckEncode(desoTxn.PublicKey, false, s.node.Params),
	}

	numOps := int64(0)
	totalInput := desoTxn.TxnFeeNanos
	for _, output := range desoTxn.TxOutputs {
		totalInput += output.AmountNanos
	}
	var operations []*types.Operation
	operations = append(operations, &types.Operation{
		OperationIdentifier: &types.OperationIdentifier{
			Index: numOps,
		},

		Account: &types.AccountIdentifier{
			Address: lib.Base58CheckEncode(desoTxn.PublicKey, false, s.node.Params),
		},

		Amount: &types.Amount{
			Value:    strconv.FormatInt(int64(totalInput)*-1, 10),
			Currency: &deso.Currency,
		},

		Type: deso.InputOpType,
	})
	numOps += 1
	for _, output := range desoTxn.TxOutputs {
		totalInput += output.AmountNanos
		op := &types.Operation{
			OperationIdentifier: &types.OperationIdentifier{
				Index: numOps,
			},

			Account: &types.AccountIdentifier{
				Address: lib.Base58CheckEncode(output.PublicKey, false, s.node.Params),
			},

			Amount: &types.Amount{
				Value:    strconv.FormatUint(output.AmountNanos, 10),
				Currency: &deso.Currency,
			},

			Type: deso.OutputOpType,
		}

		numOps += 1
		operations = append(operations, op)
	}

	if request.Signed {
		return &types.ConstructionParseResponse{
			Operations:               operations,
			AccountIdentifierSigners: []*types.AccountIdentifier{signer},
		}, nil
	} else {
		return &types.ConstructionParseResponse{
			Operations:               operations,
			AccountIdentifierSigners: nil,
		}, nil
	}

}

func (s *ConstructionAPIService) ConstructionSubmit(ctx context.Context, request *types.ConstructionSubmitRequest) (*types.TransactionIdentifierResponse, *types.Error) {
	if s.config.Mode != deso.Online {
		return nil, ErrUnavailableOffline
	}

	txnBytes, err := hex.DecodeString(request.SignedTransaction)
	if err != nil {
		return nil, wrapErr(ErrInvalidTransaction, err)
	}

	desoTxn := &lib.MsgDeSoTxn{}
	if err = desoTxn.FromBytes(txnBytes); err != nil {
		return nil, wrapErr(ErrInvalidTransaction, err)
	}

	if err := s.node.VerifyAndBroadcastTransaction(desoTxn); err != nil {
		return nil, wrapErr(ErrDeSo, err)
	}

	return &types.TransactionIdentifierResponse{
		TransactionIdentifier: &types.TransactionIdentifier{
			Hash: desoTxn.Hash().String(),
		},
	}, nil
}
