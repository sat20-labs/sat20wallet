package wallet

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"
)

// Reflection lets this independent contract test compile before the approved
// RGB-only field exists. The baseline must fail for the missing V1 field, not
// for a compiler error. It does not invent valid RGB consensus evidence.
func rgbRecoveryV1Slot(t *testing.T, value *ChannelCommitmentV1) reflect.Value {
	t.Helper()
	typ := reflect.TypeOf(ChannelCommitmentV1{})
	field, ok := typ.FieldByName("RGBClientRecovery")
	if !ok || len(field.Index) != 1 {
		t.Fatal("ChannelCommitmentV1 must directly own the optional RGBClientRecovery field")
	}
	if field.Type != reflect.TypeOf((*CommitmentTx)(nil)) {
		t.Fatalf("RGBClientRecovery must be one *CommitmentTx, got %s", field.Type)
	}
	return reflect.ValueOf(value).Elem().FieldByIndex(field.Index)
}

func TestSTPRGBClientRecoverySlotSchema(t *testing.T) {
	value := NewChannelCommitment()
	if !rgbRecoveryV1Slot(t, &value.ChannelCommitmentV1).IsNil() {
		t.Fatal("constructing an ordinary commitment populated RGB-only recovery state")
	}
	field, ok := reflect.TypeOf(ChannelCommitment{}).FieldByName("Others")
	if !ok || field.Type != reflect.TypeOf([]*CommitmentTx(nil)) {
		t.Fatal("the reserved alternative-commitment container changed")
	}
}

func TestSTPRGBClientRecoverySlotNilForRealLegacyGob(t *testing.T) {
	root := filepath.Join("testdata", "legacy_rgb_slot")
	read := func(name string) []byte {
		raw, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	digest := func(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }
	var manifest struct {
		Format                        int
		WalletCommit, GeneratorSHA256 string
		SourceSHA256                  map[string]string
		Fixtures                      []struct{ File, SHA256 string }
	}
	if err := json.Unmarshal(read("manifest.json"), &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Format != 1 || manifest.WalletCommit != "1a9aa9d67d741ba66556982fa53b1b81f4d41559" ||
		manifest.SourceSHA256["sdk/wallet/channel_commitment.go"] != "7604e364e888e1b12cb0fad49e88c5f70fc808cd38fcd0550defd36152769863" ||
		digest(read(filepath.Join("generator", "main.go"))) != manifest.GeneratorSHA256 {
		t.Fatal("genuine pinned old gob provenance changed")
	}
	for _, name := range []string{"channel_plain.gob", "channel_ordx.gob"} {
		t.Run(name, func(t *testing.T) {
			raw := read(name)
			want := ""
			for _, entry := range manifest.Fixtures {
				if entry.File == name {
					want = entry.SHA256
				}
			}
			if want == "" || digest(raw) != want {
				t.Fatal("genuine old gob bytes changed")
			}
			var stored ChannelInDB
			if err := DecodeFromBytes(raw, &stored); err != nil {
				t.Fatal(err)
			}
			for _, commit := range []*ChannelCommitment{stored.LocalCommitment, stored.RemoteCommitment} {
				if commit == nil {
					t.Fatal("old nested commitment disappeared")
				}
				if !rgbRecoveryV1Slot(t, &commit.ChannelCommitmentV1).IsNil() {
					t.Fatal("an actual pre-RGB record acquired RGB recovery state")
				}
			}
		})
	}
}

// Opaque sidecar bytes are intentional here: this is a serialization isolation
// test, not a native proof or broadcast authorization test. Real 2-of-2 Bitcoin
// bytes come from the independently checked ordinary diagnostic fixture.
func TestSTPRGBRecoverySlotRoundTripPreservesIndependentPackages(t *testing.T) {
	f := newReservedOthersFixture(t)
	original := f.channel.LocalCommitment
	copy := *original
	otherRoot := wire.OutPoint{Hash: chainhash.Hash{119}}
	other := f.spend(t, otherRoot, wire.NewTxOut(80_000, f.script), 70_000)
	copy.Others = []*CommitmentTx{{CommitTx: other}}
	parent := f.expected[1]
	child := f.spend(t, wire.OutPoint{Hash: parent.TxHash(), Index: 1}, parent.TxOut[1], 40_000)
	recovery := &CommitmentTx{CommitTx: child, AssetProofs: map[string][]byte{"rgb11": []byte("opaque proof bytes for round-trip only")}}
	rgbRecoveryV1Slot(t, &copy.ChannelCommitmentV1).Set(reflect.ValueOf(recovery))
	raw, err := EncodeToBytes(&copy)
	if err != nil {
		t.Fatal(err)
	}
	var restored ChannelCommitment
	if err := DecodeFromBytes(raw, &restored); err != nil {
		t.Fatal(err)
	}
	got := rgbRecoveryV1Slot(t, &restored.ChannelCommitmentV1).Interface().(*CommitmentTx)
	if got == nil || !equalSTPBitcoinChain([]*wire.MsgTx{got.CommitTx}, []*wire.MsgTx{recovery.CommitTx}) || !bytes.Equal(got.AssetProofs["rgb11"], recovery.AssetProofs["rgb11"]) {
		t.Fatal("RGB recovery transaction witness or its independent proof was changed")
	}
	if !equalSTPBitcoinChain(restored.PrevTxs, original.PrevTxs) || !equalSTPBitcoinChain(restored.NextTxs, original.NextTxs) ||
		len(restored.Others) != 1 || !equalSTPBitcoinChain([]*wire.MsgTx{restored.Others[0].CommitTx}, []*wire.MsgTx{copy.Others[0].CommitTx}) {
		t.Fatal("the RGB-only slot replaced or reclassified PrevTxs, NextTxs, or reserved Others")
	}
}
