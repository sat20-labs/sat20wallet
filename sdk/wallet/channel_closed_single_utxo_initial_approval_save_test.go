package wallet

import (
	"bytes"
	"encoding/json"
	"errors"
	indexer "github.com/sat20-labs/indexer/common"
	"testing"
)

var errSingleUTXOInitialApprovalWrite = errors.New("ordinary initial approved-owner persistence failure")

type singleUTXOInitialApprovalWriteFailure struct {
	indexer.KVDB
	failures               int
	firstPhase, firstQuote string
}

func (d *singleUTXOInitialApprovalWriteFailure) Write(key, value []byte) error {
	if d.failures > 0 && bytes.HasPrefix(key, []byte(GetDBKeyPrefix()+DB_KEY_RESV+RESV_TYPE_LOCALACTION+"-")) {
		d.failures--
		var owner LocalActionPerformData
		if DecodeFromBytes(value, &owner) == nil {
			if intent, err := loadClosedChannelIntent(&owner); err == nil {
				var record struct{ Phase, Quote string }
				if json.Unmarshal([]byte(intent.TopupJSON), &record) == nil {
					d.firstPhase, d.firstQuote = record.Phase, record.Quote
				}
			}
		}
		return errSingleUTXOInitialApprovalWrite
	}
	return d.KVDB.Write(key, value)
}
func TestSTPSingleUTXOTopupInitialApprovalWriteFailureNeverCreatesEmptyOwner(t *testing.T) {
	f := newSingleUTXOExecutionFixture(t)
	quote := f.quote(t)
	api := singleUTXOTopupAPIForTest(t, f.manager)
	probe := &singleUTXOInitialApprovalWriteFailure{KVDB: f.manager.db, failures: 1}
	f.manager.db = probe
	if _, err := api.ConfirmClosedChannelSingleUTXOTopup(quote); !errors.Is(err, errSingleUTXOInitialApprovalWrite) {
		t.Fatalf("initial approval write fault not reached: %v", err)
	}
	if probe.firstPhase != "approved" || probe.firstQuote != quote {
		t.Fatal("first attempted durable owner write omitted the exact approved unsigned plan")
	}
	if *f.signatures != 0 || len(f.rpc.broadcasts) != 0 || len(f.manager.GetLocalActionReservations()) != 0 || len(f.manager.utxoLockerL1.GetLockedUtxoList()) != 0 {
		t.Fatal("failed initial write published an empty owner or used funds")
	}
	resumed, err := api.QuoteClosedChannelSingleUTXOTopup(5)
	if err != nil {
		t.Fatal(err)
	}
	if resumed != quote {
		t.Fatal("initial write retry changed approved quote")
	}
	if _, err = api.ConfirmClosedChannelSingleUTXOTopup(quote); err != nil {
		t.Fatal(err)
	}
	if *f.signatures != 1 || len(f.rpc.broadcasts) != 1 || len(f.manager.GetLocalActionReservations()) != 1 || f.rpc.durableError != nil {
		t.Fatal("initial write failure retry did not produce just one durable original payment")
	}
}
