//go:build js && wasm

package main

import (
	"strings"
	"syscall/js"
	"testing"
	"time"

	walletsdk "github.com/sat20-labs/sat20wallet/sdk/wallet"
)

func closedRecoveryWASMErrorEnvelope(t *testing.T, result js.Value) string {
	t.Helper()
	if result.Type() != js.TypeObject || result.IsNull() {
		t.Fatal("actual WASM export did not return a response envelope")
	}
	if result.Get("code").Type() != js.TypeNumber || result.Get("code").Int() == 0 || result.Get("msg").Type() != js.TypeString || result.Get("msg").String() == "" || !result.Get("data").IsNull() {
		t.Fatalf("invalid error envelope: %s", js.Global().Get("JSON").Call("stringify", result).String())
	}
	message := result.Get("msg").String()
	if strings.Contains(strings.ToLower(message), "panic") {
		t.Fatalf("actual export panicked rather than refusing safely: %s", message)
	}
	return message
}

func TestClosedRecoveryWASMActualExportsErrorAndPromiseContracts(t *testing.T) {
	previous := _mgr
	_mgr = nil
	defer func() { _mgr = previous }()
	// Run production main registration, not a recreated obj.Set adapter. It
	// only registers functions and blocks; no wallet/manager is initialized.
	go main()
	deadline := time.Now().Add(5 * time.Second)
	var exports js.Value
	for time.Now().Before(deadline) {
		exports = js.Global().Get(module)
		if exports.Type() == js.TypeObject && !exports.IsNull() {
			break
		}
		time.Sleep(time.Millisecond)
	}
	names := []string{"quoteClosedChannelSingleUTXOTopup", "confirmClosedChannelSingleUTXOTopup", "previewClosedChannelRecovery", "withdrawClosedChannelExact"}
	for _, name := range names {
		if exports.Type() != js.TypeObject || exports.Get(name).Type() != js.TypeFunction {
			t.Fatalf("actual production namespace is missing %s", name)
		}
		result := exports.Call(name)
		if got := closedRecoveryWASMErrorEnvelope(t, result); got != "Manager not initialized" {
			t.Fatalf("%s missing manager: %q", name, got)
		}
	}
	// This empty real SDK manager is deliberately not an initialized wallet:
	// it permits wrapper validation and real asynchronous SDK refusals, while
	// having no wallet signer, database, transport, balance or funding inputs.
	safeManager := &walletsdk.Manager{}
	_mgr = safeManager
	invalid := []struct {
		name string
		args []any
		want string
	}{
		{names[0], nil, "explicit fee rate"}, {names[0], []any{5}, "explicit fee rate"}, {names[0], []any{"5", "extra"}, "explicit fee rate"},
		{names[0], []any{""}, "positive integer"}, {names[0], []any{"0"}, "positive integer"}, {names[0], []any{"-1"}, "positive integer"}, {names[0], []any{"1.5"}, "positive integer"}, {names[0], []any{"9223372036854775808"}, "positive integer"},
		{names[1], nil, "exact approved quote"}, {names[1], []any{5}, "exact approved quote"}, {names[1], []any{""}, "exact approved quote"}, {names[1], []any{"{}", "extra"}, "exact approved quote"},
		{names[2], nil, "identifier string"}, {names[2], []any{5}, "identifier string"}, {names[2], []any{nil}, "identifier string"}, {names[2], []any{"", "extra"}, "identifier string"},
		{names[3], nil, "exact reviewed channel"}, {names[3], []any{"channel"}, "exact reviewed channel"}, {names[3], []any{"channel", "parent", "extra"}, "exact reviewed channel"}, {names[3], []any{5, "parent"}, "exact reviewed channel"}, {names[3], []any{"channel", 5}, "exact reviewed channel"}, {names[3], []any{"", "parent"}, "exact reviewed channel"}, {names[3], []any{"channel", ""}, "exact reviewed channel"},
	}
	for _, testCase := range invalid {
		result := exports.Call(testCase.name, testCase.args...)
		if result.Get("then").Type() == js.TypeFunction {
			t.Fatalf("%s invalid arguments escaped synchronous wrapper validation", testCase.name)
		}
		if message := closedRecoveryWASMErrorEnvelope(t, result); !strings.Contains(message, testCase.want) {
			t.Fatalf("%s invalid arguments: got %q want %q", testCase.name, message, testCase.want)
		}
	}
	accepted := []struct {
		name string
		args []any
		want string
	}{
		{names[0], []any{"5"}, "wallet"},
		{names[1], []any{"{}"}, "quote"},
		// Empty channel is valid only for the readonly captured-account locator.
		{names[2], []any{""}, "wallet"}, {names[2], []any{"known-channel"}, "wallet"},
		{names[3], []any{"known-channel", "reviewed-parent"}, "wallet"},
	}
	for _, testCase := range accepted {
		promise := exports.Call(testCase.name, testCase.args...)
		if promise.Type() != js.TypeObject || promise.Get("then").Type() != js.TypeFunction {
			t.Fatalf("%s valid argument shape did not return an actual JavaScript Promise", testCase.name)
		}
		result := awaitAccountResult(t, promise)
		if message := closedRecoveryWASMErrorEnvelope(t, result); !strings.Contains(strings.ToLower(message), testCase.want) {
			t.Fatalf("%s actual async SDK refusal: %q", testCase.name, message)
		}
	}
	if safeManager.GetWallet() != nil || len(safeManager.GetAllResv()) != 0 || len(safeManager.GetAllChannels()) != 0 {
		t.Fatal("error/shape controls created wallet or financial lifecycle state")
	}
	t.Log("actual production exports: uniform error envelopes, count/type/empty validation, readonly empty-ID real Promise, asynchronous SDK refusal, no wallet/funding state")
}
