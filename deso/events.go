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

	// Iterate over all PKID mappings to get all public keys that may
	// have been affected by a SwapIdentity. If we don't do this we'll
	// miss swaps that involve a public key with a missing profile.
	lockedBalances := make(map[lib.PublicKey]uint64, len(event.UtxoView.PublicKeyToPKIDEntry))
	for pubKey, _ := range event.UtxoView.PublicKeyToPKIDEntry {
		balanceToSet := uint64(0)
		profileFound := event.UtxoView.GetProfileEntryForPublicKey(pubKey[:])
		if profileFound != nil && !profileFound.IsDeleted() {
			balanceToSet = profileFound.CreatorCoinEntry.DeSoLockedNanos
		}
		lockedBalances[*lib.NewPublicKey(pubKey[:])] = balanceToSet
	}

	// Iterate over all profiles that may have been modified.
	profileEntries := event.UtxoView.ProfilePKIDToProfileEntry
	for _, profile := range profileEntries {
		balanceToPut := uint64(0)
		if !profile.IsDeleted() {
			balanceToPut = profile.CreatorCoinEntry.DeSoLockedNanos
		}
		lockedBalances[*lib.NewPublicKey(profile.PublicKey)] = balanceToPut
	}

	err = node.Index.PutBalanceSnapshot(event.Block, true, lockedBalances)
	if err != nil {
		glog.Errorf("PutLockedBalanceSnapshot: %v", err)
	}
}

func (node *Node) handleTransactionConnected(event *lib.TransactionEvent) {
	// TODO
}
