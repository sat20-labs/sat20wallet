//go:build wasm

package lightnode

import (
	"encoding/base64"
	"fmt"
	"syscall/js"

	"strings"

	"github.com/sat20-labs/indexer/common"
)

var Log = common.Log

type jsBatchWrite struct {
	db        *jsDB
	batch     map[string]string
	deletions []string
}

func (b *jsBatchWrite) Put(key, value []byte) error {
	keyStr := string(key)
	valueData := base64.StdEncoding.EncodeToString(value)
	b.batch[keyStr] = valueData
	return nil
}

func (b *jsBatchWrite) Delete(key []byte) error {
	keyStr := string(key)
	b.deletions = append(b.deletions, keyStr)
	return nil
}

func (b *jsBatchWrite) Flush() error {
	if b.db.isExtension {
		b.db.putBatch_Chrome(b.batch)
		b.db.removeBatch_Chrome(b.deletions)
	} else {
		for keyStr, value := range b.batch {
			b.db.db.Call("setItem", keyStr, string(value))
		}
		for _, keyStr := range b.deletions {
			b.db.db.Call("removeItem", keyStr)
		}
	}

	return nil
}

func (b *jsBatchWrite) Close() {
	// Clear the batch data
	b.batch = nil
	b.deletions = nil
}

type jsDB struct {
	db          js.Value
	isExtension bool
	batch       map[string][]byte
}

// 页面模式下使用 localStorage ，插件模式下使用 chrome.storage.local
func NewKVDB() common.KVDB {
	var store js.Value
	isExtension := false

	// 检查是否在插件环境下
	chrome := js.Global().Get("chrome")
	if !chrome.IsUndefined() {
		storage := chrome.Get("storage")
		if !storage.IsUndefined() {
			local := storage.Get("local")
			if !local.IsUndefined() {
				store = local
				isExtension = true
			}
		}
	}

	// 如果不是插件环境，则尝试页面环境 localStorage
	if store.IsUndefined() {
		localStorage := js.Global().Get("localStorage")
		if !localStorage.IsUndefined() {
			store = localStorage
			isExtension = false
		}
	}

	// 两种环境都不可用时，报错
	if store.IsUndefined() {
		Log.Errorf("No suitable storage API is available (neither chrome.storage.local nor localStorage)")
		return nil
	}

	kvdb := jsDB{
		db:          store,
		isExtension: isExtension,
	}
	return &kvdb
}

func (p *jsDB) get(key []byte) ([]byte, error) {
	keyStr := string(key)
	var value js.Value
	if p.isExtension {
		executor := js.FuncOf(func(this js.Value, args []js.Value) interface{} {
			resolve := args[0]
			reject := args[1]

			var cb js.Func
			cb = js.FuncOf(func(this js.Value, args []js.Value) interface{} {
				// 在回调执行时释放 cb，避免提前释放的问题
				cb.Release()
				if err := js.Global().Get("chrome").Get("runtime").Get("lastError"); !err.IsUndefined() {
					reject.Invoke(err.Get("message").String())
					return nil
				}

				// args[0] 是 result object
				result := args[0]
				if result.IsUndefined() || result.Get(keyStr).IsUndefined() {
					reject.Invoke(common.ErrKeyNotFound.Error())
					return nil
				}

				resolve.Invoke(result.Get(keyStr))
				return nil
			})

			// 调用 chrome.storage.local.get（异步）
			p.db.Call("get", keyStr, cb)

			return nil
		})
		// executor 会在 Promise 构造时被同步调用，所以这里可以在 New 之后释放 executor
		getPromise := js.Global().Get("Promise").New(executor)
		executor.Release()

		// 等待 Promise 完成
		value = await(getPromise)
		if value.IsUndefined() {
			return nil, common.ErrKeyNotFound
		}

	} else {
		value = p.db.Call("getItem", keyStr)
		if value.IsNull() {
			return nil, common.ErrKeyNotFound // Key not found
		}
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

	if p.isExtension {
		// 创建存储对象
		data := make(map[string]interface{})
		data[keyStr] = valueStr

		executor := js.FuncOf(func(this js.Value, args []js.Value) interface{} {
			resolve := args[0]
			reject := args[1]

			var cb js.Func
			cb = js.FuncOf(func(this js.Value, args []js.Value) interface{} {
				// 在回调里释放 cb
				cb.Release()
				if err := js.Global().Get("chrome").Get("runtime").Get("lastError"); !err.IsUndefined() {
					reject.Invoke(err.Get("message").String())
					return nil
				}

				resolve.Invoke(nil)
				return nil
			})

			// 调用 chrome.storage.local.set
			p.db.Call("set", data, cb)

			return nil
		})
		setPromise := js.Global().Get("Promise").New(executor)
		executor.Release()

		// 等待 Promise 完成
		await(setPromise)

	} else {
		p.db.Call("setItem", keyStr, valueStr)
	}

	return nil
}

func (p *jsDB) remove(key []byte) error {
	keyStr := string(key)
	if p.isExtension {
		executor := js.FuncOf(func(this js.Value, args []js.Value) interface{} {
			resolve := args[0]
			reject := args[1]

			var cb js.Func
			cb = js.FuncOf(func(this js.Value, args []js.Value) interface{} {
				// 在回调里释放 cb
				cb.Release()
				if err := js.Global().Get("chrome").Get("runtime").Get("lastError"); !err.IsUndefined() {
					reject.Invoke(err.Get("message").String())
					return nil
				}

				resolve.Invoke(nil)
				return nil
			})

			// 调用 chrome.storage.local.remove
			p.db.Call("remove", keyStr, cb)

			return nil
		})
		removePromise := js.Global().Get("Promise").New(executor)
		executor.Release()

		// 等待 Promise 完成
		await(removePromise)
	} else {
		p.db.Call("removeItem", keyStr)
	}

	return nil
}

// 辅助函数：等待 Promise 完成
func await(promise js.Value) js.Value {
	done := make(chan js.Value)
	var success js.Value

	var thenCb js.Func
	thenCb = js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		// thenCb 会在这里被调用，发送结果，然后释放自身
		thenCb.Release()
		if len(args) > 0 {
			success = args[0]
		} else {
			success = js.Undefined()
		}
		done <- success
		return nil
	})

	var catchCb js.Func
	catchCb = js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		// catchCb 会在这里被调用，发送 reject 的值（如果有），然后释放自身
		catchCb.Release()
		if len(args) > 0 {
			done <- args[0]
		} else {
			done <- js.Undefined()
		}
		return nil
	})

	// 注意：thenCb/catchCb 在被调用时会释放自身，避免提前释放导致 "call to released function"
	promise.Call("then", thenCb, catchCb)

	return <-done
}

func (p *jsDB) commit() error {
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

func (p *jsDB) Close() error {
	return nil
}

func (p *jsDB) DropPrefix(prefix []byte) error {
	deletingKeyMap := make(map[string]bool)
	err := p.BatchRead(prefix, false, func(k, v []byte) error {
		deletingKeyMap[string(k)] = true
		return nil
	})
	if err != nil {
		return err
	}
	wb := p.NewWriteBatch()
	defer wb.Close()

	for k := range deletingKeyMap {
		wb.Delete([]byte(k))
	}
	return wb.Flush()
}

func (p *jsDB) DropAll() error {
	deletingKeyMap := make(map[string]bool)
	err := p.BatchRead(nil, false, func(k, v []byte) error {
		deletingKeyMap[string(k)] = true
		return nil
	})
	if err != nil {
		return err
	}
	wb := p.NewWriteBatch()
	defer wb.Close()

	for k := range deletingKeyMap {
		wb.Delete([]byte(k))
	}
	return wb.Flush()
}

func (p *jsDB) NewWriteBatch() common.WriteBatch {
	return &jsBatchWrite{
		db:        p,
		batch:     make(map[string]string),
		deletions: make([]string, 0),
	}
}

func (p *jsDB) SetReverse(bool) {
}

func (p *jsDB) BatchRead(prefix []byte, reverse bool, r func(k, v []byte) error) error {
	prefixStr := string(prefix)
	if p.isExtension {
		executor := js.FuncOf(func(this js.Value, args []js.Value) interface{} {
			resolve := args[0]
			reject := args[1]

			var cb js.Func
			cb = js.FuncOf(func(this js.Value, args []js.Value) interface{} {
				// 回调里释放 cb
				cb.Release()
				if err := js.Global().Get("chrome").Get("runtime").Get("lastError"); !err.IsUndefined() {
					reject.Invoke(err.Get("message").String())
					return nil
				}

				result := args[0]
				if result.IsUndefined() {
					reject.Invoke("storage is empty")
					return nil
				}

				// 解析存储的数据
				resolve.Invoke(result)
				return nil
			})

			// 获取所有存储的数据
			p.db.Call("get", js.Null(), cb)

			return nil
		})
		getPromise := js.Global().Get("Promise").New(executor)
		executor.Release()

		// 等待 Promise 完成
		value := await(getPromise)
		if value.IsUndefined() {
			return fmt.Errorf("failed to fetch storage data")
		}

		// 遍历存储数据，筛选匹配前缀的键值
		keys := js.Global().Get("Object").Call("keys", value)
		for i := 0; i < keys.Length(); i++ {
			key := keys.Index(i).String()
			if strings.HasPrefix(key, prefixStr) {
				rawValue := value.Get(key).String()

				// 解码 base64 数据
				decodedValue, err := base64.StdEncoding.DecodeString(rawValue)
				if err != nil {
					return fmt.Errorf("failed to decode value for key %s: %w", key, err)
				}

				// 调用回调函数 `r`
				if err := r([]byte(key), decodedValue); err != nil {
					return err
				}
			}
		}
	} else {
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
				if err := r([]byte(key), decodedValue); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

func (p *jsDB) BatchReadV2(prefix, seekKey []byte, reverse bool, r func(k, v []byte) error) error {
	return fmt.Errorf("not implementd")
}

// 可选：添加批量操作方法
func (p *jsDB) putBatch_Chrome(entries map[string]string) error {
	data := make(map[string]interface{})
	for key, value := range entries {
		data[key] = value
	}

	executor := js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		resolve := args[0]
		reject := args[1]

		var cb js.Func
		cb = js.FuncOf(func(this js.Value, args []js.Value) interface{} {
			// 回调里释放 cb
			cb.Release()
			if err := js.Global().Get("chrome").Get("runtime").Get("lastError"); !err.IsUndefined() {
				reject.Invoke(err.Get("message").String())
				return nil
			}
			resolve.Invoke(nil)
			return nil
		})

		p.db.Call("set", data, cb)

		return nil
	})
	setPromise := js.Global().Get("Promise").New(executor)
	executor.Release()

	await(setPromise)
	return nil
}

type jsReadBatch struct {
	db *jsDB
}

func (p *jsReadBatch) Get(key []byte) ([]byte, error) {
	return p.db.Read(key)
}

func (p *jsReadBatch) GetRef(key []byte) ([]byte, error) {
	return p.db.Read(key)
}

// View 在一致性快照中执行只读操作
func (p *jsDB) View(fn func(txn common.ReadBatch) error) error {
	rb := jsReadBatch{
		db: p,
	}

	return fn(&rb)
}

func (p *jsDB) removeBatch_Chrome(entries []string) error {

	data := make([]interface{}, 0)
	for _, value := range entries {
		data = append(data, value)
	}

	executor := js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		resolve := args[0]
		reject := args[1]

		var cb js.Func
		cb = js.FuncOf(func(this js.Value, args []js.Value) interface{} {
			// 回调里释放 cb
			cb.Release()
			if err := js.Global().Get("chrome").Get("runtime").Get("lastError"); !err.IsUndefined() {
				reject.Invoke(err.Get("message").String())
				return nil
			}
			resolve.Invoke(nil)
			return nil
		})

		p.db.Call("remove", data, cb)

		return nil
	})
	setPromise := js.Global().Get("Promise").New(executor)
	executor.Release()

	await(setPromise)
	return nil
}
