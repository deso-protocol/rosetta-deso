package services

import (
	"context"
	"strconv"

	"github.com/bitclout/rosetta-bitclout/bitclout"
	"github.com/bitclout/rosetta-bitclout/configuration"
	"github.com/coinbase/rosetta-sdk-go/server"
	"github.com/coinbase/rosetta-sdk-go/types"
)

type NetworkAPIService struct {
	config *configuration.Configuration
	node   *bitclout.Node
}

func NewNetworkAPIService(config *configuration.Configuration, node *bitclout.Node) server.NetworkAPIServicer {
	return &NetworkAPIService{
		config: config,
		node:   node,
	}
}

func (s *NetworkAPIService) NetworkList(ctx context.Context, request *types.MetadataRequest) (*types.NetworkListResponse, *types.Error) {
	return &types.NetworkListResponse{
		NetworkIdentifiers: []*types.NetworkIdentifier{s.config.Network},
	}, nil
}

func (s *NetworkAPIService) NetworkStatus(ctx context.Context, request *types.NetworkRequest) (*types.NetworkStatusResponse, *types.Error) {
	genesisBlock := s.node.GetBlockAtHeight(0)
	currentBlock := s.node.CurrentBlock()

	peers := []*types.Peer{}
	for _, peer := range s.node.GetConnectionManager().GetAllPeers() {
		peers = append(peers, &types.Peer{
			PeerID: strconv.FormatUint(peer.ID, 10),
			Metadata: map[string]interface{}{
				"address": peer.Address(),
			},
		})
	}

	return &types.NetworkStatusResponse{
		CurrentBlockIdentifier: currentBlock.BlockIdentifier,
		CurrentBlockTimestamp:  currentBlock.Timestamp,
		GenesisBlockIdentifier: genesisBlock.BlockIdentifier,
		Peers:                  peers,
	}, nil
}

// TODO (go): Implement
func (s *NetworkAPIService) NetworkOptions(ctx context.Context, request *types.NetworkRequest) (*types.NetworkOptionsResponse, *types.Error) {
	return &types.NetworkOptionsResponse{
		Version: &types.Version{
			RosettaVersion: "1.4.10",
			NodeVersion:    "0.0.1",
		},
		Allow: &types.Allow{
			OperationStatuses: []*types.OperationStatus{
				{
					Status:     "Success",
					Successful: true,
				},
				{
					Status:     "Reverted",
					Successful: false,
				},
			},
			OperationTypes: bitclout.OperationTypes,
			Errors:         Errors,
		},
	}, nil
}
