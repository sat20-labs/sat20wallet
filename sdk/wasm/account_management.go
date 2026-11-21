//go:build js && wasm

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"syscall/js"

	"github.com/sat20-labs/sat20wallet/sdk/wallet"
)

type accountErrorData struct {
	data any
	code string
}

// Keep the existing async result tuple and pending-operation data. Preserve the error code for callers that can act on it.
func accountAsyncError(data any, err error) (interface{}, int, string) {
	if errors.Is(err, wallet.ErrStorageTTLChanged) {
		return accountErrorData{data: data, code: "STORAGE_TTL_CHANGED"}, -1, err.Error()
	}
	var refusal *wallet.DKVSError
	if errors.As(err, &refusal) && refusal != nil && refusal.Code != "" {
		data = accountErrorData{data: data, code: string(refusal.Code)}
	}
	return data, -1, err.Error()
}

func accountParseJSON(args []js.Value, target any) error {
	if len(args) != 1 || args[0].Type() != js.TypeString {
		return fmt.Errorf("request must be a JSON string")
	}
	if err := json.Unmarshal([]byte(args[0].String()), target); err != nil {
		return fmt.Errorf("invalid account request")
	}
	return nil
}
func accountViewResult(value any, err error) (interface{}, int, string) {
	if err != nil {
		return accountAsyncError(nil, err)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return accountAsyncError(nil, err)
	}
	return js.Global().Get("JSON").Call("parse", string(encoded)), 0, "ok"
}

func accountGetAccountView(_ js.Value, args []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		return accountViewResult(_mgr.GetAccountView(context.Background()))
	}))
}

func accountRefreshAccount(_ js.Value, args []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		return accountViewResult(_mgr.RefreshAccount(context.Background()))
	}))
}

func accountPreflightRecovery(_ js.Value, args []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	var request struct {
		Password  string  `json:"password"`
		WalletIDs []int64 `json:"wallet_ids"`
	}
	if err := accountParseJSON(args, &request); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		return accountViewResult(_mgr.PreflightRecovery(request.Password, request.WalletIDs))
	}))
}

func accountGetStorageOptions(_ js.Value, args []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	var request struct {
		RecordCount      uint64 `json:"record_count"`
		AvailabilityOnly bool   `json:"availability_only"`
	}
	if err := accountParseJSON(args, &request); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		if request.AvailabilityOnly {
			return accountViewResult(_mgr.GetStorageAvailability(context.Background()))
		}
		return accountViewResult(_mgr.GetStorageOptions(context.Background(), request.RecordCount))
	}))
}

func accountConfirmStorage(_ js.Value, args []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	var request wallet.StorageOption
	if err := accountParseJSON(args, &request); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		return accountViewResult(_mgr.ConfirmStorage(context.Background(), request))
	}))
}

func accountCancelStorageSetup(_ js.Value, args []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		return accountViewResult(nil, _mgr.CancelStorageSetup())
	}))
}

func accountFundAutopay(_ js.Value, args []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	var request wallet.PaymentQuote
	if err := accountParseJSON(args, &request); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		return accountViewResult(_mgr.FundAutopay(context.Background(), request))
	}))
}

func accountGetGuardianContact(_ js.Value, args []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	var request struct {
		Password string `json:"password"`
	}
	if err := accountParseJSON(args, &request); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		return accountViewResult(_mgr.GetGuardianContact(request.Password))
	}))
}

func accountCreateRecovery(_ js.Value, args []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	var request wallet.RecoverySetup
	if err := accountParseJSON(args, &request); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		return accountViewResult(_mgr.CreateRecovery(context.Background(), request))
	}))
}

func accountAcceptGuardianSetup(_ js.Value, args []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	var request struct {
		Password string `json:"password"`
		Setup    string `json:"setup"`
	}
	if err := accountParseJSON(args, &request); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		return accountViewResult(_mgr.AcceptGuardianSetup(context.Background(), request.Password, request.Setup))
	}))
}

func accountConfirmGuardianSetup(_ js.Value, args []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	var request struct {
		SessionID string `json:"session_id"`
		Receipt   string `json:"receipt"`
	}
	if err := accountParseJSON(args, &request); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		return accountViewResult(_mgr.ConfirmGuardianSetup(context.Background(), request.SessionID, request.Receipt))
	}))
}

func accountActivateRecovery(_ js.Value, args []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	var request struct {
		SessionID string                 `json:"session_id"`
		Input     wallet.ActivationInput `json:"input"`
	}
	if err := accountParseJSON(args, &request); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		return accountViewResult(_mgr.ActivateRecovery(context.Background(), request.SessionID, request.Input))
	}))
}

func accountLoadRecovery(_ js.Value, args []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	var request struct {
		RecoveryFile string `json:"recovery_file"`
		Rehearsal    bool   `json:"rehearsal"`
	}
	if err := accountParseJSON(args, &request); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		return accountViewResult(_mgr.LoadRecovery(context.Background(), request.RecoveryFile, request.Rehearsal))
	}))
}

func accountPreviewRecovery(_ js.Value, args []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	var request struct {
		SessionID string                 `json:"session_id"`
		Factors   wallet.RecoveryFactors `json:"factors"`
	}
	if err := accountParseJSON(args, &request); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		return accountViewResult(_mgr.PreviewRecovery(context.Background(), request.SessionID, request.Factors))
	}))
}

func accountCreateGuardianRequest(_ js.Value, args []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	var request struct {
		SessionID string `json:"session_id"`
	}
	if err := accountParseJSON(args, &request); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		return accountViewResult(_mgr.CreateGuardianRequest(context.Background(), request.SessionID))
	}))
}

func accountCreateGuardianResponse(_ js.Value, args []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	var request struct {
		Password string `json:"password"`
		Request  string `json:"request"`
	}
	if err := accountParseJSON(args, &request); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		return accountViewResult(_mgr.CreateGuardianResponse(context.Background(), request.Password, request.Request))
	}))
}

func accountCommitRecovery(_ js.Value, args []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	var request struct {
		SessionID   string `json:"session_id"`
		NewPassword string `json:"new_password"`
	}
	if err := accountParseJSON(args, &request); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		return accountViewResult(_mgr.CommitRecovery(context.Background(), request.SessionID, request.NewPassword))
	}))
}

func accountAbortRecovery(_ js.Value, args []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	var request struct {
		SessionID string `json:"session_id"`
	}
	if err := accountParseJSON(args, &request); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		return accountViewResult(nil, _mgr.AbortRecovery(request.SessionID))
	}))
}

func init() {
	obj := js.Global().Get("Object").New()

	obj.Set("getAccountView", js.FuncOf(accountGetAccountView))

	obj.Set("refreshAccount", js.FuncOf(accountRefreshAccount))

	obj.Set("preflightRecovery", js.FuncOf(accountPreflightRecovery))

	obj.Set("getStorageOptions", js.FuncOf(accountGetStorageOptions))

	obj.Set("confirmStorage", js.FuncOf(accountConfirmStorage))

	obj.Set("cancelStorageSetup", js.FuncOf(accountCancelStorageSetup))

	obj.Set("fundAutopay", js.FuncOf(accountFundAutopay))

	obj.Set("getGuardianContact", js.FuncOf(accountGetGuardianContact))

	obj.Set("createRecovery", js.FuncOf(accountCreateRecovery))

	obj.Set("acceptGuardianSetup", js.FuncOf(accountAcceptGuardianSetup))

	obj.Set("confirmGuardianSetup", js.FuncOf(accountConfirmGuardianSetup))

	obj.Set("activateRecovery", js.FuncOf(accountActivateRecovery))

	obj.Set("loadRecovery", js.FuncOf(accountLoadRecovery))

	obj.Set("previewRecovery", js.FuncOf(accountPreviewRecovery))

	obj.Set("createGuardianRequest", js.FuncOf(accountCreateGuardianRequest))

	obj.Set("createGuardianResponse", js.FuncOf(accountCreateGuardianResponse))

	obj.Set("commitRecovery", js.FuncOf(accountCommitRecovery))

	obj.Set("abortRecovery", js.FuncOf(accountAbortRecovery))

	js.Global().Set("sat20account_wasm", obj)
}
