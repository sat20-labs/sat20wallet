//go:build js && wasm
// +build js,wasm

package main

import (
	"encoding/json"
	"strings"
	"syscall/js"

	"github.com/sat20-labs/sat20wallet/sdk/wallet"
)

const operationLogModule = "sat20wallet_operation_log"

type operationLogCreateRequest struct {
	Category   string            `json:"category"`
	Action     string            `json:"action"`
	Title      string            `json:"title"`
	Summary    string            `json:"summary"`
	Parameters map[string]string `json:"parameters"`
}

type operationLogUpdateRequest struct {
	Status  string            `json:"status"`
	Message string            `json:"message"`
	Details map[string]string `json:"details"`
	Result  map[string]string `json:"result"`
	TxID    string            `json:"txid"`
}

func beginOperationLog(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(p) != 1 || p[0].Type() != js.TypeString || strings.TrimSpace(p[0].String()) == "" {
		return createJsRet(nil, -1, "operation log request is required")
	}
	var req operationLogCreateRequest
	if err := json.Unmarshal([]byte(p[0].String()), &req); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		record, err := _mgr.BeginOperationLog(wallet.OperationLogCreate{
			Category:   req.Category,
			Action:     req.Action,
			Title:      req.Title,
			Summary:    req.Summary,
			Parameters: req.Parameters,
		})
		if err != nil {
			return nil, -1, err.Error()
		}
		return map[string]any{"id": record.ID}, 0, "ok"
	})
	return js.Global().Get("Promise").New(handler)
}

func updateOperationLog(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(p) != 2 || p[0].Type() != js.TypeString || strings.TrimSpace(p[0].String()) == "" ||
		p[1].Type() != js.TypeString || strings.TrimSpace(p[1].String()) == "" {
		return createJsRet(nil, -1, "operation log id and update are required")
	}
	id := strings.TrimSpace(p[0].String())
	var req operationLogUpdateRequest
	if err := json.Unmarshal([]byte(p[1].String()), &req); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		record, err := _mgr.UpdateOperationLog(id, wallet.OperationLogUpdate{
			Status:  wallet.OperationLogStatus(req.Status),
			Message: req.Message,
			Details: req.Details,
			Result:  req.Result,
			TxID:    req.TxID,
		})
		if err != nil {
			return nil, -1, err.Error()
		}
		encoded, err := json.Marshal(record)
		if err != nil {
			return nil, -1, err.Error()
		}
		return map[string]any{"log": string(encoded)}, 0, "ok"
	})
	return js.Global().Get("Promise").New(handler)
}

func getOperationLogs(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		items, err := _mgr.GetOperationLogs()
		if err != nil {
			return nil, -1, err.Error()
		}
		encoded, err := json.Marshal(items)
		if err != nil {
			return nil, -1, err.Error()
		}
		return map[string]any{"logs": string(encoded)}, 0, "ok"
	})
	return js.Global().Get("Promise").New(handler)
}

func getOperationLog(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	if len(p) != 1 || p[0].Type() != js.TypeString || strings.TrimSpace(p[0].String()) == "" {
		return createJsRet(nil, -1, "operation log id is required")
	}
	id := strings.TrimSpace(p[0].String())
	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		item, err := _mgr.GetOperationLog(id)
		if err != nil {
			return nil, -1, err.Error()
		}
		if item == nil {
			return nil, -1, "operation log not found"
		}
		encoded, err := json.Marshal(item)
		if err != nil {
			return nil, -1, err.Error()
		}
		return map[string]any{"log": string(encoded)}, 0, "ok"
	})
	return js.Global().Get("Promise").New(handler)
}

func deleteAllOperationLogs(this js.Value, p []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	handler := createAsyncJsHandler(func() (interface{}, int, string) {
		if err := _mgr.DeleteAllOperationLogs(); err != nil {
			return nil, -1, err.Error()
		}
		return nil, 0, "ok"
	})
	return js.Global().Get("Promise").New(handler)
}

func init() {
	obj := js.Global().Get("Object").New()
	obj.Set("beginOperationLog", js.FuncOf(beginOperationLog))
	obj.Set("updateOperationLog", js.FuncOf(updateOperationLog))
	obj.Set("getOperationLogs", js.FuncOf(getOperationLogs))
	obj.Set("getOperationLog", js.FuncOf(getOperationLog))
	obj.Set("deleteAllOperationLogs", js.FuncOf(deleteAllOperationLogs))
	js.Global().Set(operationLogModule, obj)
}
