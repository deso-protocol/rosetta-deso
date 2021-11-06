package deso

import (
	"bytes"
	"encoding/gob"
	"github.com/deso-protocol/core/lib"
	"github.com/dgraph-io/badger/v3"
)

const (
	PrefixSpentUtxos = byte(0)

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
// Spent UTXOs
//

func (index *Index) spentUtxosKey(blockHash *lib.BlockHash) []byte {
	prefix := append([]byte{}, PrefixSpentUtxos)
	prefix = append(prefix, blockHash.ToBytes()...)
	return prefix
}

func (index *Index) PutSpentUtxos(block *lib.MsgDeSoBlock, spentUtxos map[lib.UtxoKey]uint64) error {
	blockHash, err := block.Hash()
	if err != nil {
		return err
	}

	buf := bytes.NewBuffer([]byte{})
	err = gob.NewEncoder(buf).Encode(spentUtxos)
	if err != nil {
		return err
	}

	return index.db.Update(func(txn *badger.Txn) error {
		return txn.Set(index.spentUtxosKey(blockHash), buf.Bytes())
	})
}

func (index *Index) GetSpentUtxos(block *lib.MsgDeSoBlock) (map[lib.UtxoKey]uint64, error) {
	blockHash, err := block.Hash()
	if err != nil {
		return nil, err
	}

	var spentUtxos map[lib.UtxoKey]uint64

	err = index.db.View(func(txn *badger.Txn) error {
		spentUtxosItem, err := txn.Get(index.spentUtxosKey(blockHash))
		if err != nil {
			return err
		}

		return spentUtxosItem.Value(func(valBytes []byte) error {
			return gob.NewDecoder(bytes.NewReader(valBytes)).Decode(&spentUtxos)
		})
	})
	if err != nil {
		return nil, err
	}

	return spentUtxos, nil
}

//
// Balance Snapshots
//

func (index *Index) balanceSnapshotKey(blockHash *lib.BlockHash) []byte {
	prefix := append([]byte{}, PrefixBalanceSnapshots)
	prefix = append(prefix, blockHash.ToBytes()...)
	return prefix
}

func (index *Index) PutBalanceSnapshot(block *lib.MsgDeSoBlock, balances map[lib.PublicKey]uint64) error {
	blockHash, err := block.Hash()
	if err != nil {
		return err
	}

	buf := bytes.NewBuffer([]byte{})
	err = gob.NewEncoder(buf).Encode(balances)
	if err != nil {
		return err
	}

	return index.db.Update(func(txn *badger.Txn) error {
		return txn.Set(index.balanceSnapshotKey(blockHash), buf.Bytes())
	})
}

func (index *Index) GetBalanceSnapshot(block *lib.MsgDeSoBlock) (map[lib.PublicKey]uint64, error) {
	blockHash, err := block.Hash()
	if err != nil {
		return nil, err
	}

	var balances map[lib.PublicKey]uint64

	err = index.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(index.balanceSnapshotKey(blockHash))
		if err != nil {
			return err
		}

		return item.Value(func(valBytes []byte) error {
			return gob.NewDecoder(bytes.NewReader(valBytes)).Decode(&balances)
		})
	})
	if err != nil {
		return nil, err
	}

	return balances, nil
}

//
// Lobkec Balance Snapshots (Creator Coins)
//

func (index *Index) lockedBalanceSnapshotKey(blockHash *lib.BlockHash) []byte {
	prefix := append([]byte{}, PrefixLockedBalanceSnapshots)
	prefix = append(prefix, blockHash.ToBytes()...)
	return prefix
}

func (index *Index) PutLockedBalanceSnapshot(block *lib.MsgDeSoBlock, lockedBalances map[lib.PublicKey]uint64) error {
	blockHash, err := block.Hash()
	if err != nil {
		return err
	}

	buf := bytes.NewBuffer([]byte{})
	err = gob.NewEncoder(buf).Encode(lockedBalances)
	if err != nil {
		return err
	}

	return index.db.Update(func(txn *badger.Txn) error {
		return txn.Set(index.lockedBalanceSnapshotKey(blockHash), buf.Bytes())
	})
}

func (index *Index) GetLockedBalanceSnapshot(block *lib.MsgDeSoBlock) (map[lib.PublicKey]uint64, error) {
	blockHash, err := block.Hash()
	if err != nil {
		return nil, err
	}

	var lockedBalances map[lib.PublicKey]uint64

	err = index.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(index.lockedBalanceSnapshotKey(blockHash))
		if err != nil {
			return err
		}

		return item.Value(func(valBytes []byte) error {
			return gob.NewDecoder(bytes.NewReader(valBytes)).Decode(&lockedBalances)
		})
	})
	if err != nil {
		return nil, err
	}

	return lockedBalances, nil
}
