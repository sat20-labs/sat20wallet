package wallet

import "testing"

func TestContractInvokeOperationLogIsSimpleExplicitAction(t *testing.T) {
	mgr := &Manager{db: newMemoryKVDB()}
	logID := mgr.beginContractInvokeOperationLog(
		"channel:asset:transcend.tc",
		`{"Action":"deposit","Param":"{}"}`,
		"Bitcoin",
		"ordx:f:test",
		"100",
	)
	if logID == "" {
		t.Fatal("contract invoke log was not explicitly created")
	}
	mgr.completeContractInvokeOperationLog(logID, "tx-contract-1")

	logs, err := mgr.GetOperationLogs()
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 {
		t.Fatalf("unexpected log count: %d", len(logs))
	}
	log := logs[0]
	if log.Action != "invoke_contract" || log.Status != OperationLogSucceeded {
		t.Fatalf("unexpected contract invoke log: %+v", log)
	}
	if log.Parameters["contract"] != "channel:asset:transcend.tc" ||
		log.Parameters["network"] != "Bitcoin" ||
		log.Parameters["asset"] != "ordx:f:test" ||
		log.Parameters["amount"] != "100" {
		t.Fatalf("contract invoke parameters are incomplete: %+v", log.Parameters)
	}
	if log.Result["txid"] != "tx-contract-1" || log.TxID != "tx-contract-1" {
		t.Fatalf("contract invoke result is incomplete: %+v", log)
	}
	if len(log.History) != 2 || log.History[1].Message != "Contract invocation submitted" {
		t.Fatalf("unexpected contract invoke history: %+v", log.History)
	}
}

func TestContractInvokeOperationLogRecordsFailureWithoutReservation(t *testing.T) {
	mgr := &Manager{db: newMemoryKVDB()}
	logID := mgr.beginContractInvokeOperationLog(
		"channel:asset:transcend.tc",
		`{"Action":"withdraw","Param":"{}"}`,
		"SatoshiNet",
		"ordx:f:test",
		"50",
	)
	mgr.failContractInvokeOperationLog(logID, errOperationLogTestFailure{})

	logs, err := mgr.GetOperationLogs()
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 || logs[0].Status != OperationLogFailed {
		t.Fatalf("failed contract invoke log missing: %+v", logs)
	}
	if logs[0].ReservationID != 0 || logs[0].ReservationType != "" {
		t.Fatalf("simple contract invoke unexpectedly acquired reservation relation: %+v", logs[0])
	}
	if logs[0].History[len(logs[0].History)-1].Message != "contract invoke failed" {
		t.Fatalf("failure message missing: %+v", logs[0].History)
	}
}

type errOperationLogTestFailure struct{}

func (errOperationLogTestFailure) Error() string { return "contract invoke failed" }
