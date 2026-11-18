//go:build js && wasm

package main

import "syscall/js"

func checkDIDName(_ js.Value, args []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(args) != 1 || args[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "name is required")
	}
	name := args[0].String()
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		status, err := _mgr.CheckDIDName(name)
		if err != nil {
			return nil, -1, err.Error()
		}
		return map[string]any{"status": status}, 0, "ok"
	}))
}
