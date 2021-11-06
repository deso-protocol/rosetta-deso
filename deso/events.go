package deso

import (
	"github.com/deso-protocol/core/lib"
	"github.com/golang/glog"
)

func (node *Node) handleBlockConnected(event *lib.BlockEvent) {
	glog.Info("handleBlockConnected: %s", event.Block.String())

	var spentUtxos map[lib.UtxoKey]uint64

	// Find all spent UTXOs for this block
	for _, utxoOps := range event.UtxoOps {
		for _, utxoOp := range utxoOps {
			if utxoOp.Type == lib.OperationTypeSpendUtxo {
				spentUtxos[*utxoOp.Entry.UtxoKey] = utxoOp.Entry.AmountNanos
			}
		}
	}

	// Save the spent utxos
	err := node.Index.PutSpentUtxos(event.Block, spentUtxos)
	if err != nil {
		glog.Errorf("PutSpentUtxos: %v", err)
	}

	// Save a balance snapshot
	balances := event.UtxoView.PublicKeyToDeSoBalanceNanos
	err = node.Index.PutBalanceSnapshot(event.Block, balances)
	if err != nil {
		glog.Errorf("PutBalanceSnapshot: %v", err)
	}

	// Save a locked creator coins snapshot
	var lockedBalances map[lib.PublicKey]uint64
	for _, profile := range event.UtxoView.ProfilePKIDToProfileEntry {
		lockedBalances[*lib.NewPublicKey(profile.PublicKey)] = profile.DeSoLockedNanos
	}
	err = node.Index.PutLockedBalanceSnapshot(event.Block, lockedBalances)
	if err != nil {
		glog.Errorf("PutLockedBalanceSnapshot: %v", err)
	}
}

func (node *Node) handleTransactionConnected(event *lib.TransactionEvent) {
	// TODO
}
