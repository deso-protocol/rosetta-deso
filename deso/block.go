package deso

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"

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
	block := blockchain.GetBlock(blockHash)
	if block == nil {
		return nil
	}

	return node.convertBlock(block)
}

func (node *Node) GetBlockAtHeight(height int64) *types.Block {
	blockchain := node.GetBlockchain()
	block := blockchain.GetBlockAtHeight(uint32(height))
	if block == nil {
		return nil
	}

	return node.convertBlock(block)
}

func (node *Node) CurrentBlock() *types.Block {
	blockchain := node.GetBlockchain()

	return node.GetBlockAtHeight(int64(blockchain.BlockTip().Height))
}

func (node *Node) convertBlock(block *lib.MsgDeSoBlock) *types.Block {
	blockchain := node.GetBlockchain()

	blockHash, _ := block.Hash()

	blockIdentifier := &types.BlockIdentifier{
		Index: int64(block.Header.Height),
		Hash:  blockHash.String(),
	}

	var parentBlockIdentifier *types.BlockIdentifier
	if block.Header.Height == 0 {
		parentBlockIdentifier = blockIdentifier
	} else {
		parentBlock := blockchain.GetBlock(block.Header.PrevBlockHash)
		parentBlockHash, _ := parentBlock.Hash()
		parentBlockIdentifier = &types.BlockIdentifier{
			Index: int64(parentBlock.Header.Height),
			Hash:  parentBlockHash.String(),
		}
	}

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

		// Add all the special ops for specific txn types.
		if len(utxoOpsForBlock) > 0 {
			utxoOpsForTxn := utxoOpsForBlock[txnIndexInBlock]

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
			transaction.Operations = append(transaction.Operations, buyNowNftBidOps...)

			// Add inputs for update profile
			updateProfileOps := node.getUpdateProfileOps(txn, utxoOpsForTxn, len(ops))
			ops = append(ops, updateProfileOps...)
		}

		transaction.Operations = squashOperations(ops)

		transactions = append(transactions, transaction)
	}

	return &types.Block{
		BlockIdentifier:       blockIdentifier,
		ParentBlockIdentifier: parentBlockIdentifier,
		Timestamp:             int64(block.Header.TstampSecs) * 1000,
		Transactions:          transactions,
	}
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

	return operations
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
	if acceptNFTOp.PrevCoinEntry.CoinsInCirculationNanos >= node.Params.CreatorCoinAutoSellThresholdNanos {
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

	// Add outputs for each additional creator coin royalty
	for _, publicKeyRoyaltyPair := range acceptNFTOp.AcceptNFTBidAdditionalCoinRoyalties {
		coinRoyaltyAccount := &types.AccountIdentifier{
			Address: lib.PkToString(publicKeyRoyaltyPair.PublicKey, node.Params),
			SubAccount: &types.SubAccountIdentifier{
				Address: CreatorCoin,
			},
		}
		operations = append(operations, &types.Operation{
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

	// We only care about NFT bids that generate creator royalties. This only occurs for NFT bids on Buy Now NFTs.
	// Only NFT bids on Buy Now NFTs will have NFTBidcreatorRoyaltyNanos > 0. Even in the case that an NFT is a buy now,
	// it is possible that there are no royalties to the creator coin generated. In this situation, it is okay to exit
	// early since there are not output to add.
	if nftBidOp.NFTBidCreatorRoyaltyNanos == 0 {
		return nil
	}

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
	if nftBidOp.PrevCoinEntry.CoinsInCirculationNanos >= node.Params.CreatorCoinAutoSellThresholdNanos {
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

	// Add outputs for each additional creator coin royalty
	for _, publicKeyRoyaltyPair := range nftBidOp.NFTBidAdditionalCoinRoyalties {
		coinRoyaltyAccount := &types.AccountIdentifier{
			Address: lib.PkToString(publicKeyRoyaltyPair.PublicKey, node.Params),
			SubAccount: &types.SubAccountIdentifier{
				Address: CreatorCoin,
			},
		}
		operations = append(operations, &types.Operation{
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

			numOps += 1
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
