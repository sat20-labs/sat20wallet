//go:build js && wasm

package main

import (
	"context"
	"fmt"
	"strconv"
	"syscall/js"

	"github.com/sat20-labs/sat20wallet/sdk/wallet"
)

func silentPayAccountArgs(args []js.Value) (int64, uint32, error) {
	if len(args) < 2 || args[0].Type() != js.TypeString ||
		args[1].Type() != js.TypeNumber {
		return 0, 0, fmt.Errorf("silentpay: wallet ID and account index are required")
	}
	id, err := strconv.ParseInt(args[0].String(), 10, 64)
	if err != nil { return 0, 0, err }
	value := args[1].Float()
	if value < 0 || value >= float64(uint64(1)<<31) || float64(uint32(value)) != value {
		return 0, 0, fmt.Errorf("silentpay: invalid account index")
	}
	return id, uint32(value), nil
}

func enableSilentPay(this js.Value, args []js.Value) any {
	if _mgr == nil { return createJsRet(nil, -1, "Manager not initialized") }
	id, index, err := silentPayAccountArgs(args)
	if err != nil { return createJsRet(nil, -1, err.Error()) }
	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		address, err := _mgr.EnableSilentPay(id, index)
		if err != nil { return nil, -1, err.Error() }
		return map[string]any{"enabled":true,"address":address}, 0, "ok"
	})
	return js.Global().Get("Promise").New(handler)
}

func getSilentPayAccount(this js.Value, args []js.Value) any {
	if _mgr == nil { return createJsRet(nil, -1, "Manager not initialized") }
	id, index, err := silentPayAccountArgs(args)
	if err != nil { return createJsRet(nil, -1, err.Error()) }
	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		enabled, address, err := _mgr.SilentPayAccount(id, index)
		if err != nil { return nil, -1, err.Error() }
		return map[string]any{"enabled":enabled,"address":address}, 0, "ok"
	})
	return js.Global().Get("Promise").New(handler)
}

func syncSilentPayLive(this js.Value, args []js.Value) any {
	if _mgr == nil { return createJsRet(nil, -1, "Manager not initialized") }
	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		result, err := _mgr.SyncSilentPayLive(context.Background())
		if err != nil { return nil, -1, err.Error() }
		return jsSafeData(map[string]any{"accounts":result}), 0, "ok"
	})
	return js.Global().Get("Promise").New(handler)
}

func getSilentPayBalance(this js.Value, args []js.Value) any {
	if _mgr == nil { return createJsRet(nil, -1, "Manager not initialized") }
	id, index, err := silentPayAccountArgs(args)
	if err != nil { return createJsRet(nil, -1, err.Error()) }
	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		balance, err := _mgr.LastSyncedSilentPay(id,index)
		if err != nil { return nil, -1, err.Error() }
		return jsSafeData(map[string]any{"balance":balance}), 0, "ok"
	})
	return js.Global().Get("Promise").New(handler)
}

func sendSilentPayBitcoin(this js.Value, args []js.Value) any {
	if _mgr == nil { return createJsRet(nil, -1, "Manager not initialized") }
	if len(args) < 3 || args[0].Type() != js.TypeString ||
		args[1].Type() != js.TypeString || args[2].Type() != js.TypeString {
		return createJsRet(nil, -1, "silentpay: address, amount (sats) and fee rate are required")
	}
	address := args[0].String()
	amount, err := strconv.ParseInt(args[1].String(), 10, 64)
	if err != nil { return createJsRet(nil,-1,err.Error()) }
	fee, err := strconv.ParseInt(args[2].String(), 10, 64)
	if err != nil { return createJsRet(nil,-1,err.Error()) }
	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		txid, err := _mgr.SendSilentPayment(address, amount, fee)
		if err != nil { return nil, -1, err.Error() }
		return map[string]any{"txId":txid},0,"ok"
	})
	return js.Global().Get("Promise").New(handler)
}

var _ = wallet.ErrSilentPayUnsupported
