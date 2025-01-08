//go:build js && wasm
// +build js,wasm

package main

import (
	"fmt"
	"strconv"
	"strings"
	"syscall/js"

	"github.com/sat20-labs/sat20wallet/wallet"
	"github.com/sirupsen/logrus"
)

const (
	module = "sat20wallet_wasm"
)

var _mgr *wallet.Manager

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

func parseConfigFromJS(jsConfig js.Value) (*wallet.Config, error) {
	cfg := &wallet.Config{}

	// Chain
	if chain := jsConfig.Get("Chain"); chain.Type() == js.TypeString {
		cfg.Chain = chain.String()
	} else {
		return nil, fmt.Errorf("Chain must be a string")
	}

	// Mode
	if mode := jsConfig.Get("Mode"); mode.Type() == js.TypeString {
		cfg.Mode = mode.String()
	} else {
		return nil, fmt.Errorf("Mode must be a string")
	}

	// Btcd
	btcd := jsConfig.Get("Btcd")
	if btcd.Type() == js.TypeObject {
		if host := btcd.Get("Host"); host.Type() == js.TypeString {
			cfg.Btcd.Host = host.String()
		} else {
			return nil, fmt.Errorf("Btcd.Host must be a string")
		}
		if user := btcd.Get("User"); user.Type() == js.TypeString {
			cfg.Btcd.User = user.String()
		} else {
			return nil, fmt.Errorf("Btcd.User must be a string")
		}
		if password := btcd.Get("Password"); password.Type() == js.TypeString {
			cfg.Btcd.Password = password.String()
		} else {
			return nil, fmt.Errorf("Btcd.Password must be a string")
		}
		if zmqpubrawblock := btcd.Get("Zmqpubrawblock"); zmqpubrawblock.Type() == js.TypeString {
			cfg.Btcd.Zmqpubrawblock = zmqpubrawblock.String()
		} else {
			return nil, fmt.Errorf("Btcd.Zmqpubrawblock must be a string")
		}
		if zmqpubrawtx := btcd.Get("Zmqpubrawtx"); zmqpubrawtx.Type() == js.TypeString {
			cfg.Btcd.Zmqpubrawtx = zmqpubrawtx.String()
		} else {
			return nil, fmt.Errorf("Btcd.Zmqpubrawtx must be a string")
		}
	} else {
		return nil, fmt.Errorf("Btcd must be an object")
	}

	// IndexerL1
	indexerL1 := jsConfig.Get("IndexerL1")
	if indexerL1.Type() == js.TypeObject {
		if scheme := indexerL1.Get("Scheme"); scheme.Type() == js.TypeString {
			cfg.IndexerL1.Scheme = scheme.String()
		} else {
			cfg.IndexerL1.Scheme = "http"
		}
		if host := indexerL1.Get("Host"); host.Type() == js.TypeString {
			cfg.IndexerL1.Host = host.String()
		} else {
			return nil, fmt.Errorf("IndexerL1.Host must be a string")
		}
	} else {
		return nil, fmt.Errorf("IndexerL1 must be an object")
	}

	// IndexerL2
	indexerL2 := jsConfig.Get("IndexerL2")
	if indexerL2.Type() == js.TypeObject {
		if scheme := indexerL2.Get("Scheme"); scheme.Type() == js.TypeString {
			cfg.IndexerL2.Scheme = scheme.String()
		} else {
			cfg.IndexerL2.Scheme = "http"
		}
		if host := indexerL2.Get("Host"); host.Type() == js.TypeString {
			cfg.IndexerL2.Host = host.String()
		} else {
			return nil, fmt.Errorf("IndexerL2.Host must be a string")
		}
	} else {
		return nil, fmt.Errorf("IndexerL2 must be an object")
	}

	// Log
	if log := jsConfig.Get("Log"); log.Type() == js.TypeString {
		cfg.Log = log.String()
	} else {
		return nil, fmt.Errorf("Log must be a string")
	}

	return cfg, nil
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

	err = db.BatchRead([]byte("intValue"), func(k, v []byte) error {
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
	code := 0
	msg := "ok"
	if _mgr != nil {
		code = -1
		msg = "STPManager is initialized"
		return createJsRet(nil, code, msg)
	}

	if len(p) < 2 {
		errMsg := "Expected 1 parameters"
		wallet.Log.Error(errMsg)
		return createJsRet(nil, 1, errMsg)
	}

	if p[0].Type() != js.TypeObject {
		code = -1
		msg = "config parameter should be a string"
		wallet.Log.Error(msg)
		return createJsRet(nil, code, msg)
	}
	var cfg *wallet.Config
	cfg, err := parseConfigFromJS(p[0])
	if err != nil {
		return createJsRet(nil, -1, fmt.Sprintf("Failed to parse config: %v", err))
	}

	if p[1].Type() != js.TypeNumber {
		code = -1
		msg = "log level parameter should be a number, 0: Panic, 1: Fatal, 2: Error, 3: Warn, 4: Info, 5: Debug, 6: Trace"
		wallet.Log.Error(msg)
		return createJsRet(nil, code, msg)
	}

	logLevel := logrus.Level(p[1].Int())
	if logLevel > 6 {
		code = -1
		msg = "log level parameter should be a number, 0: Panic, 1: Fatal, 2: Error, 3: Warn, 4: Info, 5: Debug, 6: Trace"
		wallet.Log.Error(msg)
		return createJsRet(nil, code, msg)
	}
	wallet.Log.SetLevel(logLevel)

	_mgr = wallet.NewManager(cfg, make(chan struct{}))
	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		err := _mgr.Init()
		if err != nil {
			wallet.Log.Errorf("init error: %v", err)
			return nil, -1, err.Error()
		}
		return nil, 0, "ok"
	})
	
	wallet.Log.Info("STP manager initialized")
	return js.Global().Get("Promise").New(jsHandler)
}


func releaseManager(this js.Value, p []js.Value) any {
	code := 0
	msg := "ok"
	if _mgr == nil {
		code = -1
		msg = "STPManager not initialized"
		return createJsRet(nil, code, msg)
	}
	_mgr.Close()
	_mgr = nil
	return createJsRet(nil, code, msg)
}

func createWallet(this js.Value, p []js.Value) any {
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
		msg = "password parameter should be a string"
		wallet.Log.Error(msg)
		return createJsRet(nil, code, msg)
	}
	password := p[0].String()
	mnemonic, err := _mgr.CreateWallet(password)
	if err != nil {
		code = -1
		msg = err.Error()
		return createJsRet(nil, code, msg)
	}
	data := map[string]any{
		"mnemonic": mnemonic,
	}
	return createJsRet(data, code, msg)
}


func isWalletExist(this js.Value, p []js.Value) any {
	code := 0
	msg := "ok"
	if _mgr == nil {
		code = -1
		msg = "STPManager not initialized"
		return createJsRet(nil, code, msg)
	}
	exist := _mgr.IsWalletExist()
	return createJsRet(exist, code, msg)
}

func importWallet(this js.Value, p []js.Value) any {
	code := 0
	msg := "ok"
	if _mgr == nil {
		code = -1
		msg = "STPManager not initialized"
		return createJsRet(nil, code, msg)
	}
	if len(p) < 2 {
		code = -1
		msg = "Expected 2 parameters"
		wallet.Log.Error(msg)
		return createJsRet(nil, code, msg)
	}

	if p[0].Type() != js.TypeString {
		code = -1
		msg = "mnemonic parameter should be a string"
		wallet.Log.Error(msg)
		return createJsRet(nil, code, msg)
	}
	mnemonic := p[0].String()

	if p[1].Type() != js.TypeString {
		code = -1
		msg = "password parameter should be a string"
		wallet.Log.Error(msg)
		return createJsRet(nil, code, msg)
	}
	password := p[1].String()

	wallet.Log.Infof("ImportWallet %s %s", mnemonic, password)
	err := _mgr.ImportWallet(mnemonic, password)
	if err != nil {
		code = -1
		msg = err.Error()
		return createJsRet(nil, code, msg)
	}
	return createJsRet(nil, code, msg)
}

func unlockWallet(this js.Value, p []js.Value) any {
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
		msg = "password parameter should be a string"
		wallet.Log.Error(msg)
		return createJsRet(nil, code, msg)
	}
	password := p[0].String()
	err := _mgr.UnlockWallet(password)

	if err != nil {
		code = -1
		msg = err.Error()
		return createJsRet(nil, code, msg)
	}
	return createJsRet(nil, code, msg)
}

func getMnemonic(this js.Value, p []js.Value) any {
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
		msg = "password parameter should be a string"
		wallet.Log.Error(msg)
		return createJsRet(nil, code, msg)
	}
	password := p[0].String()
	mnemonic := _mgr.GetMnemonic(password)

	return createJsRet(mnemonic, code, msg)
}

func getWallet(this js.Value, p []js.Value) any {
	code := 0
	msg := "ok"
	if _mgr == nil {
		code = -1
		msg = "STPManager not initialized"
		return createJsRet(nil, code, msg)
	}
	_wallet := _mgr.GetWallet()
	if _wallet == nil {
		code = -1
		msg = "wallet is nil"
		wallet.Log.Error(msg)
		return createJsRet(nil, code, msg)
	}
	data := map[string]any{
		"paymentAddress": _wallet.GetP2TRAddress(),
	}
	return createJsRet(data, code, msg)
}


func sendUtxosL1(this js.Value, p []js.Value) any {
	code := 0
	msg := "ok"
	if _mgr == nil {
		code = -1
		msg = "STPManager not initialized"
		return createJsRet(nil, code, msg)
	}

	if len(p) < 3 {
		code = -1
		msg = "Expected 3 parameters"
		wallet.Log.Error(msg)
		return createJsRet(nil, code, msg)
	}

	if p[0].Type() != js.TypeString {
		code = -1
		msg = "chanPoint parameter should be a string"
		wallet.Log.Error(msg)
		return createJsRet(nil, code, msg)
	}
	destAddress := p[0].String()

	utxoList, err := getStringVector(p[1])
	if err != nil {
		msg = err.Error()
		wallet.Log.Error(msg)
		return createJsRet(nil, -1, msg)
	}

	// amount
	p2 := p[2]
	if p2.Type() != js.TypeNumber {
		code = -1
		msg = "amount parameter should be a int"
		wallet.Log.Error(msg)
		return createJsRet(nil, code, msg)
	}
	amt := p2.Int()

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		txid, err := _mgr.SendUtxos(destAddress, utxoList, int64(amt))
		if err != nil {
			wallet.Log.Errorf("SendUtxos error: %v", err)
			return nil, -1, err.Error()
		}

		return map[string]interface{}{
			"txId": txid,
		}, 0, "ok"
	})
	return js.Global().Get("Promise").New(jsHandler)
}

func sendUtxos(this js.Value, p []js.Value) any {
	code := 0
	msg := "ok"
	if _mgr == nil {
		code = -1
		msg = "STPManager not initialized"
		return createJsRet(nil, code, msg)
	}

	if len(p) < 3 {
		code = -1
		msg = "Expected 3 parameters"
		wallet.Log.Error(msg)
		return createJsRet(nil, code, msg)
	}

	if p[0].Type() != js.TypeString {
		code = -1
		msg = "chanPoint parameter should be a string"
		wallet.Log.Error(msg)
		return createJsRet(nil, code, msg)
	}
	destAddress := p[0].String()

	utxoList, err := getStringVector(p[1])
	if err != nil {
		msg = err.Error()
		wallet.Log.Error(msg)
		return createJsRet(nil, -1, msg)
	}

	feeList, err := getStringVector(p[2])
	if err != nil {
		msg = err.Error()
		wallet.Log.Error(msg)
		return createJsRet(nil, -1, msg)
	}


	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		txid, err := _mgr.SendUtxos_SatsNet(destAddress, utxoList, feeList)
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

func sendAssets(this js.Value, p []js.Value) any {
	code := 0
	msg := "ok"
	if _mgr == nil {
		code = -1
		msg = "STPManager not initialized"
		return createJsRet(nil, code, msg)
	}

	if len(p) < 3 {
		code = -1
		msg = "Expected 3 parameters"
		wallet.Log.Error(msg)
		return createJsRet(nil, code, msg)
	}

	if p[0].Type() != js.TypeString {
		code = -1
		msg = "chanPoint parameter should be a string"
		wallet.Log.Error(msg)
		return createJsRet(nil, code, msg)
	}
	destAddress := p[0].String()

	if p[1].Type() != js.TypeString {
		code = -1
		msg = "chanPoint parameter should be a string"
		wallet.Log.Error(msg)
		return createJsRet(nil, code, msg)
	}
	assetName := p[1].String()

	// amount
	p2 := p[2]
	if p2.Type() != js.TypeNumber {
		code = -1
		msg = "amount parameter should be a int"
		wallet.Log.Error(msg)
		return createJsRet(nil, code, msg)
	}
	amt := p2.Int()

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		txid, err := _mgr.SendAssets_SatsNet(destAddress, assetName, int64(amt))
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


func getVersion(this js.Value, p []js.Value) any {
	code := 0
	msg := "ok"
	return createJsRet(wallet.SOFTWARE_VERSION, code, msg)
}


func registerCallbacks(this js.Value, args []js.Value) interface{} {
	code := 0
	msg := "ok"
	if len(args) != 1 {
		return nil
	}
	if _mgr == nil {
		code = -1
		msg = "STPManager not initialized"
		return createJsRet(nil, code, msg)
	}
	callback := args[0]
	_mgr.RegisterCallback(callback)
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


func main() {
	obj := js.Global().Get("Object").New()
	//obj.Set("batchDbTest", js.FuncOf(batchDbTest))
	obj.Set("init", js.FuncOf(initManager))
	obj.Set("release", js.FuncOf(releaseManager))
	obj.Set("isWalletExist", js.FuncOf(isWalletExist))
	obj.Set("createWallet", js.FuncOf(createWallet))
	obj.Set("importWallet", js.FuncOf(importWallet))
	obj.Set("unlockWallet", js.FuncOf(unlockWallet))
	obj.Set("getMnemonice", js.FuncOf(getMnemonic))
	obj.Set("getWallet", js.FuncOf(getWallet))
	
	obj.Set("sendUtxosL1", js.FuncOf(sendUtxosL1))
	obj.Set("sendUtxos", js.FuncOf(sendUtxos))
	obj.Set("sendAssets", js.FuncOf(sendAssets))
	obj.Set("getVersion", js.FuncOf(getVersion))
	obj.Set("registerCallback", js.FuncOf(registerCallbacks))
	
	js.Global().Set(module, obj)
	wallet.Log.SetLevel(logrus.DebugLevel)
	<-make(chan bool)
}

// func NewDefaultYamlConf() *wallet.Config {
// 	chain := "testnet4"
// 	ret := &wallet.Config{
// 		Chain: chain,
// 		Mode:  "client",
// 		Btcd: wallet.Bitcoin{
// 			Host:           "192.168.10.102:28332",
// 			User:           "jacky",
// 			Password:       "123456",
// 			Zmqpubrawblock: "tcp://192.168.10.102:58332",
// 			Zmqpubrawtx:    "tcp://192.168.10.102:58333",
// 		},
// 		IndexerL1: wallet.Indexer{
// 			Host: "192.168.10.104:8009",
// 		},
// 		IndexerL2: wallet.Indexer{
// 			Host: "192.168.10.104:8019",
// 		},
// 		Log: "debug",
// 	}

// 	return ret
// }
