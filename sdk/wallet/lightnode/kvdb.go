//go:build wasm

package lightnode

import (
	"encoding/base64"
	"fmt"
	"strings"
	"syscall/js"

	"github.com/sat20-labs/sat20wallet/sdk/common"
)

type jsBatchWrite struct {
	db        *jsDB
	batch     map[string][]byte
	deletions []string
}

func (b *jsBatchWrite) Put(key, value []byte) error {
	keyStr := string(key)
	valueData := base64.StdEncoding.EncodeToString(value)
	b.batch[keyStr] = []byte(valueData)
	return nil
}

func (b *jsBatchWrite) Remove(key []byte) error {
	keyStr := string(key)
	b.deletions = append(b.deletions, keyStr)
	return nil
}

func (b *jsBatchWrite) Flush() error {
	for keyStr, value := range b.batch {
		b.db.db.Call("setItem", keyStr, string(value))
	}
	for _, keyStr := range b.deletions {
		b.db.db.Call("removeItem", keyStr)
	}
	return nil
}

func (b *jsBatchWrite) Close() {
	// Clear the batch data
	b.batch = nil
	b.deletions = nil
}

type jsDB struct {
	db        js.Value
	batch     map[string][]byte
	deletions []string
}

func openDB() (js.Value, error) {
	db := js.Global().Get("localStorage")
	if db.IsUndefined() {
		return js.Undefined(), fmt.Errorf("localStorage is not available")
	}
	return db, nil
}

func NewKVDB() common.KVDB {
	db, err := initDB()
	if err != nil {
		return nil
	}

	kvdb := jsDB{db: db}
	return &kvdb
}
func initDB() (js.Value, error) {
	return openDB()
}

func (p *jsDB) get(key []byte) ([]byte, error) {
	keyStr := string(key)
	value := p.db.Call("getItem", keyStr)
	if value.IsNull() {
		return nil, fmt.Errorf("key not found") // Key not found
	}
	valueData, err := base64.StdEncoding.DecodeString(value.String())
	if err != nil {
		return nil, err
	}
	return valueData, nil
}

func (p *jsDB) put(key, value []byte) error {
	keyStr := string(key)
	valueStr := base64.StdEncoding.EncodeToString(value)
	p.db.Call("setItem", keyStr, valueStr)
	return nil
}

func (p *jsDB) remove(key []byte) error {
	keyStr := string(key)
	p.db.Call("removeItem", keyStr)
	return nil
}

func (p *jsDB) commit() error {
	// localStorage is synchronous, so no commit is needed
	return nil
}

func (p *jsDB) Read(key []byte) ([]byte, error) {
	return p.get(key)
}

func (p *jsDB) Write(key, value []byte) error {
	err := p.put(key, value)
	if err != nil {
		return err
	}
	return p.commit()
}

func (p *jsDB) Delete(key []byte) error {
	err := p.remove(key)
	if err != nil {
		return err
	}
	return p.commit()
}

func (p *jsDB) NewBatchWrite() common.WriteBatch {
	return &jsBatchWrite{
		db:        p,
		batch:     make(map[string][]byte),
		deletions: make([]string, 0),
	}
}

func (p *jsDB) BatchRead(prefix []byte, r func(k, v []byte) error) error {
	prefixStr := string(prefix)
	localStorage := js.Global().Get("localStorage")
	length := localStorage.Get("length").Int()

	for i := 0; i < length; i++ {
		keyJS := localStorage.Call("key", i)
		key := keyJS.String()
		if strings.HasPrefix(key, prefixStr) {
			valueJS := localStorage.Call("getItem", key)
			if valueJS.IsNull() || valueJS.IsUndefined() {
				continue
			}
			decodedValue, err := base64.StdEncoding.DecodeString(valueJS.String())
			if err != nil {
				return err
			}
			decodedKey := string(key)
			if err := r([]byte(decodedKey), []byte(decodedValue)); err != nil {
				return err
			}
		}
	}
	return nil
}
