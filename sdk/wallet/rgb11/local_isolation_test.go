package rgb11wallet

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	indexer "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/rgb11/invoicing"
	"github.com/sat20-labs/rgb11/seals"
	corewallet "github.com/sat20-labs/rgb11/wallet"
)

// Exercise the actual unchanged compressor across literal/token and stored
// block boundaries. Random inputs are deterministic codec fixtures, not keys.
func TestLocalDeflateBound(t *testing.T) {
	for _, n := range []int{0, 1, 16383, 16384, 16385, 65534, 65535, 65536, 1 << 20} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			raw := make([]byte, n)
			if _, err := rand.New(rand.NewSource(11)).Read(raw); err != nil {
				t.Fatal(err)
			}
			for _, data := range [][]byte{raw, bytes.Repeat([]byte{0x81}, n)} {
				encoded, err := deflateRecovery(data)
				if err != nil || len(encoded) > 2*n+64 {
					t.Fatalf("codec size=%d exceeds bound=%d err=%v", len(encoded), 2*n+64, err)
				}
				decoded, err := inflateRecovery(encoded)
				if n == 0 {
					if err == nil {
						t.Fatal("empty recovery unexpectedly accepted")
					}
					continue
				}
				if err != nil || !bytes.Equal(decoded, data) {
					t.Fatal("actual compressor round trip failed")
				}
			}
		})
	}
}

func TestLocalBoundUsesActualCodec(t *testing.T) {
	seal, err := seals.RandomWitnessBlindSeal(1)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := &RGB11WalletSnapshot{Version: WalletSnapshotVersion, WalletID: "bound-wallet", EngineBuildID: NativeEngineBuildID}
	for i, n := range []int{1, 16384, 65535} {
		invoice := make([]byte, n)
		if _, err := rand.New(rand.NewSource(int64(i + 31))).Read(invoice); err != nil {
			t.Fatal(err)
		}
		req := &corewallet.ReceiveRequest{Version: corewallet.ReceiveVersion, Mode: corewallet.ReceiveWitness,
			RequestID: fmt.Sprintf("request-%d", i), RecipientID: "recipient", Seal: seal, Invoice: string(invoice),
			Status: corewallet.ReceivePrepared, CreatedAt: 1, Expiry: 2}
		raw, err := corewallet.EncodeReceiveRequest(req)
		if err != nil {
			t.Fatal(err)
		}
		snapshot.EngineRecords = append(snapshot.EngineRecords, SnapshotRecord{Key: "wallet/receive/" + req.RequestID, Value: raw})
	}
	pack, err := RecoveryPackageFromSnapshot(snapshot, 0)
	if err != nil {
		t.Fatal(err)
	}
	encoded, actual, err := EncodeRecoveryPackageWithSize(pack)
	if err != nil {
		t.Fatal(err)
	}
	raw, bound, err := LocalRecoveryBound(snapshot, snapshot)
	if err != nil || raw != actual || len(encoded) > bound {
		t.Fatalf("framing or bound differs: raw=%d actual=%d encoded=%d bound=%d err=%v", raw, actual, len(encoded), bound, err)
	}
	// The count-only helper must reserve original damaged bytes even when no
	// valid recovery pack can be produced. It cannot return a fake upload body.
	broken := *snapshot
	broken.ProjectionRecords = []SnapshotRecord{{Key: "proof-damaged", Value: []byte{0xff}}}
	if _, err := RecoveryPackageFromSnapshot(&broken, 0); err == nil {
		t.Fatal("strict recovery accepted broken proof")
	}
	raw2, _, err := LocalRecoveryBound(&broken, snapshot)
	if err != nil || raw2 <= raw {
		t.Fatal("count-only bound discarded original damaged material")
	}
}

func TestLocalBoundRejectsCodecLimits(t *testing.T) {
	base := RGB11WalletSnapshot{Version: WalletSnapshotVersion, WalletID: "bound", EngineBuildID: NativeEngineBuildID}
	for _, kind := range []string{"field", "key", "count", "raw"} {
		t.Run(kind, func(t *testing.T) {
			full := base
			switch kind {
			case "field":
				full.ProjectionRecords = []SnapshotRecord{{Key: "record", Value: make([]byte, rgb11RecoveryMaxFieldSize+1)}}
			case "key":
				full.ProjectionRecords = []SnapshotRecord{{Key: strings.Repeat("k", 64*1024+1), Value: []byte{1}}}
			case "count":
				for i := 0; i <= rgb11RecoveryMaxRecords; i++ {
					full.ProjectionRecords = append(full.ProjectionRecords, SnapshotRecord{Key: fmt.Sprint(i), Value: []byte{1}})
				}
			case "raw":
				for i := 0; i < 4; i++ {
					full.ProjectionRecords = append(full.ProjectionRecords, SnapshotRecord{Key: fmt.Sprint(i), Value: make([]byte, rgb11RecoveryMaxFieldSize)})
				}
			}
			if _, _, err := LocalRecoveryBound(&full, &base); err == nil {
				t.Fatal("oversized complete local data passed capacity")
			}
		})
	}
}

// Reuse native issued/validated material; these assertions concern local
// storage bytes and never authorize a spend from the fixture sidecars.
func nativeLocalRows(t *testing.T) ([]SnapshotRecord, string, indexer.AssetName) {
	t.Helper()
	sources, facts := issuedChannelFunding(t)
	source := sources[1] // this carrier has exactly one native Opout
	receipt, err := ValidateWith(context.Background(), NewNativeConsensusValidator(), source.Consignment, facts)
	if err != nil {
		t.Fatal(err)
	}
	var allocation *ValidatedAllocation
	for i := range receipt.Allocations {
		if receipt.Allocations[i].OutPoint == source.OutPoint {
			if allocation != nil {
				t.Fatal("fixture carrier has multiple Opouts")
			}
			allocation = &receipt.Allocations[i]
		}
	}
	if allocation == nil {
		t.Fatal("native allocation missing")
	}
	hash, err := receipt.Hash()
	if err != nil {
		t.Fatal(err)
	}
	output := indexer.NewTxOutput(source.Value)
	output.OutPointStr, output.OutValue.PkScript = source.OutPoint, append([]byte(nil), source.PkScript...)
	asset := indexer.AssetInfo{Name: allocation.AssetName, Amount: *allocation.Amount.Clone()}
	proof := &AllocationProof{OutPoint: allocation.OutPoint, AssetName: allocation.AssetName, OperationID: allocation.OperationID,
		AssignmentType: allocation.AssignmentType, AssignmentIndex: allocation.AssignmentIndex, StateClass: allocation.StateClass,
		StateData: append([]byte(nil), allocation.StateData...), SealDisclosure: append([]byte(nil), allocation.SealDisclosure...),
		ConsignmentHash: receipt.ConsignmentHash, ValidationHash: hash, Status: "spending"}
	rows, err := AddValidatedConsignmentProjectionSnapshot(nil, source.Consignment, receipt, []ProjectionReplacement{{Output: output, Asset: &asset, Proof: proof}})
	if err != nil {
		t.Fatal(err)
	}
	return rows, source.OutPoint, allocation.AssetName
}

func TestLocalHealthyHistory(t *testing.T) {
	rows, _, _ := nativeLocalRows(t)
	for _, status := range []string{"valid", "settled", "spending", "orphaned"} {
		t.Run(status, func(t *testing.T) {
			changed := cloneRecoveryRecords(rows)
			for i, r := range changed {
				if strings.HasPrefix(r.Key, "proof-") {
					var proof AllocationProof
					if decode(r.Value, &proof) != nil {
						t.Fatal("decode native sidecar")
					}
					proof.Status = status
					changed[i].Value = mustEncodeRecoveryTest(t, &proof)
				}
			}
			if err := validateProjectionSnapshot(changed); err != nil {
				t.Fatal(err)
			}
			view, err := SplitLocalProjection(changed)
			if err != nil || len(view.Faults) != 0 || !sameLocalRecords(changed, view.Records) {
				t.Fatal("healthy historical semantics changed")
			}
			var without []SnapshotRecord
			for _, r := range changed {
				if !strings.HasPrefix(r.Key, "output-") {
					without = append(without, r)
				}
			}
			if err := validateProjectionSnapshot(without); err == nil {
				t.Fatal("fixture no longer exercises strict missing-output rejection")
			}
			if _, err := SplitLocalProjection(without); err == nil {
				t.Fatal("local view silently dropped unlocated historical proof")
			}
		})
	}
}

func TestLocalFrozenRowsAfterHydrate(t *testing.T) {
	for _, embedded := range []bool{false, true} {
		t.Run(fmt.Sprint(embedded), func(t *testing.T) {
			db := indexerdb.NewKVDB(t.TempDir())
			defer db.Close()
			store := NewProjectionStore(db, nil)
			if err := store.SetScope("freeze"); err != nil {
				t.Fatal(err)
			}
			engineStore := NewEngineStore(db)
			if err := engineStore.SetScope("freeze"); err != nil {
				t.Fatal(err)
			}
			engine, err := corewallet.NewEngine(engineStore)
			if err != nil {
				t.Fatal(err)
			}
			request, err := engine.CreateReceive(corewallet.ReceiveParams{Mode: corewallet.ReceiveWitness, Network: invoicing.BitcoinRegtest,
				RecipientID: "frozen-engine", WitnessVout: 1, WitnessScript: append([]byte{0, 32}, bytes.Repeat([]byte{2}, 32)...), Expiry: 2_000_000_000})
			if err != nil {
				t.Fatal(err)
			}
			engineRows, err := engineStore.ExportSnapshot()
			if err != nil {
				t.Fatal(err)
			}
			rows, point, name := nativeLocalRows(t)
			var object []byte
			for _, r := range rows {
				if strings.HasPrefix(r.Key, "object-") {
					object = r.Value
					break
				}
			}
			digest := sha256.Sum256(object)
			hash := hex.EncodeToString(digest[:])
			pending := &PendingTransfer{State: TransferState{TransferID: "frozen-send", Direction: "send", Status: "prepared",
				InputOutPoints: []string{point}}, LocalObjectHash: hash, RecipientObjectHash: hash}
			if embedded {
				pending.LocalConsignment = append([]byte(nil), object...)
				pending.RecipientConsignment = append([]byte(nil), object...)
			}
			state := &TransferState{TransferID: "frozen-receive", Direction: "receive", Status: "settled", ReceiveRequestID: request.RequestID, OutputOutPoints: []string{point}}
			rows = append(rows, SnapshotRecord{Key: "pending-frozen-send", Value: mustEncodeRecoveryTest(t, pending)}, SnapshotRecord{Key: "transfer-frozen-receive", Value: mustEncodeRecoveryTest(t, state)})
			// Only the already validated proof's bytes are damaged; other raw records
			// deliberately retain their persisted embedded or normalized representation.
			proofKey := "proof-" + point + "-" + name.String()
			for i := range rows {
				if rows[i].Key == proofKey {
					rows[i].Value = []byte{0xff}
				}
			}
			for _, r := range rows {
				if err := db.Write([]byte("rgb11-freeze-"+r.Key), r.Value); err != nil {
					t.Fatal(err)
				}
			}
			baseline, err := store.ReadLocalRecords()
			if err != nil {
				t.Fatal(err)
			}
			view, err := SplitLocalProjection(baseline)
			if err != nil || !view.Requests[request.RequestID] {
				t.Fatalf("fault did not freeze linked lifecycle: %v", err)
			}
			batch := db.NewWriteBatch()
			defer batch.Close()
			if err := store.StageLocalProjection(batch, baseline, baseline, view.Faults); err != nil {
				t.Fatal(err)
			}
			if err := engineStore.StageSnapshotImport(batch, engineRows); err != nil {
				t.Fatal(err)
			}
			if err := batch.Flush(); err != nil {
				t.Fatal(err)
			}
			after, err := store.ReadLocalRecords()
			if err != nil || !sameLocalRecords(baseline, after) {
				t.Fatal("rehydration rewrote or deleted frozen persisted rows")
			}
			for _, r := range engineRows {
				raw, err := db.Read([]byte("rgb11-engine-freeze-" + r.Key))
				if err != nil || !bytes.Equal(raw, r.Value) {
					t.Fatal("frozen engine row changed")
				}
			}
			if _, err := store.ExportSnapshot(); err == nil {
				t.Fatal("strict backup accepted the damaged proof")
			}
		})
	}
}
