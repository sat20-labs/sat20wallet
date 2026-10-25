package wallet

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"testing"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
)

// Research-only diagnostics, deliberately not registered as Test functions.
// The user confirmed that production has no RGB channel/contract records.
// Earlier experimental records with precision stored in N are not supported
// by this new-creation correction. Their original failing requirements are
// retained below; no key migration or historical Merkle fallback is installed.
// Luna's pre-change red evidence remains independently preserved.

func diagnoseUnsupportedHistoricalRGBMetadataRestore(t *testing.T) {
	for _, precision := range []int{0, 2} {
		t.Run(fmt.Sprint(precision), func(t *testing.T) {
			p := precisionSeparationRuntime(t)
			output := precisionSeparationValidatedSource(t, precision)
			p.AssetName = output.Assets[0].Name
			if err := p.initializeNativePublicMetadata(output); err != nil {
				t.Fatal(err)
			}
			raw, err := p.db.Read(p.nativePublicMetadataKey())
			if err != nil {
				t.Fatal(err)
			}
			// Reconstruct the old on-disk runtime interpretation without changing
			// the metadata record. Loading must preserve its authenticated root.
			p.N, p.Divisibility = precision, precision
			p.nativeMetadataInitialized = false
			oldRoot := CalcContractRuntimeBaseMerkleRoot(&p.ContractRuntimeBase)
			if err := p.restoreNativePublicMetadata(); err != nil {
				t.Fatal(err)
			}
			if p.N != 0 || p.Divisibility != precision || !p.nativeMetadataInitialized || p.GetAssetNameV2().N != 0 {
				t.Errorf("restored RGB binding/precision/known state is wrong: N=%d precision=%d known=%t", p.N, p.Divisibility, p.nativeMetadataInitialized)
			}
			if !bytes.Equal(oldRoot, CalcContractRuntimeBaseMerkleRoot(&p.ContractRuntimeBase)) {
				t.Error("reading historical RGB metadata changed the authenticated runtime root")
			}
			after, err := p.db.Read(p.nativePublicMetadataKey())
			if err != nil || !bytes.Equal(raw, after) {
				t.Fatalf("metadata read rewrote the persisted record: %v", err)
			}
			if err := p.applyNativePublicMetadata(&nativePublicMetadata{Precision: precision + 1}); err == nil {
				t.Error("known precision, including true zero, was treated as unknown")
			}
		})
	}
	t.Run("unknown-stays-unknown", func(t *testing.T) {
		p := precisionSeparationRuntime(t)
		if err := p.restoreNativePublicMetadata(); err != nil {
			t.Fatal(err)
		}
		if p.nativeMetadataInitialized {
			t.Fatal("missing metadata was invented as known zero precision")
		}
	})
	t.Run("legacy-does-not-read-native-record", func(t *testing.T) {
		p := precisionSeparationRuntime(t)
		p.AssetName = indexer.AssetName{Protocol: "ordx", Type: "f", Ticker: "token"}
		p.N, p.Divisibility = 100, 0
		if err := p.db.Write(p.nativePublicMetadataKey(), []byte("not a native record")); err != nil {
			t.Fatal(err)
		}
		before := CalcContractRuntimeBaseMerkleRoot(&p.ContractRuntimeBase)
		if err := p.restoreNativePublicMetadata(); err != nil {
			t.Fatalf("legacy protocol read native metadata: %v", err)
		}
		if p.N != 100 || p.Divisibility != 0 || !bytes.Equal(before, CalcContractRuntimeBaseMerkleRoot(&p.ContractRuntimeBase)) {
			t.Fatal("native metadata handling changed legacy runtime state or hash")
		}
	})
}

func diagnoseUnsupportedHistoricalRGBGobLookup(t *testing.T) {
	historical := AssetName{AssetName: precisionSeparationAsset(), N: 2}
	canonical := AssetName{AssetName: historical.AssetName, N: 0}
	legacy := AssetName{AssetName: indexer.AssetName{Protocol: "ordx", Type: "f", Ticker: "token"}, N: 100}
	output := indexer.NewTxOutput(1000)
	output.Assets = indexer.TxAssets{{Name: historical.AssetName, Amount: *indexer.NewDecimal(123, 2), BindingSat: 0}}
	channel := NewChannelInDB()
	channel.Status = CS_CLOSED
	channel.ChannelId = "historical-rgb-precision"
	channel.FundingUtxos[historical] = []*TxOutput{output}
	channel.LocalCommitment, channel.RemoteCommitment = NewChannelCommitment(), NewChannelCommitment()
	channel.LocalCommitment.LocalBalance[historical] = indexer.NewDecimal(100, 2)
	channel.LocalCommitment.RemoteBalance[historical] = indexer.NewDecimal(23, 2)
	channel.RemoteCommitment.RemoteBalance[historical] = indexer.NewDecimal(100, 2)
	channel.RemoteCommitment.LocalBalance[historical] = indexer.NewDecimal(23, 2)
	channel.LocalCommitment.LocalBalance[legacy] = indexer.NewDefaultDecimal(7)
	channel.LocalCommitment.CommitTx = wire.NewMsgTx(2)
	channel.LocalCommitment.CommitTx.LockTime = 42
	var txBefore bytes.Buffer
	if err := channel.LocalCommitment.CommitTx.Serialize(&txBefore); err != nil {
		t.Fatal(err)
	}
	// Use the ordinary gob encoder directly to model the exact historical
	// structure, not a new producer or a helper that can normalize it first.
	var encoded bytes.Buffer
	if err := gob.NewEncoder(&encoded).Encode(channel); err != nil {
		t.Fatal(err)
	}
	var restored ChannelInDB
	if err := DecodeFromBytes(encoded.Bytes(), &restored); err != nil {
		t.Fatal(err)
	}
	if got := restored.GetFundingOutputs(&canonical); len(got) != 1 || got[0].OutValue.Value != 1000 {
		t.Errorf("binding-correct RGB identity lost historical precision-keyed funding: %d", len(got))
	}
	if got := restored.GetCommitLocalValue(&canonical); got == nil || got.Cmp(indexer.NewDecimal(100, 2)) != 0 {
		t.Errorf("binding-correct RGB identity lost historical local balance: %v", got)
	}
	if got := restored.GetCommitRemoteValue(&canonical); got == nil || got.Cmp(indexer.NewDecimal(23, 2)) != 0 {
		t.Errorf("binding-correct RGB identity lost historical remote balance: %v", got)
	}
	if _, ok := restored.FundingUtxos[historical]; !ok {
		t.Error("reading a historical record rewrote its stored RGB key")
	}
	if _, ok := restored.FundingUtxos[canonical]; ok {
		t.Error("reading a historical record inserted a second identity")
	}
	wrongLegacy := legacy
	wrongLegacy.N = 0
	if restored.GetCommitLocalValue(&wrongLegacy).Sign() != 0 {
		t.Error("RGB historical-key handling changed legacy N identity")
	}
	var txAfter bytes.Buffer
	if err := restored.LocalCommitment.CommitTx.Serialize(&txAfter); err != nil || !bytes.Equal(txBefore.Bytes(), txAfter.Bytes()) {
		t.Fatalf("historical lookup changed retained transaction bytes: %v", err)
	}
}
