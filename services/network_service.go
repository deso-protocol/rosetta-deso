package services

import (
	"context"
	"github.com/deso-protocol/core/lib"
	"strconv"

	"github.com/coinbase/rosetta-sdk-go/server"
	"github.com/coinbase/rosetta-sdk-go/types"
	"github.com/deso-protocol/rosetta-deso/deso"
)

type NetworkAPIService struct {
	config *deso.Config
	node   *deso.Node
}

func NewNetworkAPIService(config *deso.Config, node *deso.Node) server.NetworkAPIServicer {
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
	peers := []*types.Peer{}
	for _, peer := range s.node.GetConnectionManager().GetAllPeers() {
		peers = append(peers, &types.Peer{
			PeerID: strconv.FormatUint(peer.ID, 10),
			Metadata: map[string]interface{}{
				"address": peer.Address(),
			},
		})
	}

	syncStatus := &types.SyncStatus{
		CurrentIndex: new(int64),
		TargetIndex:  new(int64),
		Stage:        new(string),
		Synced:       new(bool),
	}

	// If TXIndex is enabled we wait for it to process blocks
	if s.node.TXIndex != nil {
		blockchain := s.node.TXIndex.TXIndexChain
		*syncStatus.CurrentIndex = int64(blockchain.BlockTip().Height)
		*syncStatus.TargetIndex = int64(blockchain.HeaderTip().Height)
		*syncStatus.Stage = blockchain.ChainState().String()
	} else {
		blockchain := s.node.GetBlockchain()
		*syncStatus.CurrentIndex = int64(blockchain.BlockTip().Height)
		*syncStatus.TargetIndex = int64(blockchain.HeaderTip().Height)
		*syncStatus.Stage = blockchain.ChainState().String()
	}

	// Synced means we are fully synced OR we are only three blocks behind
	isSyncing := *syncStatus.Stage == lib.SyncStateSyncingBlocks.String() || *syncStatus.Stage == lib.SyncStateNeedBlocksss.String()
	*syncStatus.Synced = *syncStatus.Stage == lib.SyncStateFullyCurrent.String() ||
		(isSyncing && (*syncStatus.TargetIndex-*syncStatus.CurrentIndex) <= 3)

	genesisBlock := s.node.GetBlockAtHeight(0)
	currentBlock := s.node.GetBlockAtHeight(*syncStatus.CurrentIndex)

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
			NodeVersion:    "1.2.3",
		},
		Allow: &types.Allow{
			OperationStatuses: []*types.OperationStatus{
				{
					Status:     deso.SuccessStatus,
					Successful: true,
				},
				{
					Status:     deso.RevertedStatus,
					Successful: false,
				},
			},
			OperationTypes: deso.OperationTypes,
			Errors:         Errors,
		},
	}, nil
}
