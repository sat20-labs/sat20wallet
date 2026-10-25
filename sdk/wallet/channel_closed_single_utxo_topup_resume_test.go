package wallet

import "testing"

func TestSTPSingleUTXOTopupSignedRetryKeepsOriginalQuoteAndRaw(t *testing.T) {
	f := newSingleUTXOExecutionFixture(t)
	f.rpc.testFailures = 1
	quote := f.quote(t)
	api := singleUTXOTopupAPIForTest(t, f.manager)
	if _, err := api.ConfirmClosedChannelSingleUTXOTopup(quote); err == nil {
		t.Fatal("first temporary acceptance-test failure should pause before broadcast")
	}
	if *f.signatures != 1 || len(f.rpc.broadcasts) != 0 || len(f.manager.GetLocalActionReservations()) != 1 || len(f.manager.utxoLockerL1.GetLockedUtxoList()) != 1 {
		t.Fatal("signed failure did not retain only the original durable intent and owned inputs")
	}
	resumed, err := api.QuoteClosedChannelSingleUTXOTopup(5)
	if err != nil {
		t.Fatalf("read-only signed retry is blocked by its own recovery lifecycle: %v", err)
	}
	if resumed != quote {
		t.Fatal("read-only retry replaced the original approved quote")
	}
	if _, err = api.ConfirmClosedChannelSingleUTXOTopup(resumed); err != nil {
		t.Fatal(err)
	}
	if *f.signatures != 1 || len(f.rpc.broadcasts) != 1 || f.rpc.durableError != nil {
		t.Fatalf("signed retry re-signed or replaced raw/owner: signs=%d sends=%d durable=%v", *f.signatures, len(f.rpc.broadcasts), f.rpc.durableError)
	}
}

func TestSTPSingleUTXOTopupUndeliveredSendReplaysOnlyExactSignedRaw(t *testing.T) {
	f := newSingleUTXOExecutionFixture(t)
	f.rpc.rejectBeforeAccept = 1
	quote := f.quote(t)
	api := singleUTXOTopupAPIForTest(t, f.manager)
	first, err := api.ConfirmClosedChannelSingleUTXOTopup(quote)
	if err != nil {
		t.Fatal(err)
	}
	if first == "" || len(f.rpc.broadcasts) != 1 || *f.signatures != 1 {
		t.Fatal("first undelivered attempt lost its original transaction")
	}
	if _, err = api.ConfirmClosedChannelSingleUTXOTopup(quote); err != nil {
		t.Fatal(err)
	}
	if len(f.rpc.broadcasts) != 2 {
		t.Fatalf("undelivered signed transaction was not retried: network attempts=%d, want 2 for the same tx", len(f.rpc.broadcasts))
	}
	before, _ := EncodeMsgTx(f.rpc.broadcasts[0])
	after, _ := EncodeMsgTx(f.rpc.broadcasts[1])
	if before != after || f.rpc.broadcasts[0].TxID() != f.rpc.broadcasts[1].TxID() || *f.signatures != 1 || len(f.manager.GetLocalActionReservations()) != 1 || f.rpc.durableError != nil {
		t.Fatal("retry changed signed bytes, inputs, txid, signature or original durable owner")
	}
}
