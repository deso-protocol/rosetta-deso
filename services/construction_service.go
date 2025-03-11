package services

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/coinbase/rosetta-sdk-go/server"
	"github.com/coinbase/rosetta-sdk-go/types"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"github.com/deso-protocol/core/lib"
	merkletree "github.com/deso-protocol/go-merkle-tree"
	"github.com/deso-protocol/rosetta-deso/deso"
	"github.com/pkg/errors"
	"reflect"
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

			if request.Metadata != nil {
				if useLegacyUtxos, exists := request.Metadata["legacy_utxo_selection"]; exists && useLegacyUtxos.(bool) {
					txId, txnIndex, err := ParseCoinIdentifier(op.CoinChange.CoinIdentifier)
					if err != nil {
						return nil, wrapErr(ErrInvalidCoin, err)
					}

					// Include the inputs in case we use legacy utxo selection
					optionsObj.DeSoInputs = append(optionsObj.DeSoInputs, &desoInput{
						TxHex: hex.EncodeToString(txId.ToBytes()),
						Index: txnIndex,
					})
				}
			}
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
	if err = types.UnmarshalMap(request.Options, &options); err != nil {
		return nil, wrapErr(ErrUnableToParseIntermediateResult, err)
	}

	if options.LegacyUTXOSelection &&
		s.node.GetBlockchain().BlockTip().Height >= s.node.Params.ForkHeights.BalanceModelBlockHeight {
		return nil, ErrLegacyUtxoSelectionNotAllowed
	}

	// Determine the network-wide feePerKB rate
	feePerKB := mempoolView.GetCurrentGlobalParamsEntry().MinimumNetworkFeeNanosPerKB
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

	// Support legacy utxo selection
	if options.LegacyUTXOSelection {
		txn.TxInputs = []*lib.DeSoInput{}
		for _, input := range options.DeSoInputs {
			txId, err := hex.DecodeString(input.TxHex)
			if err != nil {
				return nil, wrapErr(ErrInvalidTransaction, err)
			}

			txn.TxInputs = append(txn.TxInputs, &lib.DeSoInput{
				TxID:  *lib.NewBlockHash(txId),
				Index: input.Index,
			})
		}

		txnBytes, err := txn.ToBytes(true)
		if err != nil {
			return nil, wrapErr(ErrInvalidTransaction, err)
		}

		// Override fee calculation
		txnSize := uint64(len(txnBytes) + MaxDERSigLen + FeeByteBuffer)
		fee = feePerKB * txnSize / BytesPerKb
	} else {
		_, _, _, fee, err = s.node.GetBlockchain().AddInputsAndChangeToTransaction(txn, feePerKB, s.node.GetMempool())
		if err != nil {
			return nil, wrapErr(ErrInvalidTransaction, err)
		}
	}

	// If the caller specified a partial ID, apply it to the transaction's nonce (as long as the nonce exists).
	if options.NoncePartialID > 0 && txn.TxnNonce != nil {
		txn.TxnNonce.PartialID = options.NoncePartialID
	}
	// Get the current max nonce expiration block height offset and current block height
	currentMaxExpirationBlockHeightOffset := uint64(lib.DefaultMaxNonceExpirationBlockHeightOffset)
	if mempoolView.GetCurrentGlobalParamsEntry().MaxNonceExpirationBlockHeightOffset > 0 {
		currentMaxExpirationBlockHeightOffset = mempoolView.GetCurrentGlobalParamsEntry().MaxNonceExpirationBlockHeightOffset
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
		LegacyUTXOSelection:              options.LegacyUTXOSelection,
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
	// TODO: This check is disabled to support legacy utxo selection
	//if len(inputAmounts) != 1 {
	//	return nil, wrapErr(ErrInvalidTransaction, fmt.Errorf("Txn must have exactly one input but found %v", len(inputAmounts)))
	//}

	unsignedBytes := merkletree.Sha256DoubleHash(desoTxnBytes)

	unsignedTxn, err := json.Marshal(&transactionMetadata{
		Transaction:                      desoTxnBytes,
		InputAmounts:                     inputAmounts,
		LegacyUTXOSelection:              metadata.LegacyUTXOSelection,
		NoncePartialID:                   metadata.NoncePartialID,
		NonceExpirationBlockHeight:       metadata.NonceExpirationBlockHeight,
		NonceExpirationBlockHeightOffset: metadata.NonceExpirationBlockHeightOffset,
		TxnFeeNanos:                      metadata.TxnFeeNanos,
		FeeRateNanosPerKB:                metadata.FeeRateNanosPerKB,
	})

	return &types.ConstructionPayloadsResponse{
		UnsignedTransaction: hex.EncodeToString(unsignedTxn),
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

	var unsignedTxn transactionMetadata
	if err := json.Unmarshal(unsignedTxnBytes, &unsignedTxn); err != nil {
		return nil, wrapErr(ErrInvalidTransaction, err)
	}

	desoTxn := &lib.MsgDeSoTxn{}
	if err = desoTxn.FromBytes(unsignedTxn.Transaction); err != nil {
		return nil, wrapErr(ErrInvalidTransaction, err)
	}

	// signature is in form of R || S
	signatureBytes := request.Signatures[0].Bytes
	var rBytes [32]byte
	copy(rBytes[:], signatureBytes[:32])
	sigR := secp256k1.ModNScalar{}
	sigR.SetBytes(&rBytes)
	var sBytes [32]byte
	copy(sBytes[:], signatureBytes[32:64])
	sigS := secp256k1.ModNScalar{}
	sigS.SetBytes(&sBytes)
	desoTxn.Signature.SetSignature(ecdsa.NewSignature(&sigR, &sigS))

	signedTxnBytes, err := desoTxn.ToBytes(false)
	if err != nil {
		return nil, wrapErr(ErrInvalidTransaction, err)
	}

	signedTxn, err := json.Marshal(&transactionMetadata{
		Transaction:                      signedTxnBytes,
		InputAmounts:                     unsignedTxn.InputAmounts,
		LegacyUTXOSelection:              unsignedTxn.LegacyUTXOSelection,
		NoncePartialID:                   unsignedTxn.NoncePartialID,
		NonceExpirationBlockHeight:       unsignedTxn.NonceExpirationBlockHeight,
		NonceExpirationBlockHeightOffset: unsignedTxn.NonceExpirationBlockHeightOffset,
		TxnFeeNanos:                      unsignedTxn.TxnFeeNanos,
		FeeRateNanosPerKB:                unsignedTxn.FeeRateNanosPerKB,
	})

	return &types.ConstructionCombineResponse{
		SignedTransaction: hex.EncodeToString(signedTxn),
	}, nil
}

func (s *ConstructionAPIService) ConstructionHash(ctx context.Context, request *types.ConstructionHashRequest) (*types.TransactionIdentifierResponse, *types.Error) {
	txnBytes, err := hex.DecodeString(request.SignedTransaction)
	if err != nil {
		return nil, wrapErr(ErrInvalidTransaction, err)
	}

	var signedTx transactionMetadata
	if err := json.Unmarshal(txnBytes, &signedTx); err != nil {
		return nil, wrapErr(ErrInvalidTransaction, err)
	}

	txn := &lib.MsgDeSoTxn{}
	if err = txn.FromBytes(signedTx.Transaction); err != nil {
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

	var metadata transactionMetadata
	if err := json.Unmarshal(txnBytes, &metadata); err != nil {
		return nil, wrapErr(ErrInvalidTransaction, err)
	}

	desoTxn := &lib.MsgDeSoTxn{}
	if err = desoTxn.FromBytes(metadata.Transaction); err != nil {
		return nil, wrapErr(ErrInvalidTransaction, err)
	}

	signer := &types.AccountIdentifier{
		Address: lib.Base58CheckEncode(desoTxn.PublicKey, false, s.node.Params),
	}

	numOps := int64(0)
	var operations []*types.Operation

	for _, inputAmount := range metadata.InputAmounts {
		operations = append(operations, &types.Operation{
			Type: deso.InputOpType,
			OperationIdentifier: &types.OperationIdentifier{
				Index: numOps,
			},
			Account: signer,
			Amount: &types.Amount{
				Value:    inputAmount,
				Currency: s.config.Currency,
			},
		})
		numOps += 1
	}

	for _, output := range desoTxn.TxOutputs {
		// Skip the change output when NOT using legacy utxo selection AND it's not a balance model transaction
		if !metadata.LegacyUTXOSelection && reflect.DeepEqual(output.PublicKey, desoTxn.PublicKey) &&
			desoTxn.TxnNonce == nil {
			continue
		}

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

	var txn transactionMetadata
	if err := json.Unmarshal(txnBytes, &txn); err != nil {
		return nil, wrapErr(ErrInvalidTransaction, err)
	}

	desoTxn := &lib.MsgDeSoTxn{}
	if err = desoTxn.FromBytes(txn.Transaction); err != nil {
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
