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

	for _, txn := range block.Txns {
		metadataJSON, _ := json.Marshal(txn.TxnMeta)

		var metadata map[string]interface{}
		_ = json.Unmarshal(metadataJSON, &metadata)

		txnHash := txn.Hash().String()
		transaction := &types.Transaction{
			TransactionIdentifier: &types.TransactionIdentifier{Hash: txnHash},
			Metadata:              metadata,
		}

		transaction.Operations = []*types.Operation{}

		for _, input := range txn.TxInputs {
			networkIndex := int64(input.Index)

			// Fetch the input amount from TXIndex
			amount := node.getInputAmount(input)

			op := &types.Operation{
				OperationIdentifier: &types.OperationIdentifier{
					Index:        int64(len(transaction.Operations)),
					NetworkIndex: &networkIndex,
				},

				Account: &types.AccountIdentifier{
					Address: lib.Base58CheckEncode(txn.PublicKey, false, node.Params),
				},

				Amount: amount,

				CoinChange: &types.CoinChange{
					CoinIdentifier: &types.CoinIdentifier{
						Identifier: fmt.Sprintf("%v:%d", input.TxID.String(), input.Index),
					},
					CoinAction: types.CoinSpent,
				},

				Status: &SuccessStatus,
				Type:   InputOpType,
			}

			transaction.Operations = append(transaction.Operations, op)
		}

		for index, output := range txn.TxOutputs {
			networkIndex := int64(index)

			op := &types.Operation{
				OperationIdentifier: &types.OperationIdentifier{
					Index:        int64(len(transaction.Operations)),
					NetworkIndex: &networkIndex,
				},

				Account: &types.AccountIdentifier{
					Address: lib.Base58CheckEncode(output.PublicKey, false, node.Params),
				},

				Amount: &types.Amount{
					Value:    strconv.FormatUint(output.AmountNanos, 10),
					Currency: &Currency,
				},

				CoinChange: &types.CoinChange{
					CoinIdentifier: &types.CoinIdentifier{
						Identifier: fmt.Sprintf("%v:%d", txn.Hash().String(), networkIndex),
					},
					CoinAction: types.CoinCreated,
				},

				Status: &SuccessStatus,
				Type:   OutputOpType,
			}

			transaction.Operations = append(transaction.Operations, op)
		}

		if node.TXIndex != nil {
			txnMeta := lib.DbGetTxindexTransactionRefByTxID(node.TXIndex.TXIndexChain.DB(), txn.Hash())
			if txnMeta != nil {
				// Add implicit outputs from TXIndex
				implicitOutputs := node.getImplicitOutputs(txn, txnMeta, len(transaction.Operations))
				transaction.Operations = append(transaction.Operations, implicitOutputs...)

				// Add inputs/outputs for creator coins
				creatorCoinOps := node.getCreatorCoinOps(txn, txnMeta, len(transaction.Operations))
				transaction.Operations = append(transaction.Operations, creatorCoinOps...)

				// Add inputs/outputs for swap identity
				swapIdentityOps := node.getSwapIdentityOps(txnMeta, len(transaction.Operations))
				transaction.Operations = append(transaction.Operations, swapIdentityOps...)

				// Add inputs for accept nft bid
				acceptNftOps := node.getAcceptNFTOps(txn, txnMeta, len(transaction.Operations))
				transaction.Operations = append(transaction.Operations, acceptNftOps...)
			}
		}

		transactions = append(transactions, transaction)
	}

	return &types.Block{
		BlockIdentifier:       blockIdentifier,
		ParentBlockIdentifier: parentBlockIdentifier,
		Timestamp:             int64(block.Header.TstampSecs) * 1000,
		Transactions:          transactions,
	}
}

func (node *Node) getCreatorCoinOps(txn *lib.MsgDeSoTxn, meta *lib.TransactionMetadata, numOps int) []*types.Operation {
	creatorCoinMeta := meta.CreatorCoinTxindexMetadata
	if creatorCoinMeta == nil {
		return nil
	}

	var operations []*types.Operation

	// Extract creator public key
	var creatorPublicKey string
	for _, key := range meta.AffectedPublicKeys {
		if key.Metadata == "CreatorPublicKey" {
			creatorPublicKey = key.PublicKeyBase58Check
			break
		}
	}

	account := &types.AccountIdentifier{
		Address: creatorPublicKey,
		SubAccount: &types.SubAccountIdentifier{
			Address: CreatorCoin,
		},
	}

	// This amount is negative for sells and positive for buys
	amount := &types.Amount{
		Value:    strconv.FormatInt(creatorCoinMeta.DESOLockedNanosDiff, 10),
		Currency: &Currency,
	}

	realTxnMeta := txn.TxnMeta.(*lib.CreatorCoinMetadataa)
	if realTxnMeta.OperationType == lib.CreatorCoinOperationTypeSell {
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
	} else if realTxnMeta.OperationType == lib.CreatorCoinOperationTypeBuy {
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

func (node *Node) getSwapIdentityOps(meta *lib.TransactionMetadata, numOps int) []*types.Operation {
	swapMeta := meta.SwapIdentityTxindexMetadata
	if swapMeta == nil {
		return nil
	}

	var operations []*types.Operation

	fromAccount := &types.AccountIdentifier{
		Address: swapMeta.FromPublicKeyBase58Check,
		SubAccount: &types.SubAccountIdentifier{
			Address: CreatorCoin,
		},
	}

	toAccount := &types.AccountIdentifier{
		Address: swapMeta.ToPublicKeyBase58Check,
		SubAccount: &types.SubAccountIdentifier{
			Address: CreatorCoin,
		},
	}

	// TXIndex creates metadata after a transaction has been connected. ToDeSoLockedNanos and FromDeSoLockedNanos
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
			Value:    strconv.FormatInt(int64(swapMeta.ToDeSoLockedNanos)*-1, 10),
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
			Value:    strconv.FormatInt(int64(swapMeta.FromDeSoLockedNanos)*-1, 10),
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
			Value:    strconv.FormatUint(swapMeta.FromDeSoLockedNanos, 10),
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
			Value:    strconv.FormatUint(swapMeta.ToDeSoLockedNanos, 10),
			Currency: &Currency,
		},
	})

	return operations
}

func (node *Node) getAcceptNFTOps(txn *lib.MsgDeSoTxn, meta *lib.TransactionMetadata, numOps int) []*types.Operation {
	nftMeta := meta.AcceptNFTBidTxindexMetadata
	if nftMeta == nil {
		return nil
	}

	var operations []*types.Operation

	royaltyAccount := &types.AccountIdentifier{
		Address: nftMeta.CreatorPublicKeyBase58Check,
		SubAccount: &types.SubAccountIdentifier{
			Address: CreatorCoin,
		},
	}

	// Extract bidder key
	var bidderPublicKey string
	for _, pubKey := range meta.AffectedPublicKeys {
		if pubKey.Metadata == "NFTBidderPublicKeyBase58Check" {
			bidderPublicKey = pubKey.PublicKeyBase58Check
			break
		}
	}

	// Add an operation for each bidder input we consume
	txnMeta := txn.TxnMeta.(*lib.AcceptNFTBidMetadata)
	for _, input := range txnMeta.BidderInputs {
		inputAmount := node.getInputAmount(input)
		networkIndex := int64(input.Index)

		operations = append(operations, &types.Operation{
			OperationIdentifier: &types.OperationIdentifier{
				Index:        int64(numOps),
				NetworkIndex: &networkIndex,
			},
			Type:   InputOpType,
			Status: &SuccessStatus,
			Account: &types.AccountIdentifier{
				Address: bidderPublicKey,
			},
			Amount: inputAmount,
			CoinChange: &types.CoinChange{
				CoinIdentifier: &types.CoinIdentifier{
					Identifier: fmt.Sprintf("%v:%d", input.TxID.String(), networkIndex),
				},
				CoinAction: types.CoinSpent,
			},
		})

		numOps += 1
	}

	// Add an output representing the creator coin royalty
	operations = append(operations, &types.Operation{
		OperationIdentifier: &types.OperationIdentifier{
			Index: int64(numOps),
		},
		Type:    OutputOpType,
		Status:  &SuccessStatus,
		Account: royaltyAccount,
		Amount: &types.Amount{
			Value:    strconv.FormatUint(nftMeta.CreatorCoinRoyaltyNanos, 10),
			Currency: &Currency,
		},
	})

	return operations
}

func (node *Node) getImplicitOutputs(txn *lib.MsgDeSoTxn, meta *lib.TransactionMetadata, numOps int) []*types.Operation {
	var operations []*types.Operation
	numOutputs := uint32(len(txn.TxOutputs))

	for _, utxoOp := range meta.BasicTransferTxindexMetadata.UtxoOps {
		if utxoOp.Type == lib.OperationTypeAddUtxo &&
			utxoOp.Entry != nil && utxoOp.Entry.UtxoKey != nil &&
			utxoOp.Entry.UtxoKey.Index >= numOutputs {

			networkIndex := int64(utxoOp.Entry.UtxoKey.Index)
			operations = append(operations, &types.Operation{
				OperationIdentifier: &types.OperationIdentifier{
					Index:        int64(numOps),
					NetworkIndex: &networkIndex,
				},

				Account: &types.AccountIdentifier{
					Address: lib.Base58CheckEncode(utxoOp.Entry.PublicKey, false, node.Params),
				},

				Amount: &types.Amount{
					Value:    strconv.FormatUint(utxoOp.Entry.AmountNanos, 10),
					Currency: &Currency,
				},

				CoinChange: &types.CoinChange{
					CoinIdentifier: &types.CoinIdentifier{
						Identifier: fmt.Sprintf("%v:%d", txn.Hash().String(), networkIndex),
					},
					CoinAction: types.CoinCreated,
				},

				Status: &SuccessStatus,
				Type:   OutputOpType,
			})

			numOps += 1
		}
	}

	return operations
}

func (node *Node) getInputAmount(input *lib.DeSoInput) *types.Amount {
	amount := types.Amount{}

	if node.TXIndex == nil {
		return nil
	}

	// Temporary fix for returning input amounts for genesis block transactions
	// This will be removed once most node operators have regenerated their txindex
	zeroBlockHash := lib.BlockHash{}
	if input.TxID == zeroBlockHash {
		output := node.Params.GenesisBlock.Txns[0].TxOutputs[input.Index]
		amount.Value = strconv.FormatInt(int64(output.AmountNanos)*-1, 10)
		amount.Currency = &Currency
		return &amount
	}

	txnMeta := lib.DbGetTxindexTransactionRefByTxID(node.TXIndex.TXIndexChain.DB(), &input.TxID)
	if txnMeta == nil {
		return nil
	}

	// Iterate over the UtxoOperations created by the txn to find the one corresponding to the index specified.
	for _, utxoOp := range txnMeta.BasicTransferTxindexMetadata.UtxoOps {
		if utxoOp.Type == lib.OperationTypeAddUtxo &&
			utxoOp.Entry != nil && utxoOp.Entry.UtxoKey != nil &&
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
