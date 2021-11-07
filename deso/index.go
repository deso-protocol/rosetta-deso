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
	PrefixBalanceSnapshots = byte(1)

	// Creator coins
	PrefixLockedBalanceSnapshots = byte(2)
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

func (index *Index) PutBalanceSnapshot(
	block *lib.MsgDeSoBlock, isLockedBalance bool, balances map[lib.PublicKey]uint64) error {

	height := block.Header.Height
	return index.db.Update(func(txn *badger.Txn) error {
		for pk, bal := range balances {
			if err := txn.Set(balanceSnapshotKey(isLockedBalance, &pk, height, bal), []byte{}); err != nil {
				return errors.Wrapf(err, "Error in PutBalanceSnapshot for block height: " +
					"%v pub key: %v balance: %v", height, pk, bal)
			}
		}
		return nil
	})
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

func (index *Index) GetBalanceSnapshot(isLockedBalance bool, publicKey *lib.PublicKey, blockHeight uint64) (uint64) {
	balanceFound := uint64(0)
	index.db.View(func(txn *badger.Txn) error {
		balanceFound = GetBalanceForPublicKeyAtBlockHeightWithTxn(
			txn, isLockedBalance, publicKey, blockHeight)
		return nil
	})

	return balanceFound
}
