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
)

const (
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
	db      *badger.DB
	dbMutex sync.Mutex
}

func NewIndex(db *badger.DB) *RosettaIndex {
	return &RosettaIndex{
		db: db,
	}
}

//
// Utxo Operations
//

func (index *RosettaIndex) utxoOpsKey(blockHash *lib.BlockHash) []byte {
	prefix := append([]byte{}, PrefixUtxoOps)
	prefix = append(prefix, blockHash.ToBytes()...)
	return prefix
}

func (index *RosettaIndex) PutUtxoOps(block *lib.MsgDeSoBlock, utxoOps [][]*lib.UtxoOperation) error {
	blockHash, err := block.Hash()
	if err != nil {
		return err
	}

	opBundle := &lib.UtxoOperationBundle{
		UtxoOpBundle: utxoOps,
	}
	utxoOpsBytes := lib.EncodeToBytes(block.Header.Height, opBundle)

	return index.db.Update(func(txn *badger.Txn) error {
		return txn.Set(index.utxoOpsKey(blockHash), utxoOpsBytes)
	})
}

func (index *RosettaIndex) GetUtxoOps(block *lib.MsgDeSoBlock) ([][]*lib.UtxoOperation, error) {
	blockHash, err := block.Hash()
	if err != nil {
		return nil, err
	}

	opBundle := &lib.UtxoOperationBundle{}

	err = index.db.View(func(txn *badger.Txn) error {
		utxoOpsItem, err := txn.Get(index.utxoOpsKey(blockHash))
		if err != nil {
			return err
		}

		return utxoOpsItem.Value(func(valBytes []byte) error {
			rr := bytes.NewReader(valBytes)
			if exist, err := lib.DecodeFromBytes(opBundle, rr); !exist || err != nil {
				return errors.Wrapf(err, "Problem decoding utxoops, exist: (%v)", exist)
			}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}

	return opBundle.UtxoOpBundle, nil
}

//
// Balance Snapshots
//

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

func lockedStakeBalanceSnapshotKey(balanceType BalanceType, stakerPKID *lib.PKID, validatorPKID *lib.PKID, lockedAtEpochNumber uint64, blockHeight uint64, balance uint64) []byte {
	prefix := append([]byte{}, balanceType.BalanceTypePrefix())
	prefix = append(prefix, stakerPKID[:]...)
	prefix = append(prefix, validatorPKID[:]...)
	prefix = append(prefix, lib.EncodeUint64(lockedAtEpochNumber)...)
	prefix = append(prefix, lib.EncodeUint64(blockHeight)...)
	prefix = append(prefix, lib.EncodeUint64(balance)...)
	return prefix
}

func (balanceType BalanceType) HypersyncBalanceTypePrefix() byte {
	switch balanceType {
	case DESOBalance:
		return 0
	case CreatorCoinLockedBalance:
		return 1
	case ValidatorStakedDESOBalance:
		return 2
	case LockedStakeDESOBalance:
		return 3
	default:
		panic("unknown balance type")
	}
}

func hypersyncHeightToBlockKey(blockHeight uint64, balanceType BalanceType) []byte {
	prefix := append([]byte{}, PrefixHypersyncBlockHeightToBalances)
	prefix = append(prefix, lib.EncodeUint64(blockHeight)...)
	prefix = append(prefix, balanceType.HypersyncBalanceTypePrefix())
	return prefix
}

func (index *RosettaIndex) PutHypersyncBlockBalances(blockHeight uint64, balanceType BalanceType, balances map[lib.PublicKey]uint64) {
	if balanceType != DESOBalance && balanceType != CreatorCoinLockedBalance && balanceType != ValidatorStakedDESOBalance {
		glog.Error("PutHypersyncBlockBalances: Invalid balance type passed in")
		return
	}
	err := index.db.Update(func(txn *badger.Txn) error {
		blockBytes := bytes.NewBuffer([]byte{})
		if err := gob.NewEncoder(blockBytes).Encode(&balances); err != nil {
			return err
		}
		return txn.Set(hypersyncHeightToBlockKey(blockHeight, balanceType), blockBytes.Bytes())
	})
	if err != nil {
		glog.Error(errors.Wrapf(err, "PutHypersyncBlockBalances: Problem putting block: Error:"))
	}
}

func (index *RosettaIndex) PutHypersyncBlockLockedStakeBalances(blockHeight uint64, stakeEntries map[LockedStakeBalanceMapKey]uint64, balanceType BalanceType) {
	if balanceType != LockedStakeDESOBalance {
		glog.Error("PutHypersyncBlockLockedStakeBalances: Invalid balance type passed in")
		return
	}
	err := index.db.Update(func(txn *badger.Txn) error {
		blockBytes := bytes.NewBuffer([]byte{})
		if err := gob.NewEncoder(blockBytes).Encode(&stakeEntries); err != nil {
			return err
		}
		return txn.Set(hypersyncHeightToBlockKey(blockHeight, balanceType), blockBytes.Bytes())
	})
	if err != nil {
		glog.Error(errors.Wrapf(err, "PutHypersyncBlockLockedStakeBalances: Problem putting block: Error:"))
	}
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

		itemCreatorCoinLockedBalances, err := txn.Get(hypersyncHeightToBlockKey(blockHeight, CreatorCoinLockedBalance))
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
		itemValidatorStakedBalances, err := txn.Get(hypersyncHeightToBlockKey(blockHeight, ValidatorStakedDESOBalance))
		if err != nil {
			return err
		}
		validatorStakedBalancesBytes, err := itemValidatorStakedBalances.ValueCopy(nil)
		if err != nil {
			return err
		}
		if err = gob.NewDecoder(bytes.NewReader(validatorStakedBalancesBytes)).Decode(&validatorStakedDESOBalances); err != nil {
			return err
		}
		lockedStakedItemBalances, err := txn.Get(hypersyncHeightToBlockKey(blockHeight, LockedStakeDESOBalance))
		if err != nil {
			return err
		}
		lockedStakedBalancesBytes, err := lockedStakedItemBalances.ValueCopy(nil)
		if err != nil {
			return err
		}
		if err = gob.NewDecoder(bytes.NewReader(lockedStakedBalancesBytes)).Decode(&lockedStakeDESOBalances); err != nil {
			return err
		}
		return nil
	})
	// TODO: Handle this error better.
	if err != nil {
		glog.Error(errors.Wrapf(err, "GetHypersyncBlockBalances: Problem getting block at height (%v)", blockHeight))
	}

	return balances, creatorCoinLockedBalances, validatorStakedDESOBalances, lockedStakeDESOBalances
}

func (index *RosettaIndex) PutBalanceSnapshot(
	height uint64, balanceType BalanceType, balances map[lib.PublicKey]uint64) error {
	if balanceType != DESOBalance && balanceType != CreatorCoinLockedBalance && balanceType != ValidatorStakedDESOBalance {
		return errors.New("PutBalanceSnapshot: Invalid balance type passed in")
	}
	return index.db.Update(func(txn *badger.Txn) error {
		return index.PutBalanceSnapshotWithTxn(txn, height, balanceType, balances)
	})
}

func (index *RosettaIndex) PutBalanceSnapshotWithTxn(
	txn *badger.Txn, height uint64, balanceType BalanceType, balances map[lib.PublicKey]uint64) error {
	if balanceType != DESOBalance && balanceType != CreatorCoinLockedBalance && balanceType != ValidatorStakedDESOBalance {
		return errors.New("PutBalanceSnapshotWithTxn: Invalid balance type passed in")
	}
	for pk, bal := range balances {
		if err := txn.Set(balanceSnapshotKey(balanceType, &pk, height, bal), []byte{}); err != nil {
			return errors.Wrapf(err, "Error in PutBalanceSnapshot for block height: "+
				"%v pub key: %v balance: %v", height, pk, bal)
		}
	}
	return nil
}

func (index *RosettaIndex) PutSingleBalanceSnapshotWithTxn(
	txn *badger.Txn, height uint64, balanceType BalanceType, publicKey lib.PublicKey, balance uint64) error {
	if balanceType != DESOBalance && balanceType != CreatorCoinLockedBalance && balanceType != ValidatorStakedDESOBalance {
		return errors.New("PutSingleBalanceSnapshotWithTxn: Invalid balance type passed in")
	}
	if err := txn.Set(balanceSnapshotKey(balanceType, &publicKey, height, balance), []byte{}); err != nil {
		return errors.Wrapf(err, "Error in PutBalanceSnapshot for block height: "+
			"%v pub key: %v balance: %v", height, publicKey, balance)
	}
	return nil
}

func (index *RosettaIndex) PutLockedStakeBalanceSnapshot(
	height uint64, balanceType BalanceType, balances map[LockedStakeBalanceMapKey]uint64) error {
	if balanceType != LockedStakeDESOBalance {
		return errors.New("PutLockedStakeBalanceSnapshot: Invalid balance type passed in")
	}
	return index.db.Update(func(txn *badger.Txn) error {
		return index.PutLockedStakeBalanceSnapshotWithTxn(txn, height, balanceType, balances)
	})
}

func (index *RosettaIndex) PutLockedStakeBalanceSnapshotWithTxn(
	txn *badger.Txn, height uint64, balanceType BalanceType, balances map[LockedStakeBalanceMapKey]uint64) error {
	if balanceType != LockedStakeDESOBalance {
		return errors.New("PutLockedStakeBalanceSnapshotWithTxn: Invalid balance type passed in")
	}
	for pk, bal := range balances {
		if err := txn.Set(lockedStakeBalanceSnapshotKey(
			balanceType, &pk.StakerPKID, &pk.ValidatorPKID, pk.LockedAtEpochNumber, height, bal), []byte{}); err != nil {
			return errors.Wrapf(err, "Error in PutLockedStakeBalanceSnapshotWithTxn for block height: "+
				"%v pub key: %v balance: %v", height, pk, bal)
		}
	}
	return nil
}

func (index *RosettaIndex) PutSingleLockedStakeBalanceSnapshotWithTxn(
	txn *badger.Txn, height uint64, balanceType BalanceType, stakerPKID *lib.PKID,
	validatorPKID *lib.PKID, lockedAtEpochNumber uint64, balance uint64) error {
	if balanceType != LockedStakeDESOBalance {
		return errors.New("PutSingleLockedStakeBalanceSnapshotWithTxn: Invalid balance type passed in")
	}
	if err := txn.Set(lockedStakeBalanceSnapshotKey(
		balanceType, stakerPKID, validatorPKID, lockedAtEpochNumber, height, balance), []byte{}); err != nil {
		return errors.Wrapf(err, "Error in PutSingleLockedStakeBalanceSnapshotWithTxn for block height: "+
			"%v staker pkid: %v validator pkid: %v balance: %v", height, stakerPKID, validatorPKID, balance)
	}
	return nil
}

func GetBalanceForPublicKeyAtBlockHeightWithTxn(
	txn *badger.Txn, balanceType BalanceType, publicKey *lib.PublicKey, blockHeight uint64) uint64 {
	if balanceType != DESOBalance && balanceType != CreatorCoinLockedBalance && balanceType != ValidatorStakedDESOBalance {
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

func (index *RosettaIndex) GetBalanceSnapshot(balanceType BalanceType, publicKey *lib.PublicKey, blockHeight uint64) uint64 {
	if balanceType != DESOBalance && balanceType != CreatorCoinLockedBalance && balanceType != ValidatorStakedDESOBalance {
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

func GetLockedStakeBalanceForPublicKeyAtBlockHeightWithTxn(txn *badger.Txn, balanceType BalanceType, stakerPKID *lib.PKID, validatorPKID *lib.PKID, lockedAtEpochNumber uint64, blockHeight uint64) uint64 {
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
	maxPrefix := lockedStakeBalanceSnapshotKey(balanceType, stakerPKID, validatorPKID, lockedAtEpochNumber, blockHeight, math.MaxUint64)
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

func (index *RosettaIndex) GetLockedStakeBalanceSnapshot(balanceType BalanceType, stakerPKID *lib.PKID, validatorPKID *lib.PKID, lockedAtEpochNumber uint64, blockHeight uint64) uint64 {
	if balanceType != LockedStakeDESOBalance {
		glog.Errorf("GetLockedStakeBalanceSnapshot: Invalid balance type passed in")
		return 0
	}
	balanceFound := uint64(0)
	index.db.View(func(txn *badger.Txn) error {
		balanceFound = GetLockedStakeBalanceForPublicKeyAtBlockHeightWithTxn(txn, balanceType, stakerPKID, validatorPKID, lockedAtEpochNumber, blockHeight)
		return nil
	})

	return balanceFound
}
