package deso

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/golang/glog"
	"github.com/pkg/errors"
	"sort"
	"strconv"
	"strings"

	"github.com/coinbase/rosetta-sdk-go/types"
	"github.com/deso-protocol/core/lib"
)

func (node *Node) GetBlock(hash string) *types.Block {
	hashBytes, err := hex.DecodeString(hash)
	if err != nil {
		return nil
	}

	blockHash := &lib.BlockHash{}
	copy(blockHash[:], hashBytes[:])

	blockchain := node.GetBlockchain()
	blockNode := blockchain.GetBlockNodeWithHash(blockHash)
	if blockNode == nil {
		return nil
	}

	height := blockNode.Header.Height
	blockIdentifier := &types.BlockIdentifier{
		Index: int64(height),
		Hash:  hash,
	}

	var parentBlockIdentifier *types.BlockIdentifier
	if height == 0 {
		parentBlockIdentifier = blockIdentifier
	} else {
		parentBlockNode := blockchain.GetBlockNodeWithHash(blockNode.Header.PrevBlockHash)
		parentBlockHash, err := parentBlockNode.Header.Hash()
		if err != nil {
			glog.Error(errors.Wrapf(err, "GetBlock: Problem fetching parent block ndoe hash"))
		}
		parentBlockIdentifier = &types.BlockIdentifier{
			Index: int64(parentBlockNode.Header.Height),
			Hash:  parentBlockHash.String(),
		}
	}

	// If we've hypersynced the chain, we do something special. We need to return "fake genesis" blocks that
	// consolidates all balances up to this point. See commentary in events.go for more detail on how this works.
	snapshot := node.Server.GetBlockchain().Snapshot()
	if snapshot != nil && snapshot.CurrentEpochSnapshotMetadata.FirstSnapshotBlockHeight != 0 &&
		height <= snapshot.CurrentEpochSnapshotMetadata.FirstSnapshotBlockHeight {

		transactions := node.getBlockTransactionsWithHypersync(height, blockHash)
		// We return a mega-fake-genesis block with the hypersync account balances bootstrapped via output operations.
		return &types.Block{
			BlockIdentifier:       blockIdentifier,
			ParentBlockIdentifier: parentBlockIdentifier,
			Timestamp:             int64(blockNode.Header.TstampNanoSecs) / 1e6, // Convert nanoseconds to milliseconds
			Transactions:          transactions,
		}
	}

	block := blockchain.GetBlock(blockHash)
	if block == nil {
		return nil
	}
	// If we get here, we know we either don't have a snapshot, or we're past the first
	// snapshot height. This means we need to parse and return the transaction operations
	// like usual.
	return &types.Block{
		BlockIdentifier:       blockIdentifier,
		ParentBlockIdentifier: parentBlockIdentifier,
		Timestamp:             int64(blockNode.Header.TstampNanoSecs) / 1e6, // Convert nanoseconds to milliseconds
		Transactions:          node.GetTransactionsForConvertBlock(block),
	}
}

func (node *Node) GetBlockAtHeight(height int64) *types.Block {
	blockchain := node.GetBlockchain()
	// We add +1 to the height, because blockNodes are indexed from height 0.
	if int64(len(blockchain.BestChain())) < height+1 {
		return nil
	}

	// Make sure the blockNode has the correct height.
	if int64(blockchain.BestChain()[height].Header.Height) != height {
		return nil
	}
	blockHash := blockchain.BestChain()[height].Hash
	return node.GetBlock(blockHash.String())
}

func (node *Node) CurrentBlock() *types.Block {
	blockchain := node.GetBlockchain()

	return node.GetBlockAtHeight(int64(blockchain.BlockTip().Height))
}

func (node *Node) GetTransactionsForConvertBlock(block *lib.MsgDeSoBlock) []*types.Transaction {
	transactions := []*types.Transaction{}

	// Fetch the Utxo ops for this block
	utxoOpsForBlock, _ := node.Index.GetUtxoOps(block)

	// TODO: Can we be smarter about this size somehow?
	// 2x number of transactions feels like a good enough proxy for now
	spentUtxos := make(map[lib.UtxoKey]uint64, 2*len(utxoOpsForBlock))

	// Find all spent UTXOs for this block
	for _, utxoOps := range utxoOpsForBlock {
		for _, utxoOp := range utxoOps {
			if utxoOp.Type == lib.OperationTypeSpendUtxo {
				spentUtxos[*utxoOp.Entry.UtxoKey] = utxoOp.Entry.AmountNanos
			}
		}
	}

	for txnIndexInBlock, txn := range block.Txns {
		metadataJSON, _ := json.Marshal(txn.TxnMeta)

		var metadata map[string]interface{}
		_ = json.Unmarshal(metadataJSON, &metadata)

		txnHash := txn.Hash().String()

		// DeSo started with a UTXO model but switched to a balance model at a particular block
		// height. We need to handle both cases here.
		isBalanceModelTxn := false
		if block.Header.Height >= uint64(node.Params.ForkHeights.BalanceModelBlockHeight) {
			isBalanceModelTxn = true
		}

		metadata["TxnVersion"] = uint64(txn.TxnVersion)
		metadata["TxnType"] = txn.TxnMeta.GetTxnType().String()

		if isBalanceModelTxn {
			if txn.TxnNonce != nil {
				metadata["TxnNonce"] = map[string]uint64{
					"ExpirationBlockHeight": txn.TxnNonce.ExpirationBlockHeight,
					"PartialID":             txn.TxnNonce.PartialID,
				}
			}

			metadata["TxnFeeNanos"] = txn.TxnFeeNanos
		}

		transaction := &types.Transaction{
			TransactionIdentifier: &types.TransactionIdentifier{Hash: txnHash},
			Metadata:              metadata,
		}

		var ops []*types.Operation

		for _, input := range txn.TxInputs {
			// Fetch the input amount from Rosetta Index
			spentAmount, amountExists := spentUtxos[lib.UtxoKey{
				TxID:  input.TxID,
				Index: input.Index,
			}]
			if !amountExists {
				fmt.Printf("Error: input missing for txn %v index %v\n", lib.PkToStringBoth(input.TxID[:]), input.Index)
			}

			amount := &types.Amount{
				Value:    strconv.FormatInt(int64(spentAmount)*-1, 10),
				Currency: &Currency,
			}

			op := &types.Operation{
				OperationIdentifier: &types.OperationIdentifier{
					Index: int64(len(ops)),
				},

				Account: &types.AccountIdentifier{
					Address: lib.Base58CheckEncode(txn.PublicKey, false, node.Params),
				},

				Amount: amount,

				Status: &SuccessStatus,
				Type:   InputOpType,
			}

			ops = append(ops, op)
		}

		// If we are dealing with a legacy UTXO transaction, then we need to add the outputs from
		// the transaction directly rather than relying on the UtxoOps.
		if !isBalanceModelTxn {
			for _, output := range txn.TxOutputs {
				op := &types.Operation{
					OperationIdentifier: &types.OperationIdentifier{
						Index: int64(len(ops)),
					},

					Account: &types.AccountIdentifier{
						Address: lib.Base58CheckEncode(output.PublicKey, false, node.Params),
					},

					Amount: &types.Amount{
						Value:    strconv.FormatUint(output.AmountNanos, 10),
						Currency: &Currency,
					},

					Status: &SuccessStatus,
					Type:   OutputOpType,
				}

				ops = append(ops, op)
			}
		}

		// Add all the special ops for specific txn types.
		if len(utxoOpsForBlock) > 0 {
			utxoOpsForTxn := utxoOpsForBlock[txnIndexInBlock]

			// Get balance model spends
			balanceModelSpends := node.getBalanceModelSpends(txn, utxoOpsForTxn, len(ops))
			ops = append(ops, balanceModelSpends...)

			// Add implicit outputs from UtxoOps
			implicitOutputs := node.getImplicitOutputs(txn, utxoOpsForTxn, len(ops))
			ops = append(ops, implicitOutputs...)

			// Add inputs/outputs for creator coins
			creatorCoinOps := node.getCreatorCoinOps(txn, utxoOpsForTxn, len(ops))
			ops = append(ops, creatorCoinOps...)

			// Add inputs/outputs for swap identity
			swapIdentityOps := node.getSwapIdentityOps(txn, utxoOpsForTxn, len(ops))
			ops = append(ops, swapIdentityOps...)

			// Add inputs for accept nft bid
			acceptNftOps := node.getAcceptNFTOps(txn, utxoOpsForTxn, len(ops))
			ops = append(ops, acceptNftOps...)

			// Add inputs for bids on Buy Now NFTs
			buyNowNftBidOps := node.getBuyNowNFTBidOps(txn, utxoOpsForTxn, len(ops))
			ops = append(ops, buyNowNftBidOps...)

			// Add inputs for update profile
			updateProfileOps := node.getUpdateProfileOps(txn, utxoOpsForTxn, len(ops))
			ops = append(ops, updateProfileOps...)

			// Add inputs for DAO Coin Limit Orders
			daoCoinLimitOrderOps := node.getDAOCoinLimitOrderOps(txn, utxoOpsForTxn, len(ops))
			ops = append(ops, daoCoinLimitOrderOps...)

			// Add operations for stake transactions
			stakeOps := node.getStakeOps(txn, utxoOpsForTxn, len(ops))
			ops = append(ops, stakeOps...)

			// Add operations for unstake transactions
			unstakeOps := node.getUnstakeOps(txn, utxoOpsForTxn, len(ops))
			ops = append(ops, unstakeOps...)

			// Add operations for unlock stake transactions
			unlockStakeOps := node.getUnlockStakeOps(txn, utxoOpsForTxn, len(ops))
			ops = append(ops, unlockStakeOps...)
		}

		transaction.Operations = squashOperations(ops)

		transactions = append(transactions, transaction)
	}

	// Create a dummy transaction for the "block level" operations
	// when we have the additional slice of utxo operations. This is used
	// to capture staking rewards that are paid out at the block level, but
	// have no transaction associated with them. The array of utxo operations
	// at the index of # transactions in block + 1 is ALWAYS the block level
	// utxo operations. At the end of a PoS epoch, we distribute staking rewards
	// by either adding to the stake entry with the specified validator or
	// directly deposit staking rewards to the staker's DESO balance. This is based
	// on the configuration a user specifies when staking with a validator.
	// We capture the non-restaked rewards the same way we capture basic transfer outputs,
	// but with a dummy transaction. These are simple OUTPUTS to a staker's DESO balance.
	// We capture the restaked rewards by adding an OUTPUT
	// to the valiator's subaccount.
	if len(utxoOpsForBlock) == len(block.Txns)+1 {
		blockHash, err := block.Hash()
		if err != nil {
			// This is bad if this happens.
			glog.Error(errors.Wrapf(err, "GetTransactionsForConvertBlock: Problem fetching block hash"))
			return transactions
		}
		utxoOpsForBlockLevel := utxoOpsForBlock[len(utxoOpsForBlock)-1]
		var ops []*types.Operation
		// Add outputs for Stake Reward Distributions that are not re-staked. We use a fake transaction to
		// avoid panic in getStakingRewardDistributionOps.
		implicitOutputOps := node.getImplicitOutputs(&lib.MsgDeSoTxn{TxOutputs: nil}, utxoOpsForBlockLevel, len(ops))
		ops = append(ops, implicitOutputOps...)
		// Add outputs for Stake Reward Distributions that are re-staked
		stakeRewardOps := node.getStakingRewardDistributionOps(utxoOpsForBlockLevel, len(ops))
		ops = append(ops, stakeRewardOps...)

		transaction := &types.Transaction{
			TransactionIdentifier: &types.TransactionIdentifier{Hash: fmt.Sprintf("blockHash-%v", blockHash.String())},
		}
		transaction.Operations = squashOperations(ops)
		transactions = append(transactions, transaction)
	}

	return transactions
}

func (node *Node) getBlockTransactionsWithHypersync(blockHeight uint64, blockHash *lib.BlockHash) []*types.Transaction {
	// With hypersync, we don't necessarily have to download the block history, unless we're in the archival mode.
	// Otherwise, we'll only download the database snapshot at the FirstSnapshotBlockHeight. Assuming we don't know
	// the block history, we need to somehow create an "alternative" block history for Rosetta to work. Given the
	// snapshot, we have information about how much money each account has (single balance), but we don't know
	// account's transactional history. Our alternative blockchain up to FirstSnapshotBlockHeight, will then
	// basically consist of fake seed transactions. That is, transactions of type "credit PK with X balance." To make
	// it somewhat efficient, we will evenly distribute all these fake seed transactions among blocks up to snapshot.
	if blockHeight == 0 {
		return []*types.Transaction{}
	}

	// TODO: do we need staked and locked stake balances here?
	balances, lockedBalances, stakedDESOBalances, lockedStakeDESOBalances := node.Index.GetHypersyncBlockBalances(blockHeight)

	// We create a fake genesis block that will contain a portion of the balances downloaded during hypersync.
	// In addition, we need to lowkey reinvent these transactions, including, in particular, their transaction
	// hashes. To make these hashes deterministic and pseudorandom, we will start with a seed hash SH equal to
	// the reversed hex string of the block hash. This has high entropy, is prone to grinding attacks, and we'll
	// use it to generate transaction hashes. Each transaction will be a result of the iterated hash
	// SH <- sha256x2(SH), which is equivalent in collision-resistance to hashing a random string.
	reverse := func(s string) string {
		runes := []rune(s)
		for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
			runes[i], runes[j] = runes[j], runes[i]
		}
		return string(runes)
	}
	currentHash := reverse(blockHash.String())
	var transactions []*types.Transaction
	for pk, balance := range balances {
		if balance == 0 {
			continue
		}

		// Here's the iterated hash SH <- sha256x2(SH).
		nextHash := lib.Sha256DoubleHash([]byte(currentHash))
		currentHash = string(nextHash[:])
		transactions = append(transactions, &types.Transaction{
			TransactionIdentifier: &types.TransactionIdentifier{
				Hash: nextHash.String(),
			},
			Operations: squashOperations([]*types.Operation{
				{
					OperationIdentifier: &types.OperationIdentifier{
						Index: 0,
					},
					Account: &types.AccountIdentifier{
						Address: lib.Base58CheckEncode(pk[:], false, node.Params),
					},
					Amount: &types.Amount{
						Value:    strconv.FormatUint(balance, 10),
						Currency: &Currency,
					},
					Status: &SuccessStatus,
					Type:   OutputOpType,
				},
			}),
		})
	}
	for pk, balance := range lockedBalances {
		if balance == 0 {
			continue
		}

		nextHash := lib.Sha256DoubleHash([]byte(currentHash))
		currentHash = string(nextHash[:])
		transactions = append(transactions, &types.Transaction{
			TransactionIdentifier: &types.TransactionIdentifier{
				Hash: nextHash.String(),
			},
			Operations: squashOperations([]*types.Operation{
				{
					OperationIdentifier: &types.OperationIdentifier{
						Index: 0,
					},
					Account: &types.AccountIdentifier{
						Address: lib.Base58CheckEncode(pk[:], false, node.Params),
						SubAccount: &types.SubAccountIdentifier{
							Address: CreatorCoin,
						},
					},
					Amount: &types.Amount{
						Value:    strconv.FormatUint(balance, 10),
						Currency: &Currency,
					},
					Status: &SuccessStatus,
					Type:   OutputOpType,
				},
			}),
		})
	}

	// We create a fake genesis block of all the staked DESO at the latest snapshot height.
	// We represent all DESO staked to a single validator as a single subaccount.
	// This mirrors how we treat creator coins, a pool of DESO as a subaccount of the creator.
	// This acts as a pool of all the DESO staked in StakeEntry objects with a given Validator.
	// We chose to use validators instead of stakers here as there are fewer validators and
	// thus fewer subaccounts to keep track of. Note that we are using PKIDs for the validator
	// instead of a public key, so we do not need to track balance changes when a validator
	// has a swap identity performed on it.
	// Account identifier: Validator PKID
	// Subaccount identifier: VALIDATOR_ENTRY
	for pkid, balance := range stakedDESOBalances {
		if balance == 0 {
			continue
		}
		nextHash := lib.Sha256DoubleHash([]byte(currentHash))
		currentHash = string(nextHash[:])
		transactions = append(transactions, &types.Transaction{
			TransactionIdentifier: &types.TransactionIdentifier{
				Hash: nextHash.String(),
			},
			Operations: squashOperations([]*types.Operation{
				{
					OperationIdentifier: &types.OperationIdentifier{
						Index: 0,
					},
					Account: node.getValidatorEntrySubAccountIdentifierForValidator(lib.NewPKID(pkid[:])),
					Amount: &types.Amount{
						Value:    strconv.FormatUint(balance, 10),
						Currency: &Currency,
					},
					Status: &SuccessStatus,
					Type:   OutputOpType,
				},
			}),
		})
	}

	// We create a fake genesis block of all the locked stake DESO at the latest snapshot height.
	// Locked stake DESO is represented as a subaccount of the staker. A locked stake entry is
	// unique by the combination of Staker PKID + Validator PKID + LockedAtEpochNumber, so we
	// use Validator PKID + LockedAtEpochNumber as the subaccount identifier. Note that we are
	// using PKIDs for the validator and staker instead of public keys, so we do not need to
	// track balance changes if a swap identity were performed on either.
	// Account Identifier: Staker PKID
	// Subaccount Identifier: LOCKED_STAKE_ENTRY || Validator PKID || LockedAtEpochNumber
	for lockedStakeBalanceMapKey, balance := range lockedStakeDESOBalances {
		if balance == 0 {
			continue
		}
		nextHash := lib.Sha256DoubleHash([]byte(currentHash))
		currentHash = string(nextHash[:])
		transactions = append(transactions, &types.Transaction{
			TransactionIdentifier: &types.TransactionIdentifier{
				Hash: nextHash.String(),
			},
			Operations: squashOperations([]*types.Operation{
				{
					OperationIdentifier: &types.OperationIdentifier{
						Index: 0,
					},
					Account: node.getLockedStakeEntryIdentifierForValidator(
						&lockedStakeBalanceMapKey.StakerPKID,
						&lockedStakeBalanceMapKey.ValidatorPKID,
						lockedStakeBalanceMapKey.LockedAtEpochNumber,
					),
					Amount: &types.Amount{
						Value:    strconv.FormatUint(balance, 10),
						Currency: &Currency,
					},
					Status: &SuccessStatus,
					Type:   OutputOpType,
				},
			}),
		})
	}

	sort.Slice(transactions, func(ii, jj int) bool {
		return bytes.Compare([]byte(transactions[ii].TransactionIdentifier.Hash),
			[]byte(transactions[jj].TransactionIdentifier.Hash)) > 0
	})
	return transactions
}

type partialAccountIdentifier struct {
	Address    string
	SubAddress string
}

func squashOperations(ops []*types.Operation) []*types.Operation {
	opMap := make(map[partialAccountIdentifier]int64, len(ops))
	var squashedOps []*types.Operation
	nextOp := int64(0)

	for _, op := range ops {
		account := newPartialAccountIdentifier(op.Account)
		opIndex, exists := opMap[account]

		if exists {
			existingOp := squashedOps[opIndex]
			oldAmount, _ := strconv.ParseInt(existingOp.Amount.Value, 10, 64)
			addAmount, _ := strconv.ParseInt(op.Amount.Value, 10, 64)
			existingOp.Amount.Value = strconv.FormatInt(oldAmount+addAmount, 10)
		} else {
			opMap[account] = nextOp
			op.OperationIdentifier.Index = nextOp
			squashedOps = append(squashedOps, op)
			nextOp += 1
		}
	}

	return squashedOps
}

func newPartialAccountIdentifier(accountIdentifier *types.AccountIdentifier) partialAccountIdentifier {
	if accountIdentifier.SubAccount != nil {
		return partialAccountIdentifier{
			Address:    accountIdentifier.Address,
			SubAddress: accountIdentifier.SubAccount.Address,
		}
	} else {
		return partialAccountIdentifier{
			Address: accountIdentifier.Address,
		}
	}
}

func (node *Node) getCreatorCoinOps(txn *lib.MsgDeSoTxn, utxoOpsForTxn []*lib.UtxoOperation, numOps int) []*types.Operation {
	// If we're not dealing with a CreatorCoin txn then we don't have any creator
	// coin ops to add.
	if txn.TxnMeta.GetTxnType() != lib.TxnTypeCreatorCoin {
		return nil
	}
	// We extract the metadata and assume that we're dealing with a creator coin txn.
	txnMeta := txn.TxnMeta.(*lib.CreatorCoinMetadataa)

	var operations []*types.Operation

	// Extract creator public key
	creatorPublicKey := lib.PkToString(txnMeta.ProfilePublicKey, node.Params)

	// Extract the CreatorCoinOperation from tne UtxoOperations passed in
	var creatorCoinOp *lib.UtxoOperation
	for _, utxoOp := range utxoOpsForTxn {
		if utxoOp.Type == lib.OperationTypeCreatorCoin {
			creatorCoinOp = utxoOp
			break
		}
	}
	if creatorCoinOp == nil {
		fmt.Printf("Error: Missing UtxoOperation for CreaotrCoin txn: %v\n", txn.Hash())
		return nil
	}

	account := &types.AccountIdentifier{
		Address: creatorPublicKey,
		SubAccount: &types.SubAccountIdentifier{
			Address: CreatorCoin,
		},
	}

	// This amount is negative for sells and positive for buys
	amount := &types.Amount{
		Value:    strconv.FormatInt(creatorCoinOp.CreatorCoinDESOLockedNanosDiff, 10),
		Currency: &Currency,
	}

	if txnMeta.OperationType == lib.CreatorCoinOperationTypeSell {
		// Selling a creator coin uses the creator coin as input
		operations = append(operations, &types.Operation{
			OperationIdentifier: &types.OperationIdentifier{
				Index: int64(numOps),
			},
			Type:    InputOpType,
			Status:  &SuccessStatus,
			Account: account,
			Amount:  amount,
		})
	} else if txnMeta.OperationType == lib.CreatorCoinOperationTypeBuy {
		// Buying the creator coin generates an output for the creator coin
		operations = append(operations, &types.Operation{
			OperationIdentifier: &types.OperationIdentifier{
				Index: int64(numOps),
			},
			Type:    OutputOpType,
			Status:  &SuccessStatus,
			Account: account,
			Amount:  amount,
		})
	}

	return operations
}

func (node *Node) getSwapIdentityOps(txn *lib.MsgDeSoTxn, utxoOpsForTxn []*lib.UtxoOperation, numOps int) []*types.Operation {
	// We only deal with SwapIdentity txns in this function
	if txn.TxnMeta.GetTxnType() != lib.TxnTypeSwapIdentity {
		return nil
	}
	realTxMeta := txn.TxnMeta.(*lib.SwapIdentityMetadataa)

	// Extract the SwapIdentity op
	var swapIdentityOp *lib.UtxoOperation
	for _, utxoOp := range utxoOpsForTxn {
		if utxoOp.Type == lib.OperationTypeSwapIdentity {
			swapIdentityOp = utxoOp
			break
		}
	}
	if swapIdentityOp == nil {
		fmt.Printf("Error: Missing UtxoOperation for SwapIdentity txn: %v\n", txn.Hash())
		return nil
	}

	var operations []*types.Operation

	fromAccount := &types.AccountIdentifier{
		Address: lib.PkToString(realTxMeta.FromPublicKey, node.Params),
		SubAccount: &types.SubAccountIdentifier{
			Address: CreatorCoin,
		},
	}

	toAccount := &types.AccountIdentifier{
		Address: lib.PkToString(realTxMeta.ToPublicKey, node.Params),
		SubAccount: &types.SubAccountIdentifier{
			Address: CreatorCoin,
		},
	}

	// ToDeSoLockedNanos and FromDeSoLockedNanos
	// are the total DESO locked for the respective accounts after the swap has occurred.

	// We subtract the now-swaped amounts from the opposite accounts
	operations = append(operations, &types.Operation{
		OperationIdentifier: &types.OperationIdentifier{
			Index: int64(numOps),
		},
		Type:    InputOpType,
		Status:  &SuccessStatus,
		Account: fromAccount,
		Amount: &types.Amount{
			Value:    strconv.FormatInt(int64(swapIdentityOp.SwapIdentityFromDESOLockedNanos)*-1, 10),
			Currency: &Currency,
		},
	})

	operations = append(operations, &types.Operation{
		OperationIdentifier: &types.OperationIdentifier{
			Index: int64(numOps) + 1,
		},
		Type:    InputOpType,
		Status:  &SuccessStatus,
		Account: toAccount,
		Amount: &types.Amount{
			Value:    strconv.FormatInt(int64(swapIdentityOp.SwapIdentityToDESOLockedNanos)*-1, 10),
			Currency: &Currency,
		},
	})

	// Then we add the now-swapped amounts to the correct accounts
	operations = append(operations, &types.Operation{
		OperationIdentifier: &types.OperationIdentifier{
			Index: int64(numOps) + 2,
		},
		Type:    OutputOpType,
		Status:  &SuccessStatus,
		Account: fromAccount,
		Amount: &types.Amount{
			Value:    strconv.FormatUint(swapIdentityOp.SwapIdentityToDESOLockedNanos, 10),
			Currency: &Currency,
		},
	})

	operations = append(operations, &types.Operation{
		OperationIdentifier: &types.OperationIdentifier{
			Index: int64(numOps) + 3,
		},
		Type:    OutputOpType,
		Status:  &SuccessStatus,
		Account: toAccount,
		Amount: &types.Amount{
			Value:    strconv.FormatUint(swapIdentityOp.SwapIdentityFromDESOLockedNanos, 10),
			Currency: &Currency,
		},
	})

	// TODO: Do we need to do this for ValidatorEntry and LockedStakeEntries as well? If so,
	// we'll need to expose add ToValidatorEntry, FromValidatorEntry, ToLockedStakeEntries, and
	// FromLockedStakeEntries to the UtxoOperation.

	return operations
}

func addNFTRoyalties(ops []*types.Operation, numOps int,
	royalties []*lib.PublicKeyRoyaltyPair, params *lib.DeSoParams) (
	_ops []*types.Operation, _numOps int) {

	// Add outputs for each additional creator coin royalty
	for _, publicKeyRoyaltyPair := range royalties {
		if publicKeyRoyaltyPair.RoyaltyAmountNanos == 0 {
			continue
		}
		coinRoyaltyAccount := &types.AccountIdentifier{
			Address: lib.PkToString(publicKeyRoyaltyPair.PublicKey, params),
			SubAccount: &types.SubAccountIdentifier{
				Address: CreatorCoin,
			},
		}
		ops = append(ops, &types.Operation{
			OperationIdentifier: &types.OperationIdentifier{
				Index: int64(numOps),
			},
			Type:    OutputOpType,
			Status:  &SuccessStatus,
			Account: coinRoyaltyAccount,
			Amount: &types.Amount{
				Value:    strconv.FormatUint(publicKeyRoyaltyPair.RoyaltyAmountNanos, 10),
				Currency: &Currency,
			},
		})
		numOps += 1
	}

	return ops, numOps
}

func (node *Node) getAcceptNFTOps(txn *lib.MsgDeSoTxn, utxoOpsForTxn []*lib.UtxoOperation, numOps int) []*types.Operation {
	if txn.TxnMeta.GetTxnType() != lib.TxnTypeAcceptNFTBid {
		return nil
	}
	realTxnMeta := txn.TxnMeta.(*lib.AcceptNFTBidMetadata)

	// Extract the AcceptNFTBid op
	var acceptNFTOp *lib.UtxoOperation
	for _, utxoOp := range utxoOpsForTxn {
		if utxoOp.Type == lib.OperationTypeAcceptNFTBid {
			acceptNFTOp = utxoOp
			break
		}
	}
	if acceptNFTOp == nil {
		fmt.Printf("Error: Missing UtxoOperation for AcceptNFTBid txn: %v\n", txn.Hash())
		return nil
	}

	var operations []*types.Operation

	royaltyAccount := &types.AccountIdentifier{
		Address: lib.PkToString(acceptNFTOp.AcceptNFTBidCreatorPublicKey, node.Params),
		SubAccount: &types.SubAccountIdentifier{
			Address: CreatorCoin,
		},
	}

	// Add an operation for each bidder input we consume
	totalBidderInput := int64(0)
	for _, input := range realTxnMeta.BidderInputs {
		// TODO(performance): This function is a bit inefficient because it runs through *all*
		// the UTXOOps every time.
		inputAmount := node.getInputAmount(input, utxoOpsForTxn)
		if inputAmount == nil {
			fmt.Printf("Error: AcceptNFTBid input was null for input: %v", input)
			return nil
		}

		// Track the total amount the bidder had as input
		currentInputValue, err := strconv.ParseInt(inputAmount.Value, 10, 64)
		if err != nil {
			fmt.Printf("Error: Could not parse input amount in AcceptNFTBid: %v\n", err)
			return nil
		}
		totalBidderInput += currentInputValue

		operations = append(operations, &types.Operation{
			OperationIdentifier: &types.OperationIdentifier{
				Index: int64(numOps),
			},
			Type:   InputOpType,
			Status: &SuccessStatus,
			Account: &types.AccountIdentifier{
				Address: lib.PkToString(acceptNFTOp.AcceptNFTBidBidderPublicKey, node.Params),
			},
			Amount: inputAmount,
		})

		numOps += 1
	}

	// Note that the implicit bidder change output is covered by another
	// function that adds implicit outputs automatically using the UtxoOperations

	// Add an output representing the creator coin royalty only if there
	// are enough creator coins in circulation
	//
	// TODO: This if statement is needed temporarily to fix a bug whereby
	// AcceptNFTBidCreatorRoyaltyNanos is non-zero even when the royalty given
	// was zero due to this check in consensus.
	if acceptNFTOp.PrevCoinEntry.CoinsInCirculationNanos.Uint64() >= node.Params.CreatorCoinAutoSellThresholdNanos &&
		acceptNFTOp.AcceptNFTBidCreatorRoyaltyNanos > 0 {
		operations = append(operations, &types.Operation{
			OperationIdentifier: &types.OperationIdentifier{
				Index: int64(numOps),
			},
			Type:    OutputOpType,
			Status:  &SuccessStatus,
			Account: royaltyAccount,
			Amount: &types.Amount{
				Value:    strconv.FormatUint(acceptNFTOp.AcceptNFTBidCreatorRoyaltyNanos, 10),
				Currency: &Currency,
			},
		})
		numOps += 1
	}

	operations, numOps = addNFTRoyalties(
		operations, numOps, acceptNFTOp.AcceptNFTBidAdditionalCoinRoyalties, node.Params)

	return operations
}

func (node *Node) getBuyNowNFTBidOps(txn *lib.MsgDeSoTxn, utxoOpsForTxn []*lib.UtxoOperation, numOps int) []*types.Operation {
	if txn.TxnMeta.GetTxnType() != lib.TxnTypeNFTBid {
		return nil
	}

	// Extract the NFTBid op
	var nftBidOp *lib.UtxoOperation
	for _, utxoOp := range utxoOpsForTxn {
		if utxoOp.Type == lib.OperationTypeNFTBid {
			nftBidOp = utxoOp
			break
		}
	}
	if nftBidOp == nil {
		fmt.Printf("Error: Missing UtxoOperation for NFTBid txn: %v\n", txn.Hash())
		return nil
	}

	// We only care about NFT bids that generate creator royalties. This only occurs for NFT bids on Buy Now NFTs that
	// exceed the Buy Now Price. Only NFT bids that exceed the Buy Now Price on Buy Now NFTs will have
	// NFTBidCreatorRoyaltyNanos > 0.
	var operations []*types.Operation

	royaltyAccount := &types.AccountIdentifier{
		Address: lib.PkToString(nftBidOp.NFTBidCreatorPublicKey, node.Params),
		SubAccount: &types.SubAccountIdentifier{
			Address: CreatorCoin,
		},
	}

	// Add an output representing the creator coin royalty only if there
	// are enough creator coins in circulation
	//
	// TODO: This if statement is needed temporarily to fix a bug whereby
	// NFTBidCreatorRoyaltyNanos is non-zero even when the royalty given
	// was zero due to this check in consensus.
	if nftBidOp.PrevCoinEntry != nil &&
		nftBidOp.PrevCoinEntry.CoinsInCirculationNanos.Uint64() >= node.Params.CreatorCoinAutoSellThresholdNanos &&
		nftBidOp.NFTBidCreatorRoyaltyNanos > 0 {

		operations = append(operations, &types.Operation{
			OperationIdentifier: &types.OperationIdentifier{
				Index: int64(numOps),
			},
			Type:    OutputOpType,
			Status:  &SuccessStatus,
			Account: royaltyAccount,
			Amount: &types.Amount{
				Value:    strconv.FormatUint(nftBidOp.NFTBidCreatorRoyaltyNanos, 10),
				Currency: &Currency,
			},
		})
		numOps += 1
	}

	operations, numOps = addNFTRoyalties(
		operations, numOps, nftBidOp.NFTBidAdditionalCoinRoyalties, node.Params)

	return operations
}

func (node *Node) getUpdateProfileOps(txn *lib.MsgDeSoTxn, utxoOpsForTxn []*lib.UtxoOperation, numOps int) []*types.Operation {
	if txn.TxnMeta.GetTxnType() != lib.TxnTypeUpdateProfile {
		return nil
	}

	var operations []*types.Operation
	var amount *types.Amount

	for _, utxoOp := range utxoOpsForTxn {
		if utxoOp.Type == lib.OperationTypeUpdateProfile {
			if utxoOp.ClobberedProfileBugDESOLockedNanos > 0 {
				amount = &types.Amount{
					Value:    strconv.FormatInt(int64(utxoOp.ClobberedProfileBugDESOLockedNanos)*-1, 10),
					Currency: &Currency,
				}
			}
			break
		}
	}

	if amount == nil {
		return nil
	}

	// Add an input representing the clobbered nanos
	operations = append(operations, &types.Operation{
		OperationIdentifier: &types.OperationIdentifier{
			Index: int64(numOps),
		},
		Type:   InputOpType,
		Status: &SuccessStatus,
		Account: &types.AccountIdentifier{
			Address: lib.Base58CheckEncode(txn.PublicKey, false, node.Params),
			SubAccount: &types.SubAccountIdentifier{
				Address: CreatorCoin,
			},
		},
		Amount: amount,
	})

	return operations
}

func (node *Node) getDAOCoinLimitOrderOps(txn *lib.MsgDeSoTxn, utxoOpsForTxn []*lib.UtxoOperation, numOps int) []*types.Operation {
	if txn.TxnMeta.GetTxnType() != lib.TxnTypeDAOCoinLimitOrder {
		return nil
	}

	var operations []*types.Operation
	for _, bidderInput := range txn.TxnMeta.(*lib.DAOCoinLimitOrderMetadata).BidderInputs {
		bidderPublicKey := lib.Base58CheckEncode(bidderInput.TransactorPublicKey.ToBytes(), false, node.Params)
		for _, input := range bidderInput.Inputs {
			inputAmount := node.getInputAmount(input, utxoOpsForTxn)
			if inputAmount == nil {
				fmt.Printf("Error: DAOCoinLimitOrder input was null for input: %v", input)
				return nil
			}

			operations = append(operations, &types.Operation{
				OperationIdentifier: &types.OperationIdentifier{
					Index: int64(numOps),
				},
				Type:   InputOpType,
				Status: &SuccessStatus,
				Account: &types.AccountIdentifier{
					Address: bidderPublicKey,
				},
				Amount: inputAmount,
			})
			numOps++
		}

	}
	return operations
}

func (node *Node) getBalanceModelSpends(txn *lib.MsgDeSoTxn, utxoOpsForTxn []*lib.UtxoOperation, numOps int) []*types.Operation {
	var operations []*types.Operation

	for _, utxoOp := range utxoOpsForTxn {
		if utxoOp.Type == lib.OperationTypeSpendBalance {
			operations = append(operations, &types.Operation{
				OperationIdentifier: &types.OperationIdentifier{
					Index: int64(numOps),
				},
				Account: &types.AccountIdentifier{
					Address: lib.Base58CheckEncode(utxoOp.BalancePublicKey, false, node.Params),
				},
				Amount: &types.Amount{
					// We need to negate this value because it's an input, which means it's subtracting from
					// the account's balance.
					Value:    strconv.FormatInt(int64(utxoOp.BalanceAmountNanos)*-1, 10),
					Currency: &Currency,
				},

				Status: &SuccessStatus,
				Type:   InputOpType,
			})
			numOps++
		}
	}
	return operations
}

// getLockedStakeEntrySubAccountIdentifierForValidator returns a SubAccountIdentifier for a locked stake entry.
func (node *Node) getLockedStakeEntryIdentifierForValidator(stakerPKID *lib.PKID, validatorPKID *lib.PKID, lockedAtEpochNumber uint64) *types.AccountIdentifier {
	return &types.AccountIdentifier{
		Address: lib.Base58CheckEncode(stakerPKID.ToBytes(), false, node.Params),
		SubAccount: &types.SubAccountIdentifier{
			Address: fmt.Sprintf("%v-%v-%v", LockedStakeEntry, lib.Base58CheckEncode(validatorPKID.ToBytes(), false, node.Params), lockedAtEpochNumber),
		},
	}
}

func (node *Node) getValidatorEntrySubAccountIdentifierForValidator(validatorPKID *lib.PKID) *types.AccountIdentifier {
	return &types.AccountIdentifier{
		Address: lib.Base58CheckEncode(validatorPKID.ToBytes(), false, node.Params),
		SubAccount: &types.SubAccountIdentifier{
			Address: ValidatorEntry,
		},
	}
}

func (node *Node) GetValidatorPKIDFromSubAccountIdentifier(subAccount *types.SubAccountIdentifier) (*lib.PKID, error) {
	if subAccount == nil || !strings.HasPrefix(subAccount.Address, LockedStakeEntry) {
		return nil, fmt.Errorf("invalid subaccount for validator PKID extraction")
	}
	segments := strings.Split(subAccount.Address, "-")
	if len(segments) != 2 {
		return nil, fmt.Errorf("invalid subaccount for validator PKID extraction")
	}
	validatorPKIDBytes, _, err := lib.Base58CheckDecode(segments[1])
	if err != nil {
		return nil, fmt.Errorf("invalid subaccount for validator PKID extraction")
	}
	return lib.NewPKID(validatorPKIDBytes), nil
}

func (node *Node) getStakeOps(txn *lib.MsgDeSoTxn, utxoOps []*lib.UtxoOperation, numOps int) []*types.Operation {
	if txn.TxnMeta.GetTxnType() != lib.TxnTypeStake {
		return nil
	}

	var operations []*types.Operation
	for _, utxoOp := range utxoOps {
		// We only need an OUTPUT operation here as the INPUT operation
		// is covered by the getBalanceModelSpends function.
		if utxoOp.Type == lib.OperationTypeStake {
			prevValidatorEntry := utxoOp.PrevValidatorEntry
			if prevValidatorEntry == nil {
				// TODO: This is a bad error...
				glog.Error("getStakeOps: prevValidatorEntry was nil")
				continue
			}
			realTxMeta := txn.TxnMeta.(*lib.StakeMetadata)
			if realTxMeta == nil {
				glog.Error("getStakeOps: realTxMeta was nil")
				continue
			}
			stakeAmountNanos := realTxMeta.StakeAmountNanos
			if !stakeAmountNanos.IsUint64() {
				glog.Error("getStakeOps: stakeAmountNanos was not a uint64")
				continue
			}
			stakeAmountNanosUint64 := stakeAmountNanos.Uint64()

			operations = append(operations, &types.Operation{
				OperationIdentifier: &types.OperationIdentifier{
					Index: int64(numOps),
				},
				Account: node.getValidatorEntrySubAccountIdentifierForValidator(prevValidatorEntry.ValidatorPKID),
				Amount: &types.Amount{
					Value:    strconv.FormatUint(stakeAmountNanosUint64, 10),
					Currency: &Currency,
				},
				Status: &SuccessStatus,
				Type:   OutputOpType,
			})
			numOps++
		}
	}
	return operations
}

func (node *Node) getUnstakeOps(txn *lib.MsgDeSoTxn, utxoOps []*lib.UtxoOperation, numOps int) []*types.Operation {
	if txn.TxnMeta.GetTxnType() != lib.TxnTypeUnstake {
		return nil
	}

	var operations []*types.Operation
	for _, utxoOp := range utxoOps {
		if utxoOp.Type == lib.OperationTypeUnstake {
			prevValidatorEntry := utxoOp.PrevValidatorEntry
			if prevValidatorEntry == nil {
				glog.Error("getUnstakeOps: prevValidatorEntry was nil")
				continue
			}
			prevStakeEntries := utxoOp.PrevStakeEntries
			if len(prevStakeEntries) != 1 {
				glog.Error("getUnstakeOps: prevStakeEntries was not of length 1")
				continue
			}
			prevStakeEntry := prevStakeEntries[0]
			realTxMeta := txn.TxnMeta.(*lib.UnstakeMetadata)
			if realTxMeta == nil {
				glog.Error("getUnstakeOps: realTxMeta was nil")
				continue
			}
			unstakeAmountNanos := realTxMeta.UnstakeAmountNanos
			if !unstakeAmountNanos.IsUint64() {
				glog.Error("getUnstakeOps: unstakeAmountNanos was not a uint64")
				continue
			}
			unstakeAmountNanosUint64 := unstakeAmountNanos.Uint64()
			// First use an "input" from the ValidatorEntry
			operations = append(operations, &types.Operation{
				OperationIdentifier: &types.OperationIdentifier{
					Index: int64(numOps),
				},
				Account: node.getValidatorEntrySubAccountIdentifierForValidator(prevValidatorEntry.ValidatorPKID),
				Amount: &types.Amount{
					Value:    strconv.FormatUint(unstakeAmountNanosUint64, 10),
					Currency: &Currency,
				},
				Status: &SuccessStatus,
				Type:   InputOpType,
			})
			numOps++
			operations = append(operations, &types.Operation{
				OperationIdentifier: &types.OperationIdentifier{
					Index: int64(numOps),
				},
				Account: node.getLockedStakeEntryIdentifierForValidator(
					prevStakeEntry.StakerPKID,
					prevValidatorEntry.ValidatorPKID,
					utxoOp.LockedAtEpochNumber,
				),
				Amount: &types.Amount{
					Value:    strconv.FormatUint(unstakeAmountNanosUint64, 10),
					Currency: &Currency,
				},
				Status: &SuccessStatus,
				Type:   OutputOpType,
			})
			numOps++
		}
	}
	return operations
}

func (node *Node) getUnlockStakeOps(txn *lib.MsgDeSoTxn, utxoOps []*lib.UtxoOperation, numOps int) []*types.Operation {
	if txn.TxnMeta.GetTxnType() != lib.TxnTypeUnlockStake {
		return nil
	}

	var operations []*types.Operation
	for _, utxoOp := range utxoOps {
		if utxoOp.Type == lib.OperationTypeUnlockStake {
			for _, prevLockedStakeEntry := range utxoOp.PrevLockedStakeEntries {
				if !prevLockedStakeEntry.LockedAmountNanos.IsUint64() {
					glog.Error("getUnlockStakeOps: lockedAmountNanos was not a uint64")
					continue
				}
				lockedAmountNanosUint64 := prevLockedStakeEntry.LockedAmountNanos.Uint64()
				// Each locked stake entry is an "input" to the UnlockStake txn
				// We spend ALL the lockedAmountNanos from each locked stake entry in
				// an unlocked transaction
				operations = append(operations, &types.Operation{
					OperationIdentifier: &types.OperationIdentifier{
						Index: int64(numOps),
					},
					Account: node.getLockedStakeEntryIdentifierForValidator(
						prevLockedStakeEntry.StakerPKID,
						prevLockedStakeEntry.ValidatorPKID,
						prevLockedStakeEntry.LockedAtEpochNumber,
					),
					Amount: &types.Amount{
						Value:    strconv.FormatUint(lockedAmountNanosUint64, 10),
						Currency: &Currency,
					},
					Status: &SuccessStatus,
					Type:   InputOpType,
				})
				numOps++
			}
		}
	}
	return operations
}

func (node *Node) getStakingRewardDistributionOps(utxoOps []*lib.UtxoOperation, numOps int) []*types.Operation {
	var operations []*types.Operation

	for _, utxoOp := range utxoOps {
		if utxoOp.Type == lib.OperationTypeStakeDistributionRestake {
			prevValidatorEntry := utxoOp.PrevValidatorEntry
			operations = append(operations, &types.Operation{
				OperationIdentifier: &types.OperationIdentifier{
					Index: int64(numOps),
				},
				Account: node.getValidatorEntrySubAccountIdentifierForValidator(prevValidatorEntry.ValidatorPKID),
				Amount: &types.Amount{
					Value:    strconv.FormatUint(utxoOp.StakeAmountNanosDiff, 10),
					Currency: &Currency,
				},

				Status: &SuccessStatus,
				Type:   OutputOpType,
			})
			numOps++
		}
	}
	return operations
}

func (node *Node) getImplicitOutputs(txn *lib.MsgDeSoTxn, utxoOpsForTxn []*lib.UtxoOperation, numOps int) []*types.Operation {
	var operations []*types.Operation
	numOutputs := uint32(len(txn.TxOutputs))

	for _, utxoOp := range utxoOpsForTxn {
		if utxoOp.Type == lib.OperationTypeAddUtxo &&
			utxoOp.Entry != nil && utxoOp.Entry.UtxoKey != nil &&
			utxoOp.Entry.UtxoKey.Index >= numOutputs {

			operations = append(operations, &types.Operation{
				OperationIdentifier: &types.OperationIdentifier{
					Index: int64(numOps),
				},

				Account: &types.AccountIdentifier{
					Address: lib.Base58CheckEncode(utxoOp.Entry.PublicKey, false, node.Params),
				},

				Amount: &types.Amount{
					Value:    strconv.FormatUint(utxoOp.Entry.AmountNanos, 10),
					Currency: &Currency,
				},

				Status: &SuccessStatus,
				Type:   OutputOpType,
			})

			numOps++
		}
		if utxoOp.Type == lib.OperationTypeAddBalance ||
			utxoOp.Type == lib.OperationTypeStakeDistributionPayToBalance {
			operations = append(operations, &types.Operation{
				OperationIdentifier: &types.OperationIdentifier{
					Index: int64(numOps),
				},
				Account: &types.AccountIdentifier{
					Address: lib.Base58CheckEncode(utxoOp.BalancePublicKey, false, node.Params),
				},
				Amount: &types.Amount{
					Value:    strconv.FormatUint(utxoOp.BalanceAmountNanos, 10),
					Currency: &Currency,
				},

				Status: &SuccessStatus,
				Type:   OutputOpType,
			})
			numOps++
		}
	}

	return operations
}

func (node *Node) getInputAmount(input *lib.DeSoInput, utxoOpsForTxn []*lib.UtxoOperation) *types.Amount {
	amount := types.Amount{}

	// Fix for returning input amounts for genesis block transactions
	// This is needed because we don't generate UtxoOperations for the genesis
	zeroBlockHash := lib.BlockHash{}
	if input.TxID == zeroBlockHash {
		output := node.Params.GenesisBlock.Txns[0].TxOutputs[input.Index]
		amount.Value = strconv.FormatInt(int64(output.AmountNanos)*-1, 10)
		amount.Currency = &Currency
		return &amount
	}

	// Iterate over the UtxoOperations created by the txn to find the one corresponding to the index specified.
	for _, utxoOp := range utxoOpsForTxn {
		if utxoOp.Type == lib.OperationTypeSpendUtxo &&
			utxoOp.Entry != nil && utxoOp.Entry.UtxoKey != nil &&
			utxoOp.Entry.UtxoKey.TxID == input.TxID &&
			utxoOp.Entry.UtxoKey.Index == input.Index {

			amount.Value = strconv.FormatInt(int64(utxoOp.Entry.AmountNanos)*-1, 10)
			amount.Currency = &Currency
			return &amount
		}
	}

	// If we get here then we failed to find the input we were looking for.
	fmt.Printf("Error: input missing for txn %v index %v\n", lib.PkToStringBoth(input.TxID[:]), input.Index)
	return nil
}
