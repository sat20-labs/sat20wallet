package wallet

import "testing"

func TestLaunchPoolIsIdleUsesSettlementState(t *testing.T) {
	runtime := NewLaunchPoolContractRuntime(&Manager{})
	if !runtime.IsIdle() {
		t.Fatal("fresh launch pool should be idle")
	}

	runtime.mintInfoMap["settled-minter"] = &MinterStatus{Settled: true}
	runtime.invalidMintMap["settled-refund"] = &MinterStatus{Settled: true}
	if !runtime.IsIdle() {
		t.Fatal("settled historical entries should not keep launch pool busy")
	}

	runtime.mintInfoMap["pending-minter"] = &MinterStatus{Settled: false}
	if runtime.IsIdle() {
		t.Fatal("unsettled mint result must keep launch pool busy")
	}
	runtime.mintInfoMap["pending-minter"].Settled = true
	runtime.invalidMintMap["pending-refund"] = &MinterStatus{Settled: false}
	if runtime.IsIdle() {
		t.Fatal("unsettled refund result must keep launch pool busy")
	}
	runtime.invalidMintMap["pending-refund"].Settled = true

	runtime.isSending = true
	if runtime.IsIdle() {
		t.Fatal("active result broadcast must keep launch pool busy")
	}
	runtime.isSending = false
	runtime.IsLaunching = true
	if runtime.IsIdle() {
		t.Fatal("active launch must keep launch pool busy")
	}
}
