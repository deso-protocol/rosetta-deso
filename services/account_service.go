package services

import (
	"context"
	"encoding/hex"
	"fmt"
	"github.com/deso-protocol/rosetta-deso/deso"
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

	dbView, err := lib.NewUtxoView(blockchain.DB(), node.Params, nil, node.Server.GetBlockchain().Snapshot(), nil)
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
	var blockHash *lib.BlockHash
	var blockHeight uint64
	if block.Hash != nil {
		hashBytes, err := hex.DecodeString(*block.Hash)
		if err != nil {
			return nil, wrapErr(ErrDeSo, err)
		}
		blockHash = lib.NewBlockHash(hashBytes)
		blockNode := node.GetBlockchain().GetBlockNodeWithHash(blockHash)
		if blockNode == nil {
			return nil, ErrBlockNotFound
		}
		blockHeight = blockNode.Header.Height
	} else if block.Index != nil {
		blockHeight = uint64(*block.Index)
		// We add +1 to the height, because blockNodes are indexed from height 0.
		if uint64(len(node.GetBlockchain().BestChain())) < blockHeight+1 {
			return nil, ErrBlockNotFound
		}

		// Make sure the blockNode has the correct height.
		if node.GetBlockchain().BestChain()[blockHeight].Header.Height != blockHeight {
			return nil, ErrBlockNotFound
		}
		blockHash = node.GetBlockchain().BestChain()[blockHeight].Hash
	} else {
		return nil, ErrBlockNotFound
	}

	publicKeyBytes, _, err := lib.Base58CheckDecode(account.Address)
	if err != nil {
		return nil, wrapErr(ErrInvalidPublicKey, err)
	}
	publicKey := lib.NewPublicKey(publicKeyBytes)

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

	utxoView, err := lib.NewUtxoView(blockchain.DB(), s.node.Params, nil, s.node.Server.GetBlockchain().Snapshot(), nil)
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
