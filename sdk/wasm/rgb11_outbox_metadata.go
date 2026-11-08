//go:build js && wasm

package main

import (
	"encoding/json"
	"syscall/js"

	"github.com/sat20-labs/sat20wallet/sdk/wallet"
)

func getRGB11TransferOutboxMetadata(this js.Value, args []js.Value) any {
	if len(args) != 1 || args[0].Type() != js.TypeString {
		return createJsRet(nil, -1, wallet.ErrRGB11OutboxInvalidID.Error())
	}
	transferID := args[0].String()
	managerLifecycleMu.Lock()
	manager := _mgr
	unavailable := manager == nil || managerClosing || managerInitializing
	managerLifecycleMu.Unlock()
	if unavailable {
		return createJsRet(nil, -1, wallet.ErrRGB11OutboxUnavailable.Error())
	}
	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		metadata, err := manager.GetRGB11TransferOutboxMetadata(transferID)
		if err != nil {
			return nil, -1, err.Error() // SDK returns only fixed, sanitized errors.
		}
		encoded, err := json.Marshal(metadata)
		if err != nil {
			return nil, -1, wallet.ErrRGB11OutboxCorrupt.Error()
		}
		return map[string]any{"metadata": string(encoded)}, 0, "ok"
	})
	return js.Global().Get("Promise").New(handler)
}
