package services

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"

	"github.com/btcsuite/btcd/btcec"
	"github.com/coinbase/rosetta-sdk-go/server"
	"github.com/coinbase/rosetta-sdk-go/types"
	"github.com/deso-protocol/core/lib"
	"github.com/deso-protocol/rosetta-deso/deso"
	merkletree "github.com/laser/go-merkle-tree"
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
				PublicKey: op.Account.Address,
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
	for kk, _ := range inputPubKeysFoundMap {
		optionsObj.FromPublicKey = kk
		break
	}

	options, err := types.MarshalMap(optionsObj)
	if err != nil {
		return nil, wrapErr(ErrUnableToParseIntermediateResult, err)
	}

	return &types.ConstructionPreprocessResponse{
		Options: options,
	}, nil
}

// Define a helper function for computing the upper bound of the size
// of a transaction and associated fees. This basically serializes the
// transaction without the signature and then accounts for the maximum possible
// size the signature could be.
func _computeMaxTxSize(_tx *lib.MsgDeSoTxn) uint64 {
	// Compute the size of the transaction without the signature.
	txBytesNoSignature, _ := _tx.ToBytes(true /*preSignature*/)
	// Return the size the transaction would be if the signature had its
	// absolute maximum length.

	// MaxDERSigLen is the maximum size that a DER signature can be.
	//
	// Note: I am pretty sure the true maximum is 71. But since this value is
	// dependent on the size of R and S, and since it's generally used for
	// safety purposes (e.g. ensuring that enough space has been allocated),
	// it seems better to pad it a bit and stay on the safe side. You can see
	// some discussion on getting to this number here:
	// https://bitcoin.stackexchange.com/questions/77191/what-is-the-maximum-size-of-a-der-encoded-ecdsa-signature
	const MaxDERSigLen = 74

	return uint64(len(txBytesNoSignature)) + MaxDERSigLen
}

// A helper for computing the max fee given a txn. Assumes the longest signature
// length.
func _computeMaxTxFee(_tx *lib.MsgDeSoTxn, minFeeRateNanosPerKB uint64) uint64 {
	maxSizeBytes := _computeMaxTxSize(_tx)
	return maxSizeBytes * minFeeRateNanosPerKB / 1000
}

func (s *ConstructionAPIService) ConstructionMetadata(ctx context.Context, request *types.ConstructionMetadataRequest) (*types.ConstructionMetadataResponse, *types.Error) {
	if s.config.Mode != deso.Online {
		return nil, ErrUnavailableOffline
	}

	utxoView, err := s.node.GetMempool().GetAugmentedUniversalView()
	if err != nil {
		return nil, wrapErr(ErrDeSo, err)
	}

	// Determine the network-wide feePerKB rate
	feePerKB := utxoView.GlobalParamsEntry.MinimumNetworkFeeNanosPerKB
	if feePerKB == 0 {
		feePerKB = deso.MinFeeRateNanosPerKB
	}

	var options preprocessOptions
	if err := types.UnmarshalMap(request.Options, &options); err != nil {
		return nil, wrapErr(ErrUnableToParseIntermediateResult, err)
	}

	fullDeSoOutputs := []*lib.DeSoOutput{}
	for _, output := range options.DeSoOutputs {
		pkBytes, _, err := lib.Base58CheckDecode(output.PublicKey)
		if err != nil {
			return nil, wrapErr(ErrUnableToParseIntermediateResult, err)
		}
		fullDeSoOutputs = append(fullDeSoOutputs, &lib.DeSoOutput{
			PublicKey: pkBytes,
			AmountNanos: output.AmountNanos,
		})
	}

	// Use the input amount to compute how many UTXOs will be needed
	fromPubKeyBytes, _, err := lib.Base58CheckDecode(options.FromPublicKey)
	if err != nil {
		return nil, wrapErr(ErrDeSo, err)
	}
	txnn := &lib.MsgDeSoTxn{
		// The inputs will be set below.
		TxInputs:  []*lib.DeSoInput{},
		TxOutputs: fullDeSoOutputs,
		PublicKey: fromPubKeyBytes,
		TxnMeta:   &lib.BasicTransferMetadata{},
	}
	s.node.GetBlockchain().AddInputsAndChangeToTransaction(txnn, feePerKB, s.node.GetMempool())
	suggestedFeeNanos := _computeMaxTxFee(txnn, feePerKB)

	desoTxnBytes, err := txnn.ToBytes(true)
	if err != nil {
		return nil, wrapErr(ErrInvalidTransaction, err)
	}

	metadata, err := types.MarshalMap(&constructionMetadata{
		FeePerKB: feePerKB,
		DeSoSampleTxnHex: hex.EncodeToString(desoTxnBytes),
	})
	if err != nil {
		return nil, wrapErr(ErrUnableToParseIntermediateResult, err)
	}

	return &types.ConstructionMetadataResponse{
		Metadata: metadata,
		SuggestedFee: []*types.Amount{
			{
				Value:    strconv.FormatUint(suggestedFeeNanos, 10),
				Currency: &deso.Currency,
			},
		},
	}, nil
}

func (s *ConstructionAPIService) ConstructionPayloads(ctx context.Context, request *types.ConstructionPayloadsRequest) (*types.ConstructionPayloadsResponse, *types.Error) {
	// Convert map to Metadata struct
	var metadata *constructionMetadata
	if err := types.UnmarshalMap(request.Metadata, &metadata); err != nil {
		return nil, wrapErr(ErrUnableToParseIntermediateResult, err)
	}

	// The input amounts are simply what was passed in the operations
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

	unsignedTxn, err := json.Marshal(&transactionMetadata{
		Transaction:  hex.EncodeToString(desoTxnBytes),
		InputAmounts: inputAmounts,
	})

	unsignedBytes := merkletree.Sha256DoubleHash(desoTxnBytes)

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

	desoTxnBytes, err := hex.DecodeString(unsignedTxn.Transaction)
	if err != nil {
		return nil, wrapErr(ErrInvalidTransaction, err)
	}

	desoTxn := &lib.MsgDeSoTxn{}
	if err = desoTxn.FromBytes(desoTxnBytes); err != nil {
		return nil, wrapErr(ErrInvalidTransaction, err)
	}

	// signature is in form of R || S
	signatureBytes := request.Signatures[0].Bytes
	desoTxn.Signature = &btcec.Signature{
		R: new(big.Int).SetBytes(signatureBytes[:32]),
		S: new(big.Int).SetBytes(signatureBytes[32:64]),
	}

	signedTxnBytes, err := desoTxn.ToBytes(false)
	if err != nil {
		return nil, wrapErr(ErrInvalidTransaction, err)
	}

	signedTxn, err := json.Marshal(&transactionMetadata{
		Transaction:  hex.EncodeToString(signedTxnBytes),
		InputAmounts: unsignedTxn.InputAmounts,
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

	desoTxnBytes, err := hex.DecodeString(signedTx.Transaction)
	if err != nil {
		return nil, wrapErr(ErrInvalidTransaction, err)
	}

	txn := &lib.MsgDeSoTxn{}
	if err = txn.FromBytes(desoTxnBytes); err != nil {
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

	var txn transactionMetadata
	if err := json.Unmarshal(txnBytes, &txn); err != nil {
		return nil, wrapErr(ErrInvalidTransaction, err)
	}

	desoTxnBytes, err := hex.DecodeString(txn.Transaction)
	if err != nil {
		return nil, wrapErr(ErrInvalidTransaction, err)
	}

	desoTxn := &lib.MsgDeSoTxn{}
	if err = desoTxn.FromBytes(desoTxnBytes); err != nil {
		return nil, wrapErr(ErrInvalidTransaction, err)
	}

	var operations []*types.Operation
	var signer *types.AccountIdentifier

	for i, input := range desoTxn.TxInputs {
		networkIndex := int64(input.Index)

		op := &types.Operation{
			OperationIdentifier: &types.OperationIdentifier{
				Index:        int64(i),
				NetworkIndex: &networkIndex,
			},

			Account: &types.AccountIdentifier{
				Address: lib.Base58CheckEncode(desoTxn.PublicKey, false, s.node.Params),
			},

			CoinChange: &types.CoinChange{
				CoinIdentifier: &types.CoinIdentifier{
					Identifier: fmt.Sprintf("%v:%d", input.TxID.String(), input.Index),
				},
				CoinAction: types.CoinSpent,
			},

			Amount: &types.Amount{
				Value:    txn.InputAmounts[i],
				Currency: s.config.Currency,
			},

			//Status: &deso.SuccessStatus,
			Type: deso.InputOpType,
		}

		operations = append(operations, op)

		if request.Signed {
			signer = op.Account
		}

		// Can only have one signing account per transaction
		if signer != nil && signer.Address != op.Account.Address {
			return nil, ErrMultipleSigners
		}
	}

	for i, output := range desoTxn.TxOutputs {
		networkIndex := int64(i)

		op := &types.Operation{
			OperationIdentifier: &types.OperationIdentifier{
				Index:        int64(len(desoTxn.TxInputs) + i),
				NetworkIndex: &networkIndex,
			},

			Account: &types.AccountIdentifier{
				Address: lib.Base58CheckEncode(output.PublicKey, false, s.node.Params),
			},

			Amount: &types.Amount{
				Value:    strconv.FormatUint(output.AmountNanos, 10),
				Currency: &deso.Currency,
			},

			CoinChange: &types.CoinChange{
				CoinIdentifier: &types.CoinIdentifier{
					Identifier: fmt.Sprintf("%v:%d", desoTxn.Hash().String(), networkIndex),
				},
				CoinAction: types.CoinCreated,
			},

			//Status: &deso.SuccessStatus,
			Type: deso.OutputOpType,
		}

		operations = append(operations, op)
	}

	if signer != nil {
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

	desoTxnBytes, err := hex.DecodeString(txn.Transaction)
	if err != nil {
		return nil, wrapErr(ErrInvalidTransaction, err)
	}

	desoTxn := &lib.MsgDeSoTxn{}
	if err = desoTxn.FromBytes(desoTxnBytes); err != nil {
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
