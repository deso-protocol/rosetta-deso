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
	if s.node != nil && s.node.GetConnectionManager() != nil {
		for _, peer := range s.node.GetConnectionManager().GetAllPeers() {
			peers = append(peers, &types.Peer{
				PeerID: strconv.FormatUint(peer.ID, 10),
				Metadata: map[string]interface{}{
					"address": peer.Address(),
				},
			})
		}
	}

	syncStatus := &types.SyncStatus{
		CurrentIndex: new(int64),
		TargetIndex:  new(int64),
		Stage:        new(string),
		Synced:       new(bool),
	}

	blockchain := s.node.GetBlockchain()
	committedTip, exists := blockchain.GetCommittedTip()
	if !exists || committedTip == nil {
		return nil, &types.Error{
			Code:    500,
			Message: "Committed tip not found",
		}
	}

	*syncStatus.CurrentIndex = int64(committedTip.Height)
	*syncStatus.TargetIndex = int64(blockchain.HeaderTip().Height)
	*syncStatus.Stage = blockchain.ChainState().String()

	// Synced means we are fully synced OR we are only three blocks behind
	isSyncing := *syncStatus.Stage == lib.SyncStateSyncingBlocks.String() || *syncStatus.Stage == lib.SyncStateNeedBlocksss.String()
	*syncStatus.Synced = *syncStatus.Stage == lib.SyncStateFullyCurrent.String() ||
		(isSyncing && (*syncStatus.TargetIndex-*syncStatus.CurrentIndex) <= 3)

	genesisBlockNode, exists, err := blockchain.GetBlockFromBestChainByHeight(0, false)
	if err != nil || !exists {
		return nil, &types.Error{
			Code:    500,
			Message: "Failed to get genesis block",
		}
	}
	genesisBlockIdentifier := &types.BlockIdentifier{
		Index: int64(0),
		Hash:  genesisBlockNode.Hash.String(),
	}

	currentBlockIdentifier := &types.BlockIdentifier{
		Index: int64(committedTip.Height),
		Hash:  committedTip.Hash.String(),
	}

	currentBlockTimestamp := int64(committedTip.Header.TstampNanoSecs) / 1e6 // Convert nanoseconds to milliseconds

	return &types.NetworkStatusResponse{
		CurrentBlockIdentifier: currentBlockIdentifier,
		CurrentBlockTimestamp:  currentBlockTimestamp,
		GenesisBlockIdentifier: genesisBlockIdentifier,
		Peers:                  peers,
		SyncStatus:             syncStatus,
	}, nil
}

// TODO (go): Implement
func (s *NetworkAPIService) NetworkOptions(ctx context.Context, request *types.NetworkRequest) (*types.NetworkOptionsResponse, *types.Error) {
	return &types.NetworkOptionsResponse{
		Version: &types.Version{
			RosettaVersion: "4.0.3",
			NodeVersion:    "4.0.3",
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
			OperationTypes:          deso.OperationTypes,
			Errors:                  append(Errors, deso.Errors...),
			HistoricalBalanceLookup: true,
		},
	}, nil
}
