//go:build js && wasm && rgb11discard

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"syscall/js"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	"github.com/sirupsen/logrus"
)

func registerRGB11Discard(obj js.Value) {
	// These maintenance-only names are assembled under a build tag and never
	// appear in the ordinary WASM API surface.
	names := [...]string{"initRGB11Discard", "planRGB11Discard", "applyRGB11Discard",
		"planRGB11StaleLock", "applyRGB11StaleLock", "diagnoseRGB11Lock",
		"diagnoseRGB11Consistency", "planRGB11StaleOwner", "applyRGB11StaleOwner"}
	obj.Set(names[0], js.FuncOf(initRGB11Discard))
	obj.Set(names[1], js.FuncOf(planRGB11Discard))
	obj.Set(names[2], js.FuncOf(applyRGB11Discard))
	obj.Set(names[3], js.FuncOf(planRGB11StaleLock))
	obj.Set(names[4], js.FuncOf(applyRGB11StaleLock))
	obj.Set(names[5], js.FuncOf(diagnoseRGB11Lock))
	obj.Set(names[6], js.FuncOf(diagnoseRGB11Consistency))
	obj.Set(names[7], js.FuncOf(planRGB11StaleOwner))
	obj.Set(names[8], js.FuncOf(applyRGB11StaleOwner))
}

func planRGB11StaleOwner(_ js.Value, args []js.Value) any {
	if _mgr == nil || len(args) < 2 || args[0].Type() != js.TypeString || args[1].Type() != js.TypeString {
		return createJsRet(nil, -1, "expected wallet password and exact stale-owner target JSON")
	}
	password, targetJSON := args[0].String(), args[1].String()
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		if _, err := _mgr.UnlockRGB11Discard(password); err != nil {
			return nil, -1, err.Error()
		}
		var target wallet.RGB11StaleLockTarget
		if err := json.Unmarshal([]byte(targetJSON), &target); err != nil {
			return nil, -1, fmt.Sprintf("decode stale-owner target: %v", err)
		}
		plan, err := _mgr.PlanRGB11StaleOwner(target)
		if err != nil {
			return nil, -1, err.Error()
		}
		data, err := jsonObject(plan)
		if err != nil {
			return nil, -1, err.Error()
		}
		return data, 0, "dry-run only; no owner metadata changed"
	}))
}

func applyRGB11StaleOwner(_ js.Value, args []js.Value) any {
	if _mgr == nil || len(args) < 3 || args[0].Type() != js.TypeString ||
		args[1].Type() != js.TypeString || args[2].Type() != js.TypeString {
		return createJsRet(nil, -1, "expected wallet password, exact stale-owner target and approved plan JSON")
	}
	password, targetJSON, planJSON := args[0].String(), args[1].String(), args[2].String()
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		if _, err := _mgr.UnlockRGB11Discard(password); err != nil {
			return nil, -1, err.Error()
		}
		var target wallet.RGB11StaleLockTarget
		if err := json.Unmarshal([]byte(targetJSON), &target); err != nil {
			return nil, -1, fmt.Sprintf("decode stale-owner target: %v", err)
		}
		var approved wallet.RGB11StaleOwnerPlan
		if err := json.Unmarshal([]byte(planJSON), &approved); err != nil {
			return nil, -1, fmt.Sprintf("decode approved stale-owner plan: %v", err)
		}
		if approved.Target != target {
			return nil, -1, "approved stale-owner plan target differs from target JSON"
		}
		if err := _mgr.ApplyRGB11StaleOwner(approved); err != nil {
			return nil, -1, err.Error()
		}
		return nil, 0, "exact stale reservation owner metadata cleared"
	}))
}

func diagnoseRGB11Consistency(_ js.Value, args []js.Value) any {
	if _mgr == nil || len(args) != 1 || args[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "expected wallet password")
	}
	password := args[0].String()
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		if _, err := _mgr.UnlockRGB11Diag(password); err != nil {
			return nil, -1, err.Error()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		diagnostic, err := _mgr.DiagnoseRGB11Consistency(ctx)
		if err != nil {
			return nil, -1, err.Error()
		}
		data, err := jsonObject(diagnostic)
		if err != nil {
			return nil, -1, err.Error()
		}
		return data, 0, "read-only RGB11 consistency diagnostic"
	}))
}

func diagnoseRGB11Lock(_ js.Value, args []js.Value) any {
	if _mgr == nil || len(args) != 1 || args[0].Type() != js.TypeString {
		return createJsRet(nil, -1, "expected exact stale-lock outpoint")
	}
	outpoint := args[0].String()
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		diagnostic, err := _mgr.DiagnoseRGB11Lock(outpoint)
		if err != nil {
			return nil, -1, err.Error()
		}
		data, err := jsonObject(diagnostic)
		if err != nil {
			return nil, -1, err.Error()
		}
		data["runtime"] = rgb11BrowserDiag(diagnostic.RawKey, diagnostic.MarkerKey)
		return data, 0, "read-only RGB11 lock diagnostic"
	}))
}

func rgb11BrowserDiag(rawKey, markerKey string) map[string]any {
	diagnostic := map[string]any{
		"sdk_kv_config_db_ignored": true,
		"pwa_indexed_db":           "sat20-wallet-pwa",
		"pwa_indexed_store":        "wallet-state",
		"indexed_db_used_by_sdk":   false,
	}
	global := js.Global()
	if location := global.Get("location"); location.Type() == js.TypeObject {
		diagnostic["origin"] = location.Get("origin").String()
		diagnostic["pathname"] = location.Get("pathname").String()
	}
	controlled := false
	if navigator := global.Get("navigator"); navigator.Type() == js.TypeObject {
		if worker := navigator.Get("serviceWorker"); worker.Type() == js.TypeObject {
			controller := worker.Get("controller")
			controlled = controller.Type() == js.TypeObject
		}
	}
	diagnostic["service_worker_controlled"] = controlled
	backend := "unavailable"
	if chrome := global.Get("chrome"); chrome.Type() == js.TypeObject {
		if storage := chrome.Get("storage"); storage.Type() == js.TypeObject {
			if local := storage.Get("local"); local.Type() == js.TypeObject {
				backend = "chrome.storage.local"
			}
		}
	}
	localStorage := global.Get("localStorage")
	if backend == "unavailable" && localStorage.Type() == js.TypeObject {
		backend = "localStorage"
	}
	diagnostic["sdk_kv_backend"] = backend
	diagnostic["local_storage_raw"] = rgb11LocalDiag(localStorage, rawKey)
	diagnostic["local_storage_marker"] = rgb11LocalDiag(localStorage, markerKey)
	diagnostic["indexed_db_available"] = global.Get("indexedDB").Type() == js.TypeObject
	return diagnostic
}

func rgb11LocalDiag(storage js.Value, key string) (result map[string]any) {
	result = map[string]any{"state": "unavailable", "key": key}
	if storage.Type() != js.TypeObject {
		return result
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			result = map[string]any{"state": "error", "key": key, "error": fmt.Sprint(recovered)}
		}
	}()
	value := storage.Call("getItem", key)
	if value.IsNull() || value.IsUndefined() {
		result["state"] = "absent"
		return result
	}
	raw := []byte(value.String())
	digest := sha256.Sum256(raw)
	result["state"] = "present"
	result["encoded_length"] = len(raw)
	result["encoded_sha256"] = hex.EncodeToString(digest[:])
	return result
}

func planRGB11StaleLock(_ js.Value, args []js.Value) any {
	if _mgr == nil || len(args) < 2 || args[0].Type() != js.TypeString || args[1].Type() != js.TypeString {
		return createJsRet(nil, -1, "expected wallet password and exact stale-lock target JSON")
	}
	password, targetJSON := args[0].String(), args[1].String()
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		if _, err := _mgr.UnlockRGB11Discard(password); err != nil {
			return nil, -1, err.Error()
		}
		var target wallet.RGB11StaleLockTarget
		if err := json.Unmarshal([]byte(targetJSON), &target); err != nil {
			return nil, -1, fmt.Sprintf("decode stale-lock target: %v", err)
		}
		plan, err := _mgr.PlanRGB11StaleLock(target)
		if err != nil {
			return nil, -1, err.Error()
		}
		data, err := jsonObject(plan)
		if err != nil {
			return nil, -1, err.Error()
		}
		return data, 0, "dry-run only; no lock changed"
	}))
}

func applyRGB11StaleLock(_ js.Value, args []js.Value) any {
	if _mgr == nil || len(args) < 3 || args[0].Type() != js.TypeString ||
		args[1].Type() != js.TypeString || args[2].Type() != js.TypeString {
		return createJsRet(nil, -1, "expected wallet password, exact stale-lock target and approved plan JSON")
	}
	password, targetJSON, planJSON := args[0].String(), args[1].String(), args[2].String()
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		if _, err := _mgr.UnlockRGB11Discard(password); err != nil {
			return nil, -1, err.Error()
		}
		var target wallet.RGB11StaleLockTarget
		if err := json.Unmarshal([]byte(targetJSON), &target); err != nil {
			return nil, -1, fmt.Sprintf("decode stale-lock target: %v", err)
		}
		var approved wallet.RGB11StaleLockPlan
		if err := json.Unmarshal([]byte(planJSON), &approved); err != nil {
			return nil, -1, fmt.Sprintf("decode approved stale-lock plan: %v", err)
		}
		if approved.Target != target {
			return nil, -1, "approved stale-lock plan target differs from target JSON"
		}
		if err := _mgr.ApplyRGB11StaleLock(approved); err != nil {
			return nil, -1, err.Error()
		}
		return nil, 0, "exact stale RGB lock removed and absence verified"
	}))
}

func initRGB11Discard(_ js.Value, args []js.Value) any {
	if len(args) < 2 || args[0].Type() != js.TypeObject || args[1].Type() != js.TypeNumber {
		return createJsRet(nil, -1, "expected config object and log level")
	}
	config, err := parseConfigFromJS(args[0])
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	if config.Chain != "testnet" {
		return createJsRet(nil, -1, "RGB11 discard is restricted to testnet")
	}
	level := logrus.Level(args[1].Int())
	if level > logrus.TraceLevel {
		return createJsRet(nil, -1, "invalid log level")
	}
	wallet.Log.SetLevel(level)
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		managerLifecycleMu.Lock()
		defer managerLifecycleMu.Unlock()
		if _mgr != nil {
			return nil, -1, "Manager is initialized"
		}
		database := wallet.NewKVDB(config.DB)
		if database == nil {
			return nil, -1, "NewKVDB failed"
		}
		manager := wallet.NewManager(config, database)
		if manager == nil {
			_ = database.Close()
			return nil, -1, "NewManager failed"
		}
		_mgr = manager
		return nil, 0, "maintenance manager initialized without background monitors"
	}))
}

func planRGB11Discard(_ js.Value, args []js.Value) any {
	if _mgr == nil || len(args) < 2 || args[0].Type() != js.TypeString || args[1].Type() != js.TypeString {
		return createJsRet(nil, -1, "expected wallet password and exact target JSON")
	}
	password, targetJSON := args[0].String(), args[1].String()
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		if _, err := _mgr.UnlockRGB11Discard(password); err != nil {
			return nil, -1, err.Error()
		}
		var target wallet.RGB11DiscardTarget
		if err := json.Unmarshal([]byte(targetJSON), &target); err != nil {
			return nil, -1, fmt.Sprintf("decode target: %v", err)
		}
		plan, err := _mgr.PlanRGB11Discard(target)
		if err != nil {
			return nil, -1, err.Error()
		}
		data, err := jsonObject(plan)
		if err != nil {
			return nil, -1, err.Error()
		}
		return data, 0, "dry-run only; no record changed"
	}))
}

func applyRGB11Discard(_ js.Value, args []js.Value) any {
	if _mgr == nil || len(args) < 3 || args[0].Type() != js.TypeString ||
		args[1].Type() != js.TypeString || args[2].Type() != js.TypeString {
		return createJsRet(nil, -1, "expected wallet password, exact target JSON and approved plan JSON")
	}
	password, targetJSON, planJSON := args[0].String(), args[1].String(), args[2].String()
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		if _, err := _mgr.UnlockRGB11Discard(password); err != nil {
			return nil, -1, err.Error()
		}
		var target wallet.RGB11DiscardTarget
		if err := json.Unmarshal([]byte(targetJSON), &target); err != nil {
			return nil, -1, fmt.Sprintf("decode target: %v", err)
		}
		var approved wallet.RGB11DiscardPlan
		if err := json.Unmarshal([]byte(planJSON), &approved); err != nil {
			return nil, -1, fmt.Sprintf("decode approved plan: %v", err)
		}
		if approved.Target != target {
			return nil, -1, "approved plan target differs from target JSON"
		}
		if err := _mgr.ApplyRGB11Discard(approved); err != nil {
			return nil, -1, err.Error()
		}
		return nil, 0, "exact Direct message tombstoned and locally marked processed"
	}))
}
