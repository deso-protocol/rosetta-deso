package bitclout

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/bitclout/core/lib"
	"github.com/coinbase/rosetta-sdk-go/types"
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

func (node *Node) convertBlock(block *lib.MsgBitCloutBlock) *types.Block {
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

				Amount: &amount,

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

		transactions = append(transactions, transaction)
	}

	return &types.Block{
		BlockIdentifier:       blockIdentifier,
		ParentBlockIdentifier: parentBlockIdentifier,
		Timestamp:             int64(block.Header.TstampSecs) * 1000,
		Transactions:          transactions,
	}
}

func (node *Node) getInputAmount(input *lib.BitCloutInput) types.Amount {
	amount := types.Amount{}

	if node.TXIndex == nil {
		return amount
	}

	txnMeta := lib.DbGetTxindexTransactionRefByTxID(node.TXIndex.TXIndexChain.DB(), &input.TxID)
	if txnMeta == nil {
		return amount
	}

	numOutputs := uint32(len(txnMeta.TxnOutputs))
	if input.Index > numOutputs {
		return amount
	}

	if input.Index < numOutputs {
		output := txnMeta.TxnOutputs[input.Index]
		if output != nil {
			amount.Value = strconv.FormatInt(int64(output.AmountNanos) * -1, 10)
			amount.Currency = &Currency
		}
	} else if input.Index == numOutputs {
		// In this case, we are dealing with an "implicit" output.
		// Only a few transaction types can generate these.
		if txnMeta.TxnType != lib.TxnTypeBitcoinExchange.String() &&
			txnMeta.TxnType != lib.TxnTypeCreatorCoin.String() {
			return amount
		}

		// Sum up all the outputs in the txn.
		explicitTxnOutput := uint64(0)
		for _, explicitOut := range txnMeta.TxnOutputs {
			explicitTxnOutput += explicitOut.AmountNanos
		}

		// The remainder after subtracting out the fee and the actual output
		// of the txn is what this implicit output gets.
		amountNanos := txnMeta.BasicTransferTxindexMetadata.TotalOutputNanos - explicitTxnOutput
		amount.Value = strconv.FormatInt(int64(amountNanos) * -1, 10)
		amount.Currency = &Currency
	}

	return amount
}