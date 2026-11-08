package e2e

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
)

// coreE2EChildEvent intentionally has no Output field. go test -json Output
// can contain private child diagnostics and must not enter shared evidence.
type coreE2EChildEvent struct {
	Action  string
	Test    string
	Elapsed float64
}

type coreE2EChildSummary struct {
	Events   []coreE2EChildEvent
	Verdicts []coreE2EChildEvent
	// These two fields locate the last observed stage, not the failure cause.
	LastChildEvent  *coreE2EChildEvent
	LastStartedTest string
}

func collectCoreE2EChildEvents(reader io.Reader) (coreE2EChildSummary, error) {
	summary := coreE2EChildSummary{
		Events:   []coreE2EChildEvent{},
		Verdicts: []coreE2EChildEvent{},
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 65536), 4<<20)
	for scanner.Scan() {
		var event coreE2EChildEvent
		if json.Unmarshal(scanner.Bytes(), &event) != nil || !coreE2EAllowedAction(event.Action) {
			continue
		}
		summary.Events = append(summary.Events, event)
		last := event
		summary.LastChildEvent = &last
		if event.Action == "run" && event.Test != "" {
			summary.LastStartedTest = event.Test
		}
		if event.Test != "" && (event.Action == "pass" || event.Action == "fail" || event.Action == "skip") {
			summary.Verdicts = append(summary.Verdicts, event)
		}
	}
	return summary, scanner.Err()
}

func coreE2EAllowedAction(action string) bool {
	switch action {
	case "start", "run", "pause", "cont", "pass", "fail", "skip":
		return true
	default:
		return false
	}
}

func (summary coreE2EChildSummary) reportFields(runErr error, childExitCode *int, outerCtxErr error) map[string]any {
	return map[string]any{
		"child_events":        summary.Events,
		"child_exit_code":     childExitCode,
		"outer_ctx_timed_out": outerCtxErr == context.DeadlineExceeded,
		"last_child_event":    summary.LastChildEvent,
		"last_started_test":   summary.LastStartedTest,
		"passed":              runErr == nil,
		"timed_out":           outerCtxErr != nil, // Retain the existing field's meaning.
		"verdicts":            summary.Verdicts,
		"failures":            []string{}, // Never copy arbitrary child Output here.
	}
}

// Keep the actual events and parent verdict from every independent child.
// Group verdicts are never synthesized or deduplicated by this aggregation.
func mergeCoreE2EReports(reports []map[string]any, ctxErr error) map[string]any {
	var summary coreE2EChildSummary
	var browsers, metrics []map[string]any
	passed := len(reports) != 0
	outputBytes := 0
	for _, report := range reports {
		events, _ := report["child_events"].([]coreE2EChildEvent)
		verdicts, _ := report["verdicts"].([]coreE2EChildEvent)
		summary.Events = append(summary.Events, events...)
		summary.Verdicts = append(summary.Verdicts, verdicts...)
		if last, ok := report["last_child_event"].(*coreE2EChildEvent); ok {
			summary.LastChildEvent = last
		}
		if last, ok := report["last_started_test"].(string); ok {
			summary.LastStartedTest = last
		}
		values, _ := report["browser_verdicts"].([]map[string]any)
		browsers = append(browsers, values...)
		values, _ = report["metrics"].([]map[string]any)
		metrics = append(metrics, values...)
		count, _ := report["child_output_bytes"].(int)
		outputBytes += count
		passed = passed && report["passed"] == true
	}
	exitCode := 0
	var runErr error
	if !passed {
		exitCode = 1
		runErr = errors.New("one or more Core segments did not complete successfully")
	}
	report := summary.reportFields(runErr, &exitCode, ctxErr)
	report["browser_verdicts"] = browsers
	report["metrics"] = metrics
	report["child_output_bytes"] = outputBytes
	if len(reports) != 0 {
		report["real_components"] = reports[0]["real_components"]
		report["controlled_boundary"] = reports[0]["controlled_boundary"]
	}
	return report
}
