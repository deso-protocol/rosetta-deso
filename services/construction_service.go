package services

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/bitclout/core/lib"
	"github.com/bitclout/rosetta-bitclout/bitclout"
	"github.com/bitclout/rosetta-bitclout/configuration"
	"github.com/btcsuite/btcd/btcec"
	"github.com/coinbase/rosetta-sdk-go/server"
	"github.com/coinbase/rosetta-sdk-go/types"
	merkletree "github.com/laser/go-merkle-tree"
)

const (
	averageSendTxnKB = 180.0 / 1000.0
)

type ConstructionAPIService struct {
	config *configuration.Configuration
	node   *bitclout.Node
}

func NewConstructionAPIService(config *configuration.Configuration, node *bitclout.Node) server.ConstructionAPIServicer {
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
	return &types.ConstructionPreprocessResponse{}, nil
}

func (s *ConstructionAPIService) ConstructionMetadata(ctx context.Context, request *types.ConstructionMetadataRequest) (*types.ConstructionMetadataResponse, *types.Error) {
	if s.config.Mode != configuration.Online {
		return nil, ErrUnavailableOffline
	}

	utxoView, err := s.node.GetMempool().GetAugmentedUniversalView()
	if err != nil {
		return nil, wrapErr(ErrUnableToParseIntermediateResult, err)
	}

	// Determine the network-wide feePerKB rate
	feePerKB := utxoView.GlobalParamsEntry.MinimumNetworkFeeNanosPerKB
	if feePerKB == 0 {
		feePerKB = bitclout.MinFeeRateNanosPerKB
	}

	metadata, err := types.MarshalMap(&constructionMetadata{FeePerKB: feePerKB})
	if err != nil {
		return nil, wrapErr(ErrUnableToParseIntermediateResult, err)
	}

	// A reasonable default for send transaction size
	suggestedFee := uint64(float64(feePerKB) * averageSendTxnKB)

	return &types.ConstructionMetadataResponse{
		Metadata: metadata,
		SuggestedFee: []*types.Amount{
			{
				Value: strconv.FormatUint(suggestedFee, 10),
				Currency: &bitclout.Currency,
			},
		},
	}, nil
}

func (s *ConstructionAPIService) ConstructionPayloads(ctx context.Context, request *types.ConstructionPayloadsRequest) (*types.ConstructionPayloadsResponse, *types.Error) {
	bitcloutTxn := &lib.MsgBitCloutTxn{
		TxInputs:  []*lib.BitCloutInput{},
		TxOutputs: []*lib.BitCloutOutput{},
		TxnMeta:   &lib.BasicTransferMetadata{},
	}
	var signingAccount *types.AccountIdentifier
	var inputAmounts []string

	for _, operation := range request.Operations {
		if operation.Type == bitclout.InputOpType {
			txId, txIndex, err := ParseCoinIdentifier(operation.CoinChange.CoinIdentifier)
			if err != nil {
				return nil, wrapErr(ErrInvalidCoin, err)
			}

			if signingAccount == nil {
				signingAccount = operation.Account

				publicKeyBytes, _, err := lib.Base58CheckDecode(signingAccount.Address)
				if err != nil {
					return nil, wrapErr(ErrInvalidPublicKey, err)
				}

				bitcloutTxn.PublicKey = publicKeyBytes
			}

			// Can only have one signing account per transaction
			if signingAccount.Address != operation.Account.Address {
				return nil, ErrMultipleSigners
			}

			bitcloutTxn.TxInputs = append(bitcloutTxn.TxInputs, &lib.BitCloutInput{
				TxID: *txId,
				Index: txIndex,
			})

			inputAmounts = append(inputAmounts, operation.Amount.Value)
		} else if operation.Type == bitclout.OutputOpType {
			publicKeyBytes, _, err := lib.Base58CheckDecode(operation.Account.Address)
			if err != nil {
				return nil, wrapErr(ErrInvalidPublicKey, err)
			}

			amount, err := types.AmountValue(operation.Amount)
			if err != nil {
				return nil, wrapErr(ErrUnableToParseIntermediateResult, err)
			}

			bitcloutTxn.TxOutputs = append(bitcloutTxn.TxOutputs, &lib.BitCloutOutput{
				PublicKey: publicKeyBytes,
				AmountNanos: amount.Uint64(),
			})
		}
	}

	bitcloutTxnBytes, err := bitcloutTxn.ToBytes(true)
	if err != nil {
		return nil, wrapErr(ErrUnableToParseIntermediateResult, err)
	}

	unsignedTxn, err := json.Marshal(&transactionMetadata{
		Transaction:    hex.EncodeToString(bitcloutTxnBytes),
		InputAmounts:   inputAmounts,
	})

	unsignedBytes := merkletree.Sha256DoubleHash(bitcloutTxnBytes)

	return &types.ConstructionPayloadsResponse{
		UnsignedTransaction: hex.EncodeToString(unsignedTxn),
		Payloads:            []*types.SigningPayload{
			{
				AccountIdentifier: signingAccount,
				Bytes: unsignedBytes,
				SignatureType: types.Ecdsa,
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

	bitcloutTxnBytes, err := hex.DecodeString(unsignedTxn.Transaction)
	if err != nil {
		return nil, wrapErr(ErrInvalidTransaction, err)
	}

	bitcloutTxn := &lib.MsgBitCloutTxn{}
	if err = bitcloutTxn.FromBytes(bitcloutTxnBytes); err != nil {
		return nil, wrapErr(ErrInvalidTransaction, err)
	}

	signature, err := btcec.ParseDERSignature(request.Signatures[0].Bytes, btcec.S256())
	if err != nil {
		return nil, wrapErr(ErrInvalidTransaction, err)
	}
	bitcloutTxn.Signature = signature

	signedTxnBytes, err := bitcloutTxn.ToBytes(false)
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

	txn := &lib.MsgBitCloutTxn{}
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

	var txn transactionMetadata
	if err := json.Unmarshal(txnBytes, &txn); err != nil {
		return nil, wrapErr(ErrInvalidTransaction, err)
	}

	bitcloutTxnBytes, err := hex.DecodeString(txn.Transaction)
	if err != nil {
		return nil, wrapErr(ErrInvalidTransaction, err)
	}

	bitcloutTxn := &lib.MsgBitCloutTxn{}
	if err = bitcloutTxn.FromBytes(bitcloutTxnBytes); err != nil {
		return nil, wrapErr(ErrInvalidTransaction, err)
	}

	var operations []*types.Operation
	var signer *types.AccountIdentifier

	for i, input := range bitcloutTxn.TxInputs {
		networkIndex := int64(input.Index)

		op := &types.Operation{
			OperationIdentifier: &types.OperationIdentifier{
				Index:        int64(i),
				NetworkIndex: &networkIndex,
			},

			Account: &types.AccountIdentifier{
				Address: lib.Base58CheckEncode(bitcloutTxn.PublicKey, false, s.node.Params),
			},

			CoinChange: &types.CoinChange{
				CoinIdentifier: &types.CoinIdentifier{
					Identifier: fmt.Sprintf("%v:%d", input.TxID.String(), input.Index),
				},
				CoinAction: types.CoinSpent,
			},

			Amount: &types.Amount{
				Value: txn.InputAmounts[input.Index],
				Currency: s.config.Currency,
			},

			Status: &bitclout.SuccessStatus,
			Type:   bitclout.InputOpType,
		}

		operations = append(operations, op)

		if signer == nil {
			signer = op.Account
		}

		// Can only have one signing account per transaction
		if signer.Address != op.Account.Address {
			return nil, ErrMultipleSigners
		}
	}

	for i, output := range bitcloutTxn.TxOutputs {
		networkIndex := int64(i)

		op := &types.Operation{
			OperationIdentifier: &types.OperationIdentifier{
				Index:        int64(len(bitcloutTxn.TxInputs) + i),
				NetworkIndex: &networkIndex,
			},

			Account: &types.AccountIdentifier{
				Address: lib.Base58CheckEncode(output.PublicKey, false, s.node.Params),
			},

			Amount: &types.Amount{
				Value:    strconv.FormatUint(output.AmountNanos, 10),
				Currency: &bitclout.Currency,
			},

			CoinChange: &types.CoinChange{
				CoinIdentifier: &types.CoinIdentifier{
					Identifier: fmt.Sprintf("%v:%d", bitcloutTxn.Hash().String(), networkIndex),
				},
				CoinAction: types.CoinCreated,
			},

			Status: &bitclout.SuccessStatus,
			Type:   bitclout.OutputOpType,
		}

		operations = append(operations, op)
	}

	return &types.ConstructionParseResponse{
		Operations:               operations,
		AccountIdentifierSigners: []*types.AccountIdentifier{signer},
	}, nil

}

func (s *ConstructionAPIService) ConstructionSubmit(ctx context.Context, request *types.ConstructionSubmitRequest) (*types.TransactionIdentifierResponse, *types.Error) {
	if s.config.Mode != configuration.Online {
		return nil, ErrUnavailableOffline
	}

	txnBytes, err := hex.DecodeString(request.SignedTransaction)
	if err != nil {
		return nil, wrapErr(ErrInvalidTransaction, err)
	}

	txn := &lib.MsgBitCloutTxn{}
	if err = txn.FromBytes(txnBytes); err != nil {
		return nil, wrapErr(ErrInvalidTransaction, err)
	}

	if err := s.node.VerifyAndBroadcastTransaction(txn); err != nil {
		return nil, wrapErr(ErrBitclout, err)
	}

	return &types.TransactionIdentifierResponse{
		TransactionIdentifier: &types.TransactionIdentifier{
			Hash: txn.Hash().String(),
		},
	}, nil
}
