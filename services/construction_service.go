package services

import (
	"context"
	"encoding/hex"
	"fmt"
	"strconv"

	"github.com/bitclout/core/lib"
	"github.com/bitclout/rosetta-bitclout/bitclout"
	"github.com/bitclout/rosetta-bitclout/configuration"
	"github.com/btcsuite/btcd/btcec"
	"github.com/coinbase/rosetta-sdk-go/server"
	"github.com/coinbase/rosetta-sdk-go/types"
)

const (
	averageSendTxnKB = float64(180 / 1000)
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
		return nil, ErrUnableToParseIntermediateResult
	}

	// Determine the network-wide feePerKB rate
	feePerKB := utxoView.GlobalParamsEntry.MinimumNetworkFeeNanosPerKB
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
	bitcloutTxn := &lib.MsgBitCloutTxn{}
	var signingAccount *types.AccountIdentifier

	for _, operation := range request.Operations {
		if operation.Type == bitclout.InputOpType {
			txId, index, err := ParseCoinIdentifier(operation.CoinChange.CoinIdentifier)
			if err != nil {
				return nil, wrapErr(ErrUnableToParseIntermediateResult, err)
			}

			if signingAccount == nil {
				signingAccount = operation.Account
			}

			// Can only have one signing account per transaction
			if signingAccount != operation.Account {
				return nil, ErrUnableToParseIntermediateResult
			}

			bitcloutTxn.TxInputs = append(bitcloutTxn.TxInputs, &lib.BitCloutInput{
				TxID: *txId,
				Index: index,
			})
		} else if operation.Type == bitclout.OutputOpType {
			publicKeyBytes, _, err := lib.Base58CheckDecode(operation.Account.Address)
			if err != nil {
				return nil, wrapErr(ErrUnableToParseIntermediateResult, err)
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

	rawTxnBytes, err := bitcloutTxn.ToBytes(true)
	if err != nil {
		return nil, wrapErr(ErrUnableToParseIntermediateResult, err)
	}

	return &types.ConstructionPayloadsResponse{
		UnsignedTransaction: hex.EncodeToString(rawTxnBytes),
		Payloads:            []*types.SigningPayload{
			{
				AccountIdentifier: signingAccount,
				Bytes: rawTxnBytes,
				SignatureType: types.Ecdsa,
			},
		},
	}, nil
}

func (s *ConstructionAPIService) ConstructionCombine(ctx context.Context, request *types.ConstructionCombineRequest) (*types.ConstructionCombineResponse, *types.Error) {
	txnBytes, err := hex.DecodeString(request.UnsignedTransaction)
	if err != nil {
		return nil, ErrUnableToParseIntermediateResult
	}
	txn := &lib.MsgBitCloutTxn{}
	if err = txn.FromBytes(txnBytes); err != nil {
		return nil, ErrUnableToParseIntermediateResult
	}

	signature, err := btcec.ParseDERSignature(request.Signatures[0].Bytes, btcec.S256())
	if err != nil {
		return nil, ErrUnableToParseIntermediateResult
	}
	txn.Signature = signature

	txnBytes, err = txn.ToBytes(false)
	if err != nil {
		return nil, ErrUnableToParseIntermediateResult
	}

	return &types.ConstructionCombineResponse{
		SignedTransaction: hex.EncodeToString(txnBytes),
	}, nil
}

func (s *ConstructionAPIService) ConstructionHash(ctx context.Context, request *types.ConstructionHashRequest) (*types.TransactionIdentifierResponse, *types.Error) {
	txnBytes, err := hex.DecodeString(request.SignedTransaction)
	if err != nil {
		return nil, ErrUnableToParseIntermediateResult
	}

	txn := &lib.MsgBitCloutTxn{}
	if err = txn.FromBytes(txnBytes); err != nil {
		return nil, ErrUnableToParseIntermediateResult
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
		return nil, ErrUnableToParseIntermediateResult
	}

	txn := &lib.MsgBitCloutTxn{}
	if err = txn.FromBytes(txnBytes); err != nil {
		return nil, ErrUnableToParseIntermediateResult
	}

	operations := []*types.Operation{}
	var signer *types.AccountIdentifier

	for i, input := range(txn.TxInputs) {
		networkIndex := int64(input.Index)

		op := &types.Operation{
			OperationIdentifier: &types.OperationIdentifier{
				Index:        int64(i),
				NetworkIndex: &networkIndex,
			},

			Account: &types.AccountIdentifier{
				Address: lib.Base58CheckEncode(txn.PublicKey, false, s.node.Params),
			},

			CoinChange: &types.CoinChange{
				CoinIdentifier: &types.CoinIdentifier{
					Identifier: fmt.Sprintf("%v:%d", input.TxID.String(), input.Index),
				},
				CoinAction: types.CoinSpent,
			},

			Status: &bitclout.SuccessStatus,
			Type:   bitclout.InputOpType,
		}

		operations = append(operations, op)

		if signer == nil {
			signer = op.Account
		}

		// Can only have one signing account per transaction
		if signer != op.Account {
			return nil, ErrUnableToParseIntermediateResult
		}
	}

	for i, output := range(txn.TxOutputs) {
		networkIndex := int64(i)

		op := &types.Operation{
			OperationIdentifier: &types.OperationIdentifier{
				Index:        int64(len(txn.TxInputs) + i),
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
					Identifier: fmt.Sprintf("%v:%d", txn.Hash().String(), networkIndex),
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
		return nil, ErrUnableToParseIntermediateResult
	}

	txn := &lib.MsgBitCloutTxn{}
	if err = txn.FromBytes(txnBytes); err != nil {
		return nil, ErrUnableToParseIntermediateResult
	}

	blockchain := s.node.GetBlockchain()
	mempool := s.node.GetMempool()
	blockHeight := blockchain.BlockTip().Height

	if err := blockchain.ValidateTransaction(txn, blockHeight+1, true, false, 0, mempool); err != nil {
		return nil, ErrBitclout
	}

	return &types.TransactionIdentifierResponse{
		TransactionIdentifier: &types.TransactionIdentifier{
			Hash: txn.Hash().String(),
		},
	}, nil
}
