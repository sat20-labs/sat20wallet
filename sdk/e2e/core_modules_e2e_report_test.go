package e2e

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCoreE2EStructuredOutputClassification(t *testing.T) {
	for _, test := range []struct {
		name, line      string
		metric, verdict bool
	}{
		{"metric is not a browser case", `stability_test.go:37: core-e2e-metric: {"case":"account_sync","phase":"warm_noop_sync","status":"pass","duration_ms":100}`, true, false},
		{"browser pass retains nested metrics", `{"case":"Wallet PWA: asset context","status":"pass","metrics":[{"phase":"refresh","duration_ms":40}]}`, false, true},
		{"browser failure remains visible", `{"case":"Wallet PWA: asset context","status":"fail","message":"stale assets"}`, false, true},
		{"malformed metric", `core-e2e-metric: {"case":`, false, false},
		{"unstructured diagnostic", `core-e2e: genuine child failure`, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			metric, verdict := parseCoreE2EStructuredLine(test.line)
			require.Equal(t, test.metric, metric != nil, "metric classification")
			require.Equal(t, test.verdict, verdict != nil, "browser classification")
			if metric != nil {
				require.Equal(t, "warm_noop_sync", metric["phase"])
			}
			if verdict != nil {
				require.Equal(t, "Wallet PWA: asset context", verdict["case"])
				if verdict["status"] == "pass" {
					require.Len(t, verdict["metrics"], 1)
				}
			}
		})
	}
}
