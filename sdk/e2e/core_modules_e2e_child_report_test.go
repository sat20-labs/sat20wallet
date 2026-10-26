package e2e

import (
	"bufio"
	"context"
	"encoding/json"
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
	Events          []coreE2EChildEvent
	Verdicts        []coreE2EChildEvent
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
