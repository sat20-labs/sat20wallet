package wallet

import (
	"bytes"
	"encoding/json"
	"errors"
	indexer "github.com/sat20-labs/indexer/common"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
	"strings"
	"testing"
)

func singleUTXONaturalPreparedForAdopt(t *testing.T) (*singleUTXOExecutionFixture, *LocalActionPerformData, *closedChannelIntent) {
	t.Helper()
	f := newSingleUTXOExecutionFixture(t)
	f.manager.bInited, f.manager.actionMonitorRunning = true, true
	f.manager.serverNode.client = &singleUTXOLegacyReopenClient{singleUTXOTopupGateClient: f.gate}
	f.manager.l2IndexerClient = &IndexerRPCClientMgr{active: &singleUTXOLegacyLedgerFailure{IndexerRPCClient: f.rpc}}
	if _, err := f.manager.ReopenChannel(false); err == nil || !strings.Contains(err.Error(), errSingleUTXOLegacyLedgerUnavailable.Error()) {
		t.Fatalf("natural prepared read failure was not reached: %v", err)
	}
	for _, r := range f.manager.GetLocalActionReservations() {
		if owner, ok := r.(*LocalActionPerformData); ok && owner.Action == localActionClosedChannelRecovery && closedIntentWalletMatches(owner, f.manager.wallet) {
			owner.RLock()
			intent, err := loadClosedChannelIntent(owner)
			owner.RUnlock()
			if err != nil {
				t.Fatal(err)
			}
			if intent.ChannelID == f.channelID && intent.OldParentID == f.rpc.parent.TxID() {
				return f, owner, intent
			}
		}
	}
	t.Fatal("natural exact prepared owner missing")
	return nil, nil, nil
}

func TestSTPSingleUTXOTopupPreparedAdoptRejectsRetainedProtocolMaterial(t *testing.T) {
	for _, mode := range []string{"request", "drain", "child", "withdraw-choice"} {
		t.Run(mode, func(t *testing.T) {
			var f *singleUTXOExecutionFixture
			var owner *LocalActionPerformData
			var intent *closedChannelIntent
			var quote string
			if mode == "withdraw-choice" {
				f = newSingleUTXOExecutionFixture(t)
				quote = f.quote(t)
				var err error
				owner, intent, err = f.manager.ensureClosedChannelIntent(f.manager.wallet.Clone(), f.channelID, f.rpc.parent.TxID(), "withdraw")
				if err != nil {
					t.Fatal(err)
				}
			} else {
				f, owner, intent = singleUTXONaturalPreparedForAdopt(t)
				// Readonly initial quote is not asserted here so this rejection control
				// compiles/runs on the pre-adopt baseline as well.
				if mode == "request" {
					intent.Request = &wwire.OpenChannelRequest{LocalFundingAmount: 40000, NeedSendFundingTx: true}
					intent.Step = "open_inflight"
				}
				if mode == "drain" {
					intent.DrainSnapshot = []byte("retained drain material must not be ignored")
				}
				if mode == "child" {
					owner.Lock()
					owner.ActionResvs = append(owner.ActionResvs, &SubActionInfo{ActionType: RESV_TYPE_OPEN, ResvId: 77})
					owner.Unlock()
				}
				if err := f.manager.persistClosedChannelIntent(owner, intent, owner.Status); err != nil {
					t.Fatal(err)
				}
			}
			api := singleUTXOTopupAPIForTest(t, f.manager)
			if _, err := api.QuoteClosedChannelSingleUTXOTopup(5); err == nil {
				t.Fatalf("retained %s owner was treated as never dispatched", mode)
			}
			if quote != "" {
				if _, err := api.ConfirmClosedChannelSingleUTXOTopup(quote); err == nil {
					t.Fatal("pre-existing withdrawal choice accepted topup confirmation")
				}
			}
			owner.RLock()
			after, err := loadClosedChannelIntent(owner)
			owner.RUnlock()
			if err != nil {
				t.Fatal(err)
			}
			if after.TopupJSON != "" || *f.signatures != 0 || len(f.rpc.broadcasts) != 0 || len(f.manager.utxoLockerL1.GetLockedUtxoList()) != 0 || len(f.manager.GetLocalActionReservations()) != 1 {
				t.Fatal("refused adoption overwrote protocol/choice or used funds")
			}
		})
	}
}

var errSingleUTXOAdoptApprovalWrite = errors.New("ordinary existing prepared owner approval CAS write failure")

type singleUTXOAdoptApprovalWriteFailure struct {
	indexer.KVDB
	ownerID  int64
	failures int
}

func (d *singleUTXOAdoptApprovalWriteFailure) Write(key, value []byte) error {
	if d.failures > 0 && bytes.Equal(key, []byte(GetResvKey(RESV_TYPE_LOCALACTION, d.ownerID))) {
		var owner LocalActionPerformData
		if DecodeFromBytes(value, &owner) == nil {
			if intent, err := loadClosedChannelIntent(&owner); err == nil {
				var record struct{ Phase string }
				if json.Unmarshal([]byte(intent.TopupJSON), &record) == nil && record.Phase == "approved" {
					d.failures--
					return errSingleUTXOAdoptApprovalWrite
				}
			}
		}
	}
	return d.KVDB.Write(key, value)
}
func TestSTPSingleUTXOTopupPreparedAdoptWriteFailureRetainsExactOwner(t *testing.T) {
	f, owner, _ := singleUTXONaturalPreparedForAdopt(t)
	api := singleUTXOTopupAPIForTest(t, f.manager)
	quote, err := api.QuoteClosedChannelSingleUTXOTopup(5)
	if err != nil {
		t.Fatal(err)
	}
	probe := &singleUTXOAdoptApprovalWriteFailure{KVDB: f.manager.db, ownerID: owner.Id, failures: 1}
	f.manager.db = probe
	if _, err = api.ConfirmClosedChannelSingleUTXOTopup(quote); !errors.Is(err, errSingleUTXOAdoptApprovalWrite) {
		t.Fatalf("adopt approval write failure was not the pause boundary: %v", err)
	}
	owner.RLock()
	unchanged, err := loadClosedChannelIntent(owner)
	owner.RUnlock()
	if err != nil {
		t.Fatal(err)
	}
	if probe.failures != 0 || unchanged.TopupJSON != "" || unchanged.Step != "prepared" || *f.signatures != 0 || len(f.rpc.broadcasts) != 0 || len(f.manager.utxoLockerL1.GetLockedUtxoList()) != 0 || len(f.manager.GetLocalActionReservations()) != 1 {
		t.Fatal("failed adoption CAS published approval or used funds")
	}
	retry, err := api.QuoteClosedChannelSingleUTXOTopup(5)
	if err != nil {
		t.Fatal(err)
	}
	if retry != quote {
		t.Fatal("failed adopt CAS changed the reviewed plan")
	}
	if _, err = api.ConfirmClosedChannelSingleUTXOTopup(quote); err != nil {
		t.Fatal(err)
	}
	if f.manager.GetLocalActionReservations()[owner.Id] != owner || *f.signatures != 1 || len(f.rpc.broadcasts) != 1 || f.rpc.durableError != nil {
		t.Fatal("adopt retry replaced original owner or did not send one durable original payment")
	}
}
