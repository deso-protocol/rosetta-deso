package services

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"github.com/deso-protocol/rosetta-deso/deso"
	"github.com/golang/glog"
	"strconv"

	"github.com/coinbase/rosetta-sdk-go/server"
	"github.com/coinbase/rosetta-sdk-go/types"
	"github.com/deso-protocol/core/lib"
)

type AccountAPIService struct {
	config *deso.Config
	node   *deso.Node
}

func NewAccountAPIService(config *deso.Config, node *deso.Node) server.AccountAPIServicer {
	return &AccountAPIService{
		config: config,
		node:   node,
	}
}

func (s *AccountAPIService) AccountBalance(
	ctx context.Context,
	request *types.AccountBalanceRequest,
) (*types.AccountBalanceResponse, *types.Error) {
	if s.config.Mode != deso.Online {
		return nil, ErrUnavailableOffline
	}

	currentBlock := s.node.GetBlockchain().BlockTip()

	if request.BlockIdentifier == nil ||
		(request.BlockIdentifier.Hash != nil && *request.BlockIdentifier.Hash == currentBlock.Hash.String()) ||
		(request.BlockIdentifier.Index != nil && *request.BlockIdentifier.Index == int64(currentBlock.Height)) {
		return accountBalanceCurrent(s.node, request.AccountIdentifier)
	} else {
		return accountBalanceSnapshot(s.node, request.AccountIdentifier, request.BlockIdentifier)
	}
}

func accountBalanceCurrent(node *deso.Node, account *types.AccountIdentifier) (*types.AccountBalanceResponse, *types.Error) {
	publicKeyBytes, _, err := lib.Base58CheckDecode(account.Address)
	if err != nil {
		return nil, wrapErr(ErrInvalidPublicKey, err)
	}

	blockchain := node.GetBlockchain()
	currentBlock := blockchain.BlockTip()

	dbView, err := lib.NewUtxoView(blockchain.DB(), node.Params, nil, node.Server.GetBlockchain().Snapshot())
	if err != nil {
		return nil, wrapErr(ErrDeSo, err)
	}

	mempoolView, err := node.GetMempool().GetAugmentedUniversalView()
	if err != nil {
		return nil, wrapErr(ErrDeSo, err)
	}

	var dbBalance uint64
	var mempoolBalance uint64

	if account.SubAccount == nil {
		dbBalance, err = dbView.GetDeSoBalanceNanosForPublicKey(publicKeyBytes)
		if err != nil {
			return nil, wrapErr(ErrDeSo, err)
		}

		mempoolBalance, err = mempoolView.GetDeSoBalanceNanosForPublicKey(publicKeyBytes)
		if err != nil {
			return nil, wrapErr(ErrDeSo, err)
		}
	} else if account.SubAccount.Address == deso.CreatorCoin {
		dbProfileEntry := dbView.GetProfileEntryForPublicKey(publicKeyBytes)
		if dbProfileEntry != nil {
			dbBalance = dbProfileEntry.CreatorCoinEntry.DeSoLockedNanos
		}

		mempoolProfileEntry := mempoolView.GetProfileEntryForPublicKey(publicKeyBytes)
		if mempoolProfileEntry != nil {
			mempoolBalance = mempoolProfileEntry.CreatorCoinEntry.DeSoLockedNanos
		}
	}

	block := &types.BlockIdentifier{
		Index: int64(currentBlock.Height),
		Hash:  currentBlock.Hash.String(),
	}

	return &types.AccountBalanceResponse{
		BlockIdentifier: block,
		Balances: []*types.Amount{
			{
				Value:    strconv.FormatUint(dbBalance, 10),
				Currency: &deso.Currency,
				Metadata: map[string]interface{}{
					"MempoolBalance": strconv.FormatUint(mempoolBalance, 10),
				},
			},
		},
	}, nil
}

func accountBalanceSnapshot(node *deso.Node, account *types.AccountIdentifier, block *types.PartialBlockIdentifier) (*types.AccountBalanceResponse, *types.Error) {
	var desoBlock *lib.MsgDeSoBlock
	glog.Infof("got in block.Hash != nil (%v) | block.Index != nil (%v)", block.Hash != nil, block.Index != nil)
	if block.Hash != nil {
		hashBytes, err := hex.DecodeString(*block.Hash)
		if err != nil {
			return nil, wrapErr(ErrDeSo, err)
		}

		blockHash := &lib.BlockHash{}
		copy(blockHash[:], hashBytes[:])

		desoBlock = node.GetBlockchain().GetBlock(blockHash)
	} else if block.Index != nil {
		desoBlock = node.GetBlockchain().GetBlockAtHeight(uint32(*block.Index))
	} else {
		return nil, ErrBlockNotFound
	}

	if desoBlock == nil {
		return nil, ErrBlockNotFound
	}

	blockHash, _ := desoBlock.Hash()
	blockHeight := desoBlock.Header.Height

	publicKeyBytes, _, err := lib.Base58CheckDecode(account.Address)
	if err != nil {
		return nil, wrapErr(ErrInvalidPublicKey, err)
	}
	publicKey := lib.NewPublicKey(publicKeyBytes)
	glog.Infof("Get balance: pk (%v) blockheight (%v)", hex.EncodeToString(publicKeyBytes), desoBlock.Header.Height)
	defer glog.Infof("Finished get balance: pk (%v) blockheight (%v)", hex.EncodeToString(publicKeyBytes), desoBlock.Header.Height)

	snapshot := node.Server.GetBlockchain().Snapshot()
	if snapshot != nil &&
		snapshot.CurrentEpochSnapshotMetadata.FirstSnapshotBlockHeight != 0 {
		snapshotHeight := snapshot.CurrentEpochSnapshotMetadata.FirstSnapshotBlockHeight

		// If the block is before the snapshot height, we report the historical
		// balance as zero. See events.go for commentary on this. &&
		glog.Infof("stuck?")
		if blockHeight <= snapshotHeight {
			rangeStartBytes, rangeEndBytes := deso.BlockHeightToHypersyncFakeBalanceRange(blockHeight, snapshotHeight)
			if rangeStartBytes == nil || rangeEndBytes == nil {
				return &types.AccountBalanceResponse{
					BlockIdentifier: &types.BlockIdentifier{
						Hash:  blockHash.String(),
						Index: int64(blockHeight),
					},
					Balances: []*types.Amount{
						{
							Value:    strconv.FormatUint(0, 10),
							Currency: &deso.Currency,
						},
					},
				}, nil
			}
			greaterCondition := bytes.Compare(publicKeyBytes, rangeStartBytes) >= 0
			// Check that the iterator is below the rangeEnd.
			lesserCondition := bytes.Compare(publicKeyBytes, rangeEndBytes) < 0
			valid := greaterCondition
			if blockHeight < snapshotHeight {
				valid = valid && lesserCondition
			}

			glog.Infof("yeah")
			if !valid {
				return &types.AccountBalanceResponse{
					BlockIdentifier: &types.BlockIdentifier{
						Hash:  blockHash.String(),
						Index: int64(blockHeight),
					},
					Balances: []*types.Amount{
						{
							Value:    strconv.FormatUint(0, 10),
							Currency: &deso.Currency,
						},
					},
				}, nil
			} else {
				// If the block height is equal to the snapshot height, then we have a special case
				// here as well. See events.go for why this works the way it does.
				balance := uint64(0)
				if account.SubAccount == nil {
					balance = node.Index.GetHypersyncSingleBalanceSnapshot(false, publicKey)
				} else if account.SubAccount.Address == deso.CreatorCoin {
					balance = node.Index.GetHypersyncSingleBalanceSnapshot(true, publicKey)
				}

				// Look up the balances for this height. Just fetch all of them.
				return &types.AccountBalanceResponse{
					BlockIdentifier: &types.BlockIdentifier{
						Hash:  blockHash.String(),
						Index: int64(blockHeight),
					},
					Balances: []*types.Amount{
						{
							Value:    strconv.FormatUint(balance, 10),
							Currency: &deso.Currency,
						},
					},
				}, nil
			}
		}
	}

	var balance uint64
	if account.SubAccount == nil {
		balance = node.Index.GetBalanceSnapshot(false, publicKey, blockHeight)
	} else if account.SubAccount.Address == deso.CreatorCoin {
		balance = node.Index.GetBalanceSnapshot(true, publicKey, blockHeight)
	}

	//fmt.Printf("height: %v, addr (cc): %v, bal: %v\n", desoBlock.Header.Height, lib.PkToStringTestnet(publicKeyBytes), balance)
	return &types.AccountBalanceResponse{
		BlockIdentifier: &types.BlockIdentifier{
			Hash:  blockHash.String(),
			Index: int64(blockHeight),
		},
		Balances: []*types.Amount{
			{
				Value:    strconv.FormatUint(balance, 10),
				Currency: &deso.Currency,
			},
		},
	}, nil
}

func (s *AccountAPIService) AccountCoins(
	ctx context.Context,
	request *types.AccountCoinsRequest,
) (*types.AccountCoinsResponse, *types.Error) {
	if s.config.Mode != deso.Online {
		return nil, ErrUnavailableOffline
	}

	blockchain := s.node.GetBlockchain()
	currentBlock := blockchain.BlockTip()

	publicKeyBytes, _, err := lib.Base58CheckDecode(request.AccountIdentifier.Address)
	if err != nil {
		return nil, wrapErr(ErrInvalidPublicKey, err)
	}

	utxoView, err := lib.NewUtxoView(blockchain.DB(), s.node.Params, nil, s.node.Server.GetBlockchain().Snapshot())
	if err != nil {
		return nil, wrapErr(ErrDeSo, err)
	}

	utxoEntries, err := utxoView.GetUnspentUtxoEntrysForPublicKey(publicKeyBytes)
	if err != nil {
		return nil, wrapErr(ErrDeSo, err)
	}

	coins := []*types.Coin{}

	for _, utxoEntry := range utxoEntries {
		confirmations := uint64(currentBlock.Height) - uint64(utxoEntry.BlockHeight) + 1

		metadata, err := types.MarshalMap(&amountMetadata{
			Confirmations: confirmations,
		})
		if err != nil {
			return nil, wrapErr(ErrUnableToParseIntermediateResult, err)
		}

		coins = append(coins, &types.Coin{
			CoinIdentifier: &types.CoinIdentifier{
				Identifier: fmt.Sprintf("%v:%d", utxoEntry.UtxoKey.TxID.String(), utxoEntry.UtxoKey.Index),
			},
			Amount: &types.Amount{
				Value:    strconv.FormatUint(utxoEntry.AmountNanos, 10),
				Currency: s.config.Currency,
				Metadata: metadata,
			},
		})
	}

	block := &types.BlockIdentifier{
		Index: int64(currentBlock.Height),
		Hash:  currentBlock.Hash.String(),
	}

	result := &types.AccountCoinsResponse{
		BlockIdentifier: block,
		Coins:           coins,
	}

	return result, nil
}
