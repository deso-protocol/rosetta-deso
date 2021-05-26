package services

import (
	"context"
	"github.com/bitclout/rosetta-bitclout/bitclout"

	"github.com/coinbase/rosetta-sdk-go/server"
	"github.com/coinbase/rosetta-sdk-go/types"
)

type BlockAPIService struct {
	config *bitclout.Config
	node   *bitclout.Node
}

func NewBlockAPIService(config *bitclout.Config, node *bitclout.Node) server.BlockAPIServicer {
	return &BlockAPIService{
		config: config,
		node:   node,
	}
}

func (s *BlockAPIService) Block(
	ctx context.Context,
	request *types.BlockRequest,
) (*types.BlockResponse, *types.Error) {
	if s.config.Mode != bitclout.Online {
		return nil, ErrUnavailableOffline
	}

	block := &types.Block{}
	if request.BlockIdentifier.Index != nil {
		block = s.node.GetBlockAtHeight(*request.BlockIdentifier.Index)
	} else if request.BlockIdentifier.Hash != nil {
		block = s.node.GetBlock(*request.BlockIdentifier.Hash)
	} else {
		block = s.node.CurrentBlock()
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
	if s.config.Mode != bitclout.Online {
		return nil, ErrUnavailableOffline
	}

	return &types.BlockTransactionResponse{
		Transaction: &types.Transaction{},
	}, nil
}
