//go:build wasm

package lightnode

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"sync"
	"syscall/js"

	"github.com/sat20-labs/indexer/common"
)

var Log = common.Log

const indexedDBName = "sat20-wallet-sdk"
const indexedDBStore = "kv"

type jsDB struct {
	mu sync.Mutex
	db js.Value
}

// Pages and extension-owned contexts use the same transactional backend.
// Opening is lazy so storage failures can be returned through the KVDB methods.
func NewKVDB() common.KVDB {
	factory := js.Global().Get("indexedDB")
	if factory.IsUndefined() || factory.IsNull() {
		Log.Errorf("IndexedDB is unavailable")
		return nil
	}
	return &jsDB{db: js.Undefined()}
}

// JavaScript storage exceptions must not escape into the wallet as panics.
func storageCall(fn func() error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("IndexedDB: %v", recovered)
		}
	}()
	return fn()
}

func storageError(value js.Value, fallback string) error {
	if value.IsUndefined() || value.IsNull() {
		return fmt.Errorf("IndexedDB: %s", fallback)
	}
	if value.Type() == js.TypeObject {
		return fmt.Errorf("IndexedDB: %s: %s", value.Get("name").String(), value.Get("message").String())
	}
	return fmt.Errorf("IndexedDB: %s", value.String())
}

// Caller holds mu; all event callbacks remain alive until the open terminates.
func (p *jsDB) open() error {
	if !p.db.IsUndefined() {
		return nil
	}
	return storageCall(func() error {
		request := js.Global().Get("indexedDB").Call("open", indexedDBName, 1)
		done := make(chan error, 1)
		var upgradeErr error
		upgrade := js.FuncOf(func(_ js.Value, _ []js.Value) any {
			upgradeErr = storageCall(func() error {
				database := request.Get("result")
				if !database.Get("objectStoreNames").Call("contains", indexedDBStore).Bool() {
					database.Call("createObjectStore", indexedDBStore)
				}
				return nil
			})
			if upgradeErr != nil {
				_ = storageCall(func() error { request.Get("transaction").Call("abort"); return nil })
			}
			return nil
		})
		success := js.FuncOf(func(_ js.Value, _ []js.Value) any { p.db = request.Get("result"); done <- nil; return nil })
		failure := js.FuncOf(func(_ js.Value, _ []js.Value) any {
			err := upgradeErr
			if err == nil {
				err = storageError(request.Get("error"), "open failed")
			}
			done <- err
			return nil
		})
		defer upgrade.Release()
		defer success.Release()
		defer failure.Release()
		request.Set("onupgradeneeded", upgrade)
		request.Set("onsuccess", success)
		request.Set("onerror", failure)
		return <-done
	})
}

// Queue every request before yielding to the browser. The result is determined
// by transaction completion/abort, never by an individual request's success.
// User callbacks run after snapshot collection, outside the browser transaction.
func (p *jsDB) transaction(mode string, queue func(js.Value, func(js.Value, func(js.Value) error)) error) (err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err = p.open(); err != nil {
		return err
	}
	return storageCall(func() error {
		transaction := p.db.Call("transaction", indexedDBStore, mode)
		done := make(chan error, 1)
		var operationErr error
		var callbacks []js.Func
		complete := js.FuncOf(func(_ js.Value, _ []js.Value) any { done <- operationErr; return nil })
		aborted := js.FuncOf(func(_ js.Value, _ []js.Value) any {
			err := operationErr
			if err == nil {
				err = storageError(transaction.Get("error"), "transaction aborted")
			}
			done <- err
			return nil
		})
		failed := js.FuncOf(func(_ js.Value, args []js.Value) any {
			if operationErr == nil {
				operationErr = storageError(args[0].Get("target").Get("error"), "request failed")
			}
			return nil
		})
		defer func() {
			transaction.Set("oncomplete", js.Null())
			transaction.Set("onabort", js.Null())
			transaction.Set("onerror", js.Null())
			complete.Release()
			aborted.Release()
			failed.Release()
			for _, callback := range callbacks {
				callback.Release()
			}
		}()
		transaction.Set("oncomplete", complete)
		transaction.Set("onabort", aborted)
		transaction.Set("onerror", failed)
		observe := func(request js.Value, read func(js.Value) error) {
			callback := js.FuncOf(func(_ js.Value, _ []js.Value) any {
				if operationErr != nil {
					return nil
				}
				operationErr = storageCall(func() error { return read(request.Get("result")) })
				if operationErr != nil {
					_ = storageCall(func() error { transaction.Call("abort"); return nil })
				}
				return nil
			})
			callbacks = append(callbacks, callback)
			request.Set("onsuccess", callback)
		}
		operationErr = storageCall(func() error { return queue(transaction.Call("objectStore", indexedDBStore), observe) })
		if operationErr != nil {
			if abortErr := storageCall(func() error { transaction.Call("abort"); return nil }); abortErr != nil {
				return operationErr
			}
		}
		return <-done
	})
}

func (p *jsDB) Read(key []byte) (value []byte, err error) {
	missing := false
	err = p.transaction("readonly", func(store js.Value, observe func(js.Value, func(js.Value) error)) error {
		observe(store.Call("get", string(key)), func(result js.Value) error {
			if result.IsUndefined() {
				missing = true
				return nil
			}
			var err error
			value, err = base64.StdEncoding.DecodeString(result.String())
			return err
		})
		return nil
	})
	if err == nil && missing {
		err = common.ErrKeyNotFound
	}
	return
}

func (p *jsDB) Write(key, value []byte) error {
	return p.transaction("readwrite", func(store js.Value, _ func(js.Value, func(js.Value) error)) error {
		store.Call("put", base64.StdEncoding.EncodeToString(value), string(key))
		return nil
	})
}
func (p *jsDB) Delete(key []byte) error {
	return p.transaction("readwrite", func(store js.Value, _ func(js.Value, func(js.Value) error)) error {
		store.Call("delete", string(key))
		return nil
	})
}
func (p *jsDB) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.db.IsUndefined() {
		return nil
	}
	return storageCall(func() error { p.db.Call("close"); p.db = js.Undefined(); return nil })
}
func (p *jsDB) DropAll() error {
	return p.transaction("readwrite", func(store js.Value, _ func(js.Value, func(js.Value) error)) error { store.Call("clear"); return nil })
}
func (p *jsDB) DropPrefix(prefix []byte) error {
	return p.transaction("readwrite", func(store js.Value, observe func(js.Value, func(js.Value) error)) error {
		observe(store.Call("openCursor"), func(cursor js.Value) error {
			if cursor.IsNull() {
				return nil
			}
			if bytes.HasPrefix([]byte(cursor.Get("key").String()), prefix) {
				cursor.Call("delete")
			}
			cursor.Call("continue")
			return nil
		})
		return nil
	})
}

func (p *jsDB) snapshot(prefix []byte) (entries []scanEntry, err error) {
	err = p.transaction("readonly", func(store js.Value, observe func(js.Value, func(js.Value) error)) error {
		observe(store.Call("openCursor"), func(cursor js.Value) error {
			if cursor.IsNull() {
				return nil
			}
			key := []byte(cursor.Get("key").String())
			if bytes.HasPrefix(key, prefix) {
				value, err := base64.StdEncoding.DecodeString(cursor.Get("value").String())
				if err != nil {
					return err
				}
				entries = append(entries, scanEntry{key: key, value: value})
			}
			cursor.Call("continue")
			return nil
		})
		return nil
	})
	return
}
func (p *jsDB) Scan(options common.ScanOptions, r func(k, v []byte) error) error {
	entries, err := p.snapshot(options.Prefix)
	if err != nil {
		return err
	}
	return scanSnapshot(entries, options, r)
}
func (p *jsDB) BatchRead(prefix []byte, reverse bool, r func(k, v []byte) error) error {
	return p.Scan(common.ScanOptions{Prefix: prefix, Reverse: reverse}, r)
}
func (p *jsDB) BatchReadV2(prefix, seekKey []byte, reverse bool, r func(k, v []byte) error) error {
	return p.Scan(common.ScanOptions{Prefix: prefix, Start: seekKey, StartInclusive: true, Reverse: reverse}, r)
}

type jsReadBatch map[string][]byte

func (p jsReadBatch) Get(key []byte) ([]byte, error) {
	value, err := p.GetRef(key)
	if err != nil {
		return nil, err
	}
	return bytes.Clone(value), nil
}
func (p jsReadBatch) GetRef(key []byte) ([]byte, error) {
	value, ok := p[string(key)]
	if !ok {
		return nil, common.ErrKeyNotFound
	}
	return value, nil
}
func (p *jsDB) View(fn func(common.ReadBatch) error) error {
	entries, err := p.snapshot(nil)
	if err != nil {
		return err
	}
	snapshot := make(jsReadBatch, len(entries))
	for _, entry := range entries {
		snapshot[string(entry.key)] = entry.value
	}
	return fn(snapshot)
}

type jsBatchWrite struct {
	db        *jsDB
	batch     map[string]string
	deletions map[string]bool
}

func (p *jsDB) NewWriteBatch() common.WriteBatch {
	return &jsBatchWrite{db: p, batch: make(map[string]string), deletions: make(map[string]bool)}
}
func (b *jsBatchWrite) Put(key, value []byte) error {
	if b.batch == nil {
		return fmt.Errorf("IndexedDB: batch is closed")
	}
	b.batch[string(key)] = base64.StdEncoding.EncodeToString(value)
	delete(b.deletions, string(key))
	return nil
}
func (b *jsBatchWrite) Delete(key []byte) error {
	if b.batch == nil {
		return fmt.Errorf("IndexedDB: batch is closed")
	}
	delete(b.batch, string(key))
	b.deletions[string(key)] = true
	return nil
}
func (b *jsBatchWrite) Flush() error {
	if b.batch == nil {
		return fmt.Errorf("IndexedDB: batch is closed")
	}
	return b.db.transaction("readwrite", func(store js.Value, _ func(js.Value, func(js.Value) error)) error {
		for key := range b.deletions {
			store.Call("delete", key)
		}
		for key, value := range b.batch {
			store.Call("put", value, key)
		}
		return nil
	})
}
func (b *jsBatchWrite) Close() { b.batch = nil; b.deletions = nil }
