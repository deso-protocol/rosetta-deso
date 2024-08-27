package services

import (
	"context"
	"encoding/hex"
	"fmt"
	"github.com/deso-protocol/rosetta-deso/deso"
	"math"
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

	currentBlock := s.node.GetBlockchain().BlockTip()
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

func accountBalanceCurrent(node *deso.Node, account *types.AccountIdentifier) (*types.AccountBalanceResponse, *types.Error) {
	publicKeyBytes, _, err := lib.Base58CheckDecode(account.Address)
	if err != nil {
		return nil, wrapErr(ErrInvalidPublicKey, err)
	}

	blockchain := node.GetBlockchain()
	currentBlock := blockchain.BlockTip()

	dbView := lib.NewUtxoView(blockchain.DB(), node.Params, nil, node.Server.GetBlockchain().Snapshot(), nil)

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
	} else if account.SubAccount.Address == deso.ValidatorEntry {

		var dbValidatorEntry *lib.ValidatorEntry
		dbValidatorPKID := dbView.GetPKIDForPublicKey(publicKeyBytes).PKID
		dbValidatorEntry, err = dbView.GetValidatorByPKID(dbValidatorPKID)
		if err != nil {
			return nil, wrapErr(ErrDeSo, err)
		}
		if dbValidatorEntry != nil {
			if !dbValidatorEntry.TotalStakeAmountNanos.IsUint64() {
				return nil, wrapErr(ErrDeSo, fmt.Errorf("TotalStakeAmountNanos is not a uint64"))
			}
			dbBalance = dbValidatorEntry.TotalStakeAmountNanos.Uint64()
		}

		mempoolValidatorPKID := mempoolView.GetPKIDForPublicKey(publicKeyBytes).PKID

		var mempoolValidatorEntry *lib.ValidatorEntry
		mempoolValidatorEntry, err = mempoolView.GetValidatorByPKID(mempoolValidatorPKID)
		if err != nil {
			return nil, wrapErr(ErrDeSo, err)
		}
		if mempoolValidatorEntry != nil {
			if !mempoolValidatorEntry.TotalStakeAmountNanos.IsUint64() {
				return nil, wrapErr(ErrDeSo, fmt.Errorf("TotalStakeAmountNanos is not a uint64"))
			}
			mempoolBalance = mempoolValidatorEntry.TotalStakeAmountNanos.Uint64()
		}
	} else if strings.HasPrefix(account.SubAccount.Address, deso.LockedStakeEntry) {
		// Pull out the validator PKID
		var validatorPKID *lib.PKID
		validatorPKID, err = node.GetValidatorPKIDFromSubAccountIdentifier(account.SubAccount)
		if err != nil {
			return nil, wrapErr(ErrDeSo, err)
		}

		dbStakerPKID := dbView.GetPKIDForPublicKey(publicKeyBytes).PKID
		dbLockedStakeEntries, err := dbView.GetLockedStakeEntriesInRange(validatorPKID, dbStakerPKID, 0, math.MaxUint64)
		if err != nil {
			return nil, wrapErr(ErrDeSo, err)
		}
		dbRunningBalance := uint64(0)
		for _, dbLockedStakeEntry := range dbLockedStakeEntries {
			if !dbLockedStakeEntry.LockedAmountNanos.IsUint64() {
				return nil, wrapErr(ErrDeSo, fmt.Errorf("AmountNanos is not a uint64"))
			}
			dbRunningBalance, err = lib.SafeUint64().Add(dbRunningBalance, dbLockedStakeEntry.LockedAmountNanos.Uint64())
			if err != nil {
				return nil, wrapErr(ErrDeSo, err)
			}
		}
		dbBalance = dbRunningBalance

		mempoolStakerPKID := mempoolView.GetPKIDForPublicKey(publicKeyBytes).PKID

		mempoolStakeEntries, err := mempoolView.GetLockedStakeEntriesInRange(validatorPKID, mempoolStakerPKID, 0, math.MaxUint64)
		if err != nil {
			return nil, wrapErr(ErrDeSo, err)
		}
		mempoolRunningBalance := uint64(0)
		for _, mempoolStakeEntry := range mempoolStakeEntries {
			if !mempoolStakeEntry.LockedAmountNanos.IsUint64() {
				return nil, wrapErr(ErrDeSo, fmt.Errorf("AmountNanos is not a uint64"))
			}
			mempoolRunningBalance, err = lib.SafeUint64().Add(mempoolRunningBalance, mempoolStakeEntry.LockedAmountNanos.Uint64())
			if err != nil {
				return nil, wrapErr(ErrDeSo, err)
			}
		}
		mempoolBalance = mempoolRunningBalance
	} else {
		return nil, wrapErr(ErrDeSo, fmt.Errorf("Invalid SubAccount"))
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
