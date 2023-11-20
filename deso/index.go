package deso

import (
	"bytes"
	"encoding/gob"
	"github.com/deso-protocol/core/lib"
	"github.com/dgraph-io/badger/v4"
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
	// <prefix 1 byte, blockHeight 8 bytes, isLocked 1 byte> -> map[lib.PublicKey]uint64
	PrefixHypersyncBlockHeightToBalances = byte(3)
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
	bytes := lib.EncodeToBytes(block.Header.Height, opBundle)

	return index.db.Update(func(txn *badger.Txn) error {
		return txn.Set(index.utxoOpsKey(blockHash), bytes)
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

func balanceSnapshotKey(isLockedBalance bool, publicKey *lib.PublicKey, blockHeight uint64, balance uint64) []byte {
	startPrefix := PrefixBalanceSnapshots
	if isLockedBalance {
		startPrefix = PrefixLockedBalanceSnapshots
	}

	prefix := append([]byte{}, startPrefix)
	prefix = append(prefix, publicKey[:]...)
	prefix = append(prefix, lib.EncodeUint64(blockHeight)...)
	prefix = append(prefix, lib.EncodeUint64(balance)...)
	return prefix
}

func hypersyncHeightToBlockKey(blockHeight uint64, isLocked bool) []byte {

	lockedByte := byte(0)
	if isLocked {
		lockedByte = byte(1)
	}

	prefix := append([]byte{}, PrefixHypersyncBlockHeightToBalances)
	prefix = append(prefix, lib.EncodeUint64(blockHeight)...)
	prefix = append(prefix, lockedByte)
	return prefix
}

func (index *RosettaIndex) PutHypersyncBlockBalances(blockHeight uint64, isLocked bool, balances map[lib.PublicKey]uint64) {

	err := index.db.Update(func(txn *badger.Txn) error {
		blockBytes := bytes.NewBuffer([]byte{})
		if err := gob.NewEncoder(blockBytes).Encode(&balances); err != nil {
			return err
		}
		return txn.Set(hypersyncHeightToBlockKey(blockHeight, isLocked), blockBytes.Bytes())
	})
	if err != nil {
		glog.Error(errors.Wrapf(err, "PutHypersyncBlockBalances: Problem putting block: Error:"))
	}
}

func (index *RosettaIndex) GetHypersyncBlockBalances(blockHeight uint64) (
	_balances map[lib.PublicKey]uint64, _lockedBalances map[lib.PublicKey]uint64) {

	balances := make(map[lib.PublicKey]uint64)
	lockedBalances := make(map[lib.PublicKey]uint64)
	err := index.db.View(func(txn *badger.Txn) error {
		itemBalances, err := txn.Get(hypersyncHeightToBlockKey(blockHeight, false))
		if err != nil {
			return err
		}
		balancesBytes, err := itemBalances.ValueCopy(nil)
		if err != nil {
			return err
		}
		if err := gob.NewDecoder(bytes.NewReader(balancesBytes)).Decode(&balances); err != nil {
			return err
		}

		itemLocked, err := txn.Get(hypersyncHeightToBlockKey(blockHeight, true))
		if err != nil {
			return err
		}
		lockedBytes, err := itemLocked.ValueCopy(nil)
		if err != nil {
			return err
		}
		if err := gob.NewDecoder(bytes.NewReader(lockedBytes)).Decode(&lockedBalances); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		glog.Error(errors.Wrapf(err, "GetHypersyncBlockBalances: Problem getting block at height (%v)", blockHeight))
	}

	return balances, lockedBalances
}

func (index *RosettaIndex) PutBalanceSnapshot(
	height uint64, isLockedBalance bool, balances map[lib.PublicKey]uint64) error {

	return index.db.Update(func(txn *badger.Txn) error {
		return index.PutBalanceSnapshotWithTxn(txn, height, isLockedBalance, balances)
	})
}

func (index *RosettaIndex) PutBalanceSnapshotWithTxn(
	txn *badger.Txn, height uint64, isLockedBalance bool, balances map[lib.PublicKey]uint64) error {

	for pk, bal := range balances {
		if err := txn.Set(balanceSnapshotKey(isLockedBalance, &pk, height, bal), []byte{}); err != nil {
			return errors.Wrapf(err, "Error in PutBalanceSnapshot for block height: "+
				"%v pub key: %v balance: %v", height, pk, bal)
		}
	}
	return nil
}

func (index *RosettaIndex) PutSingleBalanceSnapshotWithTxn(
	txn *badger.Txn, height uint64, isLockedBalance bool, publicKey lib.PublicKey, balance uint64) error {

	if err := txn.Set(balanceSnapshotKey(isLockedBalance, &publicKey, height, balance), []byte{}); err != nil {
		return errors.Wrapf(err, "Error in PutBalanceSnapshot for block height: "+
			"%v pub key: %v balance: %v", height, publicKey, balance)
	}
	return nil
}

func GetBalanceForPublicKeyAtBlockHeightWithTxn(
	txn *badger.Txn, isLockedBalance bool, publicKey *lib.PublicKey, blockHeight uint64) uint64 {

	opts := badger.DefaultIteratorOptions
	opts.PrefetchValues = false

	// Go in reverse order.
	opts.Reverse = true

	it := txn.NewIterator(opts)
	defer it.Close()

	// Since we iterate backwards, the prefix must be bigger than all possible
	// values that could actually exist. This means the key we use the pubkey
	// and block height with max balance.
	maxPrefix := balanceSnapshotKey(isLockedBalance, publicKey, blockHeight, math.MaxUint64)
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

func (index *RosettaIndex) GetBalanceSnapshot(isLockedBalance bool, publicKey *lib.PublicKey, blockHeight uint64) uint64 {
	balanceFound := uint64(0)
	index.db.View(func(txn *badger.Txn) error {
		balanceFound = GetBalanceForPublicKeyAtBlockHeightWithTxn(
			txn, isLockedBalance, publicKey, blockHeight)
		return nil
	})

	return balanceFound
}
