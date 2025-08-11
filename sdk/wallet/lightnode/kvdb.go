//go:build wasm

package lightnode

import (
	"encoding/base64"
	"fmt"
	"syscall/js"

	"strings"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/common"
)

var Log = indexer.Log

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
		b.db.removeBatch_Chrome(b.db.deletions)
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
	db        js.Value
	isExtension bool
	batch     map[string][]byte
	deletions []string
}

// 页面模式下使用 localStorage ，插件模式下使用 chrome.storage.local
func NewKVDB() common.KVDB {
	isExtension := false
	var db js.Value
	// 页面加载只支持localStorage，以后要支持页面加载的话，需要加配置项来支持
	// db := js.Global().Get("localStorage")
	// if db.IsUndefined() {
		chrome := js.Global().Get("chrome")
		if chrome.IsUndefined() {
			Log.Errorf("chrome API is not available")
			return nil
		}
		
		db = chrome.Get("storage").Get("local")
		if db.IsUndefined() {
			Log.Errorf("chrome.storage.local is not available")
			return nil
		}
		isExtension = true
	//}

	kvdb := jsDB{
		db: db,
		isExtension: isExtension,
	}
	return &kvdb
}

func (p *jsDB) get(key []byte) ([]byte, error) {
	keyStr := string(key)
	var value js.Value
	if p.isExtension {
		 // 创建 Promise 来处理异步调用
		 getPromise := js.Global().Get("Promise").New(js.FuncOf(func(this js.Value, args []js.Value) interface{} {
			resolve := args[0]
			reject := args[1]
			
			// 调用 chrome.storage.local.get
			p.db.Call("get", keyStr, js.FuncOf(func(this js.Value, args []js.Value) interface{} {
				if err := js.Global().Get("chrome").Get("runtime").Get("lastError"); !err.IsUndefined() {
					reject.Invoke(err.Get("message").String())
					return nil
				}
				
				result := args[0]
				if result.IsUndefined() || result.Get(keyStr).IsUndefined() {
					reject.Invoke("key not found")
					return nil
				}
				
				resolve.Invoke(result.Get(keyStr))
				return nil
			}))
			
			return nil
		}))
		
		// 等待 Promise 完成
		value = await(getPromise)
		if value.IsUndefined() {
			return nil, fmt.Errorf("key not found")
		}

	} else {
		value = p.db.Call("getItem", keyStr)
		if value.IsNull() {
			return nil, fmt.Errorf("key not found") // Key not found
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
		
		// 创建 Promise 来处理异步调用
		setPromise := js.Global().Get("Promise").New(js.FuncOf(func(this js.Value, args []js.Value) interface{} {
			resolve := args[0]
			reject := args[1]
			
			// 调用 chrome.storage.local.set
			p.db.Call("set", data, js.FuncOf(func(this js.Value, args []js.Value) interface{} {
				if err := js.Global().Get("chrome").Get("runtime").Get("lastError"); !err.IsUndefined() {
					reject.Invoke(err.Get("message").String())
					return nil
				}
				
				resolve.Invoke(nil)
				return nil
			}))
			
			return nil
		}))
		
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
		// 创建 Promise 来处理异步调用
		removePromise := js.Global().Get("Promise").New(js.FuncOf(func(this js.Value, args []js.Value) interface{} {
			resolve := args[0]
			reject := args[1]
			
			// 调用 chrome.storage.local.remove
			p.db.Call("remove", keyStr, js.FuncOf(func(this js.Value, args []js.Value) interface{} {
				if err := js.Global().Get("chrome").Get("runtime").Get("lastError"); !err.IsUndefined() {
					reject.Invoke(err.Get("message").String())
					return nil
				}
				
				resolve.Invoke(nil)
				return nil
			}))
			
			return nil
		}))

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
    
    promise.Call("then",
        js.FuncOf(func(this js.Value, args []js.Value) interface{} {
            success = args[0]
            done <- success
            return nil
        }),
        js.FuncOf(func(this js.Value, args []js.Value) interface{} {
            done <- js.Undefined()
            return nil
        }),
    )
    
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
		// 创建 Promise 处理异步调用
		getPromise := js.Global().Get("Promise").New(js.FuncOf(func(this js.Value, args []js.Value) interface{} {
			resolve := args[0]
			reject := args[1]

			// 获取所有存储的数据
			p.db.Call("get", js.Null(), js.FuncOf(func(this js.Value, args []js.Value) interface{} {
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
			}))

			return nil
		}))

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
				decodedKey := string(key)
				if err := r([]byte(decodedKey), []byte(decodedValue)); err != nil {
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
    
    setPromise := js.Global().Get("Promise").New(js.FuncOf(func(this js.Value, args []js.Value) interface{} {
        resolve := args[0]
        reject := args[1]
        
        p.db.Call("set", data, js.FuncOf(func(this js.Value, args []js.Value) interface{} {
            if err := js.Global().Get("chrome").Get("runtime").Get("lastError"); !err.IsUndefined() {
                reject.Invoke(err.Get("message").String())
                return nil
            }
            resolve.Invoke(nil)
            return nil
        }))
        
        return nil
    }))
    
    await(setPromise)
    return nil
}

func (p *jsDB) removeBatch_Chrome(entries []string) error {

	data := make([]interface{}, 0)
    for _, value := range entries {
        data = append(data, value)
    }
   
    setPromise := js.Global().Get("Promise").New(js.FuncOf(func(this js.Value, args []js.Value) interface{} {
        resolve := args[0]
        reject := args[1]
        
        p.db.Call("remove", data, js.FuncOf(func(this js.Value, args []js.Value) interface{} {
            if err := js.Global().Get("chrome").Get("runtime").Get("lastError"); !err.IsUndefined() {
                reject.Invoke(err.Get("message").String())
                return nil
            }
            resolve.Invoke(nil)
            return nil
        }))
        
        return nil
    }))
    
    await(setPromise)
    return nil
}

func (p *jsDB) getStorageInfo_Chrome() (used float64, remaining float64, err error) {
    infoPromise := js.Global().Get("Promise").New(js.FuncOf(func(this js.Value, args []js.Value) interface{} {
        resolve := args[0]
        reject := args[1]
        
        p.db.Call("getBytesInUse", nil, js.FuncOf(func(this js.Value, args []js.Value) interface{} {
            if err := js.Global().Get("chrome").Get("runtime").Get("lastError"); !err.IsUndefined() {
                reject.Invoke(err.Get("message").String())
                return nil
            }
            bytesInUse := args[0].Float()
            // Chrome 存储限制通常是 5MB
            remaining := 5*1024*1024 - bytesInUse
            resolve.Invoke([]interface{}{bytesInUse, remaining})
            return nil
        }))
        
        return nil
    }))
    
    info := await(infoPromise)
    if info.IsUndefined() {
        return 0, 0, fmt.Errorf("failed to get storage info")
    }
    
    return info.Index(0).Float(), info.Index(1).Float(), nil
}
