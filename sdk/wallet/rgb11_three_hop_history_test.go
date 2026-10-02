package wallet

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	coreconsignment "github.com/sat20-labs/rgb11/consignment"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

type historicalUnknownSpendersEvidence struct {
	rgb11wallet.BitcoinEvidenceProvider
	points map[string]bool
}

type historyReorgEvidence struct {
	rgb11wallet.BitcoinEvidenceProvider
	target string
}

func (e historyReorgEvidence) GetTxStatus(txid string) (*rgb11wallet.BitcoinTxStatus, error) {
	if txid == e.target {
		return &rgb11wallet.BitcoinTxStatus{TxID: txid}, nil
	}
	return e.BitcoinEvidenceProvider.GetTxStatus(txid)
}

func (e *historicalUnknownSpendersEvidence) GetOutspend(outpoint string) (*rgb11wallet.BitcoinOutspend, error) {
	if e.points[outpoint] {
		return &rgb11wallet.BitcoinOutspend{Spent: true}, nil
	}
	return e.BitcoinEvidenceProvider.GetOutspend(outpoint)
}

// Three genuine signed and individually confirmed sends. Only the public
// outspend response omits spender IDs; raw witnesses remain available.
func testRGB11ThreeHopHistory(t *testing.T, manager *Manager, evidence rgb11wallet.BitcoinEvidenceProvider,
	first, second, third *rgb11wallet.PendingTransfer) {
	t.Helper()
	ctx := context.Background()
	// Optional offline export for the pinned official Rust history oracle.
	if dir := os.Getenv("RGB11_HISTORY_ORACLE_DIR"); dir != "" {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		for name, pending := range map[string]*rgb11wallet.PendingTransfer{"first": first, "second": second, "third": third} {
			if err := os.WriteFile(filepath.Join(dir, name+".rgba"), pending.LocalConsignment, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, pending.State.WitnessTxID+".tx"), pending.SignedTx, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	changes := func(original, successor *rgb11wallet.PendingTransfer) string {
		t.Helper()
		for _, op := range rgb11PendingChangeOutpoints(original) {
			for _, input := range successor.State.InputOutPoints {
				if input == op {
					return op
				}
			}
		}
		t.Fatal("successor did not consume preceding change")
		return ""
	}
	firstChange, secondChange := changes(first, second), changes(second, third)
	fault := &historicalUnknownSpendersEvidence{BitcoinEvidenceProvider: evidence, points: map[string]bool{firstChange: true, secondChange: true}}
	for _, edge := range []struct{ point, txid string }{{firstChange, second.State.WitnessTxID}, {secondChange, third.State.WitnessTxID}} {
		if _, ok := verifyRGB11ExpectedSpend(fault, edge.point, edge.txid); !ok {
			t.Fatal("confirmed raw successor spend is not independently verifiable")
		}
	}
	manager.rgbManager.evidence = fault
	defer func() { manager.rgbManager.evidence = evidence }()
	// A consignment describing the current third hop remains valid. The middle
	// consignment is historical and its former terminal is now spent.
	if _, err := rgb11wallet.ValidateWith(ctx, rgb11wallet.NewNativeConsensusValidatorWithReveals(third.ChangeSeals...), third.LocalConsignment, fault); err != nil {
		t.Fatalf("current third-hop consignment is invalid: %v", err)
	}
	_, middleErr := rgb11wallet.ValidateWith(ctx, rgb11wallet.NewNativeConsensusValidatorWithReveals(second.ChangeSeals...), second.LocalConsignment, fault)
	if !errors.Is(middleErr, coreconsignment.ErrOutpointSpend) {
		t.Fatalf("middle historical terminal control: %v", middleErr)
	}
	t.Logf("independent confirmed edges verified; current third consignment valid; middle-only current-state validation=%v", middleErr)
	// Revalidate the middle historical DAG without writing receipts/projections.
	validator := rgb11wallet.NewNativeConsensusValidatorWithReveals(second.ChangeSeals...)
	before, err := manager.rgbManager.projectionStore.ExportSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if err := validator.ValidateHistoricalConsignment(ctx, second.LocalConsignment, fault); err != nil {
		t.Fatalf("historical second-hop consensus rejected: %v", err)
	}
	for _, pending := range []*rgb11wallet.PendingTransfer{first, second} {
		for _, kind := range []string{"missing_raw", "hash_mismatch", "reorg"} {
			t.Run(pending.State.WitnessTxID[:8]+"_"+kind, func(t *testing.T) {
				var invalid rgb11wallet.BitcoinEvidenceProvider = &unknownSuccessorEvidence{BitcoinEvidenceProvider: fault, target: pending.State.WitnessTxID, kind: kind}
				if kind == "reorg" {
					invalid = historyReorgEvidence{BitcoinEvidenceProvider: fault, target: pending.State.WitnessTxID}
				}
				if err := validator.ValidateHistoricalConsignment(ctx, second.LocalConsignment, invalid); err == nil {
					t.Fatal("historical ancestor witness failure accepted")
				}
			})
		}
	}
	after, err := manager.rgbManager.projectionStore.ExportSnapshot()
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("history validation mutated projection: %v", err)
	}
	balanceBefore, err := manager.GetRGB11AssetBalance(&third.State.Asset.Name)
	if err != nil {
		t.Fatal(err)
	}
	result, refreshErr := manager.rgbManager.RefreshRGB11State(ctx)
	for _, pending := range []*rgb11wallet.PendingTransfer{first, second, third} {
		stored, err := manager.rgbManager.projectionStore.LoadPendingTransfer(pending.State.TransferID)
		if err != nil || stored.State.Status != "settled" {
			t.Fatalf("confirmed history changed: txid=%s err=%v", pending.State.WitnessTxID, err)
		}
	}
	if refreshErr != nil || manager.GetRGB11ConsistencyStatus() != "ok" {
		t.Fatalf("three-hop confirmed history rejected: oldest=%s middle=%s newest=%s result=%+v consistency=%s err=%v", first.State.WitnessTxID, second.State.WitnessTxID, third.State.WitnessTxID, result, manager.GetRGB11ConsistencyStatus(), refreshErr)
	}
	balanceAfter, err := manager.GetRGB11AssetBalance(&third.State.Asset.Name)
	if err != nil || !reflect.DeepEqual(balanceBefore, balanceAfter) {
		t.Fatalf("history refresh changed balance: before=%+v after=%+v err=%v", balanceBefore, balanceAfter, err)
	}
	proofs, err := manager.rgbManager.projectionStore.ListProofs()
	if err != nil {
		t.Fatal(err)
	}
	for _, proof := range proofs {
		if (proof.OutPoint == firstChange || proof.OutPoint == secondChange) && proof.Status != "spending" {
			t.Fatalf("historical spent projection revived: %s %s", proof.OutPoint, proof.Status)
		}
	}
}
