package wallet

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/btcsuite/btcd/btcutil/psbt"
	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/common"
)

var errSingleUTXOTransientSignature = errors.New("ordinary transient local SignPsbt failure")
var errSingleUTXOFirstRawWrite = errors.New("ordinary first signed-raw persistence failure")

type singleUTXOTransientSigningWallet struct {
	common.Wallet
	failures *int
}

func (w *singleUTXOTransientSigningWallet) Clone() common.Wallet {
	return &singleUTXOTransientSigningWallet{Wallet: w.Wallet.Clone(), failures: w.failures}
}
func (w *singleUTXOTransientSigningWallet) SignPsbt(packet *psbt.Packet) error {
	if *w.failures > 0 {
		*w.failures--
		return errSingleUTXOTransientSignature
	}
	return w.Wallet.SignPsbt(packet)
}

type singleUTXOFirstRawWriteFailure struct {
	indexer.KVDB
	failures                 int
	failedRaw, approvedQuote string
}

func (d *singleUTXOFirstRawWriteFailure) Write(key, value []byte) error {
	if d.failures > 0 && bytes.HasPrefix(key, []byte(GetDBKeyPrefix()+DB_KEY_RESV+RESV_TYPE_LOCALACTION+"-")) {
		var owner LocalActionPerformData
		if DecodeFromBytes(value, &owner) == nil && owner.Action == localActionClosedChannelRecovery {
			if intent, err := loadClosedChannelIntent(&owner); err == nil {
				var record struct{ Phase, SignedRaw, Quote string }
				if json.Unmarshal([]byte(intent.TopupJSON), &record) == nil && record.Phase == "signed" && record.SignedRaw != "" {
					d.failures--
					d.failedRaw = record.SignedRaw
					d.approvedQuote = record.Quote
					return errSingleUTXOFirstRawWrite
				}
			}
		}
	}
	return d.KVDB.Write(key, value)
}

func TestSTPSingleUTXOTopupTransientSignFailureCanResumeSameApproval(t *testing.T) {
	f := newSingleUTXOExecutionFixture(t)
	failures := 1
	f.manager.wallet = &singleUTXOTransientSigningWallet{Wallet: f.manager.wallet, failures: &failures}
	quote := f.quote(t)
	api := singleUTXOTopupAPIForTest(t, f.manager)
	if _, err := api.ConfirmClosedChannelSingleUTXOTopup(quote); !errors.Is(err, errSingleUTXOTransientSignature) {
		t.Fatalf("ordinary signing fault not reached: %v", err)
	}
	if failures != 0 || *f.signatures != 0 || len(f.rpc.broadcasts) != 0 || len(f.manager.GetLocalActionReservations()) != 1 || len(f.manager.utxoLockerL1.GetLockedUtxoList()) != 0 {
		t.Fatal("sign failure unexpectedly used funds or lost its one owner")
	}
	resumed, err := api.QuoteClosedChannelSingleUTXOTopup(5)
	if err != nil {
		t.Fatalf("approved pre-sign failure became permanently busy: %v", err)
	}
	if resumed != quote {
		t.Fatal("pre-sign retry selected another funding plan")
	}
	if _, err = api.ConfirmClosedChannelSingleUTXOTopup(quote); err != nil {
		t.Fatal(err)
	}
	if *f.signatures != 1 || len(f.rpc.broadcasts) != 1 || len(f.manager.GetLocalActionReservations()) != 1 || f.rpc.durableError != nil {
		t.Fatalf("same approval did not resume one durable payment: signs=%d sends=%d durable=%v", *f.signatures, len(f.rpc.broadcasts), f.rpc.durableError)
	}
}

func TestSTPSingleUTXOTopupFirstRawWriteFailureCanResumeSameApproval(t *testing.T) {
	f := newSingleUTXOExecutionFixture(t)
	quote := f.quote(t)
	api := singleUTXOTopupAPIForTest(t, f.manager)
	probe := &singleUTXOFirstRawWriteFailure{KVDB: f.manager.db, failures: 1}
	f.manager.db = probe
	if _, err := api.ConfirmClosedChannelSingleUTXOTopup(quote); !errors.Is(err, errSingleUTXOFirstRawWrite) {
		t.Fatalf("ordinary signed-raw persistence fault not reached: %v", err)
	}
	if probe.failures != 0 || probe.failedRaw == "" || probe.approvedQuote != quote || *f.signatures != 1 || len(f.rpc.broadcasts) != 0 || len(f.manager.GetLocalActionReservations()) != 1 || len(f.manager.utxoLockerL1.GetLockedUtxoList()) != 0 {
		t.Fatal("raw-write failure sent, locked or lost exact approved material")
	}
	resumed, err := api.QuoteClosedChannelSingleUTXOTopup(5)
	if err != nil {
		t.Fatalf("first raw-write failure became permanently busy: %v", err)
	}
	if resumed != quote {
		t.Fatal("failed persistence retried with another quote/input plan")
	}
	if _, err = api.ConfirmClosedChannelSingleUTXOTopup(quote); err != nil {
		t.Fatal(err)
	}
	failed, err := DecodeMsgTx(probe.failedRaw)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.rpc.broadcasts) != 1 || len(f.manager.GetLocalActionReservations()) != 1 || f.rpc.durableError != nil || failed.TxID() != f.rpc.broadcasts[0].TxID() {
		t.Fatalf("raw-write retry changed payment or sent before durable raw: sends=%d durable=%v", len(f.rpc.broadcasts), f.rpc.durableError)
	}
}
