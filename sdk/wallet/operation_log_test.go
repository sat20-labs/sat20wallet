package wallet

import "testing"

func TestOperationLogExplicitLifecycleOrderingAndHistory(t *testing.T) {
	kv := newMemoryKVDB()
	logs := NewOperationLogManager(kv)

	openLog, err := logs.Create(OperationLogCreate{
		Category: "channel",
		Action:   "open_channel",
		Title:    "Open channel",
		Summary:  "Opening a 100000 sat channel",
		Parameters: map[string]string{
			"amount":   "100000",
			"fee_rate": "5",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := logs.BindReservation(openLog.ID, RESV_TYPE_OPEN, 101); err != nil {
		t.Fatal(err)
	}

	paymentLog, err := logs.Create(OperationLogCreate{
		Category: "channel",
		Action:   "send_from_channel",
		Title:    "Send from channel",
		Summary:  "Sending asset from channel",
		Parameters: map[string]string{
			"asset":  "ordx:f:test",
			"amount": "12",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := logs.BindReservation(paymentLog.ID, RESV_TYPE_PAYMENT, 202); err != nil {
		t.Fatal(err)
	}

	updated, err := logs.UpdateByReservation(RESV_TYPE_OPEN, 101, OperationLogUpdate{
		Status:  OperationLogRunning,
		Message: "Funding transaction broadcast",
		TxID:    "funding-txid",
		Details: map[string]string{"txid": "funding-txid"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated == nil || updated.TxID != "funding-txid" {
		t.Fatalf("reservation update did not update operation log: %+v", updated)
	}
	if _, err := logs.UpdateByReservation(RESV_TYPE_OPEN, 101, OperationLogUpdate{
		Status:  OperationLogSucceeded,
		Message: "Channel is ready",
		Result:  map[string]string{"channel_id": "channel-1"},
	}); err != nil {
		t.Fatal(err)
	}

	items, err := logs.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("unexpected operation log count: got %d want 2", len(items))
	}
	if items[0].ID != openLog.ID {
		t.Fatalf("updated record was not moved to the front: got %s want %s", items[0].ID, openLog.ID)
	}
	if items[0].Status != OperationLogSucceeded || items[0].Summary != "Channel is ready" {
		t.Fatalf("unexpected final operation state: %+v", items[0])
	}
	if len(items[0].History) != 3 {
		t.Fatalf("unexpected explicit history length: got %d want 3", len(items[0].History))
	}
	if items[0].History[1].Message != "Funding transaction broadcast" || items[0].History[2].Message != "Channel is ready" {
		t.Fatalf("history is not semantic: %+v", items[0].History)
	}
	if items[0].Parameters["amount"] != "100000" {
		t.Fatalf("initial parameters were not preserved: %+v", items[0].Parameters)
	}
}

func TestOperationLogDoesNotInferContentFromReservation(t *testing.T) {
	kv := newMemoryKVDB()
	logs := NewOperationLogManager(kv)

	missing, err := logs.UpdateByReservation(RESV_TYPE_OPEN, 999, OperationLogUpdate{
		Status:  OperationLogSucceeded,
		Message: "Should not create a record",
	})
	if err != nil {
		t.Fatal(err)
	}
	if missing != nil {
		t.Fatalf("reservation update created an implicit log: %+v", missing)
	}
	items, err := logs.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("operation log must be explicitly created, got %+v", items)
	}
}

func TestOperationLogPersistenceDetailAndDeleteAll(t *testing.T) {
	kv := newMemoryKVDB()
	logs := NewOperationLogManager(kv)

	created, err := logs.Create(OperationLogCreate{
		Category: "contract",
		Action:   "invoke_contract",
		Title:    "Invoke contract",
		Summary:  "Submitting contract invocation",
		Parameters: map[string]string{
			"contract": "contract-url",
			"action":   "swap",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := logs.BindReservation(created.ID, RESV_TYPE_OPEN, 101); err != nil {
		t.Fatal(err)
	}
	if _, err := logs.Update(created.ID, OperationLogUpdate{
		Status:  OperationLogSucceeded,
		Message: "Contract invocation submitted",
		TxID:    "tx-1",
		Result:  map[string]string{"txid": "tx-1"},
	}); err != nil {
		t.Fatal(err)
	}

	reloaded := NewOperationLogManager(kv)
	detail, err := reloaded.Get(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail == nil || detail.TxID != "tx-1" || len(detail.History) != 2 {
		t.Fatalf("persisted operation detail is incomplete: %+v", detail)
	}
	logID, err := reloaded.findRelation(RESV_TYPE_OPEN, 101)
	if err != nil {
		t.Fatal(err)
	}
	if logID != created.ID {
		t.Fatalf("reservation relation was not persisted: got %s want %s", logID, created.ID)
	}

	if err := reloaded.DeleteAll(); err != nil {
		t.Fatal(err)
	}
	items, err := logs.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("operation logs were not deleted: %+v", items)
	}
	logID, err = logs.findRelation(RESV_TYPE_OPEN, 101)
	if err != nil {
		t.Fatal(err)
	}
	if logID != "" {
		t.Fatalf("reservation relation survived delete all: %s", logID)
	}
}
