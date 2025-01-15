//go:build js && wasm
// +build js,wasm

package main

import (
	"fmt"
	"sort"
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
		msg = "Manager is initialized"
		return createJsRet(nil, code, msg)
	}

	if len(p) < 2 {
		errMsg := "Expected 2 parameters"
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

	jsHandler := createAsyncJsHandler(func() (interface{}, int, string) {
		_mgr = wallet.NewManager(cfg, make(chan struct{}))
		if _mgr == nil {
			wallet.Log.Errorf("NewManager failed: %v", err)
			return nil, -1, err.Error()
		}
		return nil, 0, "ok"
	})

	wallet.Log.Info("Manager created")
	return js.Global().Get("Promise").New(jsHandler)
}

func releaseManager(this js.Value, p []js.Value) any {
	code := 0
	msg := "ok"
	if _mgr == nil {
		code = -1
		msg = "Manager not initialized"
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
		msg = "password parameter should be a string"
		wallet.Log.Error(msg)
		return createJsRet(nil, code, msg)
	}
	password := p[0].String()
	id, mnemonic, err := _mgr.CreateWallet(password)
	if err != nil {
		code = -1
		msg = err.Error()
		return createJsRet(nil, code, msg)
	}
	data := map[string]any{
		"walletId": id,
		"mnemonic": mnemonic,
	}
	return createJsRet(data, code, msg)
}

func isWalletExist(this js.Value, p []js.Value) any {
	code := 0
	msg := "ok"
	if _mgr == nil {
		code = -1
		msg = "Manager not initialized"
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
		msg = "Manager not initialized"
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
	id, err := _mgr.ImportWallet(mnemonic, password)
	if err != nil {
		code = -1
		msg = err.Error()
		return createJsRet(nil, code, msg)
	}
	data := map[string]any{
		"walletId": id,
	}
	return createJsRet(data, code, msg)
}

func unlockWallet(this js.Value, p []js.Value) any {
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
		msg = "password parameter should be a string"
		wallet.Log.Error(msg)
		return createJsRet(nil, code, msg)
	}
	password := p[0].String()
	id, err := _mgr.UnlockWallet(password)
	if err != nil {
		code = -1
		msg = err.Error()
		return createJsRet(nil, code, msg)
	}
	data := map[string]any{
		"walletId": id,
	}
	return createJsRet(data, code, msg)
}

func getAllWallets(this js.Value, p []js.Value) any {
	code := 0
	msg := "ok"
	if _mgr == nil {
		code = -1
		msg = "Manager not initialized"
		return createJsRet(nil, code, msg)
	}

	ids := _mgr.GetAllWallets()

	type WalletIdAndAccounts struct {
		Id int64
		Accounts int
	}
	result := make([]*WalletIdAndAccounts, 0)
	for k, v := range ids {
		result = append(result, &WalletIdAndAccounts{Id: k, Accounts: v})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Id < result[j].Id
	})
	data := map[string]any{
		"walletIds": result,
	}
	return createJsRet(data, code, msg)
}

func switchWallet(this js.Value, p []js.Value) any {
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
	if p[0].Type() != js.TypeNumber {
		code = -1
		msg = "Id parameter should be a number"
		wallet.Log.Error(msg)
		return createJsRet(nil, code, msg)
	}
	id := p[0].Int()

	_mgr.SwitchWallet(int64(id))

	return createJsRet(nil, code, msg)
}

func switchAccount(this js.Value, p []js.Value) any {
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
	if p[0].Type() != js.TypeNumber {
		code = -1
		msg = "Id parameter should be a number"
		wallet.Log.Error(msg)
		return createJsRet(nil, code, msg)
	}
	id := p[0].Int()

	_mgr.SwitchAccount(uint32(id))

	return createJsRet(nil, code, msg)
}

func switchChain(this js.Value, p []js.Value) any {
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
		msg = "chain parameter should be a string"
		wallet.Log.Error(msg)
		return createJsRet(nil, code, msg)
	}
	chain := p[0].String()

	_mgr.SwitchChain(chain)

	return createJsRet(nil, code, msg)
}

func getMnemonic(this js.Value, p []js.Value) any {
	code := 0
	msg := "ok"
	if _mgr == nil {
		code = -1
		msg = "Manager not initialized"
		return createJsRet(nil, code, msg)
	}
	if len(p) < 2 {
		code = -1
		msg = "Expected 2 parameters"
		wallet.Log.Error(msg)
		return createJsRet(nil, code, msg)
	}
	if p[0].Type() != js.TypeNumber {
		code = -1
		msg = "Id parameter should be a number"
		wallet.Log.Error(msg)
		return createJsRet(nil, code, msg)
	}
	id := p[0].Int()

	if p[1].Type() != js.TypeString {
		code = -1
		msg = "password parameter should be a string"
		wallet.Log.Error(msg)
		return createJsRet(nil, code, msg)
	}
	password := p[1].String()

	mnemonic := _mgr.GetMnemonic(int64(id), password)
	data := map[string]any{
		"mnemonic": mnemonic,
	}
	return createJsRet(data, code, msg)
}

func getWalletAddress(this js.Value, p []js.Value) any {
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
	if p[0].Type() != js.TypeNumber {
		code = -1
		msg = "Id parameter should be a number"
		wallet.Log.Error(msg)
		return createJsRet(nil, code, msg)
	}
	id := p[0].Int()
	_wallet := _mgr.GetWallet()
	if _wallet == nil {
		code = -1
		msg = "wallet is nil"
		wallet.Log.Error(msg)
		return createJsRet(nil, code, msg)
	}
	data := map[string]any{
		"address": _wallet.GetAddress(uint32(id)),
	}
	return createJsRet(data, code, msg)
}

func getWalletPubkey(this js.Value, p []js.Value) any {
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
	if p[0].Type() != js.TypeNumber {
		code = -1
		msg = "Id parameter should be a number"
		wallet.Log.Error(msg)
		return createJsRet(nil, code, msg)
	}
	id := p[0].Int()
	pubkey := _mgr.GetPublicKey(uint32(id))
	data := map[string]any{
		"pubkey": pubkey,
	}
	return createJsRet(data, code, msg)
}

func getCommitRootKey(this js.Value, p []js.Value) any {
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

	jsBytes := p[0]
	goBytes := make([]byte, jsBytes.Length())
	js.CopyBytesToGo(goBytes, jsBytes)

	result := _mgr.GetCommitRootKey(goBytes)

	jsBytes = js.Global().Get("Uint8Array").New(len(result))
	js.CopyBytesToJS(jsBytes, result)
	data := map[string]any{
		"commitRootKey": jsBytes,
	}
	return createJsRet(data, code, msg)
}

func getCommitSecret(this js.Value, p []js.Value) any {
	code := 0
	msg := "ok"
	if _mgr == nil {
		code = -1
		msg = "Manager not initialized"
		return createJsRet(nil, code, msg)
	}

	if len(p) < 2 {
		code = -1
		msg = "Expected 2 parameters"
		wallet.Log.Error(msg)
		return createJsRet(nil, code, msg)
	}

	jsBytes := p[0]
	goBytes := make([]byte, jsBytes.Length())
	js.CopyBytesToGo(goBytes, jsBytes)

	if p[1].Type() != js.TypeNumber {
		code = -1
		msg = "Id parameter should be a number"
		wallet.Log.Error(msg)
		return createJsRet(nil, code, msg)
	}
	index := p[1].Int()

	result := _mgr.GetCommitSecret(goBytes, index)

	jsBytes = js.Global().Get("Uint8Array").New(len(result))
	js.CopyBytesToJS(jsBytes, result)
	data := map[string]any{
		"commitSecret": jsBytes,
	}
	return createJsRet(data, code, msg)
}

func deriveRevocationPrivKey(this js.Value, p []js.Value) any {
	code := 0
	msg := "ok"
	if _mgr == nil {
		code = -1
		msg = "Manager not initialized"
		return createJsRet(nil, code, msg)
	}

	if len(p) < 1 {
		code = -1
		msg = "Expected 2 parameters"
		wallet.Log.Error(msg)
		return createJsRet(nil, code, msg)
	}

	jsBytes := p[0]
	goBytes := make([]byte, jsBytes.Length())
	js.CopyBytesToGo(goBytes, jsBytes)

	result := _mgr.DeriveRevocationPrivKey(goBytes)

	jsBytes = js.Global().Get("Uint8Array").New(len(result))
	js.CopyBytesToJS(jsBytes, result)
	data := map[string]any{
		"revocationPrivKey": jsBytes,
	}
	return createJsRet(data, code, msg)
}

func getRevocationBaseKey(this js.Value, p []js.Value) any {
	code := 0
	msg := "ok"
	if _mgr == nil {
		code = -1
		msg = "Manager not initialized"
		return createJsRet(nil, code, msg)
	}

	result := _mgr.GetRevocationBaseKey()

	jsBytes := js.Global().Get("Uint8Array").New(len(result))
	js.CopyBytesToJS(jsBytes, result)
	data := map[string]any{
		"revocationBaseKey": jsBytes,
	}
	return createJsRet(data, code, msg)
}

func getNodePubKey(this js.Value, p []js.Value) any {
	code := 0
	msg := "ok"
	if _mgr == nil {
		code = -1
		msg = "Manager not initialized"
		return createJsRet(nil, code, msg)
	}

	result := _mgr.GetNodePubKey()

	jsBytes := js.Global().Get("Uint8Array").New(len(result))
	js.CopyBytesToJS(jsBytes, result)
	data := map[string]any{
		"nodePubKey": jsBytes,
	}
	return createJsRet(data, code, msg)
}

func signMessage(this js.Value, p []js.Value) any {
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

	jsBytes := p[0]

	// 创建一个Go的字节切片，长度为jsBytes的长度
	goBytes := make([]byte, jsBytes.Length())

	// 将JavaScript字节数组复制到Go字节数组中
	js.CopyBytesToGo(goBytes, jsBytes)

	result, err := _mgr.SignMessage(goBytes)
	if err != nil {
		code = -1
		msg = "SignMessage failed"
		return createJsRet(nil, code, msg)
	}

	jsBytes = js.Global().Get("Uint8Array").New(len(result))
	js.CopyBytesToJS(jsBytes, result)
	data := map[string]any{
		"signature": jsBytes,
	}
	return createJsRet(data, code, msg)
}

func signPsbt(this js.Value, p []js.Value) any {
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
		msg = "psbt parameter should be a hex string"
		wallet.Log.Error(msg)
		return createJsRet(nil, code, msg)
	}
	psbtHex := p[0].String()

	result, err := _mgr.SignPsbt(psbtHex)
	if err != nil {
		code = -1
		msg = "SignPsbt failed"
		return createJsRet(nil, code, msg)
	}

	data := map[string]any{
		"psbt": result,
	}
	return createJsRet(data, code, msg)
}

func signPsbt_SatsNet(this js.Value, p []js.Value) any {
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
		msg = "psbt parameter should be a hex string"
		wallet.Log.Error(msg)
		return createJsRet(nil, code, msg)
	}
	psbtHex := p[0].String()

	result, err := _mgr.SignPsbt_SatsNet(psbtHex)
	if err != nil {
		code = -1
		msg = "SignPsbt failed"
		return createJsRet(nil, code, msg)
	}

	data := map[string]any{
		"psbt": result,
	}
	return createJsRet(data, code, msg)
}

func sendUtxos(this js.Value, p []js.Value) any {
	code := 0
	msg := "ok"
	if _mgr == nil {
		code = -1
		msg = "Manager not initialized"
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

func sendUtxos_SatsNet(this js.Value, p []js.Value) any {
	code := 0
	msg := "ok"
	if _mgr == nil {
		code = -1
		msg = "Manager not initialized"
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

func sendAssets_SatsNet(this js.Value, p []js.Value) any {
	code := 0
	msg := "ok"
	if _mgr == nil {
		code = -1
		msg = "Manager not initialized"
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
	data := map[string]any{
		"version": wallet.SOFTWARE_VERSION,
	}
	return createJsRet(data, code, msg)
}

func registerCallbacks(this js.Value, args []js.Value) interface{} {
	code := 0
	msg := "ok"
	if len(args) != 1 {
		return nil
	}
	if _mgr == nil {
		code = -1
		msg = "Manager not initialized"
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
	// input: node pubkey(Uint8Array); return: commit root key (Uint8Array)
	obj.Set("getCommitRootKey", js.FuncOf(getCommitRootKey))
	// input: node pubkey(Uint8Array), index; return: commit secrect (Uint8Array)
	obj.Set("getCommitSecret", js.FuncOf(getCommitSecret))
	// input: commit secrect(Uint8Array), index; return: revocation priv key (Uint8Array)
	obj.Set("deriveRevocationPrivKey", js.FuncOf(deriveRevocationPrivKey))
	// input: none, index; return: revocation base key (Uint8Array)
	obj.Set("getRevocationBaseKey", js.FuncOf(getRevocationBaseKey))
	// input: none, index; return: node pubkey (Uint8Array)
	obj.Set("getNodePubKey", js.FuncOf(getNodePubKey))
	// input: message (Uint8Array) return: signature (Uint8Array)
	obj.Set("signMessage", js.FuncOf(signMessage))
	// input: psbt(hexString); return: signed psbt (hexString)
	obj.Set("signPsbt", js.FuncOf(signPsbt))
	// input: psbt(hexString); return: signed psbt (hexString)
	obj.Set("signPsbt_SatsNet", js.FuncOf(signPsbt_SatsNet))

	obj.Set("sendUtxos", js.FuncOf(sendUtxos))
	obj.Set("sendUtxos_SatsNet", js.FuncOf(sendUtxos_SatsNet))
	obj.Set("sendAssets_SatsNet", js.FuncOf(sendAssets_SatsNet))
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
