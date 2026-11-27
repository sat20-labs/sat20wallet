//go:build js && wasm

package main

import (
	"context"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	"syscall/js"
)

func getRGB11View(_ js.Value, args []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) { return accountViewResult(_mgr.GetRGBView()) }))
}
func refreshRGB11State(_ js.Value, args []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		return accountViewResult(_mgr.RefreshRGBState(context.Background()))
	}))
}
func prepareRGB11Transfer(_ js.Value, args []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	var request wallet.RGBSendRequest
	if err := accountParseJSON(args, &request); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		return accountViewResult(_mgr.PrepareRGBTransfer(context.Background(), request))
	}))
}
func prepareRGB11Inflation(_ js.Value, args []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	var request struct {
		Invoice string `json:"invoice"`
		FeeRate int64  `json:"fee_rate"`
	}
	if err := accountParseJSON(args, &request); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		return accountViewResult(_mgr.PrepareRGBInflation(context.Background(), request.Invoice, request.FeeRate))
	}))
}
func confirmRGB11Transfer(_ js.Value, args []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	var request struct {
		ID       string   `json:"id"`
		Receipts []string `json:"receipts"`
	}
	if err := accountParseJSON(args, &request); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		return accountViewResult(_mgr.ConfirmRGBTransfer(context.Background(), request.ID, request.Receipts))
	}))
}
func cancelRGB11Transfer(_ js.Value, args []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	var request struct {
		ID string `json:"id"`
	}
	if err := accountParseJSON(args, &request); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		return accountViewResult(_mgr.CancelRGBTransfer(context.Background(), request.ID))
	}))
}
func exportRGB11Transfer(_ js.Value, args []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	var request struct {
		ID string `json:"id"`
	}
	if err := accountParseJSON(args, &request); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) { return accountViewResult(_mgr.ExportRGBTransfer(request.ID)) }))
}
func createRGB11Invoice(_ js.Value, args []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	var request wallet.RGBInvoiceRequest
	if err := accountParseJSON(args, &request); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		return accountViewResult(_mgr.CreateRGBInvoice(context.Background(), request))
	}))
}
func prepareRGB11Consignment(_ js.Value, args []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	var request struct {
		ID   string `json:"id"`
		Data []byte `json:"data"`
	}
	if err := accountParseJSON(args, &request); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		return accountViewResult(_mgr.PrepareRGBConsignment(context.Background(), request.ID, request.Data))
	}))
}
func acceptRGB11Consignment(_ js.Value, args []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	var request struct {
		ID string `json:"id"`
	}
	if err := accountParseJSON(args, &request); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		return accountViewResult(_mgr.AcceptRGBConsignment(context.Background(), request.ID))
	}))
}
func issueRGB11Asset(_ js.Value, args []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	var request wallet.RGBIssueRequest
	if err := accountParseJSON(args, &request); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		return accountViewResult(_mgr.IssueRGBAsset(context.Background(), request))
	}))
}
func importRGB11Contract(_ js.Value, args []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	var request struct {
		Data []byte `json:"data"`
	}
	if err := accountParseJSON(args, &request); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		return accountViewResult(_mgr.ImportRGBContract(context.Background(), request.Data))
	}))
}
func exportRGB11Contract(_ js.Value, args []js.Value) any {
	if _mgr == nil {
		return createJsRet(nil, -1, "Manager not initialized")
	}
	var request struct {
		ContractID string `json:"contract_id"`
	}
	if err := accountParseJSON(args, &request); err != nil {
		return createJsRet(nil, -1, err.Error())
	}
	return js.Global().Get("Promise").New(createAsyncJsHandler(func() (interface{}, int, string) {
		return accountViewResult(_mgr.ExportRGBContract(request.ContractID))
	}))
}
func registerRGBViews(obj js.Value) {
	obj.Set("getRGB11View", js.FuncOf(getRGB11View))
	obj.Set("refreshRGB11State", js.FuncOf(refreshRGB11State))
	obj.Set("prepareRGB11Transfer", js.FuncOf(prepareRGB11Transfer))
	obj.Set("prepareRGB11Inflation", js.FuncOf(prepareRGB11Inflation))
	obj.Set("confirmRGB11Transfer", js.FuncOf(confirmRGB11Transfer))
	obj.Set("cancelRGB11Transfer", js.FuncOf(cancelRGB11Transfer))
	obj.Set("exportRGB11Transfer", js.FuncOf(exportRGB11Transfer))
	obj.Set("createRGB11Invoice", js.FuncOf(createRGB11Invoice))
	obj.Set("prepareRGB11Consignment", js.FuncOf(prepareRGB11Consignment))
	obj.Set("acceptRGB11Consignment", js.FuncOf(acceptRGB11Consignment))
	obj.Set("issueRGB11Asset", js.FuncOf(issueRGB11Asset))
	obj.Set("importRGB11Contract", js.FuncOf(importRGB11Contract))
	obj.Set("exportRGB11Contract", js.FuncOf(exportRGB11Contract))
}
