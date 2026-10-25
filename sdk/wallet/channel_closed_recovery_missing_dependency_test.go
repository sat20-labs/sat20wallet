package wallet

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
)

func installClosedRecoveryOrphanDependency(t *testing.T, f *singleUTXOExecutionFixture) {
	t.Helper()
	script := f.rpc.source.TxOut[0].PkScript
	missing := wire.NewMsgTx(wire.TxVersion)
	hash := chainhash.Hash{201}
	missing.AddTxIn(wire.NewTxIn(wire.NewOutPoint(&hash, 0), nil, nil))
	missing.AddTxOut(wire.NewTxOut(50000, script))
	missingHash := missing.TxHash()
	other := wire.NewMsgTx(wire.TxVersion)
	other.AddTxIn(wire.NewTxIn(wire.NewOutPoint(&missingHash, 0), nil, nil))
	other.AddTxOut(wire.NewTxOut(49500, script))
	fetcher := txscript.NewMultiPrevOutFetcher(nil)
	AddTxOutputsToPrevFetcher(fetcher, missing)
	signed, err := SignTxWithWallet(f.manager.wallet, other, fetcher)
	if err != nil {
		t.Fatal(err)
	}
	if err = VerifySignedTx(signed, fetcher); err != nil {
		t.Fatal(err)
	}
	*f.signatures = 0
	// The complete signature is valid against its source, but that dependency
	// is absent from the local recovery package and must never be invented.
	f.stored.RemoteCommitment.Others = []*CommitmentTx{{CommitTx: signed}}
	raw, err := EncodeToBytes(f.stored)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.manager.db.Write([]byte(GetChannelKey(f.channelID)), raw); err != nil {
		t.Fatal(err)
	}
}
func TestSTPClosedRecoveryMissingDependencyDisablesPreviewWithoutPanic(t *testing.T) {
	f := newSingleUTXOExecutionFixture(t)
	installClosedRecoveryOrphanDependency(t, f)
	api := closedRecoveryChoicesAPIForTest(t, f.manager)
	var raw string
	var err error
	var crashed interface{}
	func() {
		defer func() { crashed = recover() }()
		raw, err = api.PreviewClosedChannelRecovery(f.channelID)
	}()
	if crashed != nil {
		t.Fatalf("read-only preview panicked before rejecting missing signed-package prevout: %v", crashed)
	}
	if err != nil {
		t.Fatal(err)
	}
	var options struct {
		CanWithdraw    bool
		WithdrawReason string
	}
	if err = json.Unmarshal([]byte(raw), &options); err != nil {
		t.Fatal(err)
	}
	if options.CanWithdraw || options.WithdrawReason == "" {
		t.Fatalf("missing dependency falsely advertised complete capability: %s", raw)
	}
	if *f.signatures != 0 || len(f.rpc.broadcasts) != 0 || len(f.manager.GetLocalActionReservations()) != 0 {
		t.Fatal("incomplete preview changed wallet")
	}
}
func TestSTPClosedRecoveryMissingDependencyDoesNotCrashOfflineQuoteHint(t *testing.T) {
	f := newSingleUTXOExecutionFixture(t)
	installClosedRecoveryOrphanDependency(t, f)
	f.gate.err = errors.New("offline")
	var quote string
	var err error
	var crashed interface{}
	func() {
		defer func() { crashed = recover() }()
		quote, err = singleUTXOTopupAPIForTest(t, f.manager).QuoteClosedChannelSingleUTXOTopup(5)
	}()
	if crashed != nil {
		t.Fatalf("read-only offline capability hint panicked on missing local prevout: %v", crashed)
	}
	if err == nil || quote != "" || strings.Contains(strings.ToLower(err.Error()), "broadcast your local others") {
		t.Fatalf("incomplete local package produced an unsafe hint: %q %v", quote, err)
	}
	if *f.signatures != 0 || len(f.rpc.broadcasts) != 0 || len(f.manager.GetLocalActionReservations()) != 0 {
		t.Fatal("incomplete hint changed wallet")
	}
}
