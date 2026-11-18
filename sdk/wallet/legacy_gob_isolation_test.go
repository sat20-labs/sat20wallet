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
	// The pinned standalone generator used the default mainnet bootstrap key.
	// Other E2E constructors may have selected testnet in the same Go process.
	previousChain, previousTesting := indexer.CHAIN, indexer.ENABLE_TESTING
	indexer.CHAIN, indexer.ENABLE_TESTING = "mainnet", false
	t.Cleanup(func() { indexer.CHAIN, indexer.ENABLE_TESTING = previousChain, previousTesting })
	root := filepath.Join("testdata", "legacy_channel")
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
			if legacyFixtureRGB11Field(typ.Name(), name) {
				t.Fatalf("supposed old schema already contains %s.%s", typ.Name(), name)
			}
			current, exists := typ.FieldByName(name)
			if !exists || current.Type.String() != oldType {
				t.Fatalf("old field changed: %s.%s was %s, now %+v", typ.Name(), name, oldType, current)
			}
		}
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			if f.PkgPath == "" && !legacyFixtureRGB11Field(typ.Name(), f.Name) {
				if _, ok := fields[f.Name]; !ok {
					t.Fatalf("unreviewed legacy field %s.%s", typ.Name(), f.Name)
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
	want, err := os.ReadFile(filepath.Join("testdata", "legacy_channel", entry.SemanticFile))
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
func legacyGobAssertNoRGB11State(t *testing.T, c *ChannelInDB) {
	t.Helper()
	if err := legacyFixtureRGBRecoveryIsolation(c); err != nil {
		t.Fatal(err)
	}
	if len(c.AssetFunding) != 0 || c.AssetEntropy != ([32]byte{}) || len(c.ClosingAssetProofs) != 0 || len(c.FundingParents) != 0 || len(c.FundingPrevouts) != 0 {
		t.Fatal("legacy channel acquired meaningful proof-based custody state")
	}
	for _, commit := range []*ChannelCommitment{c.LocalCommitment, c.RemoteCommitment} {
		if commit == nil {
			t.Fatal("legacy nested commitment disappeared")
		}
		if len(commit.Others) != 0 {
			t.Fatal("reserved old Others unexpectedly acquired a commitment group")
		}
		if len(commit.AssetProofs) != 0 || len(commit.RecoveryProofs) != 0 || len(commit.RecoveryFeeIndexes) != 0 {
			t.Fatal("legacy commitment acquired meaningful asset proof/recovery state")
		}
		for _, other := range commit.Others {
			if other == nil || len(other.AssetProofs) != 0 || len(other.RecoveryProofs) != 0 {
				t.Fatal("legacy Other commitment changed or acquired asset proofs")
			}
		}
	}
	if c.Status == CS_COMMIT_RECOVERY {
		t.Fatal("legacy channel entered commitment recovery")
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
		t.Fatal("read/legacy funding inspection unexpectedly changed persistent records")
	}
}

func TestLegacyChannelState(t *testing.T) {
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
			legacyGobAssertNoRGB11State(t, c)
			all, err := LoadAllChannelInDBFromDB(database)
			if err != nil {
				t.Fatal(err)
			}
			if len(all) != 1 || all[entry.ChannelID] == nil {
				t.Fatal("startup channel enumeration lost the old channel")
			}
			legacyGobAssertSemanticOracle(t, all[entry.ChannelID], entry)
			manager := &Manager{db: database}
			recovered, err := manager.LoadSTPRevocationRecovery(c)
			if err != nil || recovered != nil {
				t.Fatalf("ordinary old database required commitment recovery: recovered=%v err=%v", recovered, err)
			}
			legacyGobAssertNoRGB11State(t, c)
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
			legacyGobAssertNoRGB11State(t, &channel.ChannelInDB)
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
			legacyGobAssertNoRGB11State(t, after)
			if got, err := (&Manager{db: reopened}).LoadSTPRevocationRecovery(after); err != nil || got != nil {
				t.Fatalf("restarted old channel unexpectedly needs commitment recovery: %v", err)
			}
		})
	}
}

func TestLegacyReservationFields(t *testing.T) {
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
			// A Manager without proof persistence is intentional. These
			// genuine old protocols must return before requiring that service.
			manager := &Manager{}
			proofs, err := manager.PrepareSTPChannelFunding(channel, &asset, channel.FundingUtxos[asset])
			if err != nil || len(proofs) != 0 {
				t.Fatalf("old protocol depended on proof-backed funding persistence: proofs=%v err=%v", proofs, err)
			}
			if err := manager.AcceptSTPChannelFunding(channel, &asset, channel.FundingUtxos[asset], nil, [32]byte{}); err != nil {
				t.Fatal(err)
			}
			if needsProofJournal(channel, channel.LocalCommitment) || needsRecoveryExchange(channel, channel.LocalCommitment) != (len(channel.LocalCommitment.Others) != 0) {
				t.Fatal("old commitment incorrectly requires proof journal or unexpected recovery exchange")
			}
			if needsWithdrawalFacts(channel) {
				t.Fatal("old channel incorrectly requires extra withdrawal evidence")
			}
			if err := manager.persistSTPCommitment(channel, channel.RemoteCommitment, "remote"); err != nil {
				t.Fatalf("unexpected proof journal for an ordinary peer commitment: %v", err)
			}
			if err := manager.persistSTPCommitment(channel, channel.LocalCommitment, "local"); err == nil {
				t.Fatal("accepted a local signing obligation without durable storage")
			}
			if !bytes.Equal(before, legacyGobSemantic(t, &channel.ChannelInDB)) {
				t.Fatal("funding checks changed old channel fields")
			}
			legacyGobAssertNoRGB11State(t, &channel.ChannelInDB)
			legacyGobAssertSameDatabase(t, database, dbBefore)
			manager.db = database
			if err := manager.persistSTPCommitment(channel, channel.LocalCommitment, "local"); err != nil {
				t.Fatal(err)
			}
			if err := manager.verifyStoredCommitment(channel, channel.LocalCommitment); err != nil {
				t.Fatal(err)
			}
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
			assertLegacyReservationFields(t, loaded)
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
			assertLegacyReservationFields(t, after)
		})
	}
}

func assertLegacyReservationFields(t *testing.T, resv Reservation) {
	t.Helper()
	switch r := resv.(type) {
	case *ClosingReservation:
		if len(r.AssetProofs) != 0 {
			t.Fatal("old closing reservation acquired asset proofs")
		}
	case *SplicingReservation:
		if len(r.AssetFunding) != 0 || len(r.AssetProofs) != 0 || r.AssetEntropy != ([32]byte{}) || len(r.RGB11AnchorProof) != 0 {
			t.Fatal("old splicing reservation acquired asset proof or funding state")
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
