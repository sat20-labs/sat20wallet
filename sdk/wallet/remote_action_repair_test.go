//go:build remoteactionrepair

package wallet

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/common"
)

type remoteActionRepairNodeClient struct {
	NodeRPCClient
	acks           int
	results        int
	ackErr         error
	ackedIDs       []int64
	notifiedSigner []byte
}

const (
	remoteActionRepairFeeTxID    = "1111111111111111111111111111111111111111111111111111111111111111"
	remoteActionRepairCommitTxID = "2222222222222222222222222222222222222222222222222222222222222222"
	remoteActionRepairRevealTxID = "3333333333333333333333333333333333333333333333333333333333333333"
)

func (c *remoteActionRepairNodeClient) SendPerformRemoteActionAckReq(info *RemoteActionPerformReservation) error {
	c.acks++
	c.ackedIDs = append(c.ackedIDs, info.Id)
	if c.ackErr != nil {
		return c.ackErr
	}
	info.Status = RS_PERFORM_ACTION_COMPLETED
	return nil
}

func (c *remoteActionRepairNodeClient) SendActionResultNfty(localWallet common.Wallet, _ int64, _ string, _ int, _ string) error {
	c.results++
	c.notifiedSigner = localWallet.GetPaymentPubKey().SerializeCompressed()
	return nil
}

func newRemoteActionRepairFixture(t *testing.T) (*Manager, *RemoteActionPerformReservation,
	RemoteActionRepairTarget, *remoteActionRepairNodeClient) {
	t.Helper()
	walletValue := NewInternalWalletWithMnemonic(
		"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire",
		"", GetChainParam(),
	)
	if walletValue == nil {
		t.Fatal("create signer")
	}
	walletValue.id = 4401
	walletValue.SetSubAccount(2)
	result := RemoteDeployRunesResult{
		AssetName: "runes:f:repair", CommitTxId: remoteActionRepairCommitTxID,
		RevealTxId: remoteActionRepairRevealTxID,
	}
	resultJSON, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	resv := &RemoteActionPerformReservation{
		ReservationBase: NewReservationBase(5501, true, RS_PERFORM_ACTION_RUN_STARTED, walletValue),
		Action:          REMOTE_ACTION_DEPLOY_RUNES,
		ReqPubKey:       walletValue.GetPaymentPubKey().SerializeCompressed(),
		FeeTxId:         remoteActionRepairFeeTxID,
		Invoice:         []byte("original-invoice"),
		ActionResult:    resultJSON,
		SendTxInL1:      true,
	}
	database := newMemoryKVDB()
	client := &remoteActionRepairNodeClient{}
	manager := &Manager{
		db: database, serverNode: &Node{client: client},
		status: &Status{CurrentWallet: 4401, CurrentAccount: 2},
		wallet: walletValue, walletInfoMap: map[int64]*WalletInfo{4401: {
			WalletInDB: WalletInDB{Id: 4401, Accounts: 3}, Wallet: walletValue,
		}},
	}
	manager.resetResvMapsLocked()
	manager.AddResv(resv)
	if err := manager.SaveWalletReservation(resv); err != nil {
		t.Fatal(err)
	}
	target := RemoteActionRepairTarget{
		ReservationID: resv.Id, Action: resv.Action,
		SignerPubKey: hex.EncodeToString(resv.ReqPubKey), FeeTxID: resv.FeeTxId,
		CommitTxID: result.CommitTxId, RevealTxID: result.RevealTxId,
	}
	return manager, resv, target, client
}

func TestInspectRemoteActionRepairTargetReadsPersistedStateWithoutUnlockNetworkOrWrite(t *testing.T) {
	manager, resv, want, client := newRemoteActionRepairFixture(t)
	key := []byte(GetResvKey(RESV_TYPE_REMOTEACTION, resv.Id))
	before, err := manager.db.Read(key)
	if err != nil {
		t.Fatal(err)
	}
	before = append([]byte(nil), before...)

	// Inspection must work before any wallet is unlocked and must use the
	// persisted copy rather than the runtime-only signer.
	manager.wallet = nil
	resv.localWallet = nil
	got, err := manager.InspectRemoteActionRepairTarget(resv.Id)
	if err != nil {
		t.Fatal(err)
	}
	if *got != want {
		t.Fatalf("inspect target mismatch\n got: %+v\nwant: %+v", *got, want)
	}
	if client.acks != 0 || client.results != 0 {
		t.Fatal("inspection sent a network request")
	}
	after, err := manager.db.Read(key)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || resv.Status != RS_PERFORM_ACTION_RUN_STARTED {
		t.Fatal("inspection changed persisted or runtime reservation state")
	}
}

func TestInspectRemoteActionRepairTargetRejectsMissingAndUnsupportedReservation(t *testing.T) {
	manager, resv, _, client := newRemoteActionRepairFixture(t)
	if _, err := manager.InspectRemoteActionRepairTarget(resv.Id + 1); err == nil {
		t.Fatal("missing reservation id was accepted")
	}
	resv.Action = REMOTE_ACTION_ASCEND
	if err := manager.SaveWalletReservation(resv); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.InspectRemoteActionRepairTarget(resv.Id); err == nil {
		t.Fatal("unsupported persisted action was accepted")
	}
	if client.acks != 0 || client.results != 0 {
		t.Fatal("rejected inspection sent a network request")
	}
}

func TestInspectRemoteActionRepairTargetRejectsMalformedPersistedFields(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*RemoteActionPerformReservation)
	}{
		{name: "terminal", mutate: func(resv *RemoteActionPerformReservation) { resv.Status = RS_CLOSED }},
		{name: "signer", mutate: func(resv *RemoteActionPerformReservation) { resv.ReqPubKey = []byte{1} }},
		{name: "fee txid", mutate: func(resv *RemoteActionPerformReservation) { resv.FeeTxId = "invalid" }},
		{name: "result txids", mutate: func(resv *RemoteActionPerformReservation) {
			resv.ActionResult = []byte(`{"commitTxId":"invalid","revealTxId":"invalid"}`)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager, resv, _, client := newRemoteActionRepairFixture(t)
			test.mutate(resv)
			if err := manager.SaveWalletReservation(resv); err != nil {
				t.Fatal(err)
			}
			if _, err := manager.InspectRemoteActionRepairTarget(resv.Id); err == nil {
				t.Fatal("malformed persisted reservation was accepted")
			}
			if client.acks != 0 || client.results != 0 {
				t.Fatal("rejected inspection sent a network request")
			}
		})
	}
}

func TestRemoteActionRepairUsesOnlyOriginalReservationAndNormalACK(t *testing.T) {
	manager, resv, target, client := newRemoteActionRepairFixture(t)
	plan, err := manager.PlanRemoteActionRepair(target)
	if err != nil {
		t.Fatal(err)
	}
	if client.acks != 0 || client.results != 0 {
		t.Fatal("dry-run sent a network request")
	}
	beforeInvoice := append([]byte(nil), resv.Invoice...)
	beforeResult := append([]byte(nil), resv.ActionResult...)
	if err := manager.ApplyRemoteActionRepair(*plan); err != nil {
		t.Fatal(err)
	}
	if client.acks != 1 || client.results != 1 {
		t.Fatalf("normal state machine calls: ACK=%d result=%d", client.acks, client.results)
	}
	if !bytes.Equal(client.notifiedSigner, resv.ReqPubKey) {
		t.Fatal("completion notification used another signer")
	}
	if manager.GetResv(resv.Id) != nil || resv.Status != RS_CLOSED {
		t.Fatal("normal state machine did not persist and retire the completed reservation")
	}
	if resv.FeeTxId != target.FeeTxID || !bytes.Equal(resv.Invoice, beforeInvoice) ||
		!bytes.Equal(resv.ActionResult, beforeResult) {
		t.Fatal("repair rebuilt or rewrote original fee/invoice/action data")
	}
	stored, err := LoadReservation(manager.db, manager, RESV_TYPE_REMOTEACTION, resv.Id)
	if err != nil || stored.GetStatus() != RS_CLOSED {
		t.Fatalf("stored terminal reservation: status=%v err=%v", stored.GetStatus(), err)
	}
	logs, err := manager.GetOperationLogs()
	if err != nil || len(logs) != 0 {
		t.Fatalf("completion without a bound log should be a no-op: logs=%d err=%v", len(logs), err)
	}
}

func TestRemoteActionRepairCompletesOnlyBoundOperationLog(t *testing.T) {
	manager, resv, target, client := newRemoteActionRepairFixture(t)
	record, err := manager.BeginOperationLog(remoteActionOperationLogCreate(
		resv.Action, resv.FeeRate, resv.SendTxInL1, resv.SendToBootstrapNode,
	))
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.BindOperationLogReservation(record.ID, RESV_TYPE_REMOTEACTION, resv.Id); err != nil {
		t.Fatal(err)
	}
	var events []*ActionStatusEvent
	manager.RegisterActionStatusCallback(func(event *ActionStatusEvent) {
		events = append(events, event)
	})

	plan, err := manager.PlanRemoteActionRepair(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.ApplyRemoteActionRepair(*plan); err != nil {
		t.Fatal(err)
	}
	if client.acks != 1 || client.results != 1 {
		t.Fatalf("normal completion calls: ACK=%d result=%d", client.acks, client.results)
	}
	if len(events) != 1 || events[0].Event != ACTION_STATUS_EVENT_COMPLETED || events[0].Resv != resv {
		t.Fatalf("unexpected completion events: %+v", events)
	}
	got, err := manager.GetOperationLog(record.ID)
	if err != nil || got == nil {
		t.Fatalf("get bound operation log: record=%v err=%v", got, err)
	}
	if got.Status != OperationLogSucceeded || got.Summary != "Runes deployment completed" || got.TxID != target.FeeTxID {
		t.Fatalf("unexpected completed log: %+v", got)
	}
	if got.Result["reservation_id"] != "5501" || got.Result["txid"] != target.FeeTxID ||
		got.Result["result"] != string(resv.ActionResult) {
		t.Fatalf("unexpected completed log result: %+v", got.Result)
	}
}

func TestRemoteActionRepairAckFailureDoesNotEmitCompletion(t *testing.T) {
	manager, resv, target, client := newRemoteActionRepairFixture(t)
	record, err := manager.BeginOperationLog(remoteActionOperationLogCreate(
		resv.Action, resv.FeeRate, resv.SendTxInL1, resv.SendToBootstrapNode,
	))
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.BindOperationLogReservation(record.ID, RESV_TYPE_REMOTEACTION, resv.Id); err != nil {
		t.Fatal(err)
	}
	var events []*ActionStatusEvent
	manager.RegisterActionStatusCallback(func(event *ActionStatusEvent) {
		events = append(events, event)
	})
	plan, err := manager.PlanRemoteActionRepair(target)
	if err != nil {
		t.Fatal(err)
	}
	client.ackErr = errors.New("ACK unavailable")
	if err := manager.ApplyRemoteActionRepair(*plan); err == nil {
		t.Fatal("ACK failure was accepted")
	}
	if client.acks != 1 || client.results != 0 || len(events) != 0 {
		t.Fatalf("ACK failure side effects: ACK=%d result=%d events=%d", client.acks, client.results, len(events))
	}
	if resv.Status != RS_PERFORM_ACTION_RUN_STARTED || manager.GetResv(resv.Id) != resv {
		t.Fatalf("ACK failure changed target state: status=%v", resv.Status)
	}
	got, err := manager.GetOperationLog(record.ID)
	if err != nil || got == nil || got.Status != OperationLogRunning {
		t.Fatalf("ACK failure changed operation log: record=%+v err=%v", got, err)
	}
}

func TestRemoteActionRepairProcessesOnlyApprovedTarget(t *testing.T) {
	manager, resv, target, client := newRemoteActionRepairFixture(t)
	other := *resv
	other.ReservationBase = NewReservationBase(5502, true, RS_PERFORM_ACTION_RUN_STARTED, resv.LocalWallet())
	other.ActionResult = append([]byte(nil), resv.ActionResult...)
	manager.AddResv(&other)
	if err := manager.SaveWalletReservation(&other); err != nil {
		t.Fatal(err)
	}
	plan, err := manager.PlanRemoteActionRepair(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.ApplyRemoteActionRepair(*plan); err != nil {
		t.Fatal(err)
	}
	if len(client.ackedIDs) != 1 || client.ackedIDs[0] != resv.Id {
		t.Fatalf("unexpected ACK targets: %v", client.ackedIDs)
	}
	if other.Status != RS_PERFORM_ACTION_RUN_STARTED || manager.GetResv(other.Id) != &other {
		t.Fatalf("unrelated reservation was processed: status=%v", other.Status)
	}
}

func TestRemoteActionRepairRejectsIdentityTransactionsAndStaleApproval(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*RemoteActionRepairTarget)
	}{
		{name: "signer", mutate: func(target *RemoteActionRepairTarget) { target.SignerPubKey = "02aa" }},
		{name: "fee", mutate: func(target *RemoteActionRepairTarget) { target.FeeTxID = "another-fee" }},
		{name: "commit", mutate: func(target *RemoteActionRepairTarget) { target.CommitTxID = "another-commit" }},
		{name: "reveal", mutate: func(target *RemoteActionRepairTarget) { target.RevealTxID = "another-reveal" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager, _, target, client := newRemoteActionRepairFixture(t)
			test.mutate(&target)
			if _, err := manager.PlanRemoteActionRepair(target); err == nil {
				t.Fatal("mismatched target was accepted")
			}
			if client.acks != 0 || client.results != 0 {
				t.Fatal("rejected target sent a network request")
			}
		})
	}

	manager, resv, target, client := newRemoteActionRepairFixture(t)
	plan, err := manager.PlanRemoteActionRepair(target)
	if err != nil {
		t.Fatal(err)
	}
	resv.Status = RS_PERFORM_ACTION_TX_CONFIRMED
	if err := manager.ApplyRemoteActionRepair(*plan); err == nil {
		t.Fatal("stale approved plan was accepted")
	}
	if client.acks != 0 || client.results != 0 {
		t.Fatal("stale plan sent a network request")
	}
}
