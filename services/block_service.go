package services

import (
	"context"
	"github.com/deso-protocol/rosetta-deso/deso"

	"github.com/coinbase/rosetta-sdk-go/server"
	"github.com/coinbase/rosetta-sdk-go/types"
)

type BlockAPIService struct {
	config *deso.Config
	node   *deso.Node
}

func NewBlockAPIService(config *deso.Config, node *deso.Node) server.BlockAPIServicer {
	return &BlockAPIService{
		config: config,
		node:   node,
	}
}

func (s *BlockAPIService) Block(
	ctx context.Context,
	request *types.BlockRequest,
) (*types.BlockResponse, *types.Error) {
	if s.config.Mode != deso.Online {
		return nil, ErrUnavailableOffline
	}

	block := &types.Block{}
	var err *types.Error
	if request.BlockIdentifier.Index != nil {
		block, err = s.node.GetBlockAtHeight(*request.BlockIdentifier.Index)
	} else if request.BlockIdentifier.Hash != nil {
		block, err = s.node.GetBlock(*request.BlockIdentifier.Hash)
	} else {
		block, err = s.node.CurrentBlock()
	}

	if err != nil {
		return nil, err
	}

	if block == nil {
		return nil, ErrBlockNotFound
	}

	return &types.BlockResponse{
		Block: block,
	}, nil
}

func (s *BlockAPIService) BlockTransaction(
	ctx context.Context,
	request *types.BlockTransactionRequest,
) (*types.BlockTransactionResponse, *types.Error) {
	if s.config.Mode != deso.Online {
		return nil, ErrUnavailableOffline
	}

	return &types.BlockTransactionResponse{
		Transaction: &types.Transaction{},
	}, nil
}
