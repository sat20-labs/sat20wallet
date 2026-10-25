package wallet

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	swire "github.com/sat20-labs/satoshinet/wire"
)

// These tests do not simulate an old schema by clearing the new fields. Their
// inputs are emitted by the actual pinned old program, with old field layouts
// and independently generated semantic oracles. Missing fixtures fail loudly.
const legacyGobWalletCommit = "1a9aa9d67d741ba66556982fa53b1b81f4d41559"

type legacyGobFixtureEntry struct {
	File, SHA256, SemanticFile, SemanticSHA256, Type, ChannelID, ReservationType string
	ReservationID                                                                int64
}
type legacyGobFixtureManifest struct {
	Format                                   int
	WalletCommit, GoVersion, GeneratorSHA256 string
	SourceSHA256                             map[string]string
	TypeFields                               map[string]map[string]string
	Fixtures                                 []legacyGobFixtureEntry
}

func legacyGobReadFixture(t *testing.T, name string) ([]byte, legacyGobFixtureEntry) {
	t.Helper()
	root := filepath.Join("testdata", "legacy_pre_native")
	manifestRaw, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		t.Fatalf("genuine old fixtures are required; run the documented pinned old generator first: %v", err)
	}
	var manifest legacyGobFixtureManifest
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Format != 1 || manifest.WalletCommit != legacyGobWalletCommit {
		t.Fatalf("wrong old fixture provenance: %+v", manifest)
	}
	if len(manifest.GeneratorSHA256) != 64 || !strings.HasPrefix(manifest.GoVersion, "go1.") {
		t.Fatal("missing generator/compiler provenance")
	}
	generator, err := os.ReadFile(filepath.Join(root, "generator", "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if legacyGobDigest(generator) != manifest.GeneratorSHA256 {
		t.Fatal("fixture generator source hash differs from manifest")
	}
	for source, want := range legacyGobPinnedSourceSHA256 {
		if manifest.SourceSHA256[source] != want {
			t.Fatalf("old source hash mismatch for %s", source)
		}
	}
	for _, typ := range []reflect.Type{reflect.TypeOf(ChannelInDB{}), reflect.TypeOf(ChannelCommitmentV1{}), reflect.TypeOf(ChannelCommitment{}), reflect.TypeOf(CommitmentTx{}), reflect.TypeOf(ClosingDataInDB{}), reflect.TypeOf(SplicingDataInDB{})} {
		fields, ok := manifest.TypeFields[typ.Name()]
		if !ok || len(fields) == 0 {
			t.Fatalf("missing genuine old type shape for %s", typ.Name())
		}
		for name, oldType := range fields {
			if legacyFixtureNativeField(typ.Name(), name) {
				t.Fatalf("supposed old schema already contains %s.%s", typ.Name(), name)
			}
			current, exists := typ.FieldByName(name)
			if !exists || current.Type.String() != oldType {
				t.Fatalf("old field changed: %s.%s was %s, now %+v", typ.Name(), name, oldType, current)
			}
		}
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			if f.PkgPath == "" && !legacyFixtureNativeField(typ.Name(), f.Name) {
				if _, ok := fields[f.Name]; !ok {
					t.Fatalf("unreviewed non-native field %s.%s", typ.Name(), f.Name)
				}
			}
		}
	}
	for _, entry := range manifest.Fixtures {
		if entry.File != name {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(root, entry.File))
		if err != nil {
			t.Fatal(err)
		}
		if legacyGobDigest(raw) != entry.SHA256 {
			t.Fatalf("old gob hash mismatch for %s", name)
		}
		semantic, err := os.ReadFile(filepath.Join(root, entry.SemanticFile))
		if err != nil {
			t.Fatal(err)
		}
		if legacyGobDigest(semantic) != entry.SemanticSHA256 {
			t.Fatalf("old semantic oracle hash mismatch for %s", name)
		}
		return raw, entry
	}
	t.Fatalf("fixture %q absent from provenance manifest", name)
	return nil, legacyGobFixtureEntry{}
}
func legacyGobDigest(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }
func legacyGobSemantic(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(legacyFixtureCanonical(reflect.ValueOf(value)))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func legacyGobAssertSemanticOracle(t *testing.T, value any, entry legacyGobFixtureEntry) {
	t.Helper()
	want, err := os.ReadFile(filepath.Join("testdata", "legacy_pre_native", entry.SemanticFile))
	if err != nil {
		t.Fatal(err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, want); err != nil {
		t.Fatal(err)
	}
	got := legacyGobSemantic(t, value)
	if !bytes.Equal(got, compact.Bytes()) {
		t.Fatalf("old exported field semantics changed for %s\nwant %s\ngot  %s", entry.File, compact.Bytes(), got)
	}
}
func legacyGobOpenDB(t *testing.T, directory string) indexer.KVDB {
	t.Helper()
	database := NewKVDB(directory)
	if database == nil {
		t.Fatal("NewKVDB failed")
	}
	return database
}
func legacyGobAssertNoNativeState(t *testing.T, c *ChannelInDB) {
	t.Helper()
	if err := legacyFixtureRGBRecoveryIsolation(c); err != nil {
		t.Fatal(err)
	}
	if len(c.AssetFunding) != 0 || c.AssetEntropy != ([32]byte{}) || len(c.ClosingAssetProofs) != 0 || len(c.NativeParents) != 0 || len(c.NativePrevouts) != 0 {
		t.Fatal("legacy channel acquired meaningful native custody state")
	}
	for _, commit := range []*ChannelCommitment{c.LocalCommitment, c.RemoteCommitment} {
		if commit == nil {
			t.Fatal("legacy nested commitment disappeared")
		}
		if len(commit.Others) != 0 {
			t.Fatal("reserved old Others unexpectedly acquired a commitment group")
		}
		if len(commit.AssetProofs) != 0 || len(commit.RecoveryProofs) != 0 || len(commit.RecoveryFeeIndexes) != 0 {
			t.Fatal("legacy commitment acquired meaningful native proof/recovery state")
		}
		for _, other := range commit.Others {
			if other == nil || len(other.AssetProofs) != 0 || len(other.RecoveryProofs) != 0 {
				t.Fatal("legacy Other commitment changed or acquired native proofs")
			}
		}
	}
	if c.Status == CS_NATIVE_RECOVERY {
		t.Fatal("legacy channel entered native-only recovery")
	}
}
func legacyGobDatabaseSnapshot(t *testing.T, database indexer.KVDB) map[string][]byte {
	t.Helper()
	values, err := GetItemsFromDB(nil, database)
	if err != nil {
		t.Fatal(err)
	}
	return values
}
func legacyGobAssertSameDatabase(t *testing.T, database indexer.KVDB, before map[string][]byte) {
	t.Helper()
	if !reflect.DeepEqual(legacyGobDatabaseSnapshot(t, database), before) {
		t.Fatal("read/legacy native no-op unexpectedly changed persistent records")
	}
}

func TestLegacyGobChannelReadsPreserveOldFieldsAndAvoidNativeRecovery(t *testing.T) {
	for _, name := range []string{"channel_plain.gob", "channel_ordx.gob"} {
		t.Run(name, func(t *testing.T) {
			raw, entry := legacyGobReadFixture(t, name)
			database := legacyGobOpenDB(t, t.TempDir())
			defer database.Close()
			if err := database.Write([]byte(GetChannelKey(entry.ChannelID)), raw); err != nil {
				t.Fatal(err)
			}
			before := legacyGobDatabaseSnapshot(t, database)
			c, err := LoadChannelInDB(database, entry.ChannelID)
			if err != nil {
				t.Fatal(err)
			}
			legacyGobAssertSemanticOracle(t, c, entry)
			legacyGobAssertNoNativeState(t, c)
			all, err := LoadAllChannelInDBFromDB(database)
			if err != nil {
				t.Fatal(err)
			}
			if len(all) != 1 || all[entry.ChannelID] == nil {
				t.Fatal("startup channel enumeration lost the old channel")
			}
			legacyGobAssertSemanticOracle(t, all[entry.ChannelID], entry)
			manager := &Manager{db: database}
			recovered, err := manager.LoadSTPNativeRevocationRecovery(c)
			if err != nil || recovered != nil {
				t.Fatalf("ordinary old database required native recovery: recovered=%v err=%v", recovered, err)
			}
			legacyGobAssertNoNativeState(t, c)
			legacyGobAssertSameDatabase(t, database, before)
		})
	}
}

func TestLegacyGobChannelLedgerContinuesAcrossPhysicalDatabaseRestart(t *testing.T) {
	for _, name := range []string{"channel_plain.gob", "channel_ordx.gob"} {
		t.Run(name, func(t *testing.T) {
			raw, entry := legacyGobReadFixture(t, name)
			directory := t.TempDir()
			database := legacyGobOpenDB(t, directory)
			closed := false
			defer func() {
				if !closed {
					_ = database.Close()
				}
			}()
			if err := database.Write([]byte(GetChannelKey(entry.ChannelID)), raw); err != nil {
				t.Fatal(err)
			}
			loaded, err := LoadChannelInDB(database, entry.ChannelID)
			if err != nil {
				t.Fatal(err)
			}
			legacyGobAssertSemanticOracle(t, loaded, entry)
			packages := legacyGobChannelSignedPackage(t, loaded)
			channel := &Channel{ChannelInDB: *loaded}
			asset := PLAIN_ASSET
			if name == "channel_ordx.gob" {
				asset = AssetName{AssetName: indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_ORDX, Type: indexer.ASSET_TYPE_FT, Ticker: "legacy-fixture"}, N: 10}
			}
			// Exercise the actual existing ledger operations. This is not a
			// claim of peer signing, chain broadcast or full lifecycle E2E.
			if err := UpdateCommitWithUnlock(channel, true, &asset, indexer.NewDefaultDecimal(1000), true, 0); err != nil {
				t.Fatal(err)
			}
			if err := UpdateCommitWithLock(channel, true, &asset, indexer.NewDefaultDecimal(400), true, false, 0); err != nil {
				t.Fatal(err)
			}
			if channel.GetCommitLocalValue(&asset).Int64() != 39400 || channel.GetCommitRemoteValue(&asset).Int64() != 20600 || !channel.CheckConsistence() {
				t.Fatal("old-protocol ledger continuation changed amount semantics or mirrored balances")
			}
			legacyGobAssertNoNativeState(t, &channel.ChannelInDB)
			if !bytes.Equal(packages, legacyGobChannelSignedPackage(t, &channel.ChannelInDB)) {
				t.Fatal("ledger operations rewrote old pending transaction/signature bytes")
			}
			if err := SaveChannelInDB(database, &channel.ChannelInDB); err != nil {
				t.Fatal(err)
			}
			want := legacyGobSemantic(t, &channel.ChannelInDB)
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}
			closed = true
			reopened := legacyGobOpenDB(t, directory)
			defer reopened.Close()
			after, err := LoadChannelInDB(reopened, entry.ChannelID)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(want, legacyGobSemantic(t, after)) {
				t.Fatal("write-back/restart changed persisted old field semantics")
			}
			if !bytes.Equal(packages, legacyGobChannelSignedPackage(t, after)) {
				t.Fatal("write-back/restart changed old signed transaction package")
			}
			legacyGobAssertNoNativeState(t, after)
			if got, err := (&Manager{db: reopened}).LoadSTPNativeRevocationRecovery(after); err != nil || got != nil {
				t.Fatalf("restarted old channel unexpectedly needs native recovery: %v", err)
			}
		})
	}
}

func TestLegacyGobOldProtocolsDoNotRequireOrPopulateNativeFields(t *testing.T) {
	for _, name := range []string{"channel_plain.gob", "channel_ordx.gob"} {
		t.Run(name, func(t *testing.T) {
			raw, entry := legacyGobReadFixture(t, name)
			database := legacyGobOpenDB(t, t.TempDir())
			defer database.Close()
			if err := database.Write([]byte(GetChannelKey(entry.ChannelID)), raw); err != nil {
				t.Fatal(err)
			}
			loaded, err := LoadChannelInDB(database, entry.ChannelID)
			if err != nil {
				t.Fatal(err)
			}
			channel := &Channel{ChannelInDB: *loaded}
			asset := PLAIN_ASSET
			if name == "channel_ordx.gob" {
				asset = AssetName{AssetName: indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_ORDX, Type: indexer.ASSET_TYPE_FT, Ticker: "legacy-fixture"}, N: 10}
			}
			before := legacyGobSemantic(t, loaded)
			dbBefore := legacyGobDatabaseSnapshot(t, database)
			// A Manager without native persistence is intentional. These
			// genuine old protocols must return before requiring that service.
			manager := &Manager{}
			proofs, err := manager.PrepareSTPChannelFunding(channel, &asset, channel.FundingUtxos[asset])
			if err != nil || len(proofs) != 0 {
				t.Fatalf("old protocol depended on native funding persistence: proofs=%v err=%v", proofs, err)
			}
			if err := manager.AcceptSTPChannelFunding(channel, &asset, channel.FundingUtxos[asset], nil, [32]byte{}); err != nil {
				t.Fatal(err)
			}
			if needsSTPNativeCommitmentJournal(channel, channel.LocalCommitment) || needsSTPNativeOtherExchange(channel, channel.LocalCommitment) {
				t.Fatal("old commitment incorrectly requires native journal/exchange")
			}
			if nativeForceClose(&ClosingReservation{Channel: channel}) {
				t.Fatal("old channel incorrectly selected native force-close")
			}
			if err := manager.persistSTPNativeCommitment(channel, channel.LocalCommitment, "local"); err != nil {
				t.Fatalf("legacy journal no-op required native context: %v", err)
			}
			if !bytes.Equal(before, legacyGobSemantic(t, &channel.ChannelInDB)) {
				t.Fatal("legacy funding/native guard changed old fields")
			}
			legacyGobAssertNoNativeState(t, &channel.ChannelInDB)
			legacyGobAssertSameDatabase(t, database, dbBefore)
		})
	}
}

func TestLegacyGobPendingReservationsPreservePackagesAcrossRestart(t *testing.T) {
	for _, name := range []string{"closing.gob", "splicing.gob"} {
		t.Run(name, func(t *testing.T) {
			raw, entry := legacyGobReadFixture(t, name)
			directory := t.TempDir()
			database := legacyGobOpenDB(t, directory)
			closed := false
			defer func() {
				if !closed {
					_ = database.Close()
				}
			}()
			key := []byte(GetResvKey(entry.ReservationType, entry.ReservationID))
			if err := database.Write(key, raw); err != nil {
				t.Fatal(err)
			}
			before := legacyGobDatabaseSnapshot(t, database)
			loaded, err := LoadReservation(database, nil, entry.ReservationType, entry.ReservationID)
			if err != nil {
				t.Fatal(err)
			}
			legacyGobAssertSemanticOracle(t, loaded.GetStructInDB(), entry)
			legacyGobAssertReservationNativeEmpty(t, loaded)
			all, err := LoadAllResvFromDB(database, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(all) != 1 || all[entry.ReservationID] == nil {
				t.Fatal("startup reservation enumeration discarded an old pending operation")
			}
			legacyGobAssertSemanticOracle(t, all[entry.ReservationID].GetStructInDB(), entry)
			legacyGobAssertSameDatabase(t, database, before)
			packages := legacyGobReservationSignedPackage(t, loaded)
			// Advance only the existing durable monitor status. No chain
			// confirmation is invented or broadcast attempted by this test.
			if name == "closing.gob" {
				loaded.SetStatus(ResvStatus(CS_CLOSING_CONFIRMED))
			} else {
				loaded.SetStatus(RS_SPLICINGIN_CONFIRMED)
			}
			if err := SaveReservation(database, loaded); err != nil {
				t.Fatal(err)
			}
			want := legacyGobSemantic(t, loaded.GetStructInDB())
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}
			closed = true
			reopened := legacyGobOpenDB(t, directory)
			defer reopened.Close()
			after, err := LoadReservation(reopened, nil, entry.ReservationType, entry.ReservationID)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(want, legacyGobSemantic(t, after.GetStructInDB())) {
				t.Fatal("old reservation fields changed through write-back/restart")
			}
			if !bytes.Equal(packages, legacyGobReservationSignedPackage(t, after)) {
				t.Fatal("old pending raw transaction package was rebuilt or mutated")
			}
			legacyGobAssertReservationNativeEmpty(t, after)
		})
	}
}

func legacyGobAssertReservationNativeEmpty(t *testing.T, resv Reservation) {
	t.Helper()
	switch r := resv.(type) {
	case *ClosingReservation:
		if len(r.AssetProofs) != 0 {
			t.Fatal("old closing reservation acquired native proofs")
		}
	case *SplicingReservation:
		if len(r.AssetFunding) != 0 || len(r.AssetProofs) != 0 || r.AssetEntropy != ([32]byte{}) || len(r.NativeAnchorProof) != 0 {
			t.Fatal("old splicing reservation acquired native state")
		}
	default:
		t.Fatalf("unexpected old reservation type %T", resv)
	}
}
func legacyGobBitcoinBytes(t *testing.T, tx *wire.MsgTx) string {
	t.Helper()
	if tx == nil {
		return "nil"
	}
	var b bytes.Buffer
	if err := tx.Serialize(&b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b.Bytes())
}
func legacyGobSatsNetBytes(t *testing.T, tx *swire.MsgTx) string {
	t.Helper()
	if tx == nil {
		return "nil"
	}
	var b bytes.Buffer
	if err := tx.Serialize(&b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b.Bytes())
}
func legacyGobBitcoinChainBytes(t *testing.T, txs []*wire.MsgTx) []string {
	t.Helper()
	result := make([]string, len(txs))
	for i, tx := range txs {
		result[i] = legacyGobBitcoinBytes(t, tx)
	}
	return result
}
func legacyGobChannelSignedPackage(t *testing.T, c *ChannelInDB) []byte {
	t.Helper()
	result := make([]any, 0, 2)
	for _, commit := range []*ChannelCommitment{c.LocalCommitment, c.RemoteCommitment} {
		value := map[string]any{"CommitTx": legacyGobBitcoinBytes(t, commit.CommitTx), "DeAnchorTx": legacyGobSatsNetBytes(t, commit.DeAnchorTx), "PrevTxs": legacyGobBitcoinChainBytes(t, commit.PrevTxs), "NextTxs": legacyGobBitcoinChainBytes(t, commit.NextTxs), "CommitSig": commit.CommitSig, "DeAnchorSig": commit.DeAnchorSig, "Revocation": commit.Revocation}
		others := make([]any, len(commit.Others))
		for i, other := range commit.Others {
			others[i] = map[string]any{"CommitTx": legacyGobBitcoinBytes(t, other.CommitTx), "DeAnchorTx": legacyGobSatsNetBytes(t, other.DeAnchorTx), "PrevTxs": legacyGobBitcoinChainBytes(t, other.PrevTxs), "NextTxs": legacyGobBitcoinChainBytes(t, other.NextTxs), "CommitSig": other.CommitSig, "DeAnchorSig": other.DeAnchorSig}
		}
		value["Others"] = others
		result = append(result, value)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func legacyGobReservationSignedPackage(t *testing.T, resv Reservation) []byte {
	t.Helper()
	var value any
	switch r := resv.(type) {
	case *ClosingReservation:
		value = []any{legacyGobBitcoinChainBytes(t, r.PreTxs), legacyGobBitcoinBytes(t, r.CloseTx), legacyGobSatsNetBytes(t, r.DeAnchorTx)}
	case *SplicingReservation:
		value = []any{legacyGobBitcoinChainBytes(t, r.PreTxs), legacyGobBitcoinBytes(t, r.SplicingTx), legacyGobSatsNetBytes(t, r.AnchorTx)}
	default:
		t.Fatal(fmt.Sprintf("unexpected reservation %T", resv))
	}
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// Pinned from the recovered clean old blobs, not from the new fixture manifest.
var legacyGobPinnedSourceSHA256 = map[string]string{
	"sdk/wallet/channel_db.go":          "9662ea7384517f37ed040764196e66ecb2c1287ae28d23bdb4ea793ba7fe0e0d",
	"sdk/wallet/channel_commitment.go":  "7604e364e888e1b12cb0fad49e88c5f70fc808cd38fcd0550defd36152769863",
	"sdk/wallet/channel_config.go":      "3ea1e80a2b8814da1f01aaf54114fe01db5914f2563e3a05537b0a432ee7bf8b",
	"sdk/wallet/channel_reservation.go": "fe6c439a943d3d0735a9142d1c56a1c829ee1fb6ba756b12e9781dc2de4f90a5",
	"sdk/wallet/channel_merkle.go":      "1545ea2e1fb95da395f8fe9500e2c715b5f13014746f4f1e01a1372e0036f13a",
	"sdk/wallet/db.go":                  "60f1b6932770a3c648d3f5ccf55760989275feabce252b7c4677a96708c16558",
	"sdk/wallet/types.go":               "dcbbb6509cd069a115435d5e972dd0561ebeb8d5f7048cc95a662711d21d197a",
}
