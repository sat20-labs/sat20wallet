//go:build !wasm

package supernode

import (
	"github.com/dgraph-io/badger/v4"
	"github.com/sat20-labs/sat20wallet/sdk/common"
)

type kvDB struct {
	path string
	db   *badger.DB
}

func runBadgerGC(db *badger.DB) {
	if db.IsClosed() {
		return
	}

	for {
		err := db.RunValueLogGC(0.5)
		if err == badger.ErrNoRewrite {
			break
		} else if err != nil {
			break
		}
	}
	db.Sync()
}


func openDB(filepath string, opts badger.Options) (db *badger.DB, err error) {
	opts = opts.WithDir(filepath).WithValueDir(filepath).WithLoggingLevel(badger.WARNING)
	db, err = badger.Open(opts)
	if err != nil {
		return nil, err
	}
	runBadgerGC(db)
	return db, nil
}

func NewKVDB(path string) common.KVDB {
	db, err := initDB(path)
	if err != nil {
		return nil
	}

	kvdb := kvDB{path:path, db:db}
	return &kvdb
}

func initDB(path string) (*badger.DB, error) {
	if path == "" {
		path = "./data/db"
	}
	opts := badger.DefaultOptions("").WithBlockCacheSize(300 << 20)
	return openDB(path, opts)
}

func (p *kvDB) get(key []byte) ([]byte, error) {
	var ret []byte
	err := p.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(key)
		if err != nil {
			return err
		}
		return item.Value(func(val []byte) error {
			ret = append([]byte{}, val...)
			return nil
		})
	})

	return ret, err
}

func (p *kvDB) put(key, value []byte) error {
	err := p.db.Update(func(txn *badger.Txn) error {
		return txn.Set(key, value)
	})
	return err
}

func (p *kvDB) remove(key []byte) error {
	err :=p.db.Update(func(txn *badger.Txn) error {
		return txn.Delete(key)
	})
	return err
}

func (p *kvDB) commit() error {
	return nil
}

func (p *kvDB) Read(key []byte) ([]byte, error) {
	return p.get(key)
}

func (p *kvDB) Write(key, value []byte) error {
	err := p.put(key, value)
	if err != nil {
		return err
	}
	return p.commit()
}

func (p *kvDB) Delete(key []byte) error {
	err := p.remove(key)
	if err != nil {
		return err
	}
	return p.commit()
}

func (p *kvDB) BatchRead(prefix []byte, r func(k, v []byte) error) error {
	// 从数据库中读出所有key带有prefix前缀的value，调用r会调处理
	
	err := p.db.View(func(txn *badger.Txn) error {
		it := txn.NewIterator(badger.DefaultIteratorOptions)
		defer it.Close()

		var err error
		for it.Seek([]byte(prefix)); it.ValidForPrefix([]byte(prefix)); it.Next() {
			item := it.Item()

			if item.IsDeletedOrExpired() {
				continue
			}
			
			err = item.Value(func(data []byte) error {
				return r(item.Key(), data)
			})
			if err != nil {
				break
			}
		}
		return err
	})

	return err
}

type kvWriteBatch struct {
	wb *badger.WriteBatch
}

func (p *kvWriteBatch)Put(key, value []byte) error {
	return p.wb.Set(key, value)
}

func (p *kvWriteBatch) Remove(key []byte) error {
	return p.wb.Delete(key)
}

func (p *kvWriteBatch) Flush() error {
	return p.wb.Flush()
}

func (p *kvWriteBatch) Close() {
	p.wb.Cancel()
}

func (p *kvDB) NewBatchWrite() common.WriteBatch {
	wb := p.db.NewWriteBatch()
	return &kvWriteBatch{wb:wb}
}

