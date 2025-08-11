//go:build !wasm

package supernode

// import (
// 	"bytes"
// 	"encoding/gob"
// 	"fmt"
// 	"io"
// 	"os"

// 	"github.com/dgraph-io/badger/v4"
// 	indexer "github.com/sat20-labs/indexer/common"
// 	"github.com/sat20-labs/sat20wallet/sdk/common"
// )

// type kvDB struct {
// 	path string
// 	db   *badger.DB
// }

// var Log = indexer.Log

// func runBadgerGC(db *badger.DB) {
// 	if db.IsClosed() {
// 		return
// 	}

// 	for {
// 		err := db.RunValueLogGC(0.5)
// 		if err == badger.ErrNoRewrite {
// 			break
// 		} else if err != nil {
// 			break
// 		}
// 	}
// 	db.Sync()
// }


// func openDB(filepath string, opts badger.Options) (db *badger.DB, err error) {
// 	opts = opts.WithDir(filepath).WithValueDir(filepath).WithLoggingLevel(badger.WARNING)
// 	db, err = badger.Open(opts)
// 	if err != nil {
// 		return nil, err
// 	}
// 	runBadgerGC(db)
// 	return db, nil
// }

// func NewKVDB(path string) common.KVDB {
// 	db, err := initDB(path)
// 	if err != nil {
// 		return nil
// 	}
	

// 	kvdb := kvDB{path:path, db:db}
// 	kvdb.BackupToFile("./db_open.bk")
// 	kvdb.CompareWithBackupFile("./db_close.bk")
// 	return &kvdb
// }

// func initDB(path string) (*badger.DB, error) {
// 	if path == "" {
// 		path = "./data/db"
// 	}
// 	opts := badger.DefaultOptions("").WithBlockCacheSize(300 << 20)
// 	return openDB(path, opts)
// }

// func (p *kvDB) get(key []byte) ([]byte, error) {
// 	var ret []byte
// 	err := p.db.View(func(txn *badger.Txn) error {
// 		item, err := txn.Get(key)
// 		if err != nil {
// 			return err
// 		}
// 		return item.Value(func(val []byte) error {
// 			ret = append([]byte{}, val...)
// 			return nil
// 		})
// 	})

// 	return ret, err
// }

// func (p *kvDB) put(key, value []byte) error {
// 	err := p.db.Update(func(txn *badger.Txn) error {
// 		return txn.Set(key, value)
// 	})
// 	return err
// }

// func (p *kvDB) remove(key []byte) error {
// 	err :=p.db.Update(func(txn *badger.Txn) error {
// 		return txn.Delete(key)
// 	})
// 	return err
// }

// func (p *kvDB) close() error {
// 	return p.db.Close()
// }

// func (p *kvDB) commit() error {
// 	return nil
// }

// func (p *kvDB) Read(key []byte) ([]byte, error) {
// 	return p.get(key)
// }

// func (p *kvDB) Write(key, value []byte) error {
// 	err := p.put(key, value)
// 	if err != nil {
// 		return err
// 	}
// 	return p.commit()
// }

// func (p *kvDB) Delete(key []byte) error {
// 	err := p.remove(key)
// 	if err != nil {
// 		return err
// 	}
// 	return p.commit()
// }


// func (p *kvDB) DropPrefix(prefix []byte) error {
// 	deletingKeyMap := make(map[string]bool)
// 	err := p.BatchRead(prefix, false, func(k, v []byte) error {
// 		deletingKeyMap[string(k)] = true
// 		return nil
// 	})
// 	if err != nil {
// 		return err
// 	}
// 	wb := p.NewWriteBatch()
// 	defer wb.Close()

// 	for k := range deletingKeyMap {
// 		wb.Delete([]byte(k))
// 	}
// 	return wb.Flush()
// }

// func (p *kvDB) DropAll() error {
// 	deletingKeyMap := make(map[string]bool)
// 	err := p.BatchRead(nil, false, func(k, v []byte) error {
// 		deletingKeyMap[string(k)] = true
// 		return nil
// 	})
// 	if err != nil {
// 		return err
// 	}
// 	wb := p.NewWriteBatch()
// 	defer wb.Close()

// 	for k := range deletingKeyMap {
// 		wb.Delete([]byte(k))
// 	}
// 	return wb.Flush()
// }


// func (p *kvDB) Close() error {
// 	p.BackupToFile("./db_close.bk")
// 	return p.close()
// }

// func (p *kvDB) BatchRead(prefix []byte, reverse bool, r func(k, v []byte) error) error {
// 	// 从数据库中读出所有key带有prefix前缀的value，调用r会调处理
// 	// 默认从小到大排序 
	
// 	err := p.db.View(func(txn *badger.Txn) error {
// 		opts := badger.DefaultIteratorOptions
// 		opts.PrefetchValues = true
// 		opts.Reverse = reverse
// 		it := txn.NewIterator(opts)
// 		defer it.Close()

// 		if reverse {
// 			// 重点：倒序时从 prefix 区间的“末尾”起始
// 			it.Seek(append(prefix, 0xFF))
// 		} else {
// 			// 正序：从 prefix 开始
// 			it.Seek(prefix)
// 		}

// 		var err error
// 		for ; it.ValidForPrefix([]byte(prefix)); it.Next() {
// 			item := it.Item()

// 			if item.IsDeletedOrExpired() {
// 				continue
// 			}
			
// 			err = item.Value(func(data []byte) error {
// 				return r(item.Key(), data)
// 			})
// 			if err != nil {
// 				break
// 			}
// 		}
// 		return err
// 	})

// 	return err
// }


// func (p *kvDB) BatchReadV2(prefix, seekKey []byte, reverse bool, r func(k, v []byte) error) error {
// 	// 从数据库中读出所有key带有prefix前缀的value，调用r会调处理
// 	// 默认从小到大排序 
	
// 	err := p.db.View(func(txn *badger.Txn) error {
// 		opts := badger.DefaultIteratorOptions
// 		opts.PrefetchValues = true
// 		opts.Reverse = reverse
// 		it := txn.NewIterator(opts)
// 		defer it.Close()

// 		if reverse {
//             // 倒序：从 seekKey 开始向前遍历
//             if len(seekKey) > 0 {
//                 it.Seek(seekKey)
//             } else {
//                 it.Seek(append(prefix, 0xFF))
//             }
//         } else {
//             // 正序：从 seekKey 或 prefix 开始
//             if len(seekKey) > 0 {
//                 it.Seek(seekKey)
//             } else {
//                 it.Seek(prefix)
//             }
//         }

// 		var err error
// 		for ; it.ValidForPrefix([]byte(prefix)); it.Next() {
// 			item := it.Item()

// 			if item.IsDeletedOrExpired() {
// 				continue
// 			}
			
// 			err = item.Value(func(data []byte) error {
// 				return r(item.Key(), data)
// 			})
// 			if err != nil {
// 				break
// 			}
// 		}
// 		return err
// 	})

// 	return err
// }

// type kvWriteBatch struct {
// 	wb *badger.WriteBatch
// }

// func (p *kvWriteBatch)Put(key, value []byte) error {
// 	return p.wb.Set(key, value)
// }

// func (p *kvWriteBatch) Delete(key []byte) error {
// 	return p.wb.Delete(key)
// }

// func (p *kvWriteBatch) Flush() error {
// 	return p.wb.Flush()
// }

// func (p *kvWriteBatch) Close() {
// 	p.wb.Cancel()
// }

// func (p *kvDB) NewWriteBatch() common.WriteBatch {
// 	wb := p.db.NewWriteBatch()
// 	return &kvWriteBatch{wb:wb}
// }


// func (p *kvDB) BackupToFile(fname string) error {
// 	f, err := os.Create(fname)
// 	if err != nil {
// 		return err
// 	}
// 	defer f.Close()
// 	enc := gob.NewEncoder(f)
// 	total := 0
// 	err = p.BatchRead(nil, false, func(k, v []byte) error {
// 		total++
// 		return enc.Encode([2][]byte{k, v})
// 	})

// 	if err != nil {
// 		Log.Errorf("BackupToFile %s failed, %v", fname, err)
// 		return err
// 	}

// 	Log.Infof("BackupToFile %s succeed, total %d", fname, total)

// 	return err
// }

// func (p *kvDB) RestoreFromFile(backupFile string) error {
// 	f, err := os.Open(backupFile)
// 	if err != nil {
// 		return err
// 	}
// 	defer f.Close()
// 	dec := gob.NewDecoder(f)
// 	for {
// 		var kv [2][]byte
// 		if err := dec.Decode(&kv); err != nil {
// 			if err == io.EOF {
// 				break
// 			}
// 			return err
// 		}
// 		if err := p.put(kv[0], kv[1]); err != nil {
// 			return err
// 		}
// 	}
// 	return nil
// }


// func (p *kvDB) CompareWithBackupFile(backupFile string) error {
// 	f, err := os.Open(backupFile)
// 	if err != nil {
// 		return err
// 	}
// 	defer f.Close()

// 	itemsInFile := make(map[string][]byte)
// 	dec := gob.NewDecoder(f)
// 	for {
// 		var kv [2][]byte
// 		if err := dec.Decode(&kv); err != nil {
// 			if err == io.EOF {
// 				break
// 			}
// 			return err
// 		}
// 		itemsInFile[string(kv[0])] = append([]byte{}, kv[1]...)
// 	}

// 	itemsInDB := make(map[string][]byte)
// 	p.BatchRead(nil, false, func(k, v []byte) error {
// 		itemsInDB[string(k)] = append([]byte{}, v...)
// 		return nil
// 	})

// 	if len(itemsInFile) != len(itemsInDB) {
// 		Log.Errorf("count different %d %d", len(itemsInFile), len(itemsInDB))
// 		return fmt.Errorf("count different %d %d", len(itemsInFile), len(itemsInDB))
// 	}

// 	succ := true
// 	for k, v := range itemsInFile {
// 		v2, ok := itemsInDB[k]
// 		if !ok {
// 			Log.Errorf("can't find key %s in db", k)
// 		} else if !bytes.Equal(v, v2) {
// 			Log.Errorf("key %s value different", k)
// 			succ = false
// 		}
// 	}

// 	for k, v := range itemsInDB {
// 		v2, ok := itemsInFile[k]
// 		if !ok {
// 			Log.Errorf("can't find key %s in file", k)
// 		} else if !bytes.Equal(v, v2) {
// 			Log.Errorf("key %s value different", k)
// 			succ = false
// 		}
// 	}

// 	if succ {
// 		Log.Infof("db file check succeed")
// 	} else {
// 		Log.Infof("db file check failed")
// 	}
	

// 	return nil
// }
