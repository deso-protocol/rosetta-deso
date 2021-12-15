package services

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/btcsuite/btcd/btcec"
	"github.com/coinbase/rosetta-sdk-go/server"
	"github.com/coinbase/rosetta-sdk-go/types"
	"github.com/deso-protocol/core/lib"
	"github.com/deso-protocol/core/network"
	merkletree "github.com/deso-protocol/go-merkle-tree"
	"github.com/deso-protocol/rosetta-deso/deso"
	"math/big"
	"reflect"
	"strconv"
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

func (s *ConstructionAPIService) ConstructionMetadata(ctx context.Context, request *types.ConstructionMetadataRequest) (*types.ConstructionMetadataResponse, *types.Error) {
	if s.config.Mode != deso.Online {
		return nil, ErrUnavailableOffline
	}

	mempoolView, err := s.node.GetMempool().GetAugmentedUniversalView()
	if err != nil {
		return nil, wrapErr(ErrDeSo, err)
	}

	// Determine the network-wide feePerKB rate
	feePerKB := mempoolView.GlobalParamsEntry.MinimumNetworkFeeNanosPerKB
	if feePerKB == 0 {
		feePerKB = deso.MinFeeRateNanosPerKB
	}

	var options preprocessOptions
	if err := types.UnmarshalMap(request.Options, &options); err != nil {
		return nil, wrapErr(ErrUnableToParseIntermediateResult, err)
	}

	fullDeSoOutputs := []*net.DeSoOutput{}
	for _, output := range options.DeSoOutputs {
		pkBytes, _, err := lib.Base58CheckDecode(output.PublicKey)
		if err != nil {
			return nil, wrapErr(ErrUnableToParseIntermediateResult, err)
		}
		fullDeSoOutputs = append(fullDeSoOutputs, &net.DeSoOutput{
			PublicKey:   pkBytes,
			AmountNanos: output.AmountNanos,
		})
	}

	// Use the input amount to compute how many UTXOs will be needed
	fromPubKeyBytes, _, err := lib.Base58CheckDecode(options.FromPublicKey)
	if err != nil {
		return nil, wrapErr(ErrDeSo, err)
	}
	txnn := &net.MsgDeSoTxn{
		// The inputs will be set below.
		TxInputs:  []*net.DeSoInput{},
		TxOutputs: fullDeSoOutputs,
		PublicKey: fromPubKeyBytes,
		TxnMeta:   &net.BasicTransferMetadata{},
	}
	_, _, _, fee, err := s.node.GetBlockchain().AddInputsAndChangeToTransaction(txnn, feePerKB, s.node.GetMempool())
	if err != nil {
		return nil, wrapErr(ErrInvalidTransaction, err)
	}

	desoTxnBytes, err := txnn.ToBytes(true)
	if err != nil {
		return nil, wrapErr(ErrInvalidTransaction, err)
	}

	metadata, err := types.MarshalMap(&constructionMetadata{
		FeePerKB:         feePerKB,
		DeSoSampleTxnHex: hex.EncodeToString(desoTxnBytes),
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

	unsignedTxn, err := json.Marshal(&transactionMetadata{
		Transaction: desoTxnBytes,
		InputAmount: inputAmounts[0],
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

	desoTxn := &net.MsgDeSoTxn{}
	if err = desoTxn.FromBytes(unsignedTxn.Transaction); err != nil {
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
		Transaction: signedTxnBytes,
		InputAmount: unsignedTxn.InputAmount,
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

	txn := &net.MsgDeSoTxn{}
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

	desoTxn := &net.MsgDeSoTxn{}
	if err = desoTxn.FromBytes(metadata.Transaction); err != nil {
		return nil, wrapErr(ErrInvalidTransaction, err)
	}

	signer := &types.AccountIdentifier{
		Address: lib.Base58CheckEncode(desoTxn.PublicKey, false, s.node.Params),
	}

	operations := []*types.Operation{
		{
			Type: deso.InputOpType,
			OperationIdentifier: &types.OperationIdentifier{
				Index: 0,
			},
			Account: signer,
			Amount: &types.Amount{
				Value:    metadata.InputAmount,
				Currency: s.config.Currency,
			},
		},
	}

	numOps := int64(1)
	for _, output := range desoTxn.TxOutputs {
		// Skip the change output
		if reflect.DeepEqual(output.PublicKey, desoTxn.PublicKey) {
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

	desoTxn := &net.MsgDeSoTxn{}
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
