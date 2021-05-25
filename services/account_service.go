package services

import (
	"context"
	"fmt"
	"strconv"

	"github.com/bitclout/core/lib"
	"github.com/bitclout/rosetta-bitclout/bitclout"
	"github.com/bitclout/rosetta-bitclout/configuration"
	"github.com/coinbase/rosetta-sdk-go/server"
	"github.com/coinbase/rosetta-sdk-go/types"
)

type AccountAPIService struct {
	config *configuration.Configuration
	node   *bitclout.Node
}

func NewAccountAPIService(config *configuration.Configuration, node *bitclout.Node) server.AccountAPIServicer {
	return &AccountAPIService{
		config: config,
		node:   node,
	}
}

func (s *AccountAPIService) AccountBalance(
	ctx context.Context,
	request *types.AccountBalanceRequest,
) (*types.AccountBalanceResponse, *types.Error) {
	if s.config.Mode != configuration.Online {
		return nil, ErrUnavailableOffline
	}

	publicKeyBytes, _, err := lib.Base58CheckDecode(request.AccountIdentifier.Address)
	if err != nil {
		return nil, wrapErr(ErrInvalidPublicKey, err)
	}

	mempool := s.node.GetMempool()

	utxoView, err := mempool.GetAugmentedUtxoViewForPublicKey(publicKeyBytes, nil)
	if err != nil {
		return nil, wrapErr(ErrBitclout, err)
	}

	utxoEntries, err := utxoView.GetUnspentUtxoEntrysForPublicKey(publicKeyBytes)
	if err != nil {
		return nil, wrapErr(ErrBitclout, err)
	}

	blockchain := s.node.GetBlockchain()
	currentBlock := blockchain.BlockTip()

	var balance int64

	for _, utxoEntry := range utxoEntries {
		confirmations := int64(currentBlock.Height) - int64(utxoEntry.BlockHeight) + 1

		if confirmations > 0 {
			balance += int64(utxoEntry.AmountNanos)
		}
	}

	block := &types.BlockIdentifier{
		Index: int64(currentBlock.Height),
		Hash:  currentBlock.Hash.String(),
	}

	return &types.AccountBalanceResponse{
		BlockIdentifier: block,
		Balances: []*types.Amount{
			&types.Amount{
				Value:    strconv.FormatInt(balance, 10),
				Currency: s.config.Currency,
			},
		},
	}, nil
}

func (s *AccountAPIService) AccountCoins(
	ctx context.Context,
	request *types.AccountCoinsRequest,
) (*types.AccountCoinsResponse, *types.Error) {
	if s.config.Mode != configuration.Online {
		return nil, ErrUnavailableOffline
	}

	publicKeyBytes, _, err := lib.Base58CheckDecode(request.AccountIdentifier.Address)
	if err != nil {
		return nil, wrapErr(ErrInvalidPublicKey, err)
	}

	mempool := s.node.GetMempool()

	utxoView, err := mempool.GetAugmentedUtxoViewForPublicKey(publicKeyBytes, nil)
	if err != nil {
		return nil, wrapErr(ErrBitclout, err)
	}

	utxoEntries, err := utxoView.GetUnspentUtxoEntrysForPublicKey(publicKeyBytes)
	if err != nil {
		return nil, wrapErr(ErrBitclout, err)
	}

	blockchain := s.node.GetBlockchain()
	currentBlock := blockchain.BlockTip()

	coins := []*types.Coin{}

	for _, utxoEntry := range utxoEntries {
		confirmations := int64(currentBlock.Height) - int64(utxoEntry.BlockHeight) + 1

		if confirmations > 0 {
			coins = append(coins, &types.Coin{
				CoinIdentifier: &types.CoinIdentifier{
					Identifier: fmt.Sprintf("%v:%d", utxoEntry.UtxoKey.TxID.String(), utxoEntry.UtxoKey.Index),
				},
				Amount: &types.Amount{
					Value:    strconv.FormatUint(utxoEntry.AmountNanos, 10),
					Currency: s.config.Currency,
				},
			})
		}
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
