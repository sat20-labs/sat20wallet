package wallet

import "testing"

// A malformed stored RGB slot is an invalid authorization context, not a reason
// to panic. Keep a fully signed peer transaction so malformed wire bytes cannot
// make this test pass before it reaches the stored-slot check.
func TestSTPRGBRecoveryRejectsMalformedStoredSlotWithoutPanic(t *testing.T) {
	f, encoded := rgbRecoveryExchangeFixture(t)
	f.channel.RemoteCommitment.RGBClientRecovery.CommitTx = nil
	var gotErr error
	var panicValue any
	func() {
		defer func() { panicValue = recover() }()
		_, gotErr = validateRGBClientRecovery(f.channel, encoded)
	}()
	if panicValue != nil {
		t.Fatalf("malformed stored RGB recovery must return an error without panic: %v", panicValue)
	}
	if gotErr == nil {
		t.Fatal("malformed stored RGB recovery without a transaction was accepted")
	}
	if f.channel.RemoteCommitment.RGBClientRecovery.CommitTx != nil {
		t.Fatal("rejection rebuilt or replaced the malformed stored recovery")
	}
}
