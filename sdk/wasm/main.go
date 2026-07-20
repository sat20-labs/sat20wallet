//go:build js && wasm
// +build js,wasm

package main

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"syscall/js"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	corerelay "github.com/sat20-labs/rgb11/relay"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sirupsen/logrus"
)

const (
	module = "sat20wallet_wasm"
)

var _mgr *wallet.Manager
var _callback interface{}

type AsyncTaskFunc func() (interface{}, int, string)

func createAsyncJsHandler(task AsyncTaskFunc) js.Func {
	return js.FuncOf(func(this js.Value, args []js.Value) interface{} {
		resolve := args[0]
		// reject := args[1]

		var callback js.Func
		callback = js.FuncOf(func(this js.Value, args []js.Value) interface{} {
			callback.Release()
			go func() {
				defer func() {
					if r := recover(); r != nil {
						result := createJsRet(nil, -1, fmt.Sprintf("wasm panic: %v", r))
						jsResult := js.Global().Get("Object").New()
						for key, value := range result {
							jsResult.Set(key, value)
						}
						resolve.Invoke(jsResult)
					}
				}()
				data, code, msg := task()
				result := createJsRet(data, code, msg)
				jsResult := js.Global().Get("Object").New()
				for key, value := range result {
					jsResult.Set(key, value)
				}
				resolve.Invoke(jsResult)
			}()
			return nil
		})
		js.Global().Call("setTimeout", callback, 0)

		return nil
	})
}

var createJsRet = func(data any, code int, msg string) map[string]any {
	return map[string]any{
		"data": data,
		"code": code,
		"msg":  msg,
	}
}

func activateRGB11WalletState() map[string]any {
	result, err := _mgr.ActivateRGB11WalletState(dkvsindexer.RecordVerificationOptions{
		Now: uint64(time.Now().UnixMilli()),
	})
	if err != nil {
		wallet.Log.Errorf("automatic RGB11 wallet restore failed: %v", err)
		return map[string]any{"found": false, "restored": false, "auto_backup": false, "error": err.Error()}
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return map[string]any{"found": false, "restored": false, "auto_backup": false, "error": err.Error()}
	}
	var payload map[string]any
	if err := json.Unmarshal(encoded, &payload); err != nil {
		return map[string]any{"found": false, "restored": false, "auto_backup": false, "error": err.Error()}
	}
	return payload
}

func parseIndexerConfig(indexer js.Value) (*common.Indexer, error) {
	// IndexerL1
	if indexer.Type() == js.TypeObject {
		var cfg common.Indexer
		if scheme := indexer.Get("Scheme"); scheme.Type() == js.TypeString {
			cfg.Scheme = scheme.String()
		} else {
			cfg.Scheme = "http"
		}
		if host := indexer.Get("Host"); host.Type() == js.TypeString {
			cfg.Host = host.String()
		} else {
			return nil, fmt.Errorf("Indexer.Host must be a string")
		}
		if proxy := indexer.Get("Proxy"); proxy.Type() == js.TypeString {
			cfg.Proxy = proxy.String()
		} else {
			cfg.Proxy = ""
		}
		return &cfg, nil
	}
	return nil, fmt.Errorf("Indexer must be an object")
}

func parseConfigFromJS(jsConfig js.Value) (*common.Config, error) {
	cfg := &common.Config{Mode: wallet.LIGHT_NODE}
	// Log
	if log := jsConfig.Get("Log"); log.Type() == js.TypeString {
		cfg.Log = log.String()
	} else {
		cfg.Log = "info"
	}

	// Env
	if env := jsConfig.Get("Env"); env.Type() == js.TypeString {
		cfg.Env = env.String()
	} else {
		cfg.Env = "dev"
	}

	// Chain
	if chain := jsConfig.Get("Chain"); chain.Type() == js.TypeString {
		cfg.Chain = chain.String()
	} else {
		cfg.Chain = "testnet"
	}

	// Peers
	if peers := jsConfig.Get("Peers"); peers.Type() == js.TypeObject && peers.InstanceOf(js.Global().Get("Array")) {
		length := peers.Length()
		cfg.Peers = make([]string, length)
		for i := 0; i < length; i++ {
			if peers.Index(i).Type() != js.TypeString {
				return nil, fmt.Errorf("Peer at index %d must be a string", i)
			}
			cfg.Peers[i] = peers.Index(i).String()
		}
	} else {
		return nil, fmt.Errorf("Peers must be an array of strings")
	}

	var err error
	indexerL1 := jsConfig.Get("IndexerL1")
	cfg.IndexerL1, err = parseIndexerConfig(indexerL1)
	if err != nil {
		return nil, fmt.Errorf("L1 indexer config should be set, %v", err)
	}
	slaveIndexerL1 := jsConfig.Get("SlaveIndexerL1")
	cfg.SlaveIndexerL1, _ = parseIndexerConfig(slaveIndexerL1)

	indexerL2 := jsConfig.Get("IndexerL2")
	cfg.IndexerL2, err = parseIndexerConfig(indexerL2)
	if err != nil {
		return nil, fmt.Errorf("L2 indexer config should be set, %v", err)
	}
	slaveIndexerL2 := jsConfig.Get("SlaveIndexerL2")
	cfg.SlaveIndexerL2, _ = parseIndexerConfig(slaveIndexerL2)

	return cfg, nil
}

func dbTest(this js.Value, p []js.Value) any {
	if len(p) < 2 {
		const errMsg = "Expected 2 parameters: key, value"
		wallet.Log.Error(errMsg)
		return createJsRet(nil, 1, errMsg)
	}

	if p[0].Type() != js.TypeString {
		wallet.Log.Error("Second parameter should be a string")
		return "Error: Second parameter should be a string"
	}
	key := p[0].String()

	if p[1].Type() != js.TypeString {
		wallet.Log.Error("Second parameter should be a string")
		return "Error: Second parameter should be a string"
	}
	value := p[1].String()

	db := wallet.NewKVDB("")
	err := db.Write([]byte(key), []byte(value))
	if err != nil {
		wallet.Log.Errorf("db.Write failed, %v", err)
		return err
	}

	value2, err := db.Read([]byte(key))
	if err != nil {
		wallet.Log.Errorf("db.Read failed, %v", err)
		return err
	}
	msg := "ok"
	if value != string(value2) {
		msg = fmt.Sprintf("input %s, but output %s", value, string(value2))
	}

	return createJsRet(nil, 0, msg)
}

func batchDbTest(this js.Value, p []js.Value) any {
	if len(p) < 4 {
		const errMsg = "Expected 4 parameters: int, string, bool, and string array"
		wallet.Log.Error(errMsg)
		return createJsRet(nil, 1, errMsg)
	}

	if p[0].Type() != js.TypeNumber {
		wallet.Log.Error("First parameter should be a number")
		return "Error: First parameter should be a number"
	}
	intValue := p[0].Int()

	if p[1].Type() != js.TypeString {
		wallet.Log.Error("Second parameter should be a string")
		return "Error: Second parameter should be a string"
	}
	stringValue := p[1].String()

	if p[2].Type() != js.TypeBoolean {
		wallet.Log.Error("Third parameter should be a boolean")
		return "Error: Third parameter should be a boolean"
	}
	boolValue := p[2].Bool()

	if p[3].Type() != js.TypeObject || !p[3].InstanceOf(js.Global().Get("Array")) {
		const errMsg = "Fourth parameter should be an array"
		wallet.Log.Error(errMsg)
		return createJsRet(nil, 5, errMsg)
	}
	arrayValue := p[3]
	arrayLength := arrayValue.Length()

	stringArray := make([]string, arrayLength)
	for i := 0; i < arrayLength; i++ {
		item := arrayValue.Index(i)
		if item.Type() != js.TypeString {
			errMsg := fmt.Sprintf("Array item at index %d is not a string", i)
			wallet.Log.Error(errMsg)
			return createJsRet(nil, 6, errMsg)
		}
		stringArray[i] = item.String()
	}

	db := wallet.NewKVDB("")
	batch := db.NewWriteBatch()
	defer batch.Close()
	batch.Put([]byte("intValue0"), []byte(strconv.Itoa(intValue)))
	batch.Put([]byte("intValue1"), []byte(stringValue))
	batch.Put([]byte("intValue2"), []byte(strconv.FormatBool(boolValue)))
	batch.Put([]byte("intValue3"), []byte(strings.Join(stringArray, ",")))
	err := batch.Flush()
	if err != nil {
		wallet.Log.Errorf("db.Flush failed")
		return err
	}

	err = db.BatchRead([]byte("intValue"), false, func(k, v []byte) error {
		wallet.Log.Debugf("BatchRead intValue: key: %v, value: %v", string(k), string(v))
		return nil
	})
	if err != nil {
		wallet.Log.Errorf("db.BatchRead failed")
		return err
	}

	wallet.Log.Debugf("Int parameter: %v", intValue)
	wallet.Log.Debugf("String parameter: %v", stringValue)
	wallet.Log.Debugf("Bool parameter: %v", boolValue)
	wallet.Log.Debugf("String array parameter: %v", stringArray)

	code := 0
	msg := "ok"
	return createJsRet(nil, code, msg)
}

func initManager(this js.Value, p []js.Value) any {
	if _mgr != nil {
		return createJsRet(nil, -1, "Manager is initialized")
	}

	if len(p) < 2 {
		return createJsRet(nil, -1, "Expected 2 parameters")
	}

	if p[0].Type() != js.TypeObject {
		return createJsRet(nil, -1, "config parameter should be a object")
	}
	var cfg *common.Config
	cfg, err := parseConfigFromJS(p[0])
	if err != nil {
		return createJsRet(nil, -1, fmt.Sprintf("Failed to parse config: %v", err))
	}

	if p[1].Type() != js.TypeNumber {
		msg := "log level parameter should be a number, 0: Panic, 1: Fatal, 2: Error, 3: Warn, 4: Info, 5: Debug, 6: Trace"
		return createJsRet(nil, -1, msg)
	}

	logLevel := logrus.Level(p[1].Int())
	if logLevel > 6 {
		msg := "log level parameter should be a number, 0: Panic, 1: Fatal, 2: Error, 3: Warn, 4: Info, 5: Debug, 6: Trace"
		return createJsRet(nil, -1, msg)
	}
	wallet.Log.SetLevel(logLevel)

	// _mgr = wallet.NewManager(cfg, make(chan struct{}))
	// if _mgr == nil {
	// 	return createJsRet(nil, -1, "NewManager failed")
	// }
	// wallet.Log.Info("Manager created")
	// return createJsRet(nil, 0, "ok")

	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		db := wallet.NewKVDB(cfg.DB)
		if db == nil {
			wallet.Log.Errorf("NewKVDB failed")
			return nil, -1, "NewKVDB failed"
		}
		_mgr = wallet.NewManager(cfg, db)
		if _mgr == nil {
			return nil, -1, "NewManager failed"
		}
		_mgr.Start()
		wallet.Log.Info("Manager created")
		return nil, 0, "ok"
	})

	return js.Global().Get("Promise").New(handler)
}

func releaseManager(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	_mgr.Close()
	_mgr = nil
	return createJsRet(nil, 0, "ok")
}

func startBTCLuckyMining(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	req := wallet.BTCLuckyMiningConfig{
		Jobs:             "1",
		LowPriority:      true,
		LowPrioritySleep: "1s",
	}
	if len(p) > 0 && p[0].Type() == js.TypeObject {
		if jobs := p[0].Get("jobs"); jobs.Type() == js.TypeString {
			req.Jobs = jobs.String()
		}
		if lowPriority := p[0].Get("lowPriority"); lowPriority.Type() == js.TypeBoolean {
			req.LowPriority = lowPriority.Bool()
		}
		if sleep := p[0].Get("lowPrioritySleep"); sleep.Type() == js.TypeString {
			req.LowPrioritySleep = sleep.String()
		}
	}
	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		st, err := _mgr.StartBTCLuckyMining(req)
		if err != nil {
			return nil, -1, err.Error()
		}
		return jsSafeData(st), 0, "ok"
	})
	return js.Global().Get("Promise").New(handler)
}

func stopBTCLuckyMining(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		return jsSafeData(_mgr.StopBTCLuckyMining()), 0, "ok"
	})
	return js.Global().Get("Promise").New(handler)
}

func getBTCLuckyMiningStatus(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		return jsSafeData(_mgr.GetBTCLuckyMiningStatus()), 0, "ok"
	})
	return js.Global().Get("Promise").New(handler)
}

func jsSafeData(v any) map[string]any {
	raw, err := json.Marshal(v)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]any{"error": err.Error()}
	}
	return out
}

func createWallet(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(p) < 1 {
		return createJsRet(nil, -1, "Expected 1 parameters")
	}

	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "password parameter should be a string")
	}
	password := p[0].String()

	// id, mnemonic, err := _mgr.CreateWallet(password)
	// if err != nil {
	// 	return createJsRet(nil, -1, err.Error())
	// }
	// data := map[string]any{
	// 	"walletId": id,
	// 	"mnemonic": mnemonic,
	// }
	// return createJsRet(data, 0, "ok")

	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		id, mnemonic, err := _mgr.CreateWallet(password)
		if err != nil {
			return nil, -1, err.Error()
		}
		wallet.Log.Info("wallet created")
		return map[string]any{
			"walletId":        fmt.Sprintf("%d", id),
			"mnemonic":        mnemonic,
			"rgb11Activation": activateRGB11WalletState(),
		}, 0, "ok"
	})

	return js.Global().Get("Promise").New(handler)
}

func createMonitorWallet(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(p) < 1 {
		return createJsRet(nil, -1, "Expected 1 parameters")
	}

	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "address parameter should be a string")
	}
	address := p[0].String()

	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		id, err := _mgr.CreateMonitorWallet(address)
		if err != nil {
			return nil, -1, err.Error()
		}
		wallet.Log.Info("wallet created")
		return map[string]any{
			"walletId": fmt.Sprintf("%d", id),
		}, 0, "ok"
	})

	return js.Global().Get("Promise").New(handler)
}

func isWalletExist(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	// exist := _mgr.IsWalletExist()
	// return createJsRet(exist, 0, "ok")

	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		exist := _mgr.IsWalletExist()
		return map[string]any{
			"exists": exist,
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(handler)
}

func importWallet(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(p) < 2 {
		return createJsRet(nil, -1, "Expected 2 parameters")
	}

	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "mnemonic parameter should be a string")
	}
	mnemonic := p[0].String()

	if p[1].Type() != js.TypeString {
		return createJsRet(nil, -1, "password parameter should be a string")
	}
	password := p[1].String()

	wallet.Log.Infof("ImportWallet %s %s", mnemonic, password)

	// id, err := _mgr.ImportWallet(mnemonic, password)
	// if err != nil {
	// 	return createJsRet(nil, -1, err.Error())
	// }
	// data := map[string]any{
	// 	"walletId": id,
	// }
	// return createJsRet(data, 0, "ok")

	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		id, err := _mgr.ImportWallet(mnemonic, password)
		if err != nil {
			return nil, -1, err.Error()
		}
		return map[string]any{
			"walletId":        fmt.Sprintf("%d", id),
			"address":         _mgr.GetWallet().GetAddress(),
			"rgb11Activation": activateRGB11WalletState(),
		}, 0, "ok"
	})

	return js.Global().Get("Promise").New(handler)
}

func importWalletWithPrivKey(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(p) < 2 {
		return createJsRet(nil, -1, "Expected 2 parameters")
	}

	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "private key parameter should be a hex string")
	}
	mnemonic := p[0].String()

	if p[1].Type() != js.TypeString {
		return createJsRet(nil, -1, "password parameter should be a string")
	}
	password := p[1].String()

	wallet.Log.Infof("ImportWallet %s %s", mnemonic, password)

	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		id, err := _mgr.ImportWalletWithPrivateKey(mnemonic, password)
		if err != nil {
			return nil, -1, err.Error()
		}
		return map[string]any{
			"walletId":        fmt.Sprintf("%d", id),
			"address":         _mgr.GetWallet().GetAddress(),
			"rgb11Activation": activateRGB11WalletState(),
		}, 0, "ok"
	})

	return js.Global().Get("Promise").New(handler)
}

func unlockWallet(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(p) < 1 {
		return createJsRet(nil, -1, "Expected 1 parameters")
	}
	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "password parameter should be a string")
	}
	password := p[0].String()

	// id, err := _mgr.UnlockWallet(password)
	// if err != nil {
	// 	return createJsRet(nil, -1, err.Error())
	// }
	// data := map[string]any{
	// 	"walletId": id,
	// }
	// return createJsRet(data, 0, "ok")

	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		id, err := _mgr.UnlockWallet(password)
		if err != nil {
			return nil, -1, err.Error()
		}
		return map[string]any{
			"walletId":        fmt.Sprintf("%d", id),
			"rgb11Activation": activateRGB11WalletState(),
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(handler)
}

func getAllWallets(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}

	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		ids := _mgr.GetAllWallets()

		type WalletIdAndAccounts struct {
			Id       string
			Accounts int
		}
		result := make([]*WalletIdAndAccounts, 0)
		for k, v := range ids {
			result = append(result, &WalletIdAndAccounts{Id: fmt.Sprintf("%d", k), Accounts: v})
		}
		sort.Slice(result, func(i, j int) bool {
			return result[i].Id < result[j].Id
		})
		return map[string]any{
			"walletIds": result,
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(handler)
}

func switchWallet(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(p) < 2 {
		return createJsRet(nil, -1, "Expected 2 parameters")
	}
	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "Id parameter should be string")
	}
	id := p[0].String()
	i, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}

	if p[1].Type() != js.TypeString {
		return createJsRet(nil, -1, "password parameter should be a string")
	}
	password := p[1].String()

	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		err := _mgr.SwitchWallet(i, password)
		if err != nil {
			return nil, -1, err.Error()
		}
		return map[string]any{"rgb11Activation": activateRGB11WalletState()}, 0, "ok"
	})
	return js.Global().Get("Promise").New(handler)
}

func changePassword(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(p) < 2 {
		return createJsRet(nil, -1, "Expected 2 parameters")
	}

	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "password parameter should be a string")
	}
	oldps := p[0].String()

	if p[1].Type() != js.TypeString {
		return createJsRet(nil, -1, "password parameter should be a string")
	}
	newps := p[1].String()

	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		err := _mgr.ChangePassword(oldps, newps)
		if err != nil {
			return nil, -1, err.Error()
		}
		return nil, 0, "ok"
	})
	return js.Global().Get("Promise").New(handler)
}

func switchAccount(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(p) < 1 {
		return createJsRet(nil, -1, "Expected 1 parameters")
	}
	if p[0].Type() != js.TypeNumber {
		return createJsRet(nil, -1, "Id parameter should be a number")
	}
	id := p[0].Int()

	// _mgr.SwitchAccount(uint32(id))
	// return createJsRet(nil, 0, "ok")

	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		status := _mgr.GetStatus()
		if status != nil && status.CurrentAccount == uint32(id) {
			return nil, 0, "ok"
		}
		_mgr.SwitchAccount(uint32(id))
		return map[string]any{"rgb11Activation": activateRGB11WalletState()}, 0, "ok"
	})
	return js.Global().Get("Promise").New(handler)
}

func switchChain(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(p) < 2 {
		return createJsRet(nil, -1, "Expected 2 parameters")
	}
	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "chain parameter should be a string")
	}
	chain := p[0].String()

	if p[1].Type() != js.TypeString {
		return createJsRet(nil, -1, "password parameter should be a string")
	}
	password := p[1].String()

	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		err := _mgr.SwitchChain(chain, password)
		if err != nil {
			return nil, -1, err.Error()
		}
		return map[string]any{"rgb11Activation": activateRGB11WalletState()}, 0, "ok"
	})
	return js.Global().Get("Promise").New(handler)
}

func getMnemonic(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(p) < 2 {
		return createJsRet(nil, -1, "Expected 2 parameters")
	}
	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "Id parameter should be a string")
	}
	id := p[0].String()
	i, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}

	if p[1].Type() != js.TypeString {
		return createJsRet(nil, -1, "password should be a string")
	}
	password := p[1].String()

	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		mnemonic := _mgr.GetMnemonic(i, password)
		return map[string]any{
			"mnemonic": mnemonic,
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(handler)
}

func getWalletAddress(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(p) < 1 {
		return createJsRet(nil, -1, "Expected 1 parameters")
	}
	if p[0].Type() != js.TypeNumber {
		return createJsRet(nil, -1, "Id parameter should be a number")
	}
	id := p[0].Int()

	// wallet := _mgr.GetWallet()
	// if wallet == nil {
	// 	return createJsRet(nil, -1, "wallet is nil")
	// }
	// data := map[string]any{
	// 	"address": wallet.GetAddress(uint32(id)),
	// }
	// return createJsRet(data, 0, "ok")

	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		wallet := _mgr.GetWallet()
		if wallet == nil {
			return nil, -1, "wallet is nil"
		}
		return map[string]any{
			"address": wallet.GetAddressByIndex(uint32(id)),
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(handler)
}

func getWalletPubkey(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(p) < 1 {
		return createJsRet(nil, -1, "Expected 1 parameters")
	}
	if p[0].Type() != js.TypeNumber {
		return createJsRet(nil, -1, "Id parameter should be a number")
	}
	id := p[0].Int()

	// pubkey := _mgr.GetPublicKey(uint32(id))
	// data := map[string]any{
	// 	"pubKey": hex.EncodeToString(pubkey),
	// }
	// return createJsRet(data, 0, "ok")

	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		pubkey := _mgr.GetPublicKey(uint32(id))
		return map[string]any{
			"pubKey": hex.EncodeToString(pubkey),
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(handler)
}

func getChannelAddrByPeerPubkey(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(p) < 1 {
		return createJsRet(nil, -1, "Expected 1 parameters")
	}
	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "pubkey parameter should be a string")
	}
	pubkey := p[0].String()

	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		channelAddr, peerAddr, err := _mgr.GetChannelAddrByPeerPubkey(pubkey)
		if err != nil {
			return nil, -1, err.Error()
		}
		return map[string]any{
			"channelAddr": channelAddr,
			"peerAddr":    peerAddr,
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(handler)
}

func openChannel(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(p) < 4 {
		return createJsRet(nil, -1, "Expected 4 parameters")
	}
	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "feeRate parameter should be a string")
	}
	if p[1].Type() != js.TypeString {
		return createJsRet(nil, -1, "amount parameter should be a string")
	}
	utxoList, err := getStringVector(p[2])
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	if p[3].Type() != js.TypeString {
		return createJsRet(nil, -1, "memo parameter should be a string")
	}

	feeRate, err := strconv.ParseInt(p[0].String(), 10, 64)
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	amt, err := strconv.ParseInt(p[1].String(), 10, 64)
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	memo := p[3].String()

	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		channel, err := _mgr.OpenChannel(feeRate, amt, utxoList, memo)
		if err != nil {
			wallet.Log.Errorf("OpenChannel error: %v", err)
			return nil, -1, err.Error()
		}
		return map[string]interface{}{"channel": channel}, 0, "ok"
	})
	return js.Global().Get("Promise").New(handler)
}

func closeChannel(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(p) < 3 {
		return createJsRet(nil, -1, "Expected 3 parameters")
	}
	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "channel parameter should be a string")
	}
	if p[1].Type() != js.TypeString {
		return createJsRet(nil, -1, "feeRate parameter should be a string")
	}
	feeRate, err := strconv.ParseInt(p[1].String(), 10, 64)
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	if p[2].Type() != js.TypeBoolean {
		return createJsRet(nil, -1, "force parameter should be a boolean")
	}
	channel := p[0].String()
	force := p[2].Bool()

	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		closeTxId, deAnchorTxId, err := _mgr.CloseChannel(channel, feeRate, force)
		if err != nil {
			wallet.Log.Errorf("CloseChannel error: %v", err)
			return nil, -1, err.Error()
		}
		return map[string]interface{}{
			"closeTxId":    closeTxId,
			"deAnchorTxId": deAnchorTxId,
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(handler)
}

func channelData(channel *wallet.Channel) (map[string]any, error) {
	if channel == nil {
		return nil, fmt.Errorf("channel is nil")
	}
	info := wallet.ConvertChannel(channel)
	channelJSON, err := json.Marshal(info)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"address":   info.Address,
		"channelId": info.ChannelId,
		"status":    info.Status,
		"json":      string(channelJSON),
	}, nil
}

func getChannel(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(p) < 1 {
		return createJsRet(nil, -1, "Expected 1 parameters")
	}
	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "channel parameter should be a string")
	}
	data, err := channelData(_mgr.FindChannel(p[0].String()))
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	return createJsRet(data, 0, "ok")
}

func getCurrentChannel(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	data, err := channelData(_mgr.GetCurrentChannel())
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	return createJsRet(data, 0, "ok")
}

func getChannelStatus(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(p) < 1 {
		return createJsRet(nil, -1, "Expected 1 parameters")
	}
	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "channel parameter should be a string")
	}
	return createJsRet(_mgr.GetChannelStatus(p[0].String()), 0, "ok")
}

func getAllChannels(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	channels := _mgr.GetAllChannels()
	result := make([]*wallet.ChannelInfo, 0, len(channels))
	for _, c := range channels {
		result = append(result, wallet.ConvertChannel(c))
	}
	channelsJSON, err := json.Marshal(result)
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	return createJsRet(map[string]any{"channels": string(channelsJSON)}, 0, "ok")
}

func reservationStatus(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(p) < 1 {
		return createJsRet(nil, -1, "Expected reservation id parameter")
	}

	var id int64
	var err error
	switch p[0].Type() {
	case js.TypeNumber:
		id = int64(p[0].Int())
	case js.TypeString:
		id, err = strconv.ParseInt(strings.TrimSpace(p[0].String()), 10, 64)
		if err != nil {
			return createJsRet(nil, -1, err.Error())
		}
	default:
		return createJsRet(nil, -1, "reservation id parameter should be a number or string")
	}

	resv := _mgr.GetResv(id)
	if resv == nil {
		return createJsRet(nil, -1, "reservation not found")
	}
	buf, err := json.Marshal(resv.GetStructInDB())
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	return createJsRet(map[string]interface{}{
		"reservation_id": resv.GetId(),
		"type":           resv.GetType(),
		"status":         int(resv.GetStatus()),
		"json":           string(buf),
	}, 0, "ok")
}

func allReservations(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	resvs := _mgr.GetAllResv()
	items := make([]interface{}, 0, len(resvs))
	for _, resv := range resvs {
		item := map[string]interface{}{
			"reservation_id": resv.GetId(),
			"type":           resv.GetType(),
			"status":         int(resv.GetStatus()),
		}
		if buf, err := json.Marshal(resv.GetStructInDB()); err == nil {
			item["json"] = string(buf)
		}
		items = append(items, item)
	}
	return createJsRet(map[string]interface{}{"reservations": items}, 0, "ok")
}

func unlockFromChannel(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(p) < 4 {
		return createJsRet(nil, -1, "Expected 4 parameters")
	}
	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "channel parameter should be a string")
	}
	if p[1].Type() != js.TypeString {
		return createJsRet(nil, -1, "assetName parameter should be a string")
	}
	if p[2].Type() != js.TypeString {
		return createJsRet(nil, -1, "amount parameter should be a string")
	}
	feeUtxoList, err := getStringVector(p[3])
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	channel := p[0].String()
	assetName := p[1].String()
	amt := p[2].String()

	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		unlockTxId, _, err := _mgr.UnlockFromChannel(channel, "", assetName, amt, feeUtxoList, nil)
		if err != nil {
			wallet.Log.Errorf("UnlockFromChannel error: %v", err)
			return nil, -1, err.Error()
		}
		return map[string]interface{}{"unlockTxId": unlockTxId}, 0, "ok"
	})
	return js.Global().Get("Promise").New(handler)
}

func lockToChannel(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(p) < 5 {
		return createJsRet(nil, -1, "Expected 5 parameters")
	}
	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "channel Id parameter should be a string")
	}
	if p[1].Type() != js.TypeString {
		return createJsRet(nil, -1, "assetName parameter should be a string")
	}
	if p[2].Type() != js.TypeString {
		return createJsRet(nil, -1, "amount parameter should be a string")
	}
	utxoList, err := getStringVector(p[3])
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	feeList, err := getStringVector(p[4])
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	channel := p[0].String()
	assetName := p[1].String()
	amt := p[2].String()

	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		lockTxId, _, err := _mgr.LockToChannel(channel, assetName, amt, utxoList, feeList, nil)
		if err != nil {
			wallet.Log.Errorf("LockToChannel error: %v", err)
			return nil, -1, err.Error()
		}
		return map[string]interface{}{"lockTxId": lockTxId}, 0, "ok"
	})
	return js.Global().Get("Promise").New(handler)
}

func lockToChannelWithExpand(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(p) < 4 {
		return createJsRet(nil, -1, "Expected 4 parameters")
	}
	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "channel Id parameter should be a string")
	}
	if p[1].Type() != js.TypeString {
		return createJsRet(nil, -1, "assetName parameter should be a string")
	}
	if p[2].Type() != js.TypeString {
		return createJsRet(nil, -1, "amount parameter should be a string")
	}
	if p[3].Type() != js.TypeString {
		return createJsRet(nil, -1, "feeRate parameter should be a string")
	}
	feeRate, err := strconv.ParseInt(p[3].String(), 10, 64)
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	channel := p[0].String()
	assetName := p[1].String()
	amt := p[2].String()

	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		lockTxId, id, err := _mgr.LockToChannelWithExpand(channel, assetName, amt, feeRate)
		if err != nil {
			wallet.Log.Errorf("LockToChannelWithExpand error: %v", err)
			return nil, -1, err.Error()
		}
		return map[string]interface{}{"lockTxId": lockTxId, "id": id}, 0, "ok"
	})
	return js.Global().Get("Promise").New(handler)
}

func batchUnlockFromChannel(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(p) < 5 {
		return createJsRet(nil, -1, "Expected 5 parameters")
	}
	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "channel parameter should be a string")
	}
	destAddr, err := getStringVector(p[1])
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	if p[2].Type() != js.TypeString {
		return createJsRet(nil, -1, "assetName parameter should be a string")
	}
	if p[3].Type() != js.TypeString {
		return createJsRet(nil, -1, "amount parameter should be a string")
	}
	feeUtxos, err := getStringVector(p[4])
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	channel := p[0].String()
	assetName := p[2].String()
	amt := p[3].String()

	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		txId, id, err := _mgr.BatchUnlockFromChannel(channel, destAddr, assetName, amt, feeUtxos, nil, "", nil)
		if err != nil {
			wallet.Log.Errorf("BatchUnlockFromChannel error: %v", err)
			return nil, -1, err.Error()
		}
		return map[string]interface{}{"unlockTxId": txId, "id": id}, 0, "ok"
	})
	return js.Global().Get("Promise").New(handler)
}

func batchUnlockFromChannelV2(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(p) < 5 {
		return createJsRet(nil, -1, "Expected 5 parameters")
	}
	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "channel parameter should be a string")
	}
	destAddr, err := getStringVector(p[1])
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	if p[2].Type() != js.TypeString {
		return createJsRet(nil, -1, "assetName parameter should be a string")
	}
	amtVect, err := getStringVector(p[3])
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	feeUtxos, err := getStringVector(p[4])
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	channel := p[0].String()
	assetName := p[2].String()

	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		txId, id, err := _mgr.BatchUnlockFromChannelV2(channel, destAddr, assetName, amtVect, feeUtxos, nil, "", nil)
		if err != nil {
			wallet.Log.Errorf("BatchUnlockFromChannelV2 error: %v", err)
			return nil, -1, err.Error()
		}
		return map[string]interface{}{"unlockTxId": txId, "id": id}, 0, "ok"
	})
	return js.Global().Get("Promise").New(handler)
}

func expandChannel(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(p) < 3 {
		return createJsRet(nil, -1, "Expected 3 parameters")
	}
	if p[0].Type() != js.TypeString || p[1].Type() != js.TypeString || p[2].Type() != js.TypeString {
		return createJsRet(nil, -1, "channel, assetName and utxo parameters should be strings")
	}
	channel := p[0].String()
	assetName := p[1].String()
	utxo := p[2].String()

	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		txId, amt, id, err := _mgr.ExpandChannel(channel, assetName, utxo, "", nil)
		if err != nil {
			wallet.Log.Errorf("ExpandChannel error: %v", err)
			return nil, -1, err.Error()
		}
		return map[string]interface{}{"txId": txId, "amount": amt, "id": id}, 0, "ok"
	})
	return js.Global().Get("Promise").New(handler)
}

func expandChannel_SatsNet(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(p) < 3 {
		return createJsRet(nil, -1, "Expected 3 parameters")
	}
	if p[0].Type() != js.TypeString || p[1].Type() != js.TypeString {
		return createJsRet(nil, -1, "channel and assetName parameters should be strings")
	}
	utxos, err := getStringVector(p[2])
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	channel := p[0].String()
	assetName := p[1].String()

	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		amt, err := _mgr.ExpandChannel_SatsNet(channel, assetName, utxos)
		if err != nil {
			wallet.Log.Errorf("ExpandChannel_SatsNet error: %v", err)
			return nil, -1, err.Error()
		}
		return map[string]interface{}{"amount": amt}, 0, "ok"
	})
	return js.Global().Get("Promise").New(handler)
}

func expandAll_SatsNet(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(p) < 2 {
		return createJsRet(nil, -1, "Expected 2 parameters")
	}
	if p[0].Type() != js.TypeString || p[1].Type() != js.TypeString {
		return createJsRet(nil, -1, "channel and assetName parameters should be strings")
	}
	channel := p[0].String()
	assetName := p[1].String()

	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		amt, err := _mgr.ExpandAll_SatsNet(channel, assetName)
		if err != nil {
			wallet.Log.Errorf("ExpandAll_SatsNet error: %v", err)
			return nil, -1, err.Error()
		}
		return map[string]interface{}{"amount": amt}, 0, "ok"
	})
	return js.Global().Get("Promise").New(handler)
}

func expandAsset(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(p) < 2 {
		return createJsRet(nil, -1, "Expected 2 parameters")
	}
	if p[0].Type() != js.TypeString || p[1].Type() != js.TypeString {
		return createJsRet(nil, -1, "channel and assetName parameters should be strings")
	}
	channel := p[0].String()
	assetName := p[1].String()

	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		txIds, amt, err := _mgr.ExpandAsset(channel, assetName)
		if err != nil {
			wallet.Log.Errorf("ExpandAsset error: %v", err)
			return nil, -1, err.Error()
		}
		return map[string]interface{}{"txIds": txIds, "amount": amt}, 0, "ok"
	})
	return js.Global().Get("Promise").New(handler)
}

func reopenChannel(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(p) < 1 || p[0].Type() != js.TypeBoolean {
		return createJsRet(nil, -1, "expandAll parameter should be a boolean")
	}
	expandAll := p[0].Bool()

	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		txId, err := _mgr.ReopenChannel(expandAll)
		if err != nil {
			wallet.Log.Errorf("ReopenChannel error: %v", err)
			return nil, -1, err.Error()
		}
		return map[string]interface{}{"txId": txId}, 0, "ok"
	})
	return js.Global().Get("Promise").New(handler)
}

func rebuildChannel(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		txId, err := _mgr.RebuildChannel()
		if err != nil {
			wallet.Log.Errorf("RebuildChannel error: %v", err)
			return nil, -1, err.Error()
		}
		return map[string]interface{}{"txId": txId}, 0, "ok"
	})
	return js.Global().Get("Promise").New(handler)
}

func restoreChannel(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(p) < 1 || p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "channel parameter should be a string")
	}
	channelID := p[0].String()

	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		channel, err := _mgr.RestoreChannel(channelID)
		if err != nil {
			wallet.Log.Errorf("RestoreChannel error: %v", err)
			return nil, -1, err.Error()
		}
		data, err := channelData(channel)
		if err != nil {
			return nil, -1, err.Error()
		}
		return data, 0, "ok"
	})
	return js.Global().Get("Promise").New(handler)
}

func splicingIn(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(p) < 6 {
		return createJsRet(nil, -1, "Expected 6 parameters")
	}
	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "channel Id parameter should be a string")
	}
	if p[1].Type() != js.TypeString {
		return createJsRet(nil, -1, "assetName parameter should be a string")
	}
	utxos, err := getStringVector(p[2])
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	fees, err := getStringVector(p[3])
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	if p[4].Type() != js.TypeString {
		return createJsRet(nil, -1, "feeRate parameter should be a string")
	}
	feeRate, err := strconv.ParseInt(p[4].String(), 10, 64)
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	if p[5].Type() != js.TypeString {
		return createJsRet(nil, -1, "amount parameter should be a string")
	}
	channel := p[0].String()
	assetName := p[1].String()
	amt := p[5].String()

	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		txId, id, err := _mgr.SplicingIn(channel, assetName, amt,
			utxos, fees, nil, nil, feeRate, wallet.SPLICING_REASON_LOCAL)
		if err != nil {
			wallet.Log.Errorf("SplicingIn error: %v", err)
			return nil, -1, err.Error()
		}
		return map[string]interface{}{"txId": txId, "resvId": id}, 0, "ok"
	})
	return js.Global().Get("Promise").New(handler)
}

func splicingOut(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(p) < 6 {
		return createJsRet(nil, -1, "Expected 6 parameters")
	}
	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "channel Id parameter should be a string")
	}
	if p[1].Type() != js.TypeString {
		return createJsRet(nil, -1, "destAddr parameter should be a string")
	}
	if p[2].Type() != js.TypeString {
		return createJsRet(nil, -1, "assetName parameter should be a string")
	}
	fees, err := getStringVector(p[3])
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	if p[4].Type() != js.TypeString {
		return createJsRet(nil, -1, "feeRate parameter should be a string")
	}
	feeRate, err := strconv.ParseInt(p[4].String(), 10, 64)
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	if p[5].Type() != js.TypeString {
		return createJsRet(nil, -1, "amount parameter should be a string")
	}
	channel := p[0].String()
	destAddr := p[1].String()
	assetName := p[2].String()
	amt := p[5].String()

	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		txId, id, err := _mgr.SplicingOut(channel, destAddr, assetName, amt,
			fees, nil, nil, feeRate, wallet.SPLICING_REASON_LOCAL, nil)
		if err != nil {
			wallet.Log.Errorf("SplicingOut error: %v", err)
			return nil, -1, err.Error()
		}
		return map[string]interface{}{"txId": txId, "resvId": id}, 0, "ok"
	})
	return js.Global().Get("Promise").New(handler)
}

func getCommitSecret(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}

	if len(p) < 2 {
		return createJsRet(nil, -1, "Expected 2 parameters")
	}

	// jsBytes := p[0]
	// goBytes := make([]byte, jsBytes.Length())
	// js.CopyBytesToGo(goBytes, jsBytes)

	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "nodeId parameter should be a string")
	}
	id, err := hex.DecodeString(p[0].String())
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}

	if p[1].Type() != js.TypeNumber {
		return createJsRet(nil, -1, "index parameter should be a number")
	}
	index := p[1].Int()

	// result := _mgr.GetCommitSecret(id, index)
	// // jsBytes = js.Global().Get("Uint8Array").New(len(result))
	// // js.CopyBytesToJS(jsBytes, result)
	// data := map[string]any{
	// 	"commitSecret": hex.EncodeToString(result),
	// }
	// return createJsRet(data, 0, "ok")

	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		result := _mgr.GetCommitSecret(id, index)
		return map[string]any{
			"commitSecret": hex.EncodeToString(result),
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(handler)
}

func deriveRevocationPrivKey(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}

	if len(p) < 1 {
		return createJsRet(nil, -1, "Expected 1 parameters")
	}

	// jsBytes := p[0]
	// goBytes := make([]byte, jsBytes.Length())
	// js.CopyBytesToGo(goBytes, jsBytes)
	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "secret parameter should be a string")
	}
	secrect, err := hex.DecodeString(p[0].String())
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}

	// result := _mgr.DeriveRevocationPrivKey(secrect)
	// // jsBytes = js.Global().Get("Uint8Array").New(len(result))
	// // js.CopyBytesToJS(jsBytes, result)
	// data := map[string]any{
	// 	"revocationPrivKey": hex.EncodeToString(result),
	// }
	// return createJsRet(data, 0, "ok")

	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		result := _mgr.DeriveRevocationPrivKey(secrect)
		return map[string]any{
			"revocationPrivKey": hex.EncodeToString(result),
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(handler)
}

func getRevocationBaseKey(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}

	// result := _mgr.GetRevocationBaseKey()
	// // jsBytes := js.Global().Get("Uint8Array").New(len(result))
	// // js.CopyBytesToJS(jsBytes, result)
	// data := map[string]any{
	// 	"revocationBaseKey": hex.EncodeToString(result),
	// }
	// return createJsRet(data, 0, "ok")

	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		result := _mgr.GetRevocationBaseKey()
		return map[string]any{
			"revocationBaseKey": hex.EncodeToString(result),
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(handler)
}

func getNodePubKey(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}

	// result := _mgr.GetNodePubKey()
	// // jsBytes := js.Global().Get("Uint8Array").New(len(result))
	// // js.CopyBytesToJS(jsBytes, result)
	// data := map[string]any{
	// 	"nodePubKey": hex.EncodeToString(result),
	// }
	// return createJsRet(data, 0, "ok")

	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		result := _mgr.GetNodePubKey()
		return map[string]any{
			"nodePubKey": hex.EncodeToString(result),
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(handler)
}

func signMessage(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}

	if len(p) < 1 {
		return createJsRet(nil, -1, "Expected 1 parameters")
	}

	// jsBytes := p[0]
	// goBytes := make([]byte, jsBytes.Length())
	// js.CopyBytesToGo(goBytes, jsBytes)
	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "message parameter should be a string")
	}
	msg := p[0].String()

	// result, err := _mgr.SignMessage(msgBytes)
	// if err != nil {
	// 	return createJsRet(nil, -1, err.Error())
	// }

	// jsBytes = js.Global().Get("Uint8Array").New(len(result))
	// js.CopyBytesToJS(jsBytes, result)
	// data := map[string]any{
	// 	"signature": hex.EncodeToString(result),
	// }
	// return createJsRet(data, 0, "ok")

	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		result, err := _mgr.SignWalletMessage(msg)
		if err != nil {
			return nil, -1, err.Error()
		}
		return map[string]any{
			"signature": base64.StdEncoding.EncodeToString(result),
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(handler)
}

func signData(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}

	if len(p) < 1 {
		return createJsRet(nil, -1, "Expected 1 parameters")
	}

	// jsBytes := p[0]
	// goBytes := make([]byte, jsBytes.Length())
	// js.CopyBytesToGo(goBytes, jsBytes)
	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "message parameter should be a string")
	}
	msg := p[0].String()

	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		// 一般前端给json序列化后的string，直接转成[]byte
		result, err := _mgr.SignMessage([]byte(msg))
		if err != nil {
			return nil, -1, err.Error()
		}
		return map[string]any{
			"signature": hex.EncodeToString(result),
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(handler)
}

func signPsbt(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}

	if len(p) < 2 {
		return createJsRet(nil, -1, "Expected 2 parameters")
	}

	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "psbt parameter should be a hex string")
	}
	psbtHex := p[0].String()

	if p[1].Type() != js.TypeBoolean {
		return createJsRet(nil, -1, "extract parameter should be a bool")
	}
	extract := p[1].Bool()

	// result, err := _mgr.SignPsbt(psbtHex)
	// if err != nil {
	// 	return createJsRet(nil, -1, err.Error())
	// }
	// data := map[string]any{
	// 	"psbt": result,
	// }
	// return createJsRet(data, 0, "ok")

	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		result, err := _mgr.SignPsbt(psbtHex, extract)
		if err != nil {
			return nil, -1, err.Error()
		}

		return map[string]any{
			"psbt": result,
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(handler)
}

func signPsbts(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}

	if len(p) < 2 {
		return createJsRet(nil, -1, "Expected 2 parameters")
	}

	psbtsHex, err := getStringVector(p[0])
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}

	if p[1].Type() != js.TypeBoolean {
		return createJsRet(nil, -1, "extract parameter should be a bool")
	}
	extract := p[1].Bool()

	// result, err := _mgr.SignPsbt(psbtHex)
	// if err != nil {
	// 	return createJsRet(nil, -1, err.Error())
	// }
	// data := map[string]any{
	// 	"psbt": result,
	// }
	// return createJsRet(data, 0, "ok")

	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		result, err := _mgr.SignPsbts(psbtsHex, extract)
		if err != nil {
			return nil, -1, err.Error()
		}

		r := make([]interface{}, 0, len(result))
		for _, psbt := range result {
			r = append(r, psbt)
		}

		return map[string]any{
			"psbts": r,
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(handler)
}

func signPsbt_SatsNet(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}

	if len(p) < 2 {
		return createJsRet(nil, -1, "Expected 1 parameters")
	}

	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "psbt parameter should be a hex string")
	}
	psbtHex := p[0].String()

	if p[1].Type() != js.TypeBoolean {
		return createJsRet(nil, -1, "extract parameter should be a bool")
	}
	extract := p[1].Bool()

	// result, err := _mgr.SignPsbt_SatsNet(psbtHex)
	// if err != nil {
	// 	return createJsRet(nil, -1, err.Error())
	// }
	// data := map[string]any{
	// 	"psbt": result,
	// }
	// return createJsRet(data, 0, "ok")

	wallet.Log.Infof("SignPsbt_SatsNet  input: %s", psbtHex)
	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		result, err := _mgr.SignPsbt_SatsNet(psbtHex, extract)
		if err != nil {
			return nil, -1, err.Error()
		}
		wallet.Log.Infof("SignPsbt_SatsNet output: %s", result)
		return map[string]any{
			"psbt": result,
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(handler)
}

func signPsbts_SatsNet(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}

	if len(p) < 2 {
		return createJsRet(nil, -1, "Expected 2 parameters")
	}

	psbtsHex, err := getStringVector(p[0])
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}

	if p[1].Type() != js.TypeBoolean {
		return createJsRet(nil, -1, "extract parameter should be a bool")
	}
	extract := p[1].Bool()

	// result, err := _mgr.SignPsbt(psbtHex)
	// if err != nil {
	// 	return createJsRet(nil, -1, err.Error())
	// }
	// data := map[string]any{
	// 	"psbt": result,
	// }
	// return createJsRet(data, 0, "ok")

	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		result, err := _mgr.SignPsbts_SatsNet(psbtsHex, extract)
		if err != nil {
			return nil, -1, err.Error()
		}

		r := make([]interface{}, 0, len(result))
		for _, psbt := range result {
			r = append(r, psbt)
		}

		return map[string]any{
			"psbts": r,
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(handler)
}

func extractTxFromPsbt(this js.Value, p []js.Value) any {

	if len(p) < 1 {
		return createJsRet(nil, -1, "Expected 1 parameters")
	}

	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "psbt parameter should be a hex string")
	}
	psbtHex := p[0].String()

	result, err := wallet.ExtractTxFromPsbt(psbtHex)
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}

	data := map[string]any{
		"tx": result,
	}

	return createJsRet(data, 0, "ok")
}

func extractTxFromPsbt_SatsNet(this js.Value, p []js.Value) any {

	if len(p) < 1 {
		return createJsRet(nil, -1, "Expected 1 parameters")
	}

	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "psbt parameter should be a hex string")
	}
	psbtHex := p[0].String()

	result, err := wallet.ExtractTxFromPsbt_SatsNet(psbtHex)
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}

	data := map[string]any{
		"tx": result,
	}

	return createJsRet(data, 0, "ok")
}

func extractUnsignedTxFromPsbt(this js.Value, p []js.Value) any {

	if len(p) < 1 {
		return createJsRet(nil, -1, "Expected 1 parameters")
	}

	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "psbt parameter should be a hex string")
	}
	psbtHex := p[0].String()

	result, err := wallet.ExtractUnsignedTxFromPsbt(psbtHex)
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}

	data := map[string]any{
		"tx": result,
	}

	return createJsRet(data, 0, "ok")
}

func extractUnsignedTxFromPsbt_SatsNet(this js.Value, p []js.Value) any {

	if len(p) < 1 {
		return createJsRet(nil, -1, "Expected 1 parameters")
	}

	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "psbt parameter should be a hex string")
	}
	psbtHex := p[0].String()

	result, err := wallet.ExtractUnsignedTxFromPsbt_SatsNet(psbtHex)
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}

	data := map[string]any{
		"tx": result,
	}

	return createJsRet(data, 0, "ok")
}

func buildBatchSellOrder_SatsNet(this js.Value, p []js.Value) any {

	if len(p) < 3 {
		return createJsRet(nil, -1, "Expected 3 parameters")
	}

	utxoList, err := getStringVector(p[0])
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}

	if p[1].Type() != js.TypeString {
		return createJsRet(nil, -1, "address parameter should be a string")
	}
	address := p[1].String()

	if p[2].Type() != js.TypeString {
		return createJsRet(nil, -1, "network parameter should be a string")
	}
	network := p[2].String()

	result, err := wallet.BuildBatchSellOrder_SatsNet(utxoList, address, network)
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	wallet.Log.Infof("BuildBatchSellOrder: %s", result)

	data := map[string]any{
		"psbt": result,
	}

	return createJsRet(data, 0, "ok")
}

func splitBatchSignedPsbt_SatsNet(this js.Value, p []js.Value) any {

	if len(p) < 2 {
		return createJsRet(nil, -1, "Expected 2 parameters")
	}

	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "psbt parameter should be a string")
	}
	psbt := p[0].String()

	if p[1].Type() != js.TypeString {
		return createJsRet(nil, -1, "network parameter should be a string")
	}
	network := p[1].String()

	wallet.Log.Infof("SplitBatchSignedPsbt_SatsNet %s %s", psbt, network)
	result, err := wallet.SplitBatchSignedPsbt_SatsNet(psbt, network)
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	wallet.Log.Infof("SplitBatchSignedPsbt_SatsNet: %s", result)

	var str []interface{}
	for _, r := range result {
		str = append(str, r)
	}

	data := map[string]any{
		"psbts": str,
	}

	return createJsRet(data, 0, "ok")
}

func mergeBatchSignedPsbt_SatsNet(this js.Value, p []js.Value) any {

	if len(p) < 2 {
		return createJsRet(nil, -1, "Expected 2 parameters")
	}

	psbtsHex, err := getStringVector(p[0])
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}

	if p[1].Type() != js.TypeString {
		return createJsRet(nil, -1, "network parameter should be a string")
	}
	network := p[1].String()

	wallet.Log.Infof("MergeBatchSignedPsbt %s %s", psbtsHex, network)
	result, err := wallet.MergeBatchSignedPsbt_SatsNet(psbtsHex, network)
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	wallet.Log.Infof("MergeBatchSignedPsbt: %s", result)

	data := map[string]any{
		"psbt": result,
	}

	return createJsRet(data, 0, "ok")
}

func finalizeSellOrder_SatsNet(this js.Value, p []js.Value) any {

	if len(p) < 7 {
		return createJsRet(nil, -1, "Expected 7 parameters")
	}

	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "psbt parameter should be a string")
	}
	psbt := p[0].String()

	utxoList, err := getStringVector(p[1])
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}

	if p[2].Type() != js.TypeString {
		return createJsRet(nil, -1, "buyer address parameter should be a string")
	}
	address1 := p[2].String()

	if p[3].Type() != js.TypeString {
		return createJsRet(nil, -1, "server address parameter should be a string")
	}
	address2 := p[3].String()

	if p[4].Type() != js.TypeString {
		return createJsRet(nil, -1, "network parameter should be a string")
	}
	network := p[4].String()

	if p[5].Type() != js.TypeNumber {
		return createJsRet(nil, -1, "serviceFee parameter should be a number")
	}
	serviceFee := p[5].Int()

	if p[6].Type() != js.TypeNumber {
		return createJsRet(nil, -1, "networkFee parameter should be a number")
	}
	networkFee := p[6].Int()

	result, err := wallet.FinalizeSellOrder_SatsNet(psbt, utxoList, address1, address2, network, int64(serviceFee), int64(networkFee))
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	wallet.Log.Infof("FinalizeSellOrder: %s", result)

	data := map[string]any{
		"psbt": result,
	}

	return createJsRet(data, 0, "ok")
}

func addInputsToPsbt_SatsNet(this js.Value, p []js.Value) any {

	if len(p) < 2 {
		return createJsRet(nil, -1, "Expected 2 parameters")
	}

	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "psbt parameter should be a string")
	}
	psbt := p[0].String()

	utxoList, err := getStringVector(p[1])
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}

	result, err := wallet.AddInputsToPsbt_SatsNet(psbt, utxoList)
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}

	data := map[string]any{
		"psbt": result,
	}

	return createJsRet(data, 0, "ok")
}

func addOutputsToPsbt_SatsNet(this js.Value, p []js.Value) any {

	if len(p) < 2 {
		return createJsRet(nil, -1, "Expected 2 parameters")
	}

	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "psbt parameter should be a string")
	}
	psbt := p[0].String()

	utxoList, err := getStringVector(p[1])
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}

	result, err := wallet.AddOutputsToPsbt_SatsNet(psbt, utxoList)
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}

	data := map[string]any{
		"psbt": result,
	}

	return createJsRet(data, 0, "ok")
}

func getVersion(this js.Value, p []js.Value) any {
	data := map[string]any{
		"version": wallet.SOFTWARE_VERSION,
	}
	return createJsRet(data, 0, "ok")
}

func callbackFunc(event string, data interface{}) {
	if _callback != nil {
		_callback.(js.Value).Invoke(event, js.ValueOf(data))
	}
}

func registerCallbacks(this js.Value, args []js.Value) interface{} {
	code := 0
	msg := "ok"
	if len(args) != 1 {
		return nil
	}
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	_callback = args[0]
	_mgr.RegisterCallback(callbackFunc)
	return createJsRet(nil, code, msg)

}

func getStringVector(p js.Value) ([]string, error) {
	if p.Type() != js.TypeObject || !p.InstanceOf(js.Global().Get("Array")) {
		return nil, fmt.Errorf("parameter should be an string array")
	}
	arrayLength := p.Length()
	strs := make([]string, arrayLength)
	for i := 0; i < arrayLength; i++ {
		item := p.Index(i)
		if item.Type() != js.TypeString {
			return nil, fmt.Errorf("Array item at index %d is not a string", i)
		}
		strs[i] = item.String()
	}
	return strs, nil
}

func sendGarbage(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}

	if len(p) < 4 {
		return createJsRet(nil, -1, "Expected 4 parameters")
	}

	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "destAddr parameter should be a string")
	}
	destAddress := p[0].String()

	utxos, err := getStringVector(p[1])
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}

	// amount
	p2 := p[2]
	if p2.Type() != js.TypeNumber {
		return createJsRet(nil, -1, "amount parameter should be a number")
	}
	amt := p2.Int()

	if p[3].Type() != js.TypeString {
		return createJsRet(nil, -1, "feeRate parameter should be a string")
	}
	feeRate := p[3].String()
	feeRate64, err := strconv.ParseInt(feeRate, 10, 64)
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		tx, err := _mgr.SendGarbage(destAddress, utxos, int64(amt), feeRate64)
		if err != nil {
			wallet.Log.Errorf("SendGarbage error: %v", err)
			return nil, -1, err.Error()
		}

		return map[string]interface{}{
			"txId": tx.TxID(),
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func sendAssets(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}

	if len(p) < 4 {
		return createJsRet(nil, -1, "Expected 4 parameters")
	}

	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "destAddr parameter should be a string")
	}
	destAddress := p[0].String()

	if p[1].Type() != js.TypeString {
		return createJsRet(nil, -1, "asset name parameter should be a string")
	}
	assetName := p[1].String()

	// amount
	p2 := p[2]
	if p2.Type() != js.TypeString {
		return createJsRet(nil, -1, "amount parameter should be a string")
	}
	amt := p2.String()

	if p[3].Type() != js.TypeString {
		return createJsRet(nil, -1, "feeRate parameter should be a string")
	}
	feeRate := p[3].String()
	feeRate64, err := strconv.ParseInt(feeRate, 10, 64)
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		tx, err := _mgr.SendAssets(destAddress, assetName, amt, feeRate64, nil)
		if err != nil {
			wallet.Log.Errorf("SendAssets error: %v", err)
			return nil, -1, err.Error()
		}

		return map[string]interface{}{
			"txId": tx.TxID(),
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func sendAssets_SatsNet(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}

	if len(p) < 4 {
		return createJsRet(nil, -1, "Expected 4 parameters")
	}

	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "destAddr parameter should be a string")
	}
	destAddress := p[0].String()

	if p[1].Type() != js.TypeString {
		return createJsRet(nil, -1, "asset name parameter should be a string")
	}
	assetName := p[1].String()

	// amount
	p2 := p[2]
	if p2.Type() != js.TypeString {
		return createJsRet(nil, -1, "amount parameter should be a string")
	}
	amt := p2.String()

	p3 := p[3]
	if p3.Type() != js.TypeString {
		return createJsRet(nil, -1, "memo parameter should be a hex string")
	}
	m := p3.String()
	var memo []byte
	if m != "" {
		var err error
		memo, err = hex.DecodeString(m)
		if err != nil {
			return createJsRet(nil, -1, "memo parameter should be a hex string")
		}
	}

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		tx, err := _mgr.SendAssets_SatsNet(destAddress, assetName, amt, memo)
		if err != nil {
			wallet.Log.Errorf("SendAssets_SatsNet error: %v", err)
			return nil, -1, err.Error()
		}

		return map[string]interface{}{
			"txId": tx.TxID(),
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func batchSendAssets_SatsNet(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}

	if len(p) < 4 {
		return createJsRet(nil, -1, "Expected 4 parameters")
	}

	pn := p[0]
	if pn.Type() != js.TypeString {
		return createJsRet(nil, -1, "destAddr parameter should be a string")
	}
	destAddress := pn.String()

	pn = p[1]
	if pn.Type() != js.TypeString {
		return createJsRet(nil, -1, "asset name parameter should be a string")
	}
	assetName := pn.String()

	// amount
	pn = p[2]
	if pn.Type() != js.TypeString {
		return createJsRet(nil, -1, "amount parameter should be a string")
	}
	amt := pn.String()

	pn = p[3]
	if pn.Type() != js.TypeNumber {
		return createJsRet(nil, -1, "n parameter should be a int")
	}
	n := pn.Int()

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		txid, err := _mgr.BatchSendAssets_SatsNet(destAddress, assetName, amt, n)
		if err != nil {
			wallet.Log.Errorf("BatchSendAssets_SatsNet error: %v", err)
			return nil, -1, err.Error()
		}

		return map[string]interface{}{
			"txId": txid,
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func batchSendAssets(this js.Value, p []js.Value) any {

	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}

	if len(p) < 5 {
		return createJsRet(nil, -1, "Expected 5 parameters")
	}

	pn := p[0]
	if pn.Type() != js.TypeString {
		return createJsRet(nil, -1, "destAddr parameter should be a string")
	}
	destAddress := pn.String()

	pn = p[1]
	if pn.Type() != js.TypeString {
		return createJsRet(nil, -1, "asset name parameter should be a string")
	}
	assetName := pn.String()

	// amount
	pn = p[2]
	if pn.Type() != js.TypeString {
		return createJsRet(nil, -1, "amount parameter should be a string")
	}
	amt := pn.String()

	pn = p[3]
	if pn.Type() != js.TypeNumber {
		return createJsRet(nil, -1, "n parameter should be a int")
	}
	n := pn.Int()

	pn = p[4]
	if pn.Type() != js.TypeString {
		return createJsRet(nil, -1, "feeRate parameter should be a string")
	}
	feeRate := pn.String()
	feeRate64, err := strconv.ParseInt(feeRate, 10, 64)
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		tx, fee, err := _mgr.BatchSendAssets(destAddress, assetName, amt, n, feeRate64, nil)
		if err != nil {
			wallet.Log.Errorf("BatchSendAssets error: %v", err)
			return nil, -1, err.Error()
		}

		return map[string]interface{}{
			"txId": tx.TxID(),
			"fee":  fee,
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func batchSendAssetsV2_SatsNet(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}

	if len(p) < 3 {
		return createJsRet(nil, -1, "Expected 3 parameters")
	}

	pn := p[0]
	destAddress, err := getStringVector(pn)
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}

	pn = p[1]
	if pn.Type() != js.TypeString {
		return createJsRet(nil, -1, "asset name parameter should be a string")
	}
	assetName := pn.String()

	// amount
	pn = p[2]
	amt, err := getStringVector(pn)
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		txid, err := _mgr.BatchSendAssetsV2_SatsNet(destAddress, assetName, amt, nil)
		if err != nil {
			wallet.Log.Errorf("BatchSendAssetsV2_SatsNet error: %v", err)
			return nil, -1, err.Error()
		}

		return map[string]interface{}{
			"txId": txid,
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func getTxAssetInfoFromPsbt(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(p) < 1 {
		return createJsRet(nil, -1, "Expected 1 parameters")
	}

	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "psbt parameter should be a string")
	}
	psbt := p[0].String()

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		info, err := _mgr.GetTxAssetInfoFromPsbt(psbt)
		if err != nil {
			wallet.Log.Errorf("GetTxAssetInfoFromPsbt error: %v", err)
			return nil, -1, err.Error()
		}

		inputs, err := json.Marshal(info.InputAssets)
		if err != nil {
			return nil, -1, err.Error()
		}

		outputs, err := json.Marshal(info.OutputAssets)
		if err != nil {
			return nil, -1, err.Error()
		}

		return map[string]any{
			"txId":    info.TxId,
			"txHex":   info.TxHex,
			"inputs":  string(inputs),
			"outputs": string(outputs),
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func getTxAssetInfoFromPsbt_SatsNet(this js.Value, p []js.Value) any {
	if len(p) < 1 {
		return createJsRet(nil, -1, "Expected 2 parameters")
	}

	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "psbt parameter should be a string")
	}
	psbt := p[0].String()

	info, err := wallet.GetTxAssetInfoFromPsbt_SatsNet(psbt)
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}

	inputs, err := json.Marshal(info.InputAssets)
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}

	outputs, err := json.Marshal(info.OutputAssets)
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}

	data := map[string]any{
		"txId":    info.TxId,
		"txHex":   info.TxHex,
		"inputs":  string(inputs),
		"outputs": string(outputs),
	}

	return createJsRet(data, 0, "ok")
}

func getTickerInfo(this js.Value, p []js.Value) any {
	code := 0
	msg := "ok"
	if _mgr == nil {
		code = -1
		msg = "Manager not initialized"
		return createJsRet(nil, code, msg)
	}
	if len(p) < 1 {
		code = -1
		msg = "Expected 1 parameters"
		wallet.Log.Error(msg)
		return createJsRet(nil, code, msg)
	}

	if p[0].Type() != js.TypeString {
		code = -1
		msg = "asset name parameter should be a string"
		wallet.Log.Error(msg)
		return createJsRet(nil, code, msg)
	}
	assetName := p[0].String()

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		tickerInfo := _mgr.GetTickerInfoV2(assetName)
		if tickerInfo == nil {
			wallet.Log.Errorf("GetTickerInfo error ")
			return nil, -1, "GetTickerInfo error"
		}

		jsonStr, err := json.Marshal(tickerInfo)
		if err != nil {
			return nil, -1, err.Error()
		}

		return map[string]interface{}{
			"ticker": string(jsonStr),
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func lockUtxo(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(p) < 3 {
		return createJsRet(nil, -1, "Expected 3 parameters")
	}

	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "address parameter should be a string")
	}
	address := p[0].String()

	if p[1].Type() != js.TypeString {
		return createJsRet(nil, -1, "utxo parameter should be a string")
	}
	utxo := p[1].String()

	if p[2].Type() != js.TypeString {
		return createJsRet(nil, -1, "reason  parameter should be a string")
	}
	reason := p[2].String()

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		err := _mgr.LockUtxo(address, utxo, reason)
		if err != nil {
			return nil, -1, err.Error()
		}
		return nil, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func unlockUtxo(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(p) < 2 {
		return createJsRet(nil, -1, "Expected 2 parameters")
	}
	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "address parameter should be a string")
	}
	address := p[0].String()

	if p[1].Type() != js.TypeString {
		return createJsRet(nil, -1, "utxo parameter should be a string")
	}
	utxo := p[1].String()

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		err := _mgr.UnlockUtxo(address, utxo)
		if err != nil {
			return nil, -1, err.Error()
		}
		return nil, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func isUtxoLocked(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(p) < 2 {
		return createJsRet(nil, -1, "Expected 2 parameters")
	}

	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "address parameter should be a string")
	}
	address := p[0].String()

	if p[1].Type() != js.TypeString {
		return createJsRet(nil, -1, "utxo parameter should be a string")
	}
	utxo := p[1].String()

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		r := _mgr.IsLocked(address, utxo)
		return r, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func getAllLockedUtxo(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}

	if len(p) < 1 {
		return createJsRet(nil, -1, "Expected 1 parameters")
	}

	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "address parameter should be a string")
	}
	address := p[0].String()

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		utxoMap, err := _mgr.GetLockedUtxoList(address)
		if err != nil {
			return nil, -1, err.Error()
		}

		result := make(map[string]any)
		for k, v := range utxoMap {
			buf, err := json.Marshal(v)
			if err != nil {
				continue
			}
			result[k] = string(buf)
		}
		return result, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func lockUtxo_SatsNet(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(p) < 3 {
		return createJsRet(nil, -1, "Expected 3 parameters")
	}

	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "address parameter should be a string")
	}
	address := p[0].String()

	if p[1].Type() != js.TypeString {
		return createJsRet(nil, -1, "utxo parameter should be a string")
	}
	utxo := p[1].String()

	if p[2].Type() != js.TypeString {
		return createJsRet(nil, -1, "reason  parameter should be a string")
	}
	reason := p[2].String()

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		err := _mgr.LockUtxo_SatsNet(address, utxo, reason)
		if err != nil {
			return nil, -1, err.Error()
		}
		return nil, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func unlockUtxo_SatsNet(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(p) < 2 {
		return createJsRet(nil, -1, "Expected 2 parameters")
	}

	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "address parameter should be a string")
	}
	address := p[0].String()

	if p[1].Type() != js.TypeString {
		return createJsRet(nil, -1, "utxo parameter should be a string")
	}
	utxo := p[1].String()

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		err := _mgr.UnlockUtxo_SatsNet(address, utxo)
		if err != nil {
			return nil, -1, err.Error()
		}
		return nil, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func isUtxoLocked_SatsNet(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(p) < 2 {
		return createJsRet(nil, -1, "Expected 2 parameters")
	}

	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "address parameter should be a string")
	}
	address := p[0].String()

	if p[1].Type() != js.TypeString {
		return createJsRet(nil, -1, "utxo parameter should be a string")
	}
	utxo := p[1].String()

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		r := _mgr.IsLocked_SatsNet(address, utxo)
		return r, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func getAllLockedUtxo_SatsNet(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}

	if len(p) < 1 {
		return createJsRet(nil, -1, "Expected 1 parameters")
	}

	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "address parameter should be a string")
	}
	address := p[0].String()

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		utxoMap, err := _mgr.GetLockedUtxoList_SatsNet(address)
		if err != nil {
			return nil, -1, err.Error()
		}

		result := make(map[string]any)
		for k, v := range utxoMap {
			buf, err := json.Marshal(v)
			if err != nil {
				continue
			}
			result[k] = string(buf)
		}
		return result, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func getUtxosWithAsset(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}

	if len(p) < 3 {
		return createJsRet(nil, -1, "Expected 3 parameters")
	}

	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "address parameter should be a string")
	}
	address := p[0].String()

	if p[1].Type() != js.TypeString {
		return createJsRet(nil, -1, "amt parameter should be a string")
	}
	amt := p[1].String()

	if p[2].Type() != js.TypeString {
		return createJsRet(nil, -1, "asset name parameter should be a string")
	}
	assetName := p[2].String()

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		utxos, err := _mgr.GetUtxosWithAssetForJS(address, amt, assetName)
		if err != nil {
			return nil, -1, err.Error()
		}

		result := make([]interface{}, 0)
		for _, v := range utxos {
			result = append(result, v)
		}
		return map[string]interface{}{
			"utxos": result,
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func getUtxosWithAsset_SatsNet(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}

	if len(p) < 3 {
		return createJsRet(nil, -1, "Expected 3 parameters")
	}

	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "address parameter should be a string")
	}
	address := p[0].String()

	if p[1].Type() != js.TypeString {
		return createJsRet(nil, -1, "amt parameter should be a string")
	}
	amt := p[1].String()

	if p[2].Type() != js.TypeString {
		return createJsRet(nil, -1, "asset name parameter should be a string")
	}
	assetName := p[2].String()

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		utxos, err := _mgr.GetUtxosWithAssetForJS_SatsNet(address, amt, assetName)
		if err != nil {
			return nil, -1, err.Error()
		}

		result := make([]interface{}, 0)
		for _, v := range utxos {
			result = append(result, v)
		}
		return map[string]interface{}{
			"utxos": result,
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func getUtxosWithAssetV2(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}

	if len(p) < 4 {
		return createJsRet(nil, -1, "Expected 4 parameters")
	}

	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "address parameter should be a string")
	}
	address := p[0].String()

	if p[1].Type() != js.TypeNumber {
		return createJsRet(nil, -1, "value parameter should be a number")
	}
	value := p[1].Int()

	if p[2].Type() != js.TypeString {
		return createJsRet(nil, -1, "amt parameter should be a string")
	}
	amt := p[2].String()

	if p[3].Type() != js.TypeString {
		return createJsRet(nil, -1, "asset name parameter should be a string")
	}
	assetName := p[3].String()

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		utxos, fees, err := _mgr.GetUtxosWithAssetV2ForJS(address, int64(value), amt, assetName)
		if err != nil {
			return nil, -1, err.Error()
		}

		result := make([]interface{}, 0)
		for _, v := range utxos {
			result = append(result, v)
		}
		result2 := make([]interface{}, 0)
		for _, v := range fees {
			result2 = append(result2, v)
		}
		return map[string]interface{}{
			"utxos": result,
			"fees":  result2,
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func getUtxosWithAssetV2_SatsNet(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}

	if len(p) < 4 {
		return createJsRet(nil, -1, "Expected 4 parameters")
	}

	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "address parameter should be a string")
	}
	address := p[0].String()

	if p[1].Type() != js.TypeNumber {
		return createJsRet(nil, -1, "value parameter should be a number")
	}
	value := p[1].Int()

	if p[2].Type() != js.TypeString {
		return createJsRet(nil, -1, "amt parameter should be a string")
	}
	amt := p[2].String()

	if p[3].Type() != js.TypeString {
		return createJsRet(nil, -1, "asset name parameter should be a string")
	}
	assetName := p[3].String()

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		utxos, fees, err := _mgr.GetUtxosWithAssetV2ForJS_SatsNet(address, int64(value), amt, assetName)
		if err != nil {
			return nil, -1, err.Error()
		}

		result := make([]interface{}, 0)
		for _, v := range utxos {
			result = append(result, v)
		}
		result2 := make([]interface{}, 0)
		for _, v := range fees {
			result2 = append(result2, v)
		}
		return map[string]interface{}{
			"utxos": result,
			"fees":  result2,
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func getAssetAmount(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}

	if len(p) < 2 {
		return createJsRet(nil, -1, "Expected 2 parameters")
	}

	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "address parameter should be a string")
	}
	address := p[0].String()

	if p[1].Type() != js.TypeString {
		return createJsRet(nil, -1, "asset name parameter should be a string")
	}
	assetName := p[1].String()

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		available, locked, err := _mgr.GetAssetAmountForJS(address, assetName)
		if err != nil {
			return nil, -1, err.Error()
		}

		return map[string]any{
			"availableAmt": available.String(),
			"lockedAmt":    locked.String(),
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func getAssetAmount_SatsNet(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}

	if len(p) < 2 {
		return createJsRet(nil, -1, "Expected 2 parameters")
	}

	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "address parameter should be a string")
	}
	address := p[0].String()

	if p[1].Type() != js.TypeString {
		return createJsRet(nil, -1, "asset name parameter should be a string")
	}
	assetName := p[1].String()

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		available, locked, err := _mgr.GetAssetAmountForJS_SatsNet(address, assetName)
		if err != nil {
			return nil, -1, err.Error()
		}

		return map[string]any{
			"availableAmt": available.String(),
			"lockedAmt":    locked.String(),
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func contractTxResultForJS(result *wallet.ContractTxResult) map[string]any {
	if result == nil {
		return nil
	}
	resultJSON, _ := json.Marshal(result)
	return map[string]any{
		"result":          string(resultJSON),
		"contractType":    result.ContractType,
		"txid":            result.TxID,
		"contractAddress": result.ContractAddress,
		"caller":          result.Caller,
		"gasAssetAmount":  strconv.FormatInt(result.GasAssetAmount, 10),
		"gasFeeAmount":    strconv.FormatInt(result.GasFeeAmount, 10),
		"gasFundAmount":   strconv.FormatInt(result.GasFundAmount, 10),
		"gasLimit":        strconv.FormatInt(result.GasLimit, 10),
		"nonce":           strconv.FormatUint(result.Nonce, 10),
	}
}

func queryContract(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(p) < 1 {
		return createJsRet(nil, -1, "Expected 1 parameter")
	}
	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "contract query request parameter should be a json string")
	}
	reqJSON := p[0].String()

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		var req wallet.ContractQueryRequest
		if err := json.Unmarshal([]byte(reqJSON), &req); err != nil {
			return nil, -1, err.Error()
		}
		result, err := _mgr.QueryContract(&req)
		if err != nil {
			return nil, -1, err.Error()
		}
		return map[string]any{"result": result}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func buildUnifiedContractContent(this js.Value, p []js.Value) any {
	if len(p) < 3 {
		return createJsRet(nil, -1, "Expected 3 parameters")
	}
	for i := 0; i < 3; i++ {
		if p[i].Type() != js.TypeString {
			return createJsRet(nil, -1, "contract type, subtype and content parameters should be strings")
		}
	}
	contractType := p[0].String()
	subtype := p[1].String()
	jsonContent := p[2].String()

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		content, err := wallet.BuildUnifiedContractContent(contractType, subtype, jsonContent)
		if err != nil {
			return nil, -1, err.Error()
		}
		return map[string]any{
			"content":         content,
			"contentEncoding": "base64",
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func deployUnifiedContract(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(p) < 1 {
		return createJsRet(nil, -1, "Expected 1 parameter")
	}
	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "contract deploy request parameter should be a json string")
	}
	reqJSON := p[0].String()

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		var req wallet.ContractDeployRequest
		if err := json.Unmarshal([]byte(reqJSON), &req); err != nil {
			return nil, -1, err.Error()
		}
		result, err := _mgr.DeployUnifiedContract(&req)
		if err != nil {
			return nil, -1, err.Error()
		}
		return contractTxResultForJS(result), 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func estimateDeployUnifiedContract(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(p) < 1 {
		return createJsRet(nil, -1, "Expected 1 parameter")
	}
	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "contract deploy request parameter should be a json string")
	}
	reqJSON := p[0].String()

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		var req wallet.ContractDeployRequest
		if err := json.Unmarshal([]byte(reqJSON), &req); err != nil {
			return nil, -1, err.Error()
		}
		result, err := _mgr.EstimateDeployUnifiedContract(&req)
		if err != nil {
			return nil, -1, err.Error()
		}
		return contractTxResultForJS(result), 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func invokeUnifiedContract(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(p) < 1 {
		return createJsRet(nil, -1, "Expected 1 parameter")
	}
	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "contract invoke request parameter should be a json string")
	}
	reqJSON := p[0].String()

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		var req wallet.ContractInvokeRequest
		if err := json.Unmarshal([]byte(reqJSON), &req); err != nil {
			return nil, -1, err.Error()
		}
		result, err := _mgr.InvokeUnifiedContract(&req)
		if err != nil {
			return nil, -1, err.Error()
		}
		return contractTxResultForJS(result), 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func getParamForInvokeUnifiedContract(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(p) < 3 {
		return createJsRet(nil, -1, "Expected 3 parameters")
	}
	for i := 0; i < 3; i++ {
		if p[i].Type() != js.TypeString {
			return createJsRet(nil, -1, "contract type, subtype and action parameters should be strings")
		}
	}
	contractType := p[0].String()
	subtype := p[1].String()
	action := p[2].String()

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		param, err := _mgr.QueryParamForInvokeUnifiedContract(contractType, subtype, action)
		if err != nil {
			return nil, -1, err.Error()
		}
		return map[string]interface{}{
			"parameter": param,
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func getFeeForInvokeUnifiedContract(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(p) < 1 {
		return createJsRet(nil, -1, "Expected 1 parameter")
	}
	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "contract invoke request parameter should be a json string")
	}
	reqJSON := p[0].String()

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		var req wallet.ContractInvokeRequest
		if err := json.Unmarshal([]byte(reqJSON), &req); err != nil {
			return nil, -1, err.Error()
		}
		fee, err := _mgr.QueryFeeForInvokeUnifiedContract(&req)
		if err != nil {
			return nil, -1, err.Error()
		}
		return map[string]interface{}{
			"fee": strconv.FormatInt(fee, 10),
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func getSupportedContracts(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		contracts, err := _mgr.GetSupportContractInServer()
		if err != nil {
			return nil, -1, err.Error()
		}

		cs := make([]interface{}, 0, len(contracts))
		for _, c := range contracts {
			cs = append(cs, c)
		}

		return map[string]any{
			"contractContents": cs,
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func getDeployedContractsInServer(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		contracts, err := _mgr.GetDeployedContractInServer()
		if err != nil {
			return nil, -1, err.Error()
		}

		cs := make([]interface{}, 0, len(contracts))
		for _, c := range contracts {
			cs = append(cs, c)
		}

		return map[string]any{
			"contractURLs": cs,
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func getDeployedContractStatus(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}

	if len(p) < 1 {
		return createJsRet(nil, -1, "Expected 1 parameters")
	}

	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "contract URL parameter should be a string")
	}
	url := p[0].String()

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		status, err := _mgr.GetContractStatusInServer(url)
		if err != nil {
			return nil, -1, err.Error()
		}

		return map[string]any{
			"contractStatus": status,
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func getDeployedContractAnalytics(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}

	if len(p) < 1 {
		return createJsRet(nil, -1, "Expected 1 parameters")
	}

	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "contract URL parameter should be a string")
	}
	url := p[0].String()

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		status, err := _mgr.GetContractAnalyticsInServer(url)
		if err != nil {
			return nil, -1, err.Error()
		}

		return map[string]any{
			"Analytics": status,
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func getFeeForDeployContract(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}

	if len(p) < 3 {
		return createJsRet(nil, -1, "Expected 3 parameters")
	}

	pn := p[0]
	if pn.Type() != js.TypeString {
		return createJsRet(nil, -1, "contract template name parameter should be a string")
	}
	templateName := pn.String()

	pn = p[1]
	if pn.Type() != js.TypeString {
		return createJsRet(nil, -1, "contract content parameter should be a json string")
	}
	content := pn.String()

	pn = p[2]
	if pn.Type() != js.TypeString {
		return createJsRet(nil, -1, "feeRate parameter should be a string")
	}
	feeRate := pn.String()
	feeRate64, err := strconv.ParseInt(feeRate, 10, 64)
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		fee, err := _mgr.QueryFeeForDeployContract(templateName, (content), feeRate64)
		if err != nil {
			return nil, -1, err.Error()
		}

		return map[string]any{
			"fee": fee,
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func deployContract_Remote(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}

	if len(p) < 4 {
		return createJsRet(nil, -1, "Expected 4 parameters")
	}

	pn := p[0]
	if pn.Type() != js.TypeString {
		return createJsRet(nil, -1, "contract template name parameter should be a string")
	}
	templateName := pn.String()

	pn = p[1]
	if pn.Type() != js.TypeString {
		return createJsRet(nil, -1, "contract content parameter should be a json string")
	}
	content := pn.String()

	pn = p[2]
	if pn.Type() != js.TypeString {
		return createJsRet(nil, -1, "feeRate parameter should be a string")
	}
	feeRate := pn.String()
	feeRate64, err := strconv.ParseInt(feeRate, 10, 64)
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}

	pn = p[3]
	if pn.Type() != js.TypeBoolean {
		return createJsRet(nil, -1, "sendInL1 parameter should be a json bool")
	}
	sendInL1 := pn.Bool()

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		txId, id, url, err := _mgr.DeployContract_Remote(templateName, content, feeRate64, sendInL1)
		if err != nil {
			return nil, -1, err.Error()
		}

		return map[string]interface{}{
			"txId":        txId,
			"resvId":      id,
			"contractURL": url,
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func getParamForInvokeContract(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}

	if len(p) < 1 {
		return createJsRet(nil, -1, "Expected 2 parameters")
	}

	pn := p[0]
	if pn.Type() != js.TypeString {
		return createJsRet(nil, -1, "contract template name parameter should be a string")
	}
	templateName := pn.String()

	pn = p[1]
	if pn.Type() != js.TypeString {
		return createJsRet(nil, -1, "action name parameter should be a string")
	}
	action := pn.String()

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		param, err := _mgr.QueryParamForInvokeContract(templateName, action)
		if err != nil {
			return nil, -1, err.Error()
		}

		return map[string]interface{}{
			"parameter": param,
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func getFeeForInvokeContract(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}

	if len(p) < 2 {
		return createJsRet(nil, -1, "Expected 2 parameters")
	}

	pn := p[0]
	if pn.Type() != js.TypeString {
		return createJsRet(nil, -1, "contract URL parameter should be a string")
	}
	url := pn.String()

	pn = p[1]
	if pn.Type() != js.TypeString {
		return createJsRet(nil, -1, "contract invoke parameter should be a json string")
	}
	invoke := pn.String()

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		_, fee, err := _mgr.QueryFeeForInvokeContract(url, (invoke))
		if err != nil {
			return nil, -1, err.Error()
		}

		return map[string]interface{}{
			"fee": fee,
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func invokeContractV2(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}

	if len(p) < 3 {
		return createJsRet(nil, -1, "Expected 5 parameters")
	}

	pn := p[0]
	if pn.Type() != js.TypeString {
		return createJsRet(nil, -1, "contract URL parameter should be a string")
	}
	url := pn.String()

	pn = p[1]
	if pn.Type() != js.TypeString {
		return createJsRet(nil, -1, "contract invoke parameter should be a json string")
	}
	invoke := pn.String()

	pn = p[2]
	if pn.Type() != js.TypeString {
		return createJsRet(nil, -1, "asset name parameter should be a string")
	}
	assetName := pn.String()

	// amount
	pn = p[3]
	if pn.Type() != js.TypeString {
		return createJsRet(nil, -1, "amount parameter should be a string")
	}
	amt := pn.String()

	pn = p[4]
	if pn.Type() != js.TypeString {
		return createJsRet(nil, -1, "feeRate parameter should be a string")
	}
	feeRate := pn.String()
	feeRate64, err := strconv.ParseInt(feeRate, 10, 64)
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		txId, err := _mgr.InvokeContractV2(url, invoke, assetName, amt, feeRate64)
		if err != nil {
			return nil, -1, err.Error()
		}

		return map[string]interface{}{
			"txId": txId,
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func invokeContract_SatsNet(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}

	if len(p) < 3 {
		return createJsRet(nil, -1, "Expected 3 parameters")
	}

	pn := p[0]
	if pn.Type() != js.TypeString {
		return createJsRet(nil, -1, "contract URL parameter should be a string")
	}
	url := pn.String()

	pn = p[1]
	if pn.Type() != js.TypeString {
		return createJsRet(nil, -1, "contract invoke parameter should be a json string")
	}
	invoke := pn.String()

	pn = p[2]
	if pn.Type() != js.TypeString {
		return createJsRet(nil, -1, "feeRate parameter should be a string")
	}
	feeRate := pn.String()
	feeRate64, err := strconv.ParseInt(feeRate, 10, 64)
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		txId, err := _mgr.InvokeContract_Satsnet(url, invoke, feeRate64)
		if err != nil {
			return nil, -1, err.Error()
		}

		return map[string]interface{}{
			"txId": txId,
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func invokeContractV2_SatsNet(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}

	if len(p) < 5 {
		return createJsRet(nil, -1, "Expected 5 parameters")
	}

	pn := p[0]
	if pn.Type() != js.TypeString {
		return createJsRet(nil, -1, "contract URL parameter should be a string")
	}
	url := pn.String()

	pn = p[1]
	if pn.Type() != js.TypeString {
		return createJsRet(nil, -1, "contract invoke parameter should be a json string")
	}
	invoke := pn.String()

	pn = p[2]
	if pn.Type() != js.TypeString {
		return createJsRet(nil, -1, "asset name parameter should be a string")
	}
	assetName := pn.String()

	// amount
	pn = p[3]
	if pn.Type() != js.TypeString {
		return createJsRet(nil, -1, "amount parameter should be a string")
	}
	amt := pn.String()

	pn = p[4]
	if pn.Type() != js.TypeString {
		return createJsRet(nil, -1, "feeRate parameter should be a string")
	}
	feeRate := pn.String()
	feeRate64, err := strconv.ParseInt(feeRate, 10, 64)
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		txId, err := _mgr.InvokeContractV2_Satsnet(url, invoke, assetName, amt, feeRate64)
		if err != nil {
			return nil, -1, err.Error()
		}

		return map[string]interface{}{
			"txId": txId,
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func getContractInvokeHistoryInServer(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}

	if len(p) < 3 {
		return createJsRet(nil, -1, "Expected 3 parameters")
	}

	pn := p[0]
	if pn.Type() != js.TypeString {
		return createJsRet(nil, -1, "contract URL parameter should be a string")
	}
	url := pn.String()

	pn = p[1]
	if pn.Type() != js.TypeNumber {
		return createJsRet(nil, -1, "start parameter should be a number")
	}
	start := pn.Int()

	pn = p[2]
	if pn.Type() != js.TypeNumber {
		return createJsRet(nil, -1, "limit parameter should be a number")
	}
	limit := pn.Int()

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		history, err := _mgr.GetContractInvokeHistoryInServer(url, start, limit)
		if err != nil {
			return nil, -1, err.Error()
		}

		return map[string]interface{}{
			"history": history,
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func getContractInvokeHistoryByAddressInServer(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}

	if len(p) < 4 {
		return createJsRet(nil, -1, "Expected 4 parameters")
	}

	pn := p[0]
	if pn.Type() != js.TypeString {
		return createJsRet(nil, -1, "contract URL parameter should be a string")
	}
	url := pn.String()

	pn = p[1]
	if pn.Type() != js.TypeString {
		return createJsRet(nil, -1, "address parameter should be a string")
	}
	address := pn.String()

	pn = p[2]
	if pn.Type() != js.TypeNumber {
		return createJsRet(nil, -1, "start parameter should be a number")
	}
	start := pn.Int()

	pn = p[3]
	if pn.Type() != js.TypeNumber {
		return createJsRet(nil, -1, "limit parameter should be a number")
	}
	limit := pn.Int()

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		history, err := _mgr.GetInvokeHistoryByAddressInContract(url, address, start, limit)
		if err != nil {
			return nil, -1, err.Error()
		}

		return map[string]interface{}{
			"history": history,
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func getAllAddressInContract(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}

	if len(p) < 3 {
		return createJsRet(nil, -1, "Expected 3 parameters")
	}

	pn := p[0]
	if pn.Type() != js.TypeString {
		return createJsRet(nil, -1, "contract URL parameter should be a string")
	}
	url := pn.String()

	pn = p[1]
	if pn.Type() != js.TypeNumber {
		return createJsRet(nil, -1, "start parameter should be a number")
	}
	start := pn.Int()

	pn = p[2]
	if pn.Type() != js.TypeNumber {
		return createJsRet(nil, -1, "limit parameter should be a number")
	}
	limit := pn.Int()

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		addresses, err := _mgr.GetAllAddressesInContract(url, start, limit)
		if err != nil {
			return nil, -1, err.Error()
		}

		return map[string]interface{}{
			"addresses": addresses,
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func stakeToBeMiner(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}

	if len(p) < 2 {
		return createJsRet(nil, -1, "Expected 2 parameters")
	}

	pn := p[0]
	if pn.Type() != js.TypeBoolean {
		return createJsRet(nil, -1, "as-a-core-node parameter should be a bool")
	}
	asACoreNode := pn.Bool()

	pn = p[1]
	if pn.Type() != js.TypeString {
		return createJsRet(nil, -1, "feeRate parameter should be a string")
	}
	feeRate := pn.String()
	feeRate64, err := strconv.ParseInt(feeRate, 10, 64)
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		txId, id, err := _mgr.StakeToBeMinner(asACoreNode, feeRate64)
		if err != nil {
			return nil, -1, err.Error()
		}

		height := _mgr.GetSyncHeightL1()
		return map[string]interface{}{
			"txId":      txId,
			"resvId":    id,
			"assetName": indexer.GetStakeAssetName(height),
			"amt":       indexer.GetStakeAssetAmt(height),
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func minerUnstake(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}

	if len(p) < 1 {
		return createJsRet(nil, -1, "Expected 1 parameter")
	}

	pn := p[0]
	if pn.Type() != js.TypeString {
		return createJsRet(nil, -1, "feeRate parameter should be a string")
	}
	feeRate := pn.String()
	feeRate64, err := strconv.ParseInt(feeRate, 10, 64)
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		txId, id, err := _mgr.MinerUnstake(feeRate64)
		if err != nil {
			return nil, -1, err.Error()
		}

		return map[string]interface{}{
			"txId":   txId,
			"resvId": id,
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func deployRunesRemote(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}

	if len(p) < 7 {
		return createJsRet(nil, -1, "Expected 7 parameters")
	}

	pn := p[0]
	if pn.Type() != js.TypeString {
		return createJsRet(nil, -1, "assetName parameter should be a string")
	}
	assetName := pn.String()

	pn = p[1]
	if pn.Type() != js.TypeNumber {
		return createJsRet(nil, -1, "symbol parameter should be a number")
	}
	symbol := int32(pn.Int())

	pn = p[2]
	if pn.Type() != js.TypeString {
		return createJsRet(nil, -1, "maxSupply parameter should be a string")
	}
	maxSupply, err := strconv.ParseInt(pn.String(), 10, 64)
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}

	pn = p[3]
	if pn.Type() != js.TypeString {
		return createJsRet(nil, -1, "limit parameter should be a string")
	}
	limit, err := strconv.ParseInt(pn.String(), 10, 64)
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}

	pn = p[4]
	if pn.Type() != js.TypeBoolean {
		return createJsRet(nil, -1, "selfMint parameter should be a bool")
	}
	selfMint := pn.Bool()

	pn = p[5]
	if pn.Type() != js.TypeString {
		return createJsRet(nil, -1, "destAddr parameter should be a string")
	}
	destAddr := pn.String()

	divisibility := int64(0)
	feeRateIndex := 6
	if len(p) >= 8 {
		pn = p[6]
		if pn.Type() != js.TypeString {
			return createJsRet(nil, -1, "divisibility parameter should be a string")
		}
		divisibility, err = strconv.ParseInt(pn.String(), 10, 64)
		if err != nil {
			return createJsRet(nil, -1, err.Error())
		}
		feeRateIndex = 7
	}

	pn = p[feeRateIndex]
	if pn.Type() != js.TypeString {
		return createJsRet(nil, -1, "feeRate parameter should be a string")
	}
	feeRate, err := strconv.ParseInt(pn.String(), 10, 64)
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		txId, id, result, err := _mgr.DeployRunes_Remote(
			assetName,
			symbol,
			maxSupply,
			limit,
			selfMint,
			destAddr,
			divisibility,
			feeRate,
		)
		if err != nil {
			return nil, -1, err.Error()
		}

		return map[string]interface{}{
			"txId":   txId,
			"resvId": id,
			"result": result,
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func getAddressStatusInContract(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}

	if len(p) < 2 {
		return createJsRet(nil, -1, "Expected 2 parameters")
	}

	pn := p[0]
	if pn.Type() != js.TypeString {
		return createJsRet(nil, -1, "contract URL parameter should be a string")
	}
	url := pn.String()

	pn = p[1]
	if pn.Type() != js.TypeString {
		return createJsRet(nil, -1, "address parameter should be a json string")
	}
	address := pn.String()

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		info, err := _mgr.GetUserStatusInContract(url, (address))
		if err != nil {
			return nil, -1, err.Error()
		}

		return map[string]interface{}{
			"status": info,
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func deposit(this js.Value, p []js.Value) any {
	msg := "ok"
	if _mgr == nil {
		msg = "Manager not initialized"
		return createJsRet(nil, -1, msg)
	}

	if len(p) < 6 {
		msg = "Expected 6 parameters"
		wallet.Log.Error(msg)
		return createJsRet(nil, -1, msg)
	}

	if p[0].Type() != js.TypeString {
		msg = "destAddr parameter should be a string"
		wallet.Log.Error(msg)
		return createJsRet(nil, -1, msg)
	}
	destAddr := p[0].String()

	if p[1].Type() != js.TypeString {
		msg = "assetName parameter should be a string"
		wallet.Log.Error(msg)
		return createJsRet(nil, -1, msg)
	}
	assetName := p[1].String()

	if p[2].Type() != js.TypeString {
		msg = "amount parameter should be a string"
		wallet.Log.Error(msg)
		return createJsRet(nil, -1, msg)
	}
	amt := p[2].String()

	// utxos, err := getStringVector(p[3], 3)
	// if err != nil {
	// 	msg = err.Error()
	// 	wallet.Log.Error(msg)
	// 	return createJsRet(nil, -1, msg)
	// }

	// fees, err := getStringVector(p[4], 4)
	// if err != nil {
	// 	msg = err.Error()
	// 	wallet.Log.Error(msg)
	// 	return createJsRet(nil, -1, msg)
	// }

	if p[5].Type() != js.TypeString {
		msg = "feeRate parameter should be a string"
		wallet.Log.Error(msg)
		return createJsRet(nil, -1, msg)
	}
	feeRate := p[5].String()
	feeRate64, err := strconv.ParseInt(feeRate, 10, 64)
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		txId, err := _mgr.DepositWithContract(destAddr, assetName, amt,
			int64(feeRate64))
		if err != nil {
			wallet.Log.Errorf("Deposit error: %v", err)
			return nil, -1, err.Error()
		}

		return map[string]interface{}{
			"txId": txId,
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func withdraw(this js.Value, p []js.Value) any {
	msg := "ok"
	if _mgr == nil {
		msg = "Manager not initialized"
		return createJsRet(nil, -1, msg)
	}

	if len(p) < 6 {
		msg = "Expected 6 parameters"
		wallet.Log.Error(msg)
		return createJsRet(nil, -1, msg)
	}

	if p[0].Type() != js.TypeString {
		msg = "destAddr parameter should be a string"
		wallet.Log.Error(msg)
		return createJsRet(nil, -1, msg)
	}
	destAddr := p[0].String()

	if p[1].Type() != js.TypeString {
		msg = "assetName parameter should be a string"
		wallet.Log.Error(msg)
		return createJsRet(nil, -1, msg)
	}
	assetName := p[1].String()

	if p[2].Type() != js.TypeString {
		msg = "amount parameter should be a string"
		wallet.Log.Error(msg)
		return createJsRet(nil, -1, msg)
	}
	amt := p[2].String()

	if p[5].Type() != js.TypeString {
		msg = "feeRate parameter should be a string"
		wallet.Log.Error(msg)
		return createJsRet(nil, -1, msg)
	}
	feeRate := p[5].String()
	feeRate64, err := strconv.ParseInt(feeRate, 10, 64)
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		txId, err := _mgr.WithdrawWithContract(destAddr, assetName, amt,
			int64(feeRate64))
		if err != nil {
			wallet.Log.Errorf("Withdraw error: %v", err)
			return nil, -1, err.Error()
		}

		return map[string]interface{}{
			"txId": txId,
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func getAllRegisteredReferrerName(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}

	if len(p) < 1 {
		return createJsRet(nil, -1, "Expected 2 parameters")
	}

	pn := p[0]
	if pn.Type() != js.TypeString {
		return createJsRet(nil, -1, "server pubkey parameter should be a string")
	}
	pubkey := pn.String()

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		names, err := _mgr.GetAllRegisteredReferrerName("", pubkey)
		if err != nil {
			return nil, -1, err.Error()
		}
		var result []interface{}
		for _, name := range names {
			result = append(result, name)
		}

		return map[string]interface{}{
			"names": result,
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func registerAsReferrer(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}

	if len(p) < 2 {
		return createJsRet(nil, -1, "Expected 2 parameters")
	}

	pn := p[0]
	if pn.Type() != js.TypeString {
		return createJsRet(nil, -1, "name parameter should be a string")
	}
	name := pn.String()

	pn = p[1]
	if pn.Type() != js.TypeString {
		return createJsRet(nil, -1, "feeRate parameter should be a string")
	}
	feeRate := pn.String()
	feeRate64, err := strconv.ParseInt(feeRate, 10, 64)
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		txId, err := _mgr.RegisterAsReferrer(name, feeRate64)
		if err != nil {
			return nil, -1, err.Error()
		}

		return map[string]interface{}{
			"txId": txId,
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func bindReferrerForServer(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}

	if len(p) < 2 {
		return createJsRet(nil, -1, "Expected 2 parameters")
	}

	pn := p[0]
	if pn.Type() != js.TypeString {
		return createJsRet(nil, -1, "name parameter should be a string")
	}
	name := pn.String()

	pn = p[1]
	if pn.Type() != js.TypeString {
		return createJsRet(nil, -1, "server pubkey parameter should be a string")
	}
	pubkey := pn.String()

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		txId, err := _mgr.BindReferrerForServer(name, pubkey)
		if err != nil {
			return nil, -1, err.Error()
		}

		return map[string]interface{}{
			"txId": txId,
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func inscribeResult(resv *wallet.InscribeResv) map[string]interface{} {
	result := map[string]interface{}{
		"resvId": resv.Id,
	}
	if resv.CommitTx != nil {
		result["commitTxId"] = resv.CommitTx.TxID()
	}
	if resv.RevealTx != nil {
		txID := resv.RevealTx.TxID()
		result["revealTxId"] = txID
		result["txId"] = txID
	} else if resv.CommitTx != nil {
		result["txId"] = resv.CommitTx.TxID()
	}
	return result
}

func deployTickerOrdx(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(p) < 5 {
		return createJsRet(nil, -1, "Expected 5 parameters")
	}
	if p[0].Type() != js.TypeString || p[1].Type() != js.TypeString || p[2].Type() != js.TypeString || p[4].Type() != js.TypeString {
		return createJsRet(nil, -1, "ticker, max, limit and feeRate should be strings")
	}
	if p[3].Type() != js.TypeNumber {
		return createJsRet(nil, -1, "bindingSat should be a number")
	}
	ticker := strings.TrimSpace(p[0].String())
	maxSupply, err := strconv.ParseInt(p[1].String(), 10, 64)
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	limit, err := strconv.ParseInt(p[2].String(), 10, 64)
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	bindingSat := p[3].Int()
	feeRate, err := strconv.ParseInt(p[4].String(), 10, 64)
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		resv, err := _mgr.DeployTicker_ordx(ticker, maxSupply, limit, bindingSat, feeRate)
		if err != nil {
			return nil, -1, err.Error()
		}
		return inscribeResult(resv), 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func mintAssetOrdx(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(p) < 3 {
		return createJsRet(nil, -1, "Expected 3 parameters")
	}
	if p[0].Type() != js.TypeString || p[1].Type() != js.TypeString || p[2].Type() != js.TypeString {
		return createJsRet(nil, -1, "ticker, amount and feeRate should be strings")
	}
	ticker := strings.TrimSpace(p[0].String())
	amount, err := strconv.ParseInt(p[1].String(), 10, 64)
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	feeRate, err := strconv.ParseInt(p[2].String(), 10, 64)
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		w := _mgr.GetWallet()
		if w == nil {
			return nil, -1, "wallet is not created/unlocked"
		}
		assetName := indexer.NewAssetNameFromString(fmt.Sprintf("%s:%s:%s", indexer.PROTOCOL_NAME_ORDX, indexer.ASSET_TYPE_FT, ticker))
		tickInfo := _mgr.GetTickerInfo(assetName)
		if tickInfo == nil {
			return nil, -1, fmt.Sprintf("can't find ticker info %s", assetName.String())
		}
		resv, err := _mgr.MintAsset_ordx(w.GetAddress(), tickInfo, amount, nil, feeRate)
		if err != nil {
			return nil, -1, err.Error()
		}
		return inscribeResult(resv), 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func mintAssetRunes(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(p) < 2 {
		return createJsRet(nil, -1, "Expected 2 parameters")
	}
	if p[0].Type() != js.TypeString || p[1].Type() != js.TypeString {
		return createJsRet(nil, -1, "ticker and feeRate should be strings")
	}
	ticker := strings.TrimSpace(p[0].String())
	feeRate, err := strconv.ParseInt(p[1].String(), 10, 64)
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		w := _mgr.GetWallet()
		if w == nil {
			return nil, -1, "wallet is not created/unlocked"
		}
		assetName := indexer.NewAssetNameFromString(fmt.Sprintf("%s:%s:%s", indexer.PROTOCOL_NAME_RUNES, indexer.ASSET_TYPE_FT, ticker))
		tickInfo := _mgr.GetTickerInfo(assetName)
		if tickInfo == nil {
			return nil, -1, fmt.Sprintf("can't find ticker info %s", assetName.String())
		}
		txId, err := _mgr.MintAsset_runes(w.GetAddress(), tickInfo, feeRate)
		if err != nil {
			return nil, -1, err.Error()
		}
		return map[string]interface{}{
			"txId": txId,
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func deployTickerBrc20(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(p) < 4 {
		return createJsRet(nil, -1, "Expected 4 parameters")
	}
	if p[0].Type() != js.TypeString || p[1].Type() != js.TypeString || p[2].Type() != js.TypeString || p[3].Type() != js.TypeString {
		return createJsRet(nil, -1, "ticker, max, limit and feeRate should be strings")
	}
	ticker := strings.TrimSpace(p[0].String())
	maxSupply, err := strconv.ParseInt(p[1].String(), 10, 64)
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	limit, err := strconv.ParseInt(p[2].String(), 10, 64)
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	decimal := int64(0)
	feeRateIndex := 3
	if len(p) >= 5 {
		if p[4].Type() != js.TypeString {
			return createJsRet(nil, -1, "decimal and feeRate should be strings")
		}
		decimal, err = strconv.ParseInt(p[3].String(), 10, 64)
		if err != nil {
			return createJsRet(nil, -1, err.Error())
		}
		feeRateIndex = 4
	}
	feeRate, err := strconv.ParseInt(p[feeRateIndex].String(), 10, 64)
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		resv, err := _mgr.DeployTicker_brc20(ticker, maxSupply, limit, decimal, feeRate)
		if err != nil {
			return nil, -1, err.Error()
		}
		return inscribeResult(resv), 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func mintAssetBrc20(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(p) < 3 {
		return createJsRet(nil, -1, "Expected 3 parameters")
	}
	if p[0].Type() != js.TypeString || p[1].Type() != js.TypeString || p[2].Type() != js.TypeString {
		return createJsRet(nil, -1, "ticker, amount and feeRate should be strings")
	}
	ticker := strings.TrimSpace(p[0].String())
	amount := p[1].String()
	feeRate, err := strconv.ParseInt(p[2].String(), 10, 64)
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		w := _mgr.GetWallet()
		if w == nil {
			return nil, -1, "wallet is not created/unlocked"
		}
		assetName := indexer.NewAssetNameFromString(fmt.Sprintf("%s:%s:%s", indexer.PROTOCOL_NAME_BRC20, indexer.ASSET_TYPE_FT, ticker))
		tickInfo := _mgr.GetTickerInfo(assetName)
		if tickInfo == nil {
			return nil, -1, fmt.Sprintf("can't find ticker info %s", assetName.String())
		}
		amt, err := indexer.NewDecimalFromString(amount, tickInfo.Divisibility)
		if err != nil {
			return nil, -1, err.Error()
		}
		resv, err := _mgr.MintAsset_brc20(w.GetAddress(), assetName, amt, nil, feeRate)
		if err != nil {
			return nil, -1, err.Error()
		}
		return inscribeResult(resv), 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func inscribeName(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(p) < 2 {
		return createJsRet(nil, -1, "Expected 2 parameters")
	}
	if p[0].Type() != js.TypeString || p[1].Type() != js.TypeString {
		return createJsRet(nil, -1, "name and feeRate should be strings")
	}
	name := strings.TrimSpace(p[0].String())
	feeRate, err := strconv.ParseInt(p[1].String(), 10, 64)
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		resv, err := _mgr.InscribeName(name, feeRate)
		if err != nil {
			return nil, -1, err.Error()
		}
		return inscribeResult(resv), 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func validateBitcoinAddress(this js.Value, p []js.Value) interface{} {
	if len(p) < 1 {
		return createJsRet(nil, -1, "missing address")
	}

	address := strings.TrimSpace(p[0].String())
	if address == "" {
		return createJsRet(map[string]interface{}{"valid": false}, 0, "ok")
	}

	_, err := wallet.AddrToPkScript(address, wallet.GetChainParam())
	return createJsRet(map[string]interface{}{"valid": err == nil}, 0, "ok")
}

func validateSatsNetAddress(this js.Value, p []js.Value) interface{} {
	if len(p) < 1 {
		return createJsRet(nil, -1, "missing address")
	}

	address := strings.TrimSpace(p[0].String())
	if address == "" {
		return createJsRet(map[string]interface{}{"valid": false}, 0, "ok")
	}

	_, err := wallet.AddrToPkScript_SatsNet(address, wallet.GetChainParam_SatsNet())
	return createJsRet(map[string]interface{}{"valid": err == nil}, 0, "ok")
}

func getRGB11State(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		state, err := _mgr.GetRGB11State()
		if err != nil {
			return nil, -1, err.Error()
		}
		encoded, err := json.Marshal(state)
		if err != nil {
			return nil, -1, err.Error()
		}
		return map[string]any{"state": string(encoded)}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func createRGB11Invoice(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(p) < 1 {
		return createJsRet(nil, -1, "missing RGB11 invoice request")
	}
	var request wallet.RGB11InvoiceRequest
	if err := json.Unmarshal([]byte(p[0].String()), &request); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		receive, err := _mgr.CreateRGB11Invoice(request)
		if err != nil {
			return nil, -1, err.Error()
		}
		encoded, err := json.Marshal(receive)
		if err != nil {
			return nil, -1, err.Error()
		}
		var result map[string]any
		if err := json.Unmarshal(encoded, &result); err != nil {
			return nil, -1, err.Error()
		}
		return result, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func acceptRGB11Consignment(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(p) < 2 {
		return createJsRet(nil, -1, "missing RGB11 request id or consignment")
	}
	requestID, consignment := p[0].String(), p[1].String()
	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		receipt, err := _mgr.AcceptRGB11Consignment(context.Background(), requestID, []byte(consignment))
		if err != nil {
			return nil, -1, err.Error()
		}
		return receipt, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func importRGB11Contract(this js.Value, p []js.Value) any {
	if _mgr == nil || len(p) < 1 {
		return createJsRet(nil, -1, "missing RGB11 contract consignment")
	}
	raw := p[0].String()
	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		result, err := _mgr.ImportRGB11Contract(context.Background(), []byte(raw))
		if err != nil {
			return nil, -1, err.Error()
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			return nil, -1, err.Error()
		}
		return map[string]any{"result": string(encoded)}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func issueRGB11Asset(this js.Value, p []js.Value) any {
	if _mgr == nil || len(p) < 1 {
		return createJsRet(nil, -1, "missing RGB11 issuance request")
	}
	var request wallet.RGB11IssueRequest
	if err := json.Unmarshal([]byte(p[0].String()), &request); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		result, err := _mgr.IssueRGB11Asset(context.Background(), request)
		if err != nil {
			return nil, -1, err.Error()
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			return nil, -1, err.Error()
		}
		return map[string]any{"result": string(encoded)}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func prepareRGB11Transfer(this js.Value, p []js.Value) any {
	if _mgr == nil || len(p) < 1 {
		return createJsRet(nil, -1, "missing RGB11 send request")
	}
	var request wallet.RGB11SendRequest
	if err := json.Unmarshal([]byte(p[0].String()), &request); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		result, err := _mgr.PrepareRGB11Transfer(context.Background(), request)
		if err != nil {
			return nil, -1, err.Error()
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			return nil, -1, err.Error()
		}
		return map[string]any{"transfer": string(encoded)}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func buildRGB11RelayRecord(this js.Value, p []js.Value) any {
	if _mgr == nil || len(p) < 1 {
		return createJsRet(nil, -1, "missing RGB11 transfer id")
	}
	transferID := p[0].String()
	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		record, err := _mgr.BuildRGB11RelayRecord(transferID, "sat20-pwa")
		if err != nil {
			return nil, -1, err.Error()
		}
		encoded, err := json.Marshal(record)
		if err != nil {
			return nil, -1, err.Error()
		}
		return map[string]any{"record": string(encoded)}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func publishRGB11RelayRecord(this js.Value, p []js.Value) any {
	if _mgr == nil || len(p) < 1 {
		return createJsRet(nil, -1, "missing RGB11 transfer id")
	}
	transferID := p[0].String()
	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		record, _, err := _mgr.PublishRGB11RelayRecord(transferID, "sat20-pwa", dkvsindexer.RecordOptions{
			TTL: uint64((24 * time.Hour) / time.Millisecond),
		})
		if err != nil {
			return nil, -1, err.Error()
		}
		encoded, err := json.Marshal(record)
		if err != nil {
			return nil, -1, err.Error()
		}
		return map[string]any{"record": string(encoded)}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func acceptRGB11RelayConsignment(this js.Value, p []js.Value) any {
	if _mgr == nil || len(p) < 3 {
		return createJsRet(nil, -1, "missing RGB11 request id, relay record or consignment")
	}
	requestID, recordJSON, consignment := p[0].String(), p[1].String(), p[2].String()
	var record corerelay.RelayRecord
	if err := json.Unmarshal([]byte(recordJSON), &record); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		receipt, ack, err := _mgr.AcceptRGB11RelayConsignment(
			context.Background(), requestID, &record, []byte(consignment),
		)
		if err != nil {
			return nil, -1, err.Error()
		}
		receiptJSON, err := json.Marshal(receipt)
		if err != nil {
			return nil, -1, err.Error()
		}
		ackJSON, err := json.Marshal(ack)
		if err != nil {
			return nil, -1, err.Error()
		}
		return map[string]any{"receipt": string(receiptJSON), "ack": string(ackJSON)}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func rejectRGB11RelayConsignment(this js.Value, p []js.Value) any {
	if _mgr == nil || len(p) < 2 {
		return createJsRet(nil, -1, "missing RGB11 request id or relay record")
	}
	requestID := p[0].String()
	var record corerelay.RelayRecord
	if err := json.Unmarshal([]byte(p[1].String()), &record); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		ack, err := _mgr.RejectRGB11RelayConsignment(requestID, &record)
		if err != nil {
			return nil, -1, err.Error()
		}
		encoded, err := json.Marshal(ack)
		if err != nil {
			return nil, -1, err.Error()
		}
		return map[string]any{"ack": string(encoded)}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func publishRGB11AckRecord(this js.Value, p []js.Value) any {
	if _mgr == nil || len(p) < 2 {
		return createJsRet(nil, -1, "missing RGB11 ACK key or record")
	}
	key := p[0].String()
	var ack corerelay.AckRecord
	if err := json.Unmarshal([]byte(p[1].String()), &ack); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		_, err := _mgr.PublishRGB11AckRecord(key, &ack, dkvsindexer.RecordOptions{
			TTL: uint64((24 * time.Hour) / time.Millisecond),
		})
		if err != nil {
			return nil, -1, err.Error()
		}
		return map[string]any{"published": true}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func fetchRGB11AckRecord(this js.Value, p []js.Value) any {
	if _mgr == nil || len(p) < 1 {
		return createJsRet(nil, -1, "missing RGB11 transfer id")
	}
	transferID := p[0].String()
	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		ack, _, err := _mgr.FetchRGB11AckRecord(transferID, dkvsindexer.RecordVerificationOptions{
			Now: uint64(time.Now().UnixMilli()),
		})
		if err != nil {
			return nil, -1, err.Error()
		}
		encoded, err := json.Marshal(ack)
		if err != nil {
			return nil, -1, err.Error()
		}
		return map[string]any{"ack": string(encoded)}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func cancelRGB11BatchByNack(this js.Value, p []js.Value) any {
	if _mgr == nil || len(p) < 3 {
		return createJsRet(nil, -1, "missing RGB11 transfer id, relay record or NACK")
	}
	transferID := p[0].String()
	var record corerelay.RelayRecord
	if err := json.Unmarshal([]byte(p[1].String()), &record); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	var nack corerelay.AckRecord
	if err := json.Unmarshal([]byte(p[2].String()), &nack); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		if err := _mgr.CancelRGB11BatchByNack(transferID, &record, &nack); err != nil {
			return nil, -1, err.Error()
		}
		return map[string]any{"cancelled": true}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func broadcastRGB11Transfer(this js.Value, p []js.Value) any {
	if _mgr == nil || len(p) < 3 {
		return createJsRet(nil, -1, "missing RGB11 transfer id, relay record or ACK")
	}
	transferID := p[0].String()
	var record corerelay.RelayRecord
	if err := json.Unmarshal([]byte(p[1].String()), &record); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	var ack corerelay.AckRecord
	if err := json.Unmarshal([]byte(p[2].String()), &ack); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		txID, err := _mgr.BroadcastRGB11Transfer(transferID, &record, &ack)
		if err != nil {
			return nil, -1, err.Error()
		}
		return map[string]any{"txid": txID}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

type rgb11BatchBroadcastRequest struct {
	TransferIDs  []string                 `json:"transfer_ids"`
	RelayRecords []*corerelay.RelayRecord `json:"relay_records"`
	Acks         []*corerelay.AckRecord   `json:"acks"`
}

func broadcastRGB11Batch(this js.Value, p []js.Value) any {
	if _mgr == nil || len(p) < 1 {
		return createJsRet(nil, -1, "missing RGB11 batch ACK request")
	}
	var request rgb11BatchBroadcastRequest
	if err := json.Unmarshal([]byte(p[0].String()), &request); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		txID, err := _mgr.BroadcastRGB11Batch(request.TransferIDs, request.RelayRecords, request.Acks)
		if err != nil {
			return nil, -1, err.Error()
		}
		return map[string]any{"txid": txID}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func broadcastRGB11OutOfBand(this js.Value, p []js.Value) any {
	if _mgr == nil || len(p) < 1 {
		return createJsRet(nil, -1, "missing RGB11 out-of-band transfer ids")
	}
	var transferIDs []string
	if err := json.Unmarshal([]byte(p[0].String()), &transferIDs); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		txID, err := _mgr.BroadcastRGB11OutOfBand(transferIDs)
		if err != nil {
			return nil, -1, err.Error()
		}
		return map[string]any{"txid": txID}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func refreshRGB11State(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		result, err := _mgr.RefreshRGB11State(context.Background())
		if err != nil && result == nil {
			return nil, -1, err.Error()
		}
		encoded, encodeErr := json.Marshal(result)
		if encodeErr != nil {
			return nil, -1, encodeErr.Error()
		}
		message := "ok"
		code := 0
		if err != nil {
			message, code = err.Error(), -1
		}
		return map[string]any{"result": string(encoded)}, code, message
	})
	return js.Global().Get("Promise").New(jsHandler)
}

type rgb11BackupRequest struct {
	WalletID     string `json:"wallet_id"`
	TTL          uint64 `json:"ttl"`
	ExpiryHeight uint64 `json:"expiry_height"`
}

func backupRGB11WalletState(this js.Value, p []js.Value) any {
	if _mgr == nil || len(p) < 1 {
		return createJsRet(nil, -1, "missing RGB11 backup request")
	}
	var request rgb11BackupRequest
	if err := json.Unmarshal([]byte(p[0].String()), &request); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	if request.TTL == 0 {
		request.TTL = uint64((365 * 24 * time.Hour) / time.Millisecond)
	}
	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		head, err := _mgr.SyncRGB11WalletState(request.WalletID, dkvsindexer.RecordOptions{
			TTL: request.TTL, ExpiryHeight: request.ExpiryHeight,
		})
		if err != nil {
			return nil, -1, err.Error()
		}
		encoded, err := json.Marshal(head)
		if err != nil {
			return nil, -1, err.Error()
		}
		return map[string]any{"head": string(encoded)}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

type rgb11RestoreRequest struct {
	WalletID string `json:"wallet_id"`
	Height   uint64 `json:"height"`
	Now      uint64 `json:"now"`
}

func restoreRGB11WalletState(this js.Value, p []js.Value) any {
	if _mgr == nil || len(p) < 1 {
		return createJsRet(nil, -1, "missing RGB11 restore request")
	}
	var request rgb11RestoreRequest
	if err := json.Unmarshal([]byte(p[0].String()), &request); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	if request.Now == 0 {
		request.Now = uint64(time.Now().UnixMilli())
	}
	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		head, err := _mgr.RestoreLatestRGB11WalletState(request.WalletID, dkvsindexer.RecordVerificationOptions{
			Height: request.Height, Now: request.Now,
		})
		if err != nil {
			return nil, -1, err.Error()
		}
		encoded, err := json.Marshal(head)
		if err != nil {
			return nil, -1, err.Error()
		}
		return map[string]any{"head": string(encoded)}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func main() {
	obj := js.Global().Get("Object").New()
	obj.Set("batchDbTest", js.FuncOf(batchDbTest))
	obj.Set("dbTest", js.FuncOf(dbTest))
	// input: cfg, loglevel; return: ok
	obj.Set("init", js.FuncOf(initManager))
	// input: none
	obj.Set("release", js.FuncOf(releaseManager))
	// input: none; return: true or false
	obj.Set("isWalletExist", js.FuncOf(isWalletExist))
	// input: password;  return: walletId, mnemonic
	obj.Set("createWallet", js.FuncOf(createWallet))
	obj.Set("createMonitorWallet", js.FuncOf(createMonitorWallet))
	// input: mnemonic, password; return: walletId
	obj.Set("importWallet", js.FuncOf(importWallet))
	obj.Set("importWalletWithPrivKey", js.FuncOf(importWalletWithPrivKey))
	// input: password; return: current walletId
	obj.Set("unlockWallet", js.FuncOf(unlockWallet))
	// input: none; return: list of wallet id and account number
	obj.Set("getAllWallets", js.FuncOf(getAllWallets))
	// input: wallet id; return: ok
	obj.Set("switchWallet", js.FuncOf(switchWallet))
	obj.Set("changePassword", js.FuncOf(changePassword))
	// input: account id; return: ok
	obj.Set("switchAccount", js.FuncOf(switchAccount))
	// input: mainnet or testnet
	obj.Set("switchChain", js.FuncOf(switchChain))
	// input: walletid, password; return: mnemonic
	obj.Set("getMnemonice", js.FuncOf(getMnemonic))
	// input: account id; return: current wallet p2tr address
	obj.Set("getWalletAddress", js.FuncOf(getWalletAddress))
	obj.Set("validateBitcoinAddress", js.FuncOf(validateBitcoinAddress))
	obj.Set("validateSatsNetAddress", js.FuncOf(validateSatsNetAddress))
	// input: account id; return: current wallet public key
	obj.Set("getWalletPubkey", js.FuncOf(getWalletPubkey))
	obj.Set("startBTCLuckyMining", js.FuncOf(startBTCLuckyMining))
	obj.Set("stopBTCLuckyMining", js.FuncOf(stopBTCLuckyMining))
	obj.Set("getBTCLuckyMiningStatus", js.FuncOf(getBTCLuckyMiningStatus))
	obj.Set("getChannelAddrByPeerPubkey", js.FuncOf(getChannelAddrByPeerPubkey))
	obj.Set("openChannel", js.FuncOf(openChannel))
	obj.Set("closeChannel", js.FuncOf(closeChannel))
	obj.Set("getChannel", js.FuncOf(getChannel))
	obj.Set("getCurrentChannel", js.FuncOf(getCurrentChannel))
	obj.Set("getChannelStatus", js.FuncOf(getChannelStatus))
	obj.Set("getAllChannels", js.FuncOf(getAllChannels))
	obj.Set("reservationStatus", js.FuncOf(reservationStatus))
	obj.Set("allReservations", js.FuncOf(allReservations))
	obj.Set("unlockFromChannel", js.FuncOf(unlockFromChannel))
	obj.Set("lockToChannel", js.FuncOf(lockToChannel))
	obj.Set("lockToChannelWithExpand", js.FuncOf(lockToChannelWithExpand))
	obj.Set("batchUnlockFromChannel", js.FuncOf(batchUnlockFromChannel))
	obj.Set("batchUnlockFromChannelV2", js.FuncOf(batchUnlockFromChannelV2))
	obj.Set("expandChannel", js.FuncOf(expandChannel))
	obj.Set("expandChannel_SatsNet", js.FuncOf(expandChannel_SatsNet))
	obj.Set("expandAll_SatsNet", js.FuncOf(expandAll_SatsNet))
	obj.Set("expandAsset", js.FuncOf(expandAsset))
	obj.Set("reopenChannel", js.FuncOf(reopenChannel))
	obj.Set("rebuildChannel", js.FuncOf(rebuildChannel))
	obj.Set("restoreChannel", js.FuncOf(restoreChannel))
	obj.Set("splicingIn", js.FuncOf(splicingIn))
	obj.Set("splicingOut", js.FuncOf(splicingOut))
	// input: node pubkey(hex string), index; return: commit secrect (hex string)
	obj.Set("getCommitSecret", js.FuncOf(getCommitSecret))
	// input: commit secrect(hex string), index; return: revocation priv key (hex string)
	obj.Set("deriveRevocationPrivKey", js.FuncOf(deriveRevocationPrivKey))
	// input: none; return: revocation base key (hex string)
	obj.Set("getRevocationBaseKey", js.FuncOf(getRevocationBaseKey))
	// input: none; return: node pubkey (hex string)
	obj.Set("getNodePubKey", js.FuncOf(getNodePubKey))
	obj.Set("signData", js.FuncOf(signData))
	// input: message (hex string) return: signature (hex string)
	obj.Set("signMessage", js.FuncOf(signMessage))
	// input: psbt(hexString); return: signed psbt (hexString)
	obj.Set("signPsbt", js.FuncOf(signPsbt))
	obj.Set("signPsbts", js.FuncOf(signPsbts))
	// input: psbt(hexString); return: signed psbt (hexString)
	obj.Set("signPsbt_SatsNet", js.FuncOf(signPsbt_SatsNet))
	obj.Set("signPsbts_SatsNet", js.FuncOf(signPsbts_SatsNet))
	obj.Set("getTxAssetInfoFromPsbt", js.FuncOf(getTxAssetInfoFromPsbt))
	obj.Set("getTxAssetInfoFromPsbt_SatsNet", js.FuncOf(getTxAssetInfoFromPsbt_SatsNet))

	obj.Set("getVersion", js.FuncOf(getVersion))
	obj.Set("registerCallback", js.FuncOf(registerCallbacks))

	obj.Set("extractTxFromPsbt", js.FuncOf(extractTxFromPsbt))
	obj.Set("extractTxFromPsbt_SatsNet", js.FuncOf(extractTxFromPsbt_SatsNet))
	obj.Set("extractUnsignedTxFromPsbt", js.FuncOf(extractUnsignedTxFromPsbt))
	obj.Set("extractUnsignedTxFromPsbt_SatsNet", js.FuncOf(extractUnsignedTxFromPsbt_SatsNet))
	obj.Set("buildBatchSellOrder_SatsNet", js.FuncOf(buildBatchSellOrder_SatsNet))
	obj.Set("finalizeSellOrder_SatsNet", js.FuncOf(finalizeSellOrder_SatsNet))
	obj.Set("splitBatchSignedPsbt_SatsNet", js.FuncOf(splitBatchSignedPsbt_SatsNet))
	obj.Set("mergeBatchSignedPsbt_SatsNet", js.FuncOf(mergeBatchSignedPsbt_SatsNet))
	obj.Set("addInputsToPsbt_SatsNet", js.FuncOf(addInputsToPsbt_SatsNet))
	obj.Set("addOutputsToPsbt_SatsNet", js.FuncOf(addOutputsToPsbt_SatsNet))

	obj.Set("sendAssets", js.FuncOf(sendAssets))
	obj.Set("sendGarbage", js.FuncOf(sendGarbage))
	obj.Set("sendAssets_SatsNet", js.FuncOf(sendAssets_SatsNet))
	obj.Set("batchSendAssets_SatsNet", js.FuncOf(batchSendAssets_SatsNet))
	obj.Set("batchSendAssets", js.FuncOf(batchSendAssets))
	obj.Set("batchSendAssetsV2_SatsNet", js.FuncOf(batchSendAssetsV2_SatsNet))

	obj.Set("getTickerInfo", js.FuncOf(getTickerInfo))
	obj.Set("lockUtxo", js.FuncOf(lockUtxo))
	obj.Set("unlockUtxo", js.FuncOf(unlockUtxo))
	obj.Set("isUtxoLocked", js.FuncOf(isUtxoLocked))
	obj.Set("getAllLockedUtxo", js.FuncOf(getAllLockedUtxo))
	obj.Set("lockUtxo_SatsNet", js.FuncOf(lockUtxo_SatsNet))
	obj.Set("unlockUtxo_SatsNet", js.FuncOf(unlockUtxo_SatsNet))
	obj.Set("isUtxoLocked_SatsNet", js.FuncOf(isUtxoLocked_SatsNet))
	obj.Set("getAllLockedUtxo_SatsNet", js.FuncOf(getAllLockedUtxo_SatsNet))

	obj.Set("getUtxosWithAsset", js.FuncOf(getUtxosWithAsset))
	obj.Set("getUtxosWithAsset_SatsNet", js.FuncOf(getUtxosWithAsset_SatsNet))
	obj.Set("getUtxosWithAssetV2", js.FuncOf(getUtxosWithAssetV2))
	obj.Set("getUtxosWithAssetV2_SatsNet", js.FuncOf(getUtxosWithAssetV2_SatsNet))
	obj.Set("getAssetAmount", js.FuncOf(getAssetAmount))
	obj.Set("getAssetAmount_SatsNet", js.FuncOf(getAssetAmount_SatsNet))
	obj.Set("getRGB11State", js.FuncOf(getRGB11State))
	obj.Set("createRGB11Invoice", js.FuncOf(createRGB11Invoice))
	obj.Set("acceptRGB11Consignment", js.FuncOf(acceptRGB11Consignment))
	obj.Set("importRGB11Contract", js.FuncOf(importRGB11Contract))
	obj.Set("issueRGB11Asset", js.FuncOf(issueRGB11Asset))
	obj.Set("prepareRGB11Transfer", js.FuncOf(prepareRGB11Transfer))
	obj.Set("buildRGB11RelayRecord", js.FuncOf(buildRGB11RelayRecord))
	obj.Set("publishRGB11RelayRecord", js.FuncOf(publishRGB11RelayRecord))
	obj.Set("acceptRGB11RelayConsignment", js.FuncOf(acceptRGB11RelayConsignment))
	obj.Set("rejectRGB11RelayConsignment", js.FuncOf(rejectRGB11RelayConsignment))
	obj.Set("publishRGB11AckRecord", js.FuncOf(publishRGB11AckRecord))
	obj.Set("fetchRGB11AckRecord", js.FuncOf(fetchRGB11AckRecord))
	obj.Set("cancelRGB11BatchByNack", js.FuncOf(cancelRGB11BatchByNack))
	obj.Set("broadcastRGB11Transfer", js.FuncOf(broadcastRGB11Transfer))
	obj.Set("broadcastRGB11Batch", js.FuncOf(broadcastRGB11Batch))
	obj.Set("broadcastRGB11OutOfBand", js.FuncOf(broadcastRGB11OutOfBand))
	obj.Set("refreshRGB11State", js.FuncOf(refreshRGB11State))
	obj.Set("backupRGB11WalletState", js.FuncOf(backupRGB11WalletState))
	obj.Set("restoreRGB11WalletState", js.FuncOf(restoreRGB11WalletState))

	obj.Set("queryContract", js.FuncOf(queryContract))
	obj.Set("buildUnifiedContractContent", js.FuncOf(buildUnifiedContractContent))
	obj.Set("deployUnifiedContract", js.FuncOf(deployUnifiedContract))
	obj.Set("estimateDeployUnifiedContract", js.FuncOf(estimateDeployUnifiedContract))
	obj.Set("invokeUnifiedContract", js.FuncOf(invokeUnifiedContract))
	obj.Set("getParamForInvokeUnifiedContract", js.FuncOf(getParamForInvokeUnifiedContract))
	obj.Set("getFeeForInvokeUnifiedContract", js.FuncOf(getFeeForInvokeUnifiedContract))
	obj.Set("getSupportedContracts", js.FuncOf(getSupportedContracts))
	obj.Set("getDeployedContractsInServer", js.FuncOf(getDeployedContractsInServer))
	obj.Set("getDeployedContractStatus", js.FuncOf(getDeployedContractStatus))
	obj.Set("getDeployedContractAnalytics", js.FuncOf(getDeployedContractAnalytics))
	obj.Set("getFeeForDeployContract", js.FuncOf(getFeeForDeployContract))
	obj.Set("deployContract_Remote", js.FuncOf(deployContract_Remote))
	obj.Set("getParamForInvokeContract", js.FuncOf(getParamForInvokeContract))
	obj.Set("getFeeForInvokeContract", js.FuncOf(getFeeForInvokeContract))
	obj.Set("invokeContract_SatsNet", js.FuncOf(invokeContract_SatsNet))
	obj.Set("invokeContractV2_SatsNet", js.FuncOf(invokeContractV2_SatsNet))
	obj.Set("invokeContractV2", js.FuncOf(invokeContractV2))
	obj.Set("getContractInvokeHistoryInServer", js.FuncOf(getContractInvokeHistoryInServer))
	obj.Set("getContractInvokeHistoryByAddressInServer", js.FuncOf(getContractInvokeHistoryByAddressInServer))
	obj.Set("getAllAddressInContract", js.FuncOf(getAllAddressInContract))
	obj.Set("getAddressStatusInContract", js.FuncOf(getAddressStatusInContract))

	obj.Set("deposit", js.FuncOf(deposit))
	obj.Set("withdraw", js.FuncOf(withdraw))
	obj.Set("stakeToBeMiner", js.FuncOf(stakeToBeMiner))
	obj.Set("minerUnstake", js.FuncOf(minerUnstake))
	obj.Set("DeployRunes_Remote", js.FuncOf(deployRunesRemote))
	obj.Set("deployTickerOrdx", js.FuncOf(deployTickerOrdx))
	obj.Set("mintAssetOrdx", js.FuncOf(mintAssetOrdx))
	obj.Set("mintAssetRunes", js.FuncOf(mintAssetRunes))
	obj.Set("deployTickerBrc20", js.FuncOf(deployTickerBrc20))
	obj.Set("mintAssetBrc20", js.FuncOf(mintAssetBrc20))
	obj.Set("inscribeName", js.FuncOf(inscribeName))
	obj.Set("getAllRegisteredReferrerName", js.FuncOf(getAllRegisteredReferrerName))
	obj.Set("registerAsReferrer", js.FuncOf(registerAsReferrer))
	obj.Set("bindReferrerForServer", js.FuncOf(bindReferrerForServer))

	js.Global().Set(module, obj)
	wallet.Log.SetLevel(logrus.DebugLevel)
	<-make(chan bool)
}
