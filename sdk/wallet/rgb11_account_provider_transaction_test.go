package wallet

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestRunRGB11AccountImportStepsRollsBackFailingAndAppliedScopes(t *testing.T) {
	var calls []string
	boom := errors.New("rebuild failed")
	steps := []rgb11AccountImportStep{
		{
			apply: func() error { calls = append(calls, "apply-0"); return nil },
			rollback: func() error { calls = append(calls, "rollback-0"); return nil },
			commit: func() { calls = append(calls, "commit-0") },
		},
		{
			apply: func() error { calls = append(calls, "apply-1"); return boom },
			rollback: func() error { calls = append(calls, "rollback-1"); return nil },
			commit: func() { calls = append(calls, "commit-1") },
		},
		{
			apply: func() error { calls = append(calls, "apply-2"); return nil },
			rollback: func() error { calls = append(calls, "rollback-2"); return nil },
		},
	}

	err := runRGB11AccountImportSteps(steps)
	if !errors.Is(err, boom) {
		t.Fatalf("got %v, want original apply error", err)
	}
	want := []string{"apply-0", "apply-1", "rollback-1", "rollback-0"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls=%v, want %v", calls, want)
	}
}

func TestRunRGB11AccountImportStepsCommitsOnlyAfterAllApply(t *testing.T) {
	var calls []string
	steps := []rgb11AccountImportStep{
		{
			apply: func() error { calls = append(calls, "apply-0"); return nil },
			rollback: func() error { calls = append(calls, "rollback-0"); return nil },
			commit: func() { calls = append(calls, "commit-0") },
		},
		{
			apply: func() error { calls = append(calls, "apply-1"); return nil },
			rollback: func() error { calls = append(calls, "rollback-1"); return nil },
			commit: func() { calls = append(calls, "commit-1") },
		},
	}

	if err := runRGB11AccountImportSteps(steps); err != nil {
		t.Fatal(err)
	}
	want := []string{"apply-0", "apply-1", "commit-0", "commit-1"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls=%v, want %v", calls, want)
	}
}

func TestRunRGB11AccountImportStepsReportsRollbackFailure(t *testing.T) {
	boom := errors.New("apply failed")
	err := runRGB11AccountImportSteps([]rgb11AccountImportStep{{
		apply: func() error { return boom },
		rollback: func() error { return errors.New("restore failed") },
	}})
	if !errors.Is(err, boom) || !strings.Contains(err.Error(), "rollback failed") ||
		!strings.Contains(err.Error(), "restore failed") {
		t.Fatalf("unexpected error: %v", err)
	}
}
