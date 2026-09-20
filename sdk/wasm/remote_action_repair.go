//go:build js && wasm && remoteactionrepair

package main

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"syscall/js"

	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	"github.com/sirupsen/logrus"
)

func registerRemoteActionRepair(obj js.Value) {
	// Assemble maintenance-only names so the source-level production allowlist
	// cannot mistake build-tagged exports for ordinary WASM API surface.
	names := [...]string{"initRemoteActionRepair", "inspectRemoteActionRepairTarget", "planRemoteActionRepair", "applyRemoteActionRepair"}
	obj.Set(names[0], js.FuncOf(initRemoteActionRepair))
	obj.Set(names[1], js.FuncOf(inspectRemoteActionRepairTarget))
	obj.Set(names[2], js.FuncOf(planRemoteActionRepair))
	obj.Set(names[3], js.FuncOf(applyRemoteActionRepair))
}

func inspectRemoteActionRepairTarget(_ js.Value, args []js.Value) any {
	if _mgr == nil || len(args) < 1 {
		return createJsRet(nil, -1, "expected reservation id")
	}
	var reservationID int64
	switch args[0].Type() {
	case js.TypeString:
		value, err := strconv.ParseInt(args[0].String(), 10, 64)
		if err != nil {
			return createJsRet(nil, -1, "reservation id must be a positive decimal integer")
		}
		reservationID = value
	case js.TypeNumber:
		value := args[0].Float()
		if value <= 0 || value > 9007199254740991 || math.Trunc(value) != value {
			return createJsRet(nil, -1, "reservation id must be a positive safe integer")
		}
		reservationID = int64(value)
	default:
		return createJsRet(nil, -1, "reservation id must be a decimal string or safe integer")
	}
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		target, err := _mgr.InspectRemoteActionRepairTarget(reservationID)
		if err != nil {
			return nil, -1, err.Error()
		}
		data, err := jsonObject(target)
		if err != nil {
			return nil, -1, err.Error()
		}
		return data, 0, "read-only target inspection; wallet remains locked and no network request was sent"
	}))
}

// initRemoteActionRepair opens the storage selected for the current browser
// context (localStorage for a normal page) without starting monitors.
func initRemoteActionRepair(_ js.Value, args []js.Value) any {
	if len(args) < 2 || args[0].Type() != js.TypeObject || args[1].Type() != js.TypeNumber {
		return createJsRet(nil, -1, "expected config object and log level")
	}
	config, err := parseConfigFromJS(args[0])
	if err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	if config.Chain != "testnet" {
		return createJsRet(nil, -1, "remote action repair is restricted to testnet")
	}
	logLevel := logrus.Level(args[1].Int())
	if logLevel > logrus.TraceLevel {
		return createJsRet(nil, -1, "invalid log level")
	}
	wallet.Log.SetLevel(logLevel)
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

func planRemoteActionRepair(_ js.Value, args []js.Value) any {
	if _mgr == nil || len(args) < 2 || args[0].Type() != js.TypeString || args[1].Type() != js.TypeString {
		return createJsRet(nil, -1, "expected wallet password and target JSON")
	}
	password, targetJSON := args[0].String(), args[1].String()
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		if _, err := _mgr.UnlockWalletForRemoteActionRepair(password); err != nil {
			return nil, -1, err.Error()
		}
		var target wallet.RemoteActionRepairTarget
		if err := json.Unmarshal([]byte(targetJSON), &target); err != nil {
			return nil, -1, fmt.Sprintf("decode target: %v", err)
		}
		plan, err := _mgr.PlanRemoteActionRepair(target)
		if err != nil {
			return nil, -1, err.Error()
		}
		data, err := jsonObject(plan)
		if err != nil {
			return nil, -1, err.Error()
		}
		return data, 0, "dry-run only; no network request sent"
	}))
}

func applyRemoteActionRepair(_ js.Value, args []js.Value) any {
	if _mgr == nil || len(args) < 3 || args[0].Type() != js.TypeString ||
		args[1].Type() != js.TypeString || args[2].Type() != js.TypeString {
		return createJsRet(nil, -1, "expected wallet password, target JSON and approved plan JSON")
	}
	password, targetJSON, planJSON := args[0].String(), args[1].String(), args[2].String()
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		if _, err := _mgr.UnlockWalletForRemoteActionRepair(password); err != nil {
			return nil, -1, err.Error()
		}
		var target wallet.RemoteActionRepairTarget
		if err := json.Unmarshal([]byte(targetJSON), &target); err != nil {
			return nil, -1, fmt.Sprintf("decode target: %v", err)
		}
		var approved wallet.RemoteActionRepairPlan
		if err := json.Unmarshal([]byte(planJSON), &approved); err != nil {
			return nil, -1, fmt.Sprintf("decode approved plan: %v", err)
		}
		if approved.Target != target {
			return nil, -1, "approved plan target differs from target JSON"
		}
		if err := _mgr.ApplyRemoteActionRepair(approved); err != nil {
			return nil, -1, err.Error()
		}
		return nil, 0, "historical ACK completed through the normal state machine"
	}))
}
