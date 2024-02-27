package deso

import (
	"bytes"
	"fmt"
	"github.com/btcsuite/btcd/btcec"
	"github.com/deso-protocol/core/lib"
	"github.com/dgraph-io/badger/v3"
	"github.com/golang/glog"
	"github.com/pkg/errors"
	"math"
)

type LockedStakeBalanceMapKey struct {
	StakerPKID          lib.PKID
	ValidatorPKID       lib.PKID
	LockedAtEpochNumber uint64
}

func (node *Node) handleSnapshotCompleted() {
	node.Index.dbMutex.Lock()
	defer node.Index.dbMutex.Unlock()

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
		snapshotBlockHeight := snapshot.CurrentEpochSnapshotMetadata.FirstSnapshotBlockHeight
		{
			// Iterate through every single public key and put a balance snapshot down
			// for it for this block. We don't need to worry about ancestral records here
			// because we haven't generated any yet.

			err := node.Index.db.Update(func(indexTxn *badger.Txn) error {
				return node.GetBlockchain().DB().View(func(chainTxn *badger.Txn) error {
					opts := badger.DefaultIteratorOptions
					nodeIterator := chainTxn.NewIterator(opts)
					defer nodeIterator.Close()
					prefix := lib.Prefixes.PrefixPublicKeyToDeSoBalanceNanos

					// Partition the balances across the blocks before the snapshot block height.
					totalCount := uint64(0)
					for nodeIterator.Seek(prefix); nodeIterator.ValidForPrefix(prefix); nodeIterator.Next() {
						totalCount++
					}
					currentBlockHeight := uint64(1)
					// We'll force a ceiling on this because otherwise the last block could amass O(snapshotBlockHeight) balances
					balancesPerBlock := totalCount / snapshotBlockHeight
					balancesMap := make(map[lib.PublicKey]uint64)
					if totalCount < snapshotBlockHeight {
						balancesPerBlock = 1
					}
					currentCounter := uint64(0)

					for nodeIterator.Seek(prefix); nodeIterator.ValidForPrefix(prefix); nodeIterator.Next() {
						key := nodeIterator.Item().Key()
						keyCopy := make([]byte, len(key))
						copy(keyCopy[:], key[:])

						valCopy, err := nodeIterator.Item().ValueCopy(nil)
						if err != nil {
							return errors.Wrapf(err, "Problem iterating over chain database, "+
								"on key (%v) and value (%v)", keyCopy, valCopy)
						}

						balance := lib.DecodeUint64(valCopy)
						pubKey := lib.NewPublicKey(key[1:])
						balancesMap[*pubKey] = balance

						if err := node.Index.PutSingleBalanceSnapshotWithTxn(
							indexTxn, currentBlockHeight, DESOBalance, *pubKey, balance); err != nil {
							return errors.Wrapf(err, "Problem updating balance snapshot in index, "+
								"on key (%v), value (%v), and height (%v)", keyCopy, valCopy,
								snapshot.CurrentEpochSnapshotMetadata.FirstSnapshotBlockHeight)
						}

						currentCounter += 1
						if currentCounter >= balancesPerBlock && currentBlockHeight < snapshotBlockHeight {
							node.Index.PutHypersyncBlockBalances(currentBlockHeight, DESOBalance, balancesMap)
							balancesMap = make(map[lib.PublicKey]uint64)
							currentBlockHeight++
							currentCounter = 0
						}
					}
					if currentCounter > 0 {
						node.Index.PutHypersyncBlockBalances(currentBlockHeight, DESOBalance, balancesMap)
					}
					return nil
				})
			})
			if err != nil {
				glog.Errorf(lib.CLog(lib.Red, fmt.Sprintf("handleSnapshotCompleted: error: (%v)", err)))
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

			err := node.Index.db.Update(func(indexTxn *badger.Txn) error {
				// This is pretty much the same as lib.DBGetAllProfilesByCoinValue but we don't load all entries into memory.
				return node.GetBlockchain().DB().View(func(chainTxn *badger.Txn) error {
					dbPrefixx := append([]byte{}, lib.Prefixes.PrefixCreatorDeSoLockedNanosCreatorPKID...)
					opts := badger.DefaultIteratorOptions
					opts.PrefetchValues = false
					// Go in reverse order since a larger count is better.
					opts.Reverse = true

					it := chainTxn.NewIterator(opts)
					defer it.Close()

					totalCount := uint64(0)
					for it.Seek(dbPrefixx); it.ValidForPrefix(dbPrefixx); it.Next() {
						totalCount++
					}
					currentBlockHeight := uint64(1)
					balancesPerBlock := totalCount / snapshotBlockHeight
					balancesMap := make(map[lib.PublicKey]uint64)
					if totalCount < snapshotBlockHeight {
						balancesPerBlock = 1
					}
					currentCounter := uint64(0)

					// Since we iterate backwards, the prefix must be bigger than all possible
					// counts that could actually exist. We use eight bytes since the count is
					// encoded as a 64-bit big-endian byte slice, which will be eight bytes long.
					maxBigEndianUint64Bytes := []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}
					prefix := append(dbPrefixx, maxBigEndianUint64Bytes...)
					for it.Seek(prefix); it.ValidForPrefix(dbPrefixx); it.Next() {
						rawKey := it.Item().Key()

						// Strip the prefix off the key and check its length. If it contains
						// a big-endian uint64 then it should be at least eight bytes.
						lockedDeSoPubKeyConcatKey := rawKey[1:]
						uint64BytesLen := len(maxBigEndianUint64Bytes)
						expectedLength := uint64BytesLen + btcec.PubKeyBytesLenCompressed
						if len(lockedDeSoPubKeyConcatKey) != expectedLength {
							return fmt.Errorf("Invalid key length %d should be at least %d",
								len(lockedDeSoPubKeyConcatKey), expectedLength)
						}

						lockedDeSoNanos := lib.DecodeUint64(lockedDeSoPubKeyConcatKey[:uint64BytesLen])

						// Appended to the stake should be the profile pub key so extract it here.
						profilePKIDbytes := make([]byte, btcec.PubKeyBytesLenCompressed)
						copy(profilePKIDbytes[:], lockedDeSoPubKeyConcatKey[uint64BytesLen:])
						profilePKID := lib.PublicKeyToPKID(profilePKIDbytes)

						pkBytes := lib.DBGetPublicKeyForPKIDWithTxn(chainTxn, nil, profilePKID)
						if pkBytes == nil {
							return fmt.Errorf("DBGetPublicKeyForPKIDWithTxn: Nil pkBytes for pkid %v",
								lib.PkToStringMainnet(profilePKID[:]))
						}
						pubKey := *lib.NewPublicKey(pkBytes)
						balancesMap[pubKey] = lockedDeSoNanos

						// We have to also put the balances in the other index. Not doing this would cause
						// balances to return zero when we're PAST the first snapshot block height.
						if err := node.Index.PutSingleBalanceSnapshotWithTxn(
							indexTxn, currentBlockHeight, CreatorCoinLockedBalance, pubKey, lockedDeSoNanos); err != nil {

							return errors.Wrapf(err, "PutSingleBalanceSnapshotWithTxn: problem with "+
								"pubkey (%v), lockedDeSoNanos (%v) and firstSnapshotHeight (%v)",
								pubKey, lockedDeSoNanos, snapshot.CurrentEpochSnapshotMetadata.FirstSnapshotBlockHeight)
						}

						currentCounter += 1
						if currentCounter >= balancesPerBlock && currentBlockHeight < snapshotBlockHeight {
							node.Index.PutHypersyncBlockBalances(currentBlockHeight, CreatorCoinLockedBalance, balancesMap)
							balancesMap = make(map[lib.PublicKey]uint64)
							currentBlockHeight++
							currentCounter = 0
						}
					}
					if currentCounter > 0 {
						node.Index.PutHypersyncBlockBalances(currentBlockHeight, CreatorCoinLockedBalance, balancesMap)
					}
					return nil
				})
			})
			if err != nil {
				glog.Errorf(lib.CLog(lib.Red, fmt.Sprintf("handleSnapshotCompleted: Problem iterating locked "+
					"creator DeSo nanos: error: (%v)", err)))
			}
		}

		// Create a new scope to avoid name collision errors
		{
			// Iterate over all the validator entries in the db, look up the corresponding public
			// keys, and then set all the validator entry balances in the db.
			//
			// No need to pass the snapshot because we know we don't have any ancestral
			// records yet.

			err := node.Index.db.Update(func(indexTxn *badger.Txn) error {
				// TODO: check all these comments.
				return node.GetBlockchain().DB().View(func(chainTxn *badger.Txn) error {
					dbPrefixx := append([]byte{}, lib.Prefixes.PrefixValidatorByStatusAndStakeAmount...)
					opts := badger.DefaultIteratorOptions
					opts.PrefetchValues = false
					// Go in reverse order since a larger count is better.
					opts.Reverse = true

					it := chainTxn.NewIterator(opts)
					defer it.Close()

					totalCount := uint64(0)
					// TODO: I really hate how we iterate over the index twice
					// for each balance type. We can do better.
					for it.Seek(dbPrefixx); it.ValidForPrefix(dbPrefixx); it.Next() {
						totalCount++
					}
					currentBlockHeight := uint64(1)
					balancesPerBlock := totalCount / snapshotBlockHeight
					validatorEntryBalances := make(map[lib.PublicKey]uint64)
					if totalCount < snapshotBlockHeight {
						balancesPerBlock = 1
					}
					currentCounter := uint64(0)

					// The key for the validator by status and stake amount looks like
					// this: Prefix, <Status uint8>, <TotalStakeAmountNanos *uint256.Int>, <ValidatorPKID [33]byte> -> nil
					// So we need to chop off the status to pull out the total stake amount nanos and the validator PKID
					maxUint256 := lib.FixedWidthEncodeUint256(lib.MaxUint256)
					prefix := append(dbPrefixx, lib.EncodeUint8(math.MaxUint8)...)
					prefix = append(prefix, maxUint256...)
					for it.Seek(prefix); it.ValidForPrefix(dbPrefixx); it.Next() {
						rawKey := it.Item().Key()

						// Strip the prefix and status off the key and check its length. It
						// should contain a uint256 followed by a validator PKID.
						totalStakeAmountAndValidatorPKIDKey := rawKey[2:]
						expectedLength := len(maxUint256) + btcec.PubKeyBytesLenCompressed
						if len(totalStakeAmountAndValidatorPKIDKey) != expectedLength {
							return fmt.Errorf("invalid key length %d should be at least %d",
								len(totalStakeAmountAndValidatorPKIDKey), expectedLength)
						}
						rr := bytes.NewReader(totalStakeAmountAndValidatorPKIDKey)
						totalStakeAmountNanos, err := lib.FixedWidthDecodeUint256(rr)
						if err != nil {
							return fmt.Errorf("problem decoding stake amount: %v", err)
						}
						if !totalStakeAmountNanos.IsUint64() {
							return fmt.Errorf("FixedWidthDecodeUint256: Stake amount is not a uint64")
						}
						validatorPKIDBytes := make([]byte, btcec.PubKeyBytesLenCompressed)
						_, err = rr.Read(validatorPKIDBytes[:])
						if err != nil {
							return fmt.Errorf("problem reading validator PKID: %v", err)
						}
						// For simplicity, we just interpret the PKID as a public key
						// so we can reuse existing functions.
						validatorPubKey := *lib.NewPublicKey(validatorPKIDBytes)
						validatorEntryBalances[validatorPubKey] = totalStakeAmountNanos.Uint64()

						// We have to also put the balances in the other index. Not doing this would cause
						// balances to return zero when we're PAST the first snapshot block height.
						if err = node.Index.PutSingleBalanceSnapshotWithTxn(
							indexTxn, currentBlockHeight, ValidatorStakedDESOBalance, validatorPubKey,
							totalStakeAmountNanos.Uint64()); err != nil {
							return errors.Wrapf(err, "PutSingleBalanceSnapshotWithTxn: problem with "+
								"validatorPubKey (%v), totalStakeAmountNanos (%v) and firstSnapshotHeight (%v)",
								validatorPubKey, totalStakeAmountNanos, snapshot.CurrentEpochSnapshotMetadata.FirstSnapshotBlockHeight)
						}

						currentCounter++
						if currentCounter >= balancesPerBlock && currentBlockHeight < snapshotBlockHeight {
							node.Index.PutHypersyncBlockBalances(currentBlockHeight, ValidatorStakedDESOBalance, validatorEntryBalances)
							validatorEntryBalances = make(map[lib.PublicKey]uint64)
							currentBlockHeight++
							currentCounter = 0
						}
					}
					if currentCounter > 0 {
						node.Index.PutHypersyncBlockBalances(currentBlockHeight, ValidatorStakedDESOBalance, validatorEntryBalances)
					}
					return nil
				})
			})
			if err != nil {
				glog.Errorf(lib.CLog(lib.Red, fmt.Sprintf("handleSnapshotCompleted: Problem iterating staked "+
					"balances DeSo nanos: error: (%v)", err)))
			}

		}

		// Create a new scope to avoid name collision errors
		{
			// Iterate over all the locked stake entries in the db, look up the corresponding public
			// keys, and then set all the locked stake entry balances in the db.
			//
			// No need to pass the snapshot because we know we don't have any ancestral
			// records yet.

			err := node.Index.db.Update(func(indexTxn *badger.Txn) error {
				// TODO: check all these comments.
				return node.GetBlockchain().DB().View(func(chainTxn *badger.Txn) error {
					dbPrefixx := append([]byte{}, lib.Prefixes.PrefixLockedStakeByValidatorAndStakerAndLockedAt...)
					opts := badger.DefaultIteratorOptions
					opts.PrefetchValues = true
					// Go in reverse order since a larger count is better.
					opts.Reverse = true

					it := chainTxn.NewIterator(opts)
					defer it.Close()

					totalCount := uint64(0)
					// TODO: I really hate how we iterate over the index twice
					// for each balance type. We can do better.
					for it.Seek(dbPrefixx); it.ValidForPrefix(dbPrefixx); it.Next() {
						totalCount++
					}
					currentBlockHeight := uint64(1)
					balancesPerBlock := totalCount / snapshotBlockHeight
					lockedStakerValidatorBalances := make(map[LockedStakeBalanceMapKey]uint64)
					if totalCount < snapshotBlockHeight {
						balancesPerBlock = 1
					}
					currentCounter := uint64(0)
					for it.Seek(dbPrefixx); it.ValidForPrefix(dbPrefixx); it.Next() {
						rawValueCopy, err := it.Item().ValueCopy(nil)
						if err != nil {
							return errors.Wrapf(err, "Problem iterating over chain database, "+
								"on key (%v) and value (%v)", it.Item().Key(), rawValueCopy)
						}
						rr := bytes.NewReader(rawValueCopy)
						lockedStakeEntry, err := lib.DecodeDeSoEncoder(&lib.LockedStakeEntry{}, rr)
						if err != nil {
							return errors.Wrapf(err, "Problem decoding locked stake entry: %v", err)
						}
						if !lockedStakeEntry.LockedAmountNanos.IsUint64() {
							return fmt.Errorf("LockedAmountNanos is not a uint64")
						}
						if !lockedStakeEntry.LockedAmountNanos.IsUint64() {
							return fmt.Errorf("LockedAtEpochNumber is not a uint64")
						}
						lockedStakeAmountNanos := lockedStakeEntry.LockedAmountNanos.Uint64()

						lockedStakerMapKey := LockedStakeBalanceMapKey{
							StakerPKID:          *lockedStakeEntry.StakerPKID,
							ValidatorPKID:       *lockedStakeEntry.ValidatorPKID,
							LockedAtEpochNumber: lockedStakeEntry.LockedAtEpochNumber,
						}
						lockedStakerValidatorBalances[lockedStakerMapKey] = lockedStakeAmountNanos

						// We have to also put the balances in the other index. Not doing this would cause
						// balances to return zero when we're PAST the first snapshot block height. We are
						// writing to the same key multiple times, but this is okay since we just need the sum
						// which gets updated each time we write to the map.
						if err = node.Index.PutSingleLockedStakeBalanceSnapshotWithTxn(
							indexTxn, currentBlockHeight, LockedStakeDESOBalance, lockedStakeEntry.StakerPKID,
							lockedStakeEntry.ValidatorPKID, lockedStakeEntry.LockedAtEpochNumber,
							lockedStakeEntry.LockedAmountNanos.Uint64()); err != nil {
							return errors.Wrapf(err, "PutSingleBalanceSnapshotWithTxn: problem with "+
								"stakerPKID (%v), validatorPKID (%v) stakeAmountNanos (%v) and firstSnapshotHeight "+
								"(%v)", lockedStakeEntry.StakerPKID, lockedStakeEntry.ValidatorPKID,
								lockedStakeEntry.LockedAmountNanos.Uint64(),
								snapshot.CurrentEpochSnapshotMetadata.FirstSnapshotBlockHeight)
						}

						currentCounter++
						if currentCounter >= balancesPerBlock && currentBlockHeight < snapshotBlockHeight {
							node.Index.PutHypersyncBlockLockedStakeBalances(currentBlockHeight,
								lockedStakerValidatorBalances, LockedStakeDESOBalance)
							lockedStakerValidatorBalances = make(map[LockedStakeBalanceMapKey]uint64)
							currentBlockHeight++
							currentCounter = 0
						}
					}
					if currentCounter > 0 {
						node.Index.PutHypersyncBlockLockedStakeBalances(currentBlockHeight,
							lockedStakerValidatorBalances, LockedStakeDESOBalance)
					}
					return nil
				})
			})
			if err != nil {
				glog.Errorf(lib.CLog(lib.Red, fmt.Sprintf("handleSnapshotCompleted: Problem iterating locked stake "+
					"balances DeSo nanos: error: (%v)", err)))
			}

		}
	}
}

func (node *Node) handleBlockConnected(event *lib.BlockEvent) {
	node.Index.dbMutex.Lock()
	defer node.Index.dbMutex.Unlock()
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
	err = node.Index.PutBalanceSnapshot(event.Block.Header.Height, DESOBalance, balances)
	if err != nil {
		glog.Errorf("PutBalanceSnapshot: %v", err)
	}

	// Iterate over all PKID mappings to get all public keys that may
	// have been affected by a SwapIdentity. If we don't do this we'll
	// miss swaps that involve a public key with a missing profile.
	lockedBalances := make(map[lib.PublicKey]uint64, len(event.UtxoView.PublicKeyToPKIDEntry))
	for pubKey := range event.UtxoView.PublicKeyToPKIDEntry {
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

	err = node.Index.PutBalanceSnapshot(event.Block.Header.Height, CreatorCoinLockedBalance, lockedBalances)
	if err != nil {
		glog.Errorf("PutLockedBalanceSnapshot: %v", err)
	}
	// TODO: Add support for stake entries, locked stake entries, and locked DESO balance entries here.
}

func (node *Node) handleTransactionConnected(event *lib.TransactionEvent) {
	// TODO
}
