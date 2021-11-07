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
	desoTxn, _, txnErr := constructTransaction(request.Operations)
	if txnErr != nil {
		return nil, txnErr
	}

	desoTxnBytes, err := desoTxn.ToBytes(true)
	if err != nil {
		return nil, wrapErr(ErrInvalidTransaction, err)
	}

	txnSize := uint64(len(desoTxnBytes) + MaxDERSigLen + FeeByteBuffer)

	options, err := types.MarshalMap(&preprocessOptions{
		TransactionSizeEstimate: txnSize,
	})
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

	utxoView, err := s.node.GetMempool().GetAugmentedUniversalView()
	if err != nil {
		return nil, wrapErr(ErrDeSo, err)
	}

	// Determine the network-wide feePerKB rate
	feePerKB := utxoView.GlobalParamsEntry.MinimumNetworkFeeNanosPerKB
	if feePerKB == 0 {
		feePerKB = deso.MinFeeRateNanosPerKB
	}

	metadata, err := types.MarshalMap(&constructionMetadata{FeePerKB: feePerKB})
	if err != nil {
		return nil, wrapErr(ErrUnableToParseIntermediateResult, err)
	}

	var options preprocessOptions
	if err := types.UnmarshalMap(request.Options, &options); err != nil {
		return nil, wrapErr(ErrUnableToParseIntermediateResult, err)
	}

	suggestedFee := feePerKB * options.TransactionSizeEstimate / BytesPerKb

	return &types.ConstructionMetadataResponse{
		Metadata: metadata,
		SuggestedFee: []*types.Amount{
			{
				Value:    strconv.FormatUint(suggestedFee, 10),
				Currency: &deso.Currency,
			},
		},
	}, nil
}

func constructTransaction(operations []*types.Operation) (*lib.MsgDeSoTxn, *types.AccountIdentifier, *types.Error) {
	desoTxn := &lib.MsgDeSoTxn{
		TxInputs:  []*lib.DeSoInput{},
		TxOutputs: []*lib.DeSoOutput{},
		TxnMeta:   &lib.BasicTransferMetadata{},
	}
	var signingAccount *types.AccountIdentifier

	for _, operation := range operations {
		if operation.Type == deso.InputOpType {
			txId, txnIndex, err := ParseCoinIdentifier(operation.CoinChange.CoinIdentifier)
			if err != nil {
				return nil, nil, wrapErr(ErrInvalidCoin, err)
			}

			if signingAccount == nil {
				signingAccount = operation.Account

				publicKeyBytes, _, err := lib.Base58CheckDecode(signingAccount.Address)
				if err != nil {
					return nil, nil, wrapErr(ErrInvalidPublicKey, err)
				}

				desoTxn.PublicKey = publicKeyBytes
			}

			// Can only have one signing account per transaction
			if signingAccount.Address != operation.Account.Address {
				return nil, nil, ErrMultipleSigners
			}

			desoTxn.TxInputs = append(desoTxn.TxInputs, &lib.DeSoInput{
				TxID:  *txId,
				Index: txnIndex,
			})
		} else if operation.Type == deso.OutputOpType {
			publicKeyBytes, _, err := lib.Base58CheckDecode(operation.Account.Address)
			if err != nil {
				return nil, nil, wrapErr(ErrInvalidPublicKey, err)
			}

			amount, err := types.AmountValue(operation.Amount)
			if err != nil {
				return nil, nil, wrapErr(ErrUnableToParseIntermediateResult, err)
			}

			desoTxn.TxOutputs = append(desoTxn.TxOutputs, &lib.DeSoOutput{
				PublicKey:   publicKeyBytes,
				AmountNanos: amount.Uint64(),
			})
		}
	}

	return desoTxn, signingAccount, nil
}

func (s *ConstructionAPIService) ConstructionPayloads(ctx context.Context, request *types.ConstructionPayloadsRequest) (*types.ConstructionPayloadsResponse, *types.Error) {
	var inputAmounts []string
	for _, operation := range request.Operations {
		if operation.Type == deso.InputOpType {
			inputAmounts = append(inputAmounts, operation.Amount.Value)
		}
	}

	desoTxn, signingAccount, txnErr := constructTransaction(request.Operations)
	if txnErr != nil {
		return nil, txnErr
	}

	desoTxnBytes, err := desoTxn.ToBytes(true)
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
