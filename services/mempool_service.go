package services

import (
	"context"

	"github.com/bitclout/rosetta-bitclout/bitclout"
	"github.com/bitclout/rosetta-bitclout/configuration"
	"github.com/coinbase/rosetta-sdk-go/server"
	"github.com/coinbase/rosetta-sdk-go/types"
)

type MempoolAPIService struct {
	config *configuration.Configuration
	node   *bitclout.Node
}

func NewMempoolAPIService(config *configuration.Configuration, node *bitclout.Node) server.MempoolAPIServicer {
	return &MempoolAPIService{
		config: config,
		node:   node,
	}
}

func (s *MempoolAPIService) Mempool(ctx context.Context, request *types.NetworkRequest) (*types.MempoolResponse, *types.Error) {
	if s.config.Mode != configuration.Online {
		// TODO: Implement/Abstract
		return nil, ErrUnavailableOffline
		//return nil, wrapErr(ErrUnavailableOffline, nil)
	}

	mempool := s.node.GetMempool()
	transactions, _, err := mempool.GetTransactionsOrderedByTimeAdded()
	if err != nil {
		return nil, ErrBitclout
	}

	transactionIdentifiers := []*types.TransactionIdentifier{}
	for _, transaction := range transactions {
		transactionIdentifiers = append(transactionIdentifiers, &types.TransactionIdentifier{Hash: transaction.Hash.String()})
	}

	return &types.MempoolResponse{
		TransactionIdentifiers: transactionIdentifiers,
	}, nil
}

func (s *MempoolAPIService) MempoolTransaction(ctx context.Context, request *types.MempoolTransactionRequest) (*types.MempoolTransactionResponse, *types.Error) {
	if s.config.Mode != configuration.Online {
		return nil, ErrUnavailableOffline
	}

	return nil, ErrUnimplemented
}
