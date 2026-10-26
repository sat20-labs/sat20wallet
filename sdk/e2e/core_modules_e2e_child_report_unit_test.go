package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestCoreE2EChildSummaryNormalPass(t *testing.T) {
	input := strings.Join([]string{
		`{"Action":"start","Package":"wallet"}`,
		`{"Action":"run","Package":"wallet","Test":"TestLifecycle"}`,
		`{"Action":"pause","Test":"TestLifecycle"}`,
		`{"Action":"cont","Test":"TestLifecycle"}`,
		`{"Action":"unknown","Test":"TestLifecycle"}`,
		`{"Action":"output","Test":"TestLifecycle","Output":"ordinary text"}`,
		`{"Action":"pass","Test":"TestLifecycle","Elapsed":1.25}`,
		`{"Action":"pass","Package":"wallet","Elapsed":1.5}`,
	}, "\n")
	summary, err := collectCoreE2EChildEvents(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.Events) != 6 || len(summary.Verdicts) != 1 || summary.LastStartedTest != "TestLifecycle" {
		t.Fatalf("unexpected event summary: %+v", summary)
	}
	if summary.LastChildEvent == nil || summary.LastChildEvent.Action != "pass" || summary.LastChildEvent.Test != "" {
		t.Fatalf("package terminal event missing: %+v", summary.LastChildEvent)
	}
	code := 0
	fields := summary.reportFields(nil, &code, nil)
	if fields["passed"] != true || fields["child_exit_code"] != &code || fields["outer_ctx_timed_out"] != false || fields["timed_out"] != false {
		t.Fatalf("incorrect pass fields: %+v", fields)
	}
}

func TestCoreE2EChildSummaryPackageFailureAndNoTestVerdict(t *testing.T) {
	input := `{"Action":"start","Package":"wallet"}` + "\n" + `{"Action":"fail","Package":"wallet","Elapsed":0.3}`
	summary, err := collectCoreE2EChildEvents(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.Events) != 2 || len(summary.Verdicts) != 0 || summary.LastChildEvent == nil || summary.LastChildEvent.Action != "fail" || summary.LastChildEvent.Test != "" {
		t.Fatalf("package-level failure classified incorrectly: %+v", summary)
	}
	code := 1
	fields := summary.reportFields(errors.New("exit status 1"), &code, nil)
	if fields["passed"] != false || *(fields["child_exit_code"].(*int)) != 1 || fields["outer_ctx_timed_out"] != false {
		t.Fatalf("incorrect package-fail fields: %+v", fields)
	}
	// Package events must not satisfy the parent test's nonempty-verdict guard.
	if len(summary.Verdicts) != 0 {
		t.Fatal("package event counted as a test verdict")
	}
}

func TestCoreE2EChildSummaryEarlyExitAfterRun(t *testing.T) {
	input := `{"Action":"run","Test":"TestInterrupted"}`
	summary, err := collectCoreE2EChildEvents(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.Verdicts) != 0 || summary.LastStartedTest != "TestInterrupted" || summary.LastChildEvent == nil || summary.LastChildEvent.Action != "run" {
		t.Fatalf("early exit stage incorrect: %+v", summary)
	}
	code := 1
	fields := summary.reportFields(errors.New("exit status 1"), &code, nil)
	if fields["passed"] != false || *(fields["child_exit_code"].(*int)) != 1 {
		t.Fatalf("early process exit fields incorrect: %+v", fields)
	}
	empty := coreE2EChildSummary{}
	startFailure := empty.reportFields(errors.New("failed to start"), nil, nil)
	if startFailure["child_exit_code"] != (*int)(nil) {
		t.Fatalf("missing process must have null exit code: %+v", startFailure)
	}
}

func TestCoreE2EChildSummaryRetainsPartialEventsOnOversizedOutput(t *testing.T) {
	input := `{"Action":"run","Test":"TestBeforeLargeOutput"}` + "\n" +
		`{"Action":"output","Output":"` + strings.Repeat("x", 4<<20) + `"}`
	summary, err := collectCoreE2EChildEvents(strings.NewReader(input))
	if err == nil || len(summary.Events) != 1 || summary.LastStartedTest != "TestBeforeLargeOutput" {
		t.Fatalf("partial safe summary was lost after scanner error: events=%d last_started=%q error_present=%t", len(summary.Events), summary.LastStartedTest, err != nil)
	}
}

func TestCoreE2EChildSummaryOuterDeadlineVersusChildTimeout(t *testing.T) {
	summary := coreE2EChildSummary{}
	code := 1
	outer := summary.reportFields(context.DeadlineExceeded, &code, context.DeadlineExceeded)
	child := summary.reportFields(errors.New("child go test timed out"), &code, nil)
	if outer["outer_ctx_timed_out"] != true || outer["timed_out"] != true || outer["passed"] != false {
		t.Fatalf("outer deadline fields incorrect: %+v", outer)
	}
	if child["outer_ctx_timed_out"] != false || child["timed_out"] != false || child["passed"] != false {
		t.Fatalf("child-only timeout misreported as outer deadline: %+v", child)
	}
}

func TestCoreE2EChildSummaryNeverCopiesOutputOrTrustsPassMetric(t *testing.T) {
	const secret = "UNIQUE_SYNTHETIC_SECRET_NEVER_UPLOAD_01"
	input := strings.Join([]string{
		`{"Action":"run","Test":"TestInterrupted"}`,
		`{"Action":"output","Test":"TestInterrupted","Output":"core-e2e: ` + secret + `"}`,
		`{"Action":"fail","Test":"TestInterrupted","Elapsed":1.0,"Output":"` + secret + `"}`,
		`{"Action":"fail","Package":"wallet","Output":"core-e2e: ` + secret + `"}`,
	}, "\n")
	summary, err := collectCoreE2EChildEvents(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	code := 1
	report := summary.reportFields(errors.New("exit status 1"), &code, nil)
	report["metrics"] = []map[string]any{{"status": "pass"}}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(secret)) || bytes.Contains(raw, []byte("Output")) || bytes.Contains(raw, []byte("core-e2e:")) {
		t.Fatalf("raw child output entered shared report: %s", raw)
	}
	if !bytes.Contains(raw, []byte(`"failures":[]`)) || report["passed"] != false || summary.LastChildEvent == nil || summary.LastChildEvent.Test != "" {
		t.Fatalf("unsafe or incorrect failure report: %s", raw)
	}
}
