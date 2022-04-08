package deso

import (
	"bytes"
	"encoding/gob"
	"github.com/deso-protocol/core/lib"
	"github.com/dgraph-io/badger/v3"
	"github.com/pkg/errors"
	"math"
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
	// See comments in events.go for more details.
	// <prefix, publicKey 33 bytes, isLocked 1 byte> -> <uint64>
	PrefixHypersyncGenesisBalanceSnapshot = byte(3)
)

type Index struct {
	db *badger.DB
}

func NewIndex(db *badger.DB) *Index {
	return &Index{
		db: db,
	}
}

//
// Utxo Operations
//

func (index *Index) utxoOpsKey(blockHash *lib.BlockHash) []byte {
	prefix := append([]byte{}, PrefixUtxoOps)
	prefix = append(prefix, blockHash.ToBytes()...)
	return prefix
}

func (index *Index) PutUtxoOps(block *lib.MsgDeSoBlock, utxoOps [][]*lib.UtxoOperation) error {
	blockHash, err := block.Hash()
	if err != nil {
		return err
	}

	buf := bytes.NewBuffer([]byte{})
	err = gob.NewEncoder(buf).Encode(utxoOps)
	if err != nil {
		return err
	}

	return index.db.Update(func(txn *badger.Txn) error {
		return txn.Set(index.utxoOpsKey(blockHash), buf.Bytes())
	})
}

func (index *Index) GetUtxoOps(block *lib.MsgDeSoBlock) ([][]*lib.UtxoOperation, error) {
	blockHash, err := block.Hash()
	if err != nil {
		return nil, err
	}

	var utxoOps [][]*lib.UtxoOperation

	err = index.db.View(func(txn *badger.Txn) error {
		utxoOpsItem, err := txn.Get(index.utxoOpsKey(blockHash))
		if err != nil {
			return err
		}

		return utxoOpsItem.Value(func(valBytes []byte) error {
			return gob.NewDecoder(bytes.NewReader(valBytes)).Decode(&utxoOps)
		})
	})
	if err != nil {
		return nil, err
	}

	return utxoOps, nil
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

func (index *Index) GetHypersyncBalanceSnapshot() (
	_balances map[lib.PublicKey]uint64,
	_lockedBalances map[lib.PublicKey]uint64) {

	balances := make(map[lib.PublicKey]uint64)
	lockedBalances := make(map[lib.PublicKey]uint64)

	publicKeysWithIsLocked, balBytesList := lib.EnumerateKeysForPrefix(
		index.db, []byte{PrefixHypersyncGenesisBalanceSnapshot})
	for ii, kk := range publicKeysWithIsLocked {
		pkBytes := kk[2:]
		bal := lib.DecodeUint64(balBytesList[ii])

		isLockedByte := kk[1]
		if isLockedByte == byte(0) {
			balances[*lib.NewPublicKey(pkBytes)] = bal
		} else {
			lockedBalances[*lib.NewPublicKey(pkBytes)] = bal
		}
	}

	return balances, lockedBalances
}

func (index *Index) PutHypersyncBalanceSnapshot(
	isLockedBalance bool, balances map[lib.PublicKey]uint64) error {

	return index.db.Update(func(txn *badger.Txn) error {
		return index.PutHypersyncBalanceSnapshotWithTxn(txn, isLockedBalance, balances)
	})
}

func (index *Index) PutHypersyncBalanceSnapshotWithTxn(
	txn *badger.Txn, isLockedBalance bool, balances map[lib.PublicKey]uint64) error {

	isLockedPrefix := byte(0)
	if isLockedBalance {
		isLockedPrefix = byte(1)
	}

	for pk, bal := range balances {
		pubKeyAndIsLocked := append([]byte{}, PrefixHypersyncGenesisBalanceSnapshot)
		pubKeyAndIsLocked = append(pubKeyAndIsLocked, isLockedPrefix)
		pubKeyAndIsLocked = append(pubKeyAndIsLocked, pk[:]...)
		if err := txn.Set(pubKeyAndIsLocked, lib.EncodeUint64(bal)); err != nil {
			return errors.Wrapf(err, "PutHypersyncBalanceSnapshotWithTxn: Error for isLockedPrefix: "+
				"%v pub key: %v balance: %v", isLockedPrefix, pk, bal)
		}
	}
	return nil
}

func (index *Index) PutHypersyncSingleBalanceSnapshotWithTxn(
	txn *badger.Txn, isLockedBalance bool, publicKey lib.PublicKey, balance uint64) error {

	isLockedPrefix := byte(0)
	if isLockedBalance {
		isLockedPrefix = byte(1)
	}

	pubKeyAndIsLocked := append([]byte{}, PrefixHypersyncGenesisBalanceSnapshot)
	pubKeyAndIsLocked = append(pubKeyAndIsLocked, isLockedPrefix)
	pubKeyAndIsLocked = append(pubKeyAndIsLocked, publicKey[:]...)
	if err := txn.Set(pubKeyAndIsLocked, lib.EncodeUint64(balance)); err != nil {
		return errors.Wrapf(err, "PutHypersyncSingleBalanceSnapshotWithTxn: Error for isLockedPrefix: "+
			"%v pub key: %v balance: %v", isLockedPrefix, publicKey, balance)
	}
	return nil
}

func (index *Index) PutBalanceSnapshot(
	height uint64, isLockedBalance bool, balances map[lib.PublicKey]uint64) error {

	return index.db.Update(func(txn *badger.Txn) error {
		return index.PutBalanceSnapshotWithTxn(txn, height, isLockedBalance, balances)
	})
}

func (index *Index) PutBalanceSnapshotWithTxn(
	txn *badger.Txn, height uint64, isLockedBalance bool, balances map[lib.PublicKey]uint64) error {

	for pk, bal := range balances {
		if err := txn.Set(balanceSnapshotKey(isLockedBalance, &pk, height, bal), []byte{}); err != nil {
			return errors.Wrapf(err, "Error in PutBalanceSnapshot for block height: "+
				"%v pub key: %v balance: %v", height, pk, bal)
		}
	}
	return nil
}

func (index *Index) PutSingleBalanceSnapshotWithTxn(
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

func (index *Index) GetBalanceSnapshot(isLockedBalance bool, publicKey *lib.PublicKey, blockHeight uint64) uint64 {
	balanceFound := uint64(0)
	index.db.View(func(txn *badger.Txn) error {
		balanceFound = GetBalanceForPublicKeyAtBlockHeightWithTxn(
			txn, isLockedBalance, publicKey, blockHeight)
		return nil
	})

	return balanceFound
}
