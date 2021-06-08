package services

import (
	"context"
	"github.com/bitclout/core/lib"
	"strconv"

	"github.com/bitclout/rosetta-bitclout/bitclout"
	"github.com/coinbase/rosetta-sdk-go/server"
	"github.com/coinbase/rosetta-sdk-go/types"
)

type NetworkAPIService struct {
	config *bitclout.Config
	node   *bitclout.Node
}

func NewNetworkAPIService(config *bitclout.Config, node *bitclout.Node) server.NetworkAPIServicer {
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

	peers := []*types.Peer{}
	for _, peer := range s.node.GetConnectionManager().GetAllPeers() {
		peers = append(peers, &types.Peer{
			PeerID: strconv.FormatUint(peer.ID, 10),
			Metadata: map[string]interface{}{
				"address": peer.Address(),
			},
		})
	}

	blockchain := s.node.GetBlockchain()
	targetIndex := int64(blockchain.HeaderTip().Height)
	currentIndex := int64(blockchain.BlockTip().Height)
	stage := blockchain.ChainState().String()

	// Synced means we are fully synced OR we are only three blocks behind
	synced := blockchain.ChainState() == lib.SyncStateFullyCurrent || (targetIndex - currentIndex) <= 3

	// If TXIndex is enabled we wait for it to process blocks before increasing the CurrentIndex
	if s.node.TXIndex != nil {
		txIndex := int64(s.node.TXIndex.TXIndexChain.BlockTip().Height)
		if txIndex < currentIndex {
			currentIndex = txIndex
		}
	}

	currentBlock := s.node.GetBlockAtHeight(currentIndex)
	syncStatus := &types.SyncStatus{
		CurrentIndex: &currentIndex,
		TargetIndex:  &targetIndex,
		Stage:        &stage,
		Synced:       &synced,
	}

	return &types.NetworkStatusResponse{
		CurrentBlockIdentifier: currentBlock.BlockIdentifier,
		CurrentBlockTimestamp:  currentBlock.Timestamp,
		GenesisBlockIdentifier: genesisBlock.BlockIdentifier,
		Peers:                  peers,
		SyncStatus:             syncStatus,
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
					Status:     bitclout.SuccessStatus,
					Successful: true,
				},
				{
					Status:     bitclout.RevertedStatus,
					Successful: false,
				},
			},
			OperationTypes: bitclout.OperationTypes,
			Errors:         Errors,
		},
	}, nil
}
