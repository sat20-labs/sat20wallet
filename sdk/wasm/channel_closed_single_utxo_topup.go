//go:build js && wasm
// +build js,wasm

package main

import (
	"strconv"
	"syscall/js"
)

func quoteClosedChannelSingleUTXOTopup(this js.Value, args []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(args) != 1 || args[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "Expected an explicit fee rate string")
	}
	feeRate, err := strconv.ParseInt(args[0].String(), 10, 64)
	if err != nil || feeRate <= 0 {
		return createJsRet(nil, -1, "Fee rate must be a positive integer")
	}
	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		quote, err := _mgr.QuoteClosedChannelSingleUTXOTopup(feeRate)
		if err != nil {
			return nil, -1, err.Error()
		}
		return quote, 0, "ok"
	})
	return js.Global().Get("Promise").New(handler)
}

func confirmClosedChannelSingleUTXOTopup(this js.Value, args []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(args) != 1 || args[0].Type() != js.TypeString || args[0].String() == "" {
		return createJsRet(nil, -1, "Expected the exact approved quote")
	}
	quote := args[0].String()
	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		result, err := _mgr.ConfirmClosedChannelSingleUTXOTopup(quote)
		if err != nil {
			return nil, -1, err.Error()
		}
		return result, 0, "ok"
	})
	return js.Global().Get("Promise").New(handler)
}

func previewClosedChannelRecovery(this js.Value, args []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(args) != 1 || args[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "Expected a channel identifier string; empty selects this account local channel")
	}
	channelID := args[0].String()
	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		options, err := _mgr.PreviewClosedChannelRecovery(channelID)
		if err != nil {
			return nil, -1, err.Error()
		}
		return options, 0, "ok"
	})
	return js.Global().Get("Promise").New(handler)
}

func withdrawClosedChannelExact(this js.Value, args []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(args) != 2 || args[0].Type() != js.TypeString || args[1].Type() != js.TypeString || args[0].String() == "" || args[1].String() == "" {
		return createJsRet(nil, -1, "Expected the exact reviewed channel and server closing parent")
	}
	channelID, parentID := args[0].String(), args[1].String()
	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		id, err := _mgr.WithdrawClosedChannelExact(channelID, parentID)
		if err != nil {
			return nil, -1, err.Error()
		}
		return map[string]interface{}{"resvId": strconv.FormatInt(id, 10)}, 0, "ok"
	})
	return js.Global().Get("Promise").New(handler)
}
