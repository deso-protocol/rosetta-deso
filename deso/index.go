package deso

import (
	"bytes"
	"encoding/gob"
	"github.com/deso-protocol/core/lib"
	"github.com/dgraph-io/badger/v3"
	"github.com/golang/glog"
	"github.com/pkg/errors"
	"math"
	"sync"
	"time"
)

const (
	// Note: UtxoOps are no longer used because we directly pull them from the node's
	// badger db. We keep this prefix around so it is clear why the others are numbered
	// as they are.
	PrefixUtxoOps = byte(0)

	// Public key balances
	// <prefix 1 byte, public key 33 bytes, block height 8 bytes, balance 8 bytes> -> <>
	PrefixBalanceSnapshots = byte(1)

	// Creator coins
	// <prefix 1 byte, public key 33 bytes, block height 8 bytes, balance 8 bytes> -> <>
	PrefixLockedBalanceSnapshots = byte(2)

	// Fake genesis balances we use to initialize Rosetta when running hypersync.
	// See comments in block.go and convertBlock() for more details.
	// <prefix 1 byte, blockHeight 8 bytes, balance type prefix byte> -> map[lib.PublicKey]uint64
	PrefixHypersyncBlockHeightToBalances = byte(3)

	// DESO staked to validators
	// <prefix 1 byte, validator PKID 33 bytes, blockHeight 8 bytes, balance 8 bytes> -> <>
	PrefixValidatorStakedBalanceSnapshots = byte(4)

	// Locked Stake entries
	// <prefix 1 byte, staker PKID 33 bytes, validator PKID 33 bytes, locked at epoch number 8 bytes, blockHeight 8 bytes, balance 8 bytes> -> <>
	PrefixLockedStakeBalanceSnapshots = byte(5)
)

type RosettaIndex struct {
	db       *badger.DB
	dbMutex  sync.Mutex
	chainDB  *badger.DB
	snapshot *lib.Snapshot
}

func NewIndex(db *badger.DB, chainDB *badger.DB) *RosettaIndex {
	return &RosettaIndex{
		db:      db,
		chainDB: chainDB,
	}
}

//
// Utxo Operations
//

func (index *RosettaIndex) GetUtxoOps(block *lib.MsgDeSoBlock) ([][]*lib.UtxoOperation, error) {
	blockHash, err := block.Hash()
	if err != nil {
		return nil, err
	}
	return lib.GetUtxoOperationsForBlock(index.chainDB, index.snapshot, blockHash)
}

//
// Balance Snapshots
//

// BalanceType is introduced with PoS to differentiate between different types of balances.
// Previously, there were only two types - DESOBalance and CreatorCoinLockedBalance. These
// were distinguished by a boolean "locked". We took advantage of the fact that
// DESOBalance was represented by false (byte representation of 0) and CreatorCoinLockedBalance
// was represented by true (byte representation of 1). This allows us to avoid a resync of
// rosetta with this upgrade.
type BalanceType uint8

const (
	DESOBalance                BalanceType = 0
	CreatorCoinLockedBalance   BalanceType = 1
	ValidatorStakedDESOBalance BalanceType = 2
	LockedStakeDESOBalance     BalanceType = 3
)

func (balanceType BalanceType) BalanceTypePrefix() byte {
	switch balanceType {
	case DESOBalance:
		return PrefixBalanceSnapshots
	case CreatorCoinLockedBalance:
		return PrefixLockedBalanceSnapshots
	case ValidatorStakedDESOBalance:
		return PrefixValidatorStakedBalanceSnapshots
	case LockedStakeDESOBalance:
		return PrefixLockedStakeBalanceSnapshots
	default:
		panic("unknown balance type")
	}
}

func balanceSnapshotKey(balanceType BalanceType, publicKey *lib.PublicKey, blockHeight uint64, balance uint64) []byte {
	prefix := append([]byte{}, balanceType.BalanceTypePrefix())
	prefix = append(prefix, publicKey[:]...)
	prefix = append(prefix, lib.EncodeUint64(blockHeight)...)
	prefix = append(prefix, lib.EncodeUint64(balance)...)
	return prefix
}

func lockedStakeBalanceSnapshotKey(
	balanceType BalanceType, stakerPKID *lib.PKID, validatorPKID *lib.PKID, lockedAtEpochNumber uint64,
	blockHeight uint64, balance uint64,
) []byte {
	prefix := append([]byte{}, balanceType.BalanceTypePrefix())
	prefix = append(prefix, stakerPKID[:]...)
	prefix = append(prefix, validatorPKID[:]...)
	prefix = append(prefix, lib.EncodeUint64(lockedAtEpochNumber)...)
	prefix = append(prefix, lib.EncodeUint64(blockHeight)...)
	prefix = append(prefix, lib.EncodeUint64(balance)...)
	return prefix
}

func hypersyncHeightToBlockKey(blockHeight uint64, balanceType BalanceType) []byte {
	prefix := append([]byte{}, PrefixHypersyncBlockHeightToBalances)
	prefix = append(prefix, lib.EncodeUint64(blockHeight)...)
	prefix = append(prefix, byte(balanceType))
	return prefix
}

func (index *RosettaIndex) PutHypersyncBlockBalancesWithWB(
	wb *badger.WriteBatch, blockHeight uint64, balanceType BalanceType, balances map[lib.PublicKey]uint64) {
	putBlockStartTime := time.Now()
	if balanceType != DESOBalance && balanceType != CreatorCoinLockedBalance &&
		balanceType != ValidatorStakedDESOBalance {
		glog.Error("PutHypersyncBlockBalancesWithWB: Invalid balance type passed in")
		return
	}
	currentTime := time.Now()
	blockBytes := bytes.NewBuffer([]byte{})
	if err := gob.NewEncoder(blockBytes).Encode(&balances); err != nil {
		glog.Errorf("PutHypersyncBlockBalancesWithWB: error gob encoding balances: %v", err)
		return
	}
	glog.V(4).Infof("Time to gob encode %d balances: %v", len(balances), time.Since(currentTime))
	if err := wb.Set(hypersyncHeightToBlockKey(blockHeight, balanceType), blockBytes.Bytes()); err != nil {
		glog.Error(errors.Wrapf(err, "PutHypersyncBlockBalancesWithWB: Problem putting block: Error:"))
	}
	glog.V(4).Infof("Time to put %d balances: %v", len(balances), time.Since(putBlockStartTime))
}

func (index *RosettaIndex) PutHypersyncBlockLockedStakeBalancesWithWB(
	wb *badger.WriteBatch, blockHeight uint64, stakeEntries map[LockedStakeBalanceMapKey]uint64, balanceType BalanceType) {
	putBlockStartTime := time.Now()
	if balanceType != LockedStakeDESOBalance {
		glog.Error("PutHypersyncBlockLockedStakeBalancesWithWB: Invalid balance type passed in")
		return
	}
	currentTime := time.Now()
	blockBytes := bytes.NewBuffer([]byte{})
	if err := gob.NewEncoder(blockBytes).Encode(&stakeEntries); err != nil {
		glog.Errorf("PutHypersyncBlockLockedStakeBalancesWithDB: error gob encoding locked stake entries: %v", err)
		return
	}
	glog.V(4).Infof("Time to gob encode %d locked stake entries: %v", len(stakeEntries), time.Since(currentTime))
	if err := wb.Set(hypersyncHeightToBlockKey(blockHeight, balanceType), blockBytes.Bytes()); err != nil {
		glog.Error(errors.Wrapf(err, "PutHypersyncBlockLockedStakeBalancesWithWB: Problem putting block: Error:"))
	}
	glog.V(4).Infof("Time to put %d locked stake entries: %v", len(stakeEntries), time.Since(putBlockStartTime))
}

func (index *RosettaIndex) GetHypersyncBlockBalances(blockHeight uint64) (
	_balances map[lib.PublicKey]uint64, _lockedBalances map[lib.PublicKey]uint64,
	_stakedDESOBalances map[lib.PublicKey]uint64, _lockedStakeDESOBalances map[LockedStakeBalanceMapKey]uint64) {

	balances := make(map[lib.PublicKey]uint64)
	creatorCoinLockedBalances := make(map[lib.PublicKey]uint64)
	validatorStakedDESOBalances := make(map[lib.PublicKey]uint64)
	lockedStakeDESOBalances := make(map[LockedStakeBalanceMapKey]uint64)
	err := index.db.View(func(txn *badger.Txn) error {
		itemBalances, err := txn.Get(hypersyncHeightToBlockKey(blockHeight, DESOBalance))
		if errors.Is(err, badger.ErrKeyNotFound) {
			glog.Infof("GetHypersyncBlockBalances: No balances found for block at height (%v)", blockHeight)
		} else {
			if err != nil {
				return err
			}
			balancesBytes, err := itemBalances.ValueCopy(nil)
			if err != nil {
				return err
			}
			if err = gob.NewDecoder(bytes.NewReader(balancesBytes)).Decode(&balances); err != nil {
				return err
			}
		}

		itemCreatorCoinLockedBalances, err := txn.Get(hypersyncHeightToBlockKey(blockHeight, CreatorCoinLockedBalance))
		if errors.Is(err, badger.ErrKeyNotFound) {
			glog.Infof("GetHypersyncBlockBalances: No creator coin locked balances found for block at height (%v)", blockHeight)
		} else {
			if err != nil {
				return err
			}
			creatorCoinLockedBalancesBytes, err := itemCreatorCoinLockedBalances.ValueCopy(nil)
			if err != nil {
				return err
			}
			if err = gob.NewDecoder(bytes.NewReader(creatorCoinLockedBalancesBytes)).Decode(
				&creatorCoinLockedBalances); err != nil {
				return err
			}
		}

		itemValidatorStakedBalances, err := txn.Get(hypersyncHeightToBlockKey(blockHeight, ValidatorStakedDESOBalance))
		if errors.Is(err, badger.ErrKeyNotFound) {
			glog.Infof("GetHypersyncBlockBalances: No validator staked balances found for block at height (%v)", blockHeight)
		} else {
			if err != nil {
				return err
			}
			validatorStakedBalancesBytes, err := itemValidatorStakedBalances.ValueCopy(nil)
			if err != nil {
				return err
			}
			if err = gob.NewDecoder(bytes.NewReader(validatorStakedBalancesBytes)).
				Decode(&validatorStakedDESOBalances); err != nil {
				return err
			}
		}

		lockedStakedItemBalances, err := txn.Get(hypersyncHeightToBlockKey(blockHeight, LockedStakeDESOBalance))
		if errors.Is(err, badger.ErrKeyNotFound) {
			glog.Infof("GetHypersyncBlockBalances: No locked stake balances found for block at height (%v)", blockHeight)
		} else {
			if err != nil {
				return err
			}
			lockedStakedBalancesBytes, err := lockedStakedItemBalances.ValueCopy(nil)
			if err != nil {
				return err
			}
			if err = gob.NewDecoder(bytes.NewReader(lockedStakedBalancesBytes)).
				Decode(&lockedStakeDESOBalances); err != nil {
				return err
			}
		}
		return nil
	})
	// TODO: Handle this error better.
	if err != nil {
		glog.Error(errors.Wrapf(err, "GetHypersyncBlockBalances: Problem getting block at height (%v)", blockHeight))
	}

	return balances, creatorCoinLockedBalances, validatorStakedDESOBalances, lockedStakeDESOBalances
}

func (index *RosettaIndex) PutBalanceSnapshotWithWB(
	wb *badger.WriteBatch, height uint64, balanceType BalanceType, balances map[lib.PublicKey]uint64) error {
	if balanceType != DESOBalance && balanceType != CreatorCoinLockedBalance &&
		balanceType != ValidatorStakedDESOBalance {
		return errors.New("PutBalanceSnapshotWithTxn: Invalid balance type passed in")
	}
	for pk, bal := range balances {
		if err := wb.Set(balanceSnapshotKey(balanceType, &pk, height, bal), []byte{}); err != nil {
			return errors.Wrapf(err, "Error in PutBalanceSnapshot for block height: "+
				"%v pub key: %v balance: %v", height, pk, bal)
		}
	}
	return nil
}

func (index *RosettaIndex) PutSingleBalanceSnapshotWithWB(
	wb *badger.WriteBatch, height uint64, balanceType BalanceType, publicKey lib.PublicKey, balance uint64) error {
	if balanceType != DESOBalance && balanceType != CreatorCoinLockedBalance &&
		balanceType != ValidatorStakedDESOBalance {
		return errors.New("PutSingleBalanceSnapshotWithWB: Invalid balance type passed in")
	}
	if err := wb.Set(balanceSnapshotKey(balanceType, &publicKey, height, balance), []byte{}); err != nil {
		return errors.Wrapf(err, "Error in PutBalanceSnapshot for block height: "+
			"%v pub key: %v balance: %v", height, publicKey, balance)
	}
	return nil
}

func (index *RosettaIndex) PutLockedStakeBalanceSnapshotWithWB(
	wb *badger.WriteBatch, height uint64, balanceType BalanceType, balances map[LockedStakeBalanceMapKey]uint64) error {
	if balanceType != LockedStakeDESOBalance {
		return errors.New("PutLockedStakeBalanceSnapshotWithTxn: Invalid balance type passed in")
	}
	for pk, bal := range balances {
		if err := wb.Set(lockedStakeBalanceSnapshotKey(
			balanceType, &pk.StakerPKID, &pk.ValidatorPKID, pk.LockedAtEpochNumber, height, bal), []byte{},
		); err != nil {
			return errors.Wrapf(err, "Error in PutLockedStakeBalanceSnapshotWithTxn for block height: "+
				"%v pub key: %v balance: %v", height, pk, bal)
		}
	}
	return nil
}

func (index *RosettaIndex) PutSingleLockedStakeBalanceSnapshotWithWB(
	wb *badger.WriteBatch, height uint64, balanceType BalanceType, stakerPKID *lib.PKID,
	validatorPKID *lib.PKID, lockedAtEpochNumber uint64, balance uint64) error {
	if balanceType != LockedStakeDESOBalance {
		return errors.New("PutSingleLockedStakeBalanceSnapshotWithWB: Invalid balance type passed in")
	}
	if err := wb.Set(lockedStakeBalanceSnapshotKey(
		balanceType, stakerPKID, validatorPKID, lockedAtEpochNumber, height, balance), []byte{}); err != nil {
		return errors.Wrapf(err, "Error in PutSingleLockedStakeBalanceSnapshotWithWB for block height: "+
			"%v staker pkid: %v validator pkid: %v balance: %v", height, stakerPKID, validatorPKID, balance)
	}
	return nil
}

func GetBalanceForPublicKeyAtBlockHeightWithTxn(
	txn *badger.Txn, balanceType BalanceType, publicKey *lib.PublicKey, blockHeight uint64) uint64 {
	if balanceType != DESOBalance && balanceType != CreatorCoinLockedBalance &&
		balanceType != ValidatorStakedDESOBalance {
		glog.Errorf("GetBalanceForPublicKeyAtBlockHeightWithTxn: Invalid balance type passed in")
		return 0
	}
	opts := badger.DefaultIteratorOptions
	opts.PrefetchValues = false

	// Go in reverse order.
	opts.Reverse = true

	it := txn.NewIterator(opts)
	defer it.Close()

	// Since we iterate backwards, the prefix must be bigger than all possible
	// values that could actually exist. This means the key we use the pubkey
	// and block height with max balance.
	maxPrefix := balanceSnapshotKey(balanceType, publicKey, blockHeight, math.MaxUint64)
	// We don't want to consider any keys that don't involve our public key. This
	// will cause the iteration to stop if we don't have any values for our current
	// public key.
	// One byte for the prefix
	validForPrefix := maxPrefix[:1+len(publicKey)]
	var keyFound []byte
	for it.Seek(maxPrefix); it.ValidForPrefix(validForPrefix); it.Next() {
		item := it.Item()
		keyFound = item.Key()
		// Break after the first key we iterate over because we only
		// want the first one.
		break
	}
	// No key found means this user's balance has never appeared in a block.
	if keyFound == nil {
		return 0
	}

	// If we get here we found a valid key. Decode the balance from it
	// and return it.
	// One byte for the prefix
	return lib.DecodeUint64(keyFound[1+len(publicKey)+len(lib.EncodeUint64(blockHeight)):])
}

func (index *RosettaIndex) GetBalanceSnapshot(
	balanceType BalanceType, publicKey *lib.PublicKey, blockHeight uint64,
) uint64 {
	if balanceType != DESOBalance && balanceType != CreatorCoinLockedBalance &&
		balanceType != ValidatorStakedDESOBalance {
		glog.Errorf("GetBalanceSnapshot: Invalid balance type passed in")
		return 0
	}
	balanceFound := uint64(0)
	index.db.View(func(txn *badger.Txn) error {
		balanceFound = GetBalanceForPublicKeyAtBlockHeightWithTxn(
			txn, balanceType, publicKey, blockHeight)
		return nil
	})

	return balanceFound
}

func GetLockedStakeBalanceForPublicKeyAtBlockHeightWithTxn(
	txn *badger.Txn, balanceType BalanceType, stakerPKID *lib.PKID, validatorPKID *lib.PKID, lockedAtEpochNumber uint64,
	blockHeight uint64,
) uint64 {
	if balanceType != LockedStakeDESOBalance {
		glog.Errorf("GetLockedStakeBalanceForPublicKeyAtBlockHeightWithTxn: Invalid balance type passed in")
		return 0
	}
	opts := badger.DefaultIteratorOptions
	opts.PrefetchValues = false

	// Go in reverse order.
	opts.Reverse = true

	it := txn.NewIterator(opts)
	defer it.Close()

	// Since we iterate backwards, the prefix must be bigger than all possible
	// values that could actually exist. This means the key we use the pubkey
	// and block height with max balance.
	maxPrefix := lockedStakeBalanceSnapshotKey(
		balanceType, stakerPKID, validatorPKID, lockedAtEpochNumber, blockHeight, math.MaxUint64)
	// We don't want to consider any keys that don't involve our public key. This
	// will cause the iteration to stop if we don't have any values for our current
	// public key.
	// One byte for the prefix
	validForPrefix := maxPrefix[:1+len(stakerPKID.ToBytes())]
	var keyFound []byte
	for it.Seek(maxPrefix); it.ValidForPrefix(validForPrefix); it.Next() {
		item := it.Item()
		keyFound = item.Key()
		// Break after the first key we iterate over because we only
		// want the first one.
		break
	}
	// No key found means this user's balance has never appeared in a block.
	if keyFound == nil {
		return 0
	}

	// If we get here we found a valid key. Decode the balance from it
	// and return it.
	// One byte for the prefix
	return lib.DecodeUint64(keyFound[1+len(stakerPKID.ToBytes())+len(lib.EncodeUint64(blockHeight)):])
}

func (index *RosettaIndex) GetLockedStakeBalanceSnapshot(
	balanceType BalanceType, stakerPKID *lib.PKID, validatorPKID *lib.PKID, lockedAtEpochNumber uint64,
	blockHeight uint64,
) uint64 {
	if balanceType != LockedStakeDESOBalance {
		glog.Errorf("GetLockedStakeBalanceSnapshot: Invalid balance type passed in")
		return 0
	}
	balanceFound := uint64(0)
	index.db.View(func(txn *badger.Txn) error {
		balanceFound = GetLockedStakeBalanceForPublicKeyAtBlockHeightWithTxn(
			txn, balanceType, stakerPKID, validatorPKID, lockedAtEpochNumber, blockHeight)
		return nil
	})

	return balanceFound
}
