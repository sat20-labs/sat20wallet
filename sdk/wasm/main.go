//go:build js && wasm
// +build js,wasm

package main

import (
	"encoding/json"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"syscall/js"

	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	"github.com/sat20-labs/sat20wallet/sdk/common"
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

		go func() {
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
}

var createJsRet = func(data any, code int, msg string) map[string]any {
	return map[string]any{
		"data": data,
		"code": code,
		"msg":  msg,
	}
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
	cfg := &common.Config{}
	// Log
	if log := jsConfig.Get("Log"); log.Type() == js.TypeString {
		cfg.Log = log.String()
	} else {
		cfg.Log = "info"
	}

	// Env
	if chain := jsConfig.Get("Env"); chain.Type() == js.TypeString {
		cfg.Env = chain.String()
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
	batch := db.NewBatchWrite()
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
		return createJsRet(nil, -1, "config parameter should be a string")
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
			"walletId": fmt.Sprintf("%d", id),
			"mnemonic": mnemonic,
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
			"walletId": fmt.Sprintf("%d", id),
			"address":  _mgr.GetWallet().GetAddress(),
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
			"walletId": fmt.Sprintf("%d", id),
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
	if len(p) < 1 {
		return createJsRet(nil, -1, "Expected 1 parameters")
	}
	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "Id parameter should be string")
	}
	id := p[0].String()
	i, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}

	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		err := _mgr.SwitchWallet(i)
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
		_mgr.SwitchAccount(uint32(id))
		return nil, 0, "ok"
	})
	return js.Global().Get("Promise").New(handler)
}

func switchChain(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(p) < 1 {
		return createJsRet(nil, -1, "Expected 1 parameters")
	}
	if p[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "chain parameter should be a string")
	}
	chain := p[0].String()

	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		err := _mgr.SwitchChain(chain)
		if err != nil {
			return nil, -1, err.Error()
		}
		return nil, 0, "ok"
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


func sendAssets(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}

	if len(p) < 3 {
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
		txid, err := _mgr.SendAssets(destAddress, assetName, amt, feeRate64, nil)
		if err != nil {
			wallet.Log.Errorf("SendAssets error: %v", err)
			return nil, -1, err.Error()
		}

		return map[string]interface{}{
			"txId": txid,
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func sendAssets_SatsNet(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}

	if len(p) < 3 {
		return createJsRet(nil, -1,  "Expected 3 parameters")
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
		return createJsRet(nil, -1, "amount parameter should be a int")
	}
	amt := p2.String()

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		txid, err := _mgr.SendAssets_SatsNet(destAddress, assetName, amt, nil)
		if err != nil {
			wallet.Log.Errorf("SendUtxos_SatsNet error: %v", err)
			return nil, -1, err.Error()
		}

		return map[string]interface{}{
			"txId": txid,
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
		txid, fee, err := _mgr.BatchSendAssets(destAddress, assetName, amt, n, feeRate64, nil)
		if err != nil {
			wallet.Log.Errorf("BatchSendAssets error: %v", err)
			return nil, -1, err.Error()
		}

		return map[string]interface{}{
			"txId": txid,
			"fee": fee,
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}



func getTxAssetInfoFromPsbt(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "STPManager not initialized")
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
			"txId":  info.TxId,
			"txHex": info.TxHex,
			"inputs": string(inputs),
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
		"txId": info.TxId,
		"txHex": info.TxHex,
		"inputs": string(inputs),
		"outputs": string(outputs),
	}

	return createJsRet(data, 0, "ok")
}

func getTickerInfo(this js.Value, p []js.Value) any {
	code := 0
	msg := "ok"
	if _mgr == nil {
		code = -1
		msg = "STPManager not initialized"
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
	
	// tickerInfo := _mgr.GetTickerInfo(assetName)
	// if tickerInfo == nil {
	// 	code = -1
	// 	msg = fmt.Sprintf("can't get ticker %s", assetName)
	// 	wallet.Log.Error(msg)
	// 	return createJsRet(nil, code, msg)
	// }
	
	// data := map[string]any{
	// 	"ticker":        tickerInfo,
	// }
	// return createJsRet(data, code, msg)

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		tickerInfo := _mgr.GetTickerInfo(assetName)
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
		return createJsRet(nil, -1, "STPManager not initialized")
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
		return createJsRet(nil, -1, "STPManager not initialized")
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
		return createJsRet(nil, -1, "STPManager not initialized")
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
		return createJsRet(nil, -1, "STPManager not initialized")
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
		for k,v := range utxoMap {
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
		return createJsRet(nil, -1, "STPManager not initialized")
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
		return createJsRet(nil, -1, "STPManager not initialized")
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
		return createJsRet(nil, -1, "STPManager not initialized")
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
		return createJsRet(nil, -1, "STPManager not initialized")
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
		for k,v := range utxoMap {
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
		return createJsRet(nil, -1, "STPManager not initialized")
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
		utxos, err := _mgr.GetUtxosWithAsset(address, amt, assetName)
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
		return createJsRet(nil, -1, "STPManager not initialized")
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
		utxos, err := _mgr.GetUtxosWithAsset_SatsNet(address, amt, assetName)
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
		return createJsRet(nil, -1, "STPManager not initialized")
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
		utxos, fees, err := _mgr.GetUtxosWithAssetV2(address, int64(value), amt, assetName)
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
			"fees": result2,
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}


func getUtxosWithAssetV2_SatsNet(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "STPManager not initialized")
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
		utxos, fees, err := _mgr.GetUtxosWithAssetV2_SatsNet(address, int64(value), amt, assetName)
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
			"fees": result2,
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}


func getAssetAmount(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "STPManager not initialized")
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
		available, locked, err := _mgr.GetAssetAmount(address, assetName)
		if err != nil {
			return nil, -1, err.Error()
		}

		return map[string]any{
			"availableAmt": available.String(),
			"lockedAmt": locked.String(),
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}


func getAssetAmount_SatsNet(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "STPManager not initialized")
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
		available, locked, err := _mgr.GetAssetAmount_SatsNet(address, assetName)
		if err != nil {
			return nil, -1, err.Error()
		}

		return map[string]any{
			"availableAmt": available.String(),
			"lockedAmt": locked.String(),
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}


func getSupportedContracts(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "STPManager not initialized")
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
		return createJsRet(nil, -1, "STPManager not initialized")
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
		return createJsRet(nil, -1, "STPManager not initialized")
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


func getFeeForDeployContract(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "STPManager not initialized")
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

func getParamForInvokeContract(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "STPManager not initialized")
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
		return createJsRet(nil, -1, "STPManager not initialized")
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
		fee, err := _mgr.QueryFeeForInvokeContract(url, (invoke))
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
		return createJsRet(nil, -1, "STPManager not initialized")
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
		return createJsRet(nil, -1, "STPManager not initialized")
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
		return createJsRet(nil, -1, "STPManager not initialized")
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
		return createJsRet(nil, -1, "STPManager not initialized")
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
		return createJsRet(nil, -1, "STPManager not initialized")
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
		return createJsRet(nil, -1, "STPManager not initialized")
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


func getAddressStatusInContract(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "STPManager not initialized")
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
	// input: mnemonic, password; return: walletId
	obj.Set("importWallet", js.FuncOf(importWallet))
	// input: password; return: current walletId
	obj.Set("unlockWallet", js.FuncOf(unlockWallet))
	// input: none; return: list of wallet id and account number
	obj.Set("getAllWallets", js.FuncOf(getAllWallets))
	// input: wallet id; return: ok
	obj.Set("switchWallet", js.FuncOf(switchWallet))
	// input: account id; return: ok
	obj.Set("switchAccount", js.FuncOf(switchAccount))
	// input: mainnet or testnet
	obj.Set("switchChain", js.FuncOf(switchChain))
	// input: walletid, password; return: mnemonic
	obj.Set("getMnemonice", js.FuncOf(getMnemonic))
	// input: account id; return: current wallet p2tr address
	obj.Set("getWalletAddress", js.FuncOf(getWalletAddress))
	// input: account id; return: current wallet public key
	obj.Set("getWalletPubkey", js.FuncOf(getWalletPubkey))
	// input: node pubkey(hex string), index; return: commit secrect (hex string)
	obj.Set("getCommitSecret", js.FuncOf(getCommitSecret))
	// input: commit secrect(hex string), index; return: revocation priv key (hex string)
	obj.Set("deriveRevocationPrivKey", js.FuncOf(deriveRevocationPrivKey))
	// input: none; return: revocation base key (hex string)
	obj.Set("getRevocationBaseKey", js.FuncOf(getRevocationBaseKey))
	// input: none; return: node pubkey (hex string)
	obj.Set("getNodePubKey", js.FuncOf(getNodePubKey))
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
	obj.Set("sendAssets_SatsNet", js.FuncOf(sendAssets_SatsNet))
	obj.Set("batchSendAssets_SatsNet", js.FuncOf(batchSendAssets_SatsNet))
	obj.Set("batchSendAssets", js.FuncOf(batchSendAssets))

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


	obj.Set("getSupportedContracts", js.FuncOf(getSupportedContracts))
	obj.Set("getDeployedContractsInServer", js.FuncOf(getDeployedContractsInServer))
	obj.Set("getDeployedContractStatus", js.FuncOf(getDeployedContractStatus))
	obj.Set("getFeeForDeployContract", js.FuncOf(getFeeForDeployContract))
	obj.Set("getParamForInvokeContract", js.FuncOf(getParamForInvokeContract))
	obj.Set("getFeeForInvokeContract", js.FuncOf(getFeeForInvokeContract))
	obj.Set("invokeContract_SatsNet", js.FuncOf(invokeContract_SatsNet))
	obj.Set("invokeContractV2_SatsNet", js.FuncOf(invokeContractV2_SatsNet))
	obj.Set("invokeContractV2", js.FuncOf(invokeContractV2))
	obj.Set("getContractInvokeHistoryInServer", js.FuncOf(getContractInvokeHistoryInServer)) 
	obj.Set("getContractInvokeHistoryByAddressInServer", js.FuncOf(getContractInvokeHistoryByAddressInServer))
	obj.Set("getAllAddressInContract", js.FuncOf(getAllAddressInContract))
	obj.Set("getAddressStatusInContract", js.FuncOf(getAddressStatusInContract))


	js.Global().Set(module, obj)
	wallet.Log.SetLevel(logrus.DebugLevel)
	<-make(chan bool)
}
