package services

import (
	"context"
	"encoding/hex"
	"fmt"
	"github.com/deso-protocol/rosetta-deso/deso"
	"strconv"
	"strings"

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

	currentBlock, idx := s.node.GetBlockchain().GetCommittedTip()
	if idx == -1 || currentBlock == nil {
		return nil, ErrBlockNotFound
	}
	currentBlockHash := currentBlock.Hash.String()
	currentBlockHeight := int64(currentBlock.Height)
	currentBlockIdentifier := &types.PartialBlockIdentifier{
		Index: &currentBlockHeight,
		Hash:  &currentBlockHash,
	}

	if request.BlockIdentifier == nil ||
		(request.BlockIdentifier.Hash != nil && *request.BlockIdentifier.Hash == currentBlock.Hash.String()) ||
		(request.BlockIdentifier.Index != nil && *request.BlockIdentifier.Index == int64(currentBlock.Height)) {
		return accountBalanceSnapshot(s.node, request.AccountIdentifier, currentBlockIdentifier)
	} else {
		return accountBalanceSnapshot(s.node, request.AccountIdentifier, request.BlockIdentifier)
	}
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
		committedTip, idx := node.GetBlockchain().GetCommittedTip()
		if idx == -1 || committedTip == nil {
			return nil, ErrBlockNotFound
		}
		// We add +1 to the height, because blockNodes are indexed from height 0.
		if uint64(committedTip.Height) < blockHeight {
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
	// We assume that address is a PKID for certain subaccounts. Parsing the bytes
	// as a NewPKID will give us the appropriate subaccount.
	pkid := lib.NewPKID(publicKeyBytes)
	var balance uint64
	if account.SubAccount == nil {
		balance = node.Index.GetBalanceSnapshot(deso.DESOBalance, publicKey, blockHeight)
	} else if account.SubAccount.Address == deso.CreatorCoin {
		balance = node.Index.GetBalanceSnapshot(deso.CreatorCoinLockedBalance, publicKey, blockHeight)
	} else if strings.HasPrefix(account.SubAccount.Address, deso.ValidatorEntry) {
		balance = node.Index.GetBalanceSnapshot(deso.ValidatorStakedDESOBalance, publicKey, blockHeight)
	} else if strings.HasPrefix(account.SubAccount.Address, deso.LockedStakeEntry) {
		validatorPKID, err := node.GetValidatorPKIDFromSubAccountIdentifier(account.SubAccount)
		if err != nil {
			return nil, wrapErr(ErrDeSo, err)
		}
		balance = node.Index.GetLockedStakeBalanceSnapshot(deso.LockedStakeDESOBalance, pkid, validatorPKID, 0, blockHeight)
	} else {
		return nil, wrapErr(ErrDeSo, fmt.Errorf("Invalid SubAccount"))
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

// TODO: this needs to be entirely rewritten for balance model.
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

	utxoView := lib.NewUtxoView(blockchain.DB(), s.node.Params, nil, s.node.Server.GetBlockchain().Snapshot(), nil)

	utxoEntries, err := utxoView.GetUnspentUtxoEntrysForPublicKey(publicKeyBytes)
	if err != nil {
		return nil, wrapErr(ErrDeSo, err)
	}

	// TODO: Update for balance model.
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
