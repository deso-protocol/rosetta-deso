package deso

import (
	"bytes"
	"fmt"
	"github.com/btcsuite/btcd/btcec"
	"github.com/deso-protocol/core/lib"
	"github.com/dgraph-io/badger/v3"
	"github.com/golang/glog"
	"github.com/pkg/errors"
	"time"
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
		glog.Infof("handleSnapshotCompleted: handling snapshot with first snapshot block height %d "+
			"and snapshot block height of %d",
			snapshot.CurrentEpochSnapshotMetadata.FirstSnapshotBlockHeight,
			snapshot.CurrentEpochSnapshotMetadata.SnapshotBlockHeight)
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
		statusLoggingInterval := uint64(1000)
		{
			// Iterate through every single public key and put a balance snapshot down
			// for it for this block. We don't need to worry about ancestral records here
			// because we haven't generated any yet.
			wb := node.Index.db.NewWriteBatch()
			defer wb.Cancel()
			err := node.GetBlockchain().DB().View(func(chainTxn *badger.Txn) error {
				opts := badger.DefaultIteratorOptions
				//// We don't prefetch values when iterating over the keys.
				//opts.PrefetchValues = false
				// Set prefix on iterator options
				opts.Prefix = lib.Prefixes.PrefixPublicKeyToDeSoBalanceNanos
				nodeIterator := chainTxn.NewIterator(opts)
				defer nodeIterator.Close()
				prefix := lib.Prefixes.PrefixPublicKeyToDeSoBalanceNanos

				// Partition the balances across the blocks before the snapshot block height.
				totalCount := uint64(0)
				startTime := time.Now()
				for nodeIterator.Seek(prefix); nodeIterator.ValidForPrefix(prefix); nodeIterator.Next() {
					totalCount++
				}
				glog.Infof("handleSnapshotCompleted: Counting public keys took: %v", time.Since(startTime))
				currentBlockHeight := uint64(1)
				// We'll force a ceiling on this because otherwise the last block could amass O(snapshotBlockHeight) balances
				balancesPerBlock := totalCount / snapshotBlockHeight
				balancesMap := make(map[lib.PublicKey]uint64)
				if totalCount < snapshotBlockHeight {
					balancesPerBlock = 1
				}
				currentCounter := uint64(0)
				currentTime := time.Now()
				//// Create a new node iterator w/ prefetch values set to true.
				//opts.PrefetchValues = true
				//nodeIterator = chainTxn.NewIterator(opts)
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

					if err := node.Index.PutSingleBalanceSnapshotWithWB(
						wb, currentBlockHeight, DESOBalance, *pubKey, balance); err != nil {
						return errors.Wrapf(err, "Problem updating balance snapshot in index, "+
							"on key (%v), value (%v), and height (%v)", keyCopy, valCopy,
							snapshot.CurrentEpochSnapshotMetadata.FirstSnapshotBlockHeight)
					}

					currentCounter += 1
					if currentCounter%statusLoggingInterval == 0 {
						glog.Infof("handleSnapshotCompleted: Processed %d of %d balances in %v. %d%% complete",
							currentCounter, totalCount, time.Since(currentTime), (100*currentCounter)/totalCount)
						currentTime = time.Now()
					}
					if currentCounter >= balancesPerBlock && currentBlockHeight < snapshotBlockHeight {
						node.Index.PutHypersyncBlockBalancesWithWB(wb, currentBlockHeight, DESOBalance, balancesMap)
						balancesMap = make(map[lib.PublicKey]uint64)
						currentBlockHeight++
						currentCounter = 0
					}
				}
				if currentCounter > 0 {
					node.Index.PutHypersyncBlockBalancesWithWB(wb, currentBlockHeight, DESOBalance, balancesMap)
				}
				flushTime := time.Now()
				if err := wb.Flush(); err != nil {
					return errors.Wrapf(err, "Problem flushing write batch")
				}
				glog.Infof("handleSnapshotCompleted: Flush took: %v", time.Since(flushTime))
				return nil
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
			wb := node.Index.db.NewWriteBatch()
			defer wb.Cancel()
			// This is pretty much the same as lib.DBGetAllProfilesByCoinValue but we don't load all entries into memory.
			err := node.GetBlockchain().DB().View(func(chainTxn *badger.Txn) error {
				dbPrefixx := append([]byte{}, lib.Prefixes.PrefixCreatorDeSoLockedNanosCreatorPKID...)
				opts := badger.DefaultIteratorOptions
				opts.PrefetchValues = false

				// Set the prefix on the iterator options
				opts.Prefix = dbPrefixx

				it := chainTxn.NewIterator(opts)
				defer it.Close()

				totalCount := uint64(0)
				startTime := time.Now()
				for it.Seek(dbPrefixx); it.ValidForPrefix(dbPrefixx); it.Next() {
					totalCount++
				}
				glog.Infof("handleSnapshotCompleted: Counting CC locked balance public keys took: %v", time.Since(startTime))
				currentBlockHeight := uint64(1)
				balancesPerBlock := totalCount / snapshotBlockHeight
				balancesMap := make(map[lib.PublicKey]uint64)
				if totalCount < snapshotBlockHeight {
					balancesPerBlock = 1
				}
				currentCounter := uint64(0)
				currentTime := time.Now()
				maxBigEndianUint64Bytes := []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}
				for it.Seek(dbPrefixx); it.ValidForPrefix(dbPrefixx); it.Next() {
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
					if err := node.Index.PutSingleBalanceSnapshotWithWB(
						wb, currentBlockHeight, CreatorCoinLockedBalance, pubKey, lockedDeSoNanos); err != nil {

						return errors.Wrapf(err, "PutSingleBalanceSnapshotWithWB: problem with "+
							"pubkey (%v), lockedDeSoNanos (%v) and firstSnapshotHeight (%v)",
							pubKey, lockedDeSoNanos, snapshot.CurrentEpochSnapshotMetadata.FirstSnapshotBlockHeight)
					}

					currentCounter += 1
					if currentCounter%statusLoggingInterval == 0 {
						glog.Infof("handleSnapshotCompleted: Processed %d of %d locked creator coin balances in %v. %d%% complete",
							currentCounter, totalCount, time.Since(currentTime), (100*currentCounter)/totalCount)
						currentTime = time.Now()
					}
					if currentCounter >= balancesPerBlock && currentBlockHeight < snapshotBlockHeight {
						node.Index.PutHypersyncBlockBalancesWithWB(
							wb, currentBlockHeight, CreatorCoinLockedBalance, balancesMap)
						balancesMap = make(map[lib.PublicKey]uint64)
						currentBlockHeight++
						currentCounter = 0
					}
				}
				if currentCounter > 0 {
					node.Index.PutHypersyncBlockBalancesWithWB(
						wb, currentBlockHeight, CreatorCoinLockedBalance, balancesMap)
				}
				flushTime := time.Now()
				if err := wb.Flush(); err != nil {
					return errors.Wrapf(err, "Problem flushing write batch")
				}
				glog.Infof("handleSnapshotCompleted: CC Locked balance Flush took: %v", time.Since(flushTime))
				return nil
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
			wb := node.Index.db.NewWriteBatch()
			defer wb.Cancel()
			// TODO: check all these comments.
			err := node.GetBlockchain().DB().View(func(chainTxn *badger.Txn) error {
				dbPrefixx := append([]byte{}, lib.Prefixes.PrefixValidatorByStatusAndStakeAmount...)
				opts := badger.DefaultIteratorOptions
				opts.PrefetchValues = false

				// Set prefix on iterator
				opts.Prefix = dbPrefixx

				it := chainTxn.NewIterator(opts)
				defer it.Close()

				totalCount := uint64(0)
				startTime := time.Now()
				// TODO: I really hate how we iterate over the index twice
				// for each balance type. We can do better.
				for it.Seek(dbPrefixx); it.ValidForPrefix(dbPrefixx); it.Next() {
					totalCount++
				}
				glog.Infof("handleSnapshotCompleted: Counting validator entries took: %v", time.Since(startTime))
				currentBlockHeight := uint64(1)
				balancesPerBlock := totalCount / snapshotBlockHeight
				validatorEntryBalances := make(map[lib.PublicKey]uint64)
				if totalCount < snapshotBlockHeight {
					balancesPerBlock = 1
				}
				currentCounter := uint64(0)
				currentTime := time.Now()
				// The key for the validator by status and stake amount looks like
				// this: Prefix, <Status uint8>, <TotalStakeAmountNanos *uint256.Int>, <ValidatorPKID [33]byte> -> nil
				// So we need to chop off the status to pull out the total stake amount nanos and the validator PKID
				maxUint256 := lib.FixedWidthEncodeUint256(lib.MaxUint256)
				for it.Seek(dbPrefixx); it.ValidForPrefix(dbPrefixx); it.Next() {
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
					if err = node.Index.PutSingleBalanceSnapshotWithWB(
						wb, currentBlockHeight, ValidatorStakedDESOBalance, validatorPubKey,
						totalStakeAmountNanos.Uint64()); err != nil {
						return errors.Wrapf(err, "PutSingleBalanceSnapshotWithWB: problem with "+
							"validatorPubKey (%v), totalStakeAmountNanos (%v) and firstSnapshotHeight (%v)",
							validatorPubKey, totalStakeAmountNanos,
							snapshot.CurrentEpochSnapshotMetadata.FirstSnapshotBlockHeight)
					}

					currentCounter++
					if currentCounter%statusLoggingInterval == 0 {
						glog.Infof("handleSnapshotCompleted: Processed %d of %d validator staked balances in %v. %d%% complete",
							currentCounter, totalCount, time.Since(currentTime), (100*currentCounter)/totalCount)
						currentTime = time.Now()
					}
					if currentCounter >= balancesPerBlock && currentBlockHeight < snapshotBlockHeight {
						node.Index.PutHypersyncBlockBalancesWithWB(
							wb, currentBlockHeight, ValidatorStakedDESOBalance, validatorEntryBalances)
						validatorEntryBalances = make(map[lib.PublicKey]uint64)
						currentBlockHeight++
						currentCounter = 0
					}
				}
				if currentCounter > 0 {
					node.Index.PutHypersyncBlockBalancesWithWB(
						wb, currentBlockHeight, ValidatorStakedDESOBalance, validatorEntryBalances)
				}
				flushTime := time.Now()
				if err := wb.Flush(); err != nil {
					return errors.Wrapf(err, "Problem flushing write batch")
				}
				glog.Infof("handleSnapshotCompleted: Validator staked balance Flush took: %v", time.Since(flushTime))
				return nil
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

			wb := node.Index.db.NewWriteBatch()
			// TODO: check all these comments.
			err := node.GetBlockchain().DB().View(func(chainTxn *badger.Txn) error {
				dbPrefixx := append([]byte{}, lib.Prefixes.PrefixLockedStakeByValidatorAndStakerAndLockedAt...)
				opts := badger.DefaultIteratorOptions
				opts.PrefetchValues = true

				// Set prefix on iterator
				opts.Prefix = dbPrefixx

				it := chainTxn.NewIterator(opts)
				defer it.Close()

				totalCount := uint64(0)
				startTime := time.Now()
				// TODO: I really hate how we iterate over the index twice
				// for each balance type. We can do better.
				for it.Seek(dbPrefixx); it.ValidForPrefix(dbPrefixx); it.Next() {
					totalCount++
				}
				glog.Infof("handleSnapshotCompleted: Counting locked stake entries took: %v", time.Since(startTime))
				currentBlockHeight := uint64(1)
				balancesPerBlock := totalCount / snapshotBlockHeight
				lockedStakerValidatorBalances := make(map[LockedStakeBalanceMapKey]uint64)
				if totalCount < snapshotBlockHeight {
					balancesPerBlock = 1
				}
				currentCounter := uint64(0)
				currentTime := time.Now()
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
					if err = node.Index.PutSingleLockedStakeBalanceSnapshotWithWB(
						wb, currentBlockHeight, LockedStakeDESOBalance, lockedStakeEntry.StakerPKID,
						lockedStakeEntry.ValidatorPKID, lockedStakeEntry.LockedAtEpochNumber,
						lockedStakeEntry.LockedAmountNanos.Uint64()); err != nil {
						return errors.Wrapf(err, "PutSingleBalanceSnapshotWithWB: problem with "+
							"stakerPKID (%v), validatorPKID (%v) stakeAmountNanos (%v) and firstSnapshotHeight "+
							"(%v)", lockedStakeEntry.StakerPKID, lockedStakeEntry.ValidatorPKID,
							lockedStakeEntry.LockedAmountNanos.Uint64(),
							snapshot.CurrentEpochSnapshotMetadata.FirstSnapshotBlockHeight)
					}

					currentCounter++
					if currentCounter%statusLoggingInterval == 0 {
						glog.Infof("handleSnapshotCompleted: Processed %d of %d locked stake balances in %v. %d%% complete",
							currentCounter, totalCount, time.Since(currentTime), (100*currentCounter)/totalCount)
						currentTime = time.Now()
					}
					if currentCounter >= balancesPerBlock && currentBlockHeight < snapshotBlockHeight {
						node.Index.PutHypersyncBlockLockedStakeBalancesWithWB(wb, currentBlockHeight,
							lockedStakerValidatorBalances, LockedStakeDESOBalance)
						lockedStakerValidatorBalances = make(map[LockedStakeBalanceMapKey]uint64)
						currentBlockHeight++
						currentCounter = 0
					}
				}
				if currentCounter > 0 {
					node.Index.PutHypersyncBlockLockedStakeBalancesWithWB(wb, currentBlockHeight,
						lockedStakerValidatorBalances, LockedStakeDESOBalance)
				}
				flushTime := time.Now()
				if err := wb.Flush(); err != nil {
					return errors.Wrapf(err, "Problem flushing write batch")
				}
				glog.Infof("handleSnapshotCompleted: Locked stake balance Flush took: %v", time.Since(flushTime))
				return nil
			})
			if err != nil {
				glog.Errorf(lib.CLog(lib.Red, fmt.Sprintf("handleSnapshotCompleted: Problem iterating locked stake "+
					"balances DeSo nanos: error: (%v)", err)))
			}
		}
	}
}

func (node *Node) handleBlockCommitted(event *lib.BlockEvent) {
	node.Index.dbMutex.Lock()
	defer node.Index.dbMutex.Unlock()
	// We do some special logic if we have a snapshot.
	if node.Server != nil {
		snapshot := node.Server.GetBlockchain().Snapshot()
		if (node.Config.SyncType == lib.NodeSyncTypeHyperSync ||
			node.Config.SyncType == lib.NodeSyncTypeHyperSyncArchival) &&
			snapshot != nil &&
			snapshot.CurrentEpochSnapshotMetadata.FirstSnapshotBlockHeight != 0 {

			firstSnapshotHeight := snapshot.CurrentEpochSnapshotMetadata.FirstSnapshotBlockHeight

			// If we're before the first snapshot height, then do nothing.
			height := event.Block.Header.Height
			if height <= firstSnapshotHeight {
				return
			}
		}
	}
	glog.Infof("handleBlockCommitted: height: %d", event.Block.Header.Height)
	if event.UtxoView == nil {
		glog.Errorf("handleBlockCommitted: utxoView is nil for height: %d", event.Block.Header.Height)
		return
	}
	currentTime := time.Now()
	// If we get here, then we're connecting a block after the first snapshot OR we
	// don't have a snapshot. We output extra metadata for this block to ensure
	// Rosetta connects it appropriately.

	wb := node.Index.db.NewWriteBatch()
	defer wb.Cancel()
	// Save a balance snapshot
	balances := event.UtxoView.PublicKeyToDeSoBalanceNanos
	if err := node.Index.PutBalanceSnapshotWithWB(wb, event.Block.Header.Height, DESOBalance, balances); err != nil {
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

	if err := node.Index.PutBalanceSnapshotWithWB(wb, event.Block.Header.Height, CreatorCoinLockedBalance,
		lockedBalances); err != nil {
		glog.Errorf("PutLockedBalanceSnapshot: %v", err)
	}

	// Iterate over all validator entries that may have been modified.
	validatorEntries := event.UtxoView.ValidatorPKIDToValidatorEntry
	validatorEntryBalances := make(map[lib.PublicKey]uint64, len(validatorEntries))
	for _, validator := range validatorEntries {
		balanceToPut := uint64(0)
		if !validator.IsDeleted() {
			if !validator.TotalStakeAmountNanos.IsUint64() {
				glog.Errorf("handleBlockCommitted: TotalStakeAmountNanos is not a uint64")
				continue
			}
			balanceToPut = validator.TotalStakeAmountNanos.Uint64()
		}
		validatorEntryBalances[*lib.NewPublicKey(validator.ValidatorPKID.ToBytes())] = balanceToPut
	}

	if err := node.Index.PutBalanceSnapshotWithWB(wb, event.Block.Header.Height, ValidatorStakedDESOBalance,
		validatorEntryBalances); err != nil {
		glog.Errorf("PutStakedBalanceSnapshot: %v", err)
	}

	// Iterate over all locked stake entries that may have been modified.
	lockedStakeEntries := event.UtxoView.LockedStakeMapKeyToLockedStakeEntry
	lockedStakeEntryBalances := make(map[LockedStakeBalanceMapKey]uint64, len(lockedStakeEntries))
	for _, lockedStakeEntry := range lockedStakeEntries {
		balanceToPut := uint64(0)
		if !lockedStakeEntry.IsDeleted() {
			if !lockedStakeEntry.LockedAmountNanos.IsUint64() {
				glog.Errorf("handleBlockCommitted: LockedAmountNanos is not a uint64")
				continue
			}
			balanceToPut = lockedStakeEntry.LockedAmountNanos.Uint64()
		}
		lockedStakeEntryBalances[LockedStakeBalanceMapKey{
			StakerPKID:          *lockedStakeEntry.StakerPKID,
			ValidatorPKID:       *lockedStakeEntry.ValidatorPKID,
			LockedAtEpochNumber: lockedStakeEntry.LockedAtEpochNumber,
		}] = balanceToPut
	}

	if err := node.Index.PutLockedStakeBalanceSnapshotWithWB(wb, event.Block.Header.Height, LockedStakeDESOBalance,
		lockedStakeEntryBalances); err != nil {
		glog.Errorf("handleBlockCommitted: PutLockedStakeBalanceSnapshot: %v", err)
	}

	if err := wb.Flush(); err != nil {
		glog.Errorf("Flush: %v", err)
	}
	glog.Infof("handleBlockCommitted: Processed block %d in %v", event.Block.Header.Height, time.Since(currentTime))
}

func (node *Node) handleTransactionConnected(event *lib.TransactionEvent) {
	// TODO
}
