package deso

import (
	"github.com/deso-protocol/core/lib"
	"github.com/golang/glog"
)

func (node *Node) handleBlockConnected(event *lib.BlockEvent) {
	// Save the UTXOOps. These are used to compute all of the meta information
	// that Rosetta needs.
	err := node.Index.PutUtxoOps(event.Block, event.UtxoOps)
	if err != nil {
		glog.Errorf("PutSpentUtxos: %v", err)
	}

	// Save a balance snapshot
	balances := event.UtxoView.PublicKeyToDeSoBalanceNanos
	err = node.Index.PutBalanceSnapshot(event.Block, false, balances)
	if err != nil {
		glog.Errorf("PutBalanceSnapshot: %v", err)
	}

	// Save a locked creator coins snapshot
	profileEntries := event.UtxoView.ProfilePKIDToProfileEntry
	lockedBalances := make(map[lib.PublicKey]uint64, len(profileEntries))
	for _, profile := range profileEntries {
		lockedBalances[*lib.NewPublicKey(profile.PublicKey)] = profile.DeSoLockedNanos
	}

	err = node.Index.PutBalanceSnapshot(event.Block, true, lockedBalances)
	if err != nil {
		glog.Errorf("PutLockedBalanceSnapshot: %v", err)
	}
}

func (node *Node) handleTransactionConnected(event *lib.TransactionEvent) {
	// TODO
}
