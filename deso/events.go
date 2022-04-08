package deso

import (
	"fmt"
	"github.com/deso-protocol/core/lib"
	"github.com/golang/glog"
)

func (node *Node) handleSnapshotCompleted() {
	// We do some special logic if we have a snapshot.
	snapshot := node.Server.GetBlockchain().Snapshot()
	if snapshot != nil &&
		snapshot.CurrentEpochSnapshotMetadata.FirstSnapshotBlockHeight != 0 {

		// If we're at exactly the snapshot height then we've got some work to do.
		// Output every single balance in the db to a special index. This will
		// bootstrap Rosetta, and allow us to pass check:data. This is because we
		// treat hypersync Rosetta as follows:
		// - Before the first snapshot, all balances are zero
		// - At the first snapshot, there is a synthetic "block" that gifts all the
		//   public keys the proper amount of DESO to bootstrap.
		// - After the first snapshot, blocks increment and decrement balances as they
		//   used to before we introduced hypersync.
		//
		// The above basically makes it so that the "genesis" for Rosetta is
		// the snapshot height rather than the true genesis. This allows us to pass
		// check:data without introducing complications for Coinbase.
		{
			// Iterate through every single public key and put a balance snapshot down
			// for it for this block. We don't need to worry about ancestral records here
			// because we haven't generated any yet.
			publicKeys, balanceByteSlices := lib.EnumerateKeysForPrefix(
				node.GetBlockchain().DB(),
				lib.Prefixes.PrefixPublicKeyToDeSoBalanceNanos)
			balances := make(map[lib.PublicKey]uint64, len(publicKeys))
			for ii, balBytes := range balanceByteSlices {
				bal := lib.DecodeUint64(balBytes)
				pubKey := lib.NewPublicKey(publicKeys[ii][1:])
				balances[*pubKey] = bal
			}

			// Save all the balances for this height. This is like a fake genesis block.
			err := node.Index.PutHypersyncBalanceSnapshot(false, balances)
			if err != nil {
				glog.Errorf("PutBalanceSnapshot: %v", err)
			}
			// We have to also put the balances in the other index. Not doing this would cause
			// balances to return zero when we're PAST the first snapshot block height.
			err = node.Index.PutBalanceSnapshot(
				snapshot.CurrentEpochSnapshotMetadata.FirstSnapshotBlockHeight,
				false, balances)
			if err != nil {
				glog.Errorf("PutBalanceSnapshot: %v", err)
			}
		}

		// Create a new scope to avoid name collision errors
		{
			// Iterate over all the profiles in the db, look up the corresponding public
			// keys, and then set all the creator coin balances in the db.
			//
			// No need to pass the snapshot because we know we don't have any ancestral
			// records yet.
			//
			// TODO: Do we need to do anything special for SwapIdentity? See below for
			// some tricky logic there.
			lockedDeSoNanos, pkids, _, err := lib.DBGetAllProfilesByCoinValue(
				node.GetBlockchain().DB(), nil, false)
			if err != nil {
				glog.Errorf("DBGetAllProfilesByCoinValue: %v", err)
			}
			lockedBalances := make(map[lib.PublicKey]uint64, len(pkids))
			for ii := range lockedDeSoNanos {
				lockedNanos := lockedDeSoNanos[ii]
				pkBytes := lib.DBGetPublicKeyForPKID(node.GetBlockchain().DB(), nil, pkids[ii])
				if pkBytes == nil {
					glog.Errorf("DBGetPublicKeyForPKID: Nil pkBytes for pkid %v",
						lib.PkToStringMainnet(pkids[ii][:]))
				}
				pubKey := lib.NewPublicKey(pkBytes)
				lockedBalances[*pubKey] = lockedNanos
			}

			err = node.Index.PutHypersyncBalanceSnapshot(true, lockedBalances)
			if err != nil {
				glog.Errorf("PutLockedBalanceSnapshot: %v", err)
			}
			// We have to also put the balances in the other index. Not doing this would cause
			// balances to return zero when we're PAST the first snapshot block height.
			err = node.Index.PutBalanceSnapshot(
				snapshot.CurrentEpochSnapshotMetadata.FirstSnapshotBlockHeight,
				true, lockedBalances)
			if err != nil {
				glog.Errorf("PutLockedBalanceSnapshot: %v", err)
			}
		}

		return
	}
}

func (node *Node) handleBlockConnected(event *lib.BlockEvent) {
	// We do some special logic if we have a snapshot.
	if node.Server != nil {
		snapshot := node.Server.GetBlockchain().Snapshot()
		if snapshot != nil &&
			snapshot.CurrentEpochSnapshotMetadata.FirstSnapshotBlockHeight != 0 {

			firstSnapshotHeight := snapshot.CurrentEpochSnapshotMetadata.FirstSnapshotBlockHeight

			// If we're before the first snapshot height, then do nothing.
			height := event.Block.Header.Height
			if height <= firstSnapshotHeight {
				return
			}
		}
	}
	// If we get here, then we're connecting a block after the first snapshot OR we
	// don't have a snapshot. We output extra metadata for this block to ensure
	// Rosetta connects it appropriately.

	// Save the UTXOOps. These are used to compute all of the meta information
	// that Rosetta needs.
	err := node.Index.PutUtxoOps(event.Block, event.UtxoOps)
	if err != nil {
		glog.Errorf("PutSpentUtxos: %v", err)
	}

	// Save a balance snapshot
	balances := event.UtxoView.PublicKeyToDeSoBalanceNanos
	err = node.Index.PutBalanceSnapshot(event.Block.Header.Height, false, balances)
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

	err = node.Index.PutBalanceSnapshot(event.Block.Header.Height, true, lockedBalances)
	if err != nil {
		glog.Errorf("PutLockedBalanceSnapshot: %v", err)
	}
}

func (node *Node) handleTransactionConnected(event *lib.TransactionEvent) {
	// TODO
}
