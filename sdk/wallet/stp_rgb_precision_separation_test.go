package wallet

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	coreissuance "github.com/sat20-labs/rgb11/issuance"
	"github.com/sat20-labs/rgb11/schemas"
)

// These are isolated metadata, quantity and persistence regression tests. They
// do not assert ownership, invent indexer balances or replace native proofs.
func precisionSeparationAsset() indexer.AssetName {
	return indexer.AssetName{Protocol: "rgb11", Type: indexer.ASSET_TYPE_FT, Ticker: strings.Repeat("a", 64)}
}

func TestRGBPrecisionSeparationAssetNames(t *testing.T) {
	for _, precision := range []int{0, 2, 8} {
		t.Run(fmt.Sprint(precision), func(t *testing.T) {
			name := precisionSeparationAsset()
			ticker := &indexer.TickerInfo{AssetName: name, N: 0, Divisibility: precision}
			info := &indexer.AssetInfo{Name: name, Amount: *indexer.NewDecimal(123, precision), BindingSat: 0}
			for source, got := range map[string]*AssetName{"ticker": GetAssetName(ticker), "quantity": GetAssetName2(info)} {
				if got.AssetName != name || got.N != 0 {
					t.Errorf("%s: RGB N must remain binding 0, got %+v", source, got)
				}
			}
			if ticker.Divisibility != precision || info.Amount.Precision != precision {
				t.Fatal("constructing an identity changed explicit quantity precision")
			}
		})
	}
	legacy := &indexer.TickerInfo{AssetName: indexer.AssetName{Protocol: "ordx", Type: "f", Ticker: "token"}, N: 100, Divisibility: 0}
	if got := GetAssetName(legacy); got.N != 100 || STPAssetBindingSat(got) != 100 {
		t.Fatalf("legacy binding changed: %+v", got)
	}
}

func TestRGBPrecisionSeparationInputAndSplit(t *testing.T) {
	name := &AssetName{AssetName: precisionSeparationAsset(), N: 0}
	input := indexer.NewTxOutput(1000)
	input.Assets = indexer.TxAssets{{Name: name.AssetName, Amount: *indexer.NewDecimal(123, 2), BindingSat: 0}}
	t.Run("input", func(t *testing.T) {
		if err := (rgb11STPProtocol{}).VerifyInput(name, input); err != nil {
			t.Fatalf("valid two-decimal allocation with binding N=0 rejected: %v", err)
		}
	})
	t.Run("split", func(t *testing.T) {
		amount := indexer.NewDecimal(20, 2)
		output, change, err := (rgb11STPProtocol{}).SplitOutputs([]*TxOutput{input}, name, 330, amount)
		if err != nil {
			t.Fatalf("quantity precision must not be read from binding N: %v", err)
		}
		if output.Assets[0].BindingSat != 0 || output.Assets[0].Amount.Precision != 2 ||
			output.Assets[0].Amount.Cmp(amount) != 0 || change == nil ||
			change.Assets[0].Amount.Precision != 2 || change.Assets[0].Amount.Cmp(input.GetAsset(&name.AssetName).Sub(amount)) != 0 {
			t.Fatal("split changed quantity, explicit precision or binding")
		}
	})
	t.Run("reject-mixed-quantity-precision", func(t *testing.T) {
		other := input.Clone()
		other.Assets[0].Amount = *indexer.NewDecimal(123, 1)
		if _, _, err := (rgb11STPProtocol{}).SplitOutputs([]*TxOutput{other}, name, 330, indexer.NewDecimal(20, 2)); err == nil {
			t.Fatal("split silently reinterpreted a different native quantity precision")
		}
	})
}

type precisionSeparationContractManager struct {
	ContractManager
	database indexer.KVDB
}

func (m *precisionSeparationContractManager) GetDB() indexer.KVDB { return m.database }

func precisionSeparationRuntime(t *testing.T) *LaunchPoolContractRunTime {
	t.Helper()
	db := NewKVDB(t.TempDir())
	if db == nil {
		t.Fatal("NewKVDB failed")
	}
	t.Cleanup(func() { _ = db.Close() })
	p := NewLaunchPoolContractRuntime(&precisionSeparationContractManager{database: db})
	p.ChannelAddr = "precision-separation"
	p.AssetName = precisionSeparationAsset()
	return p
}

// This fixture obtains metadata and allocations through actual core genesis
// issuance and the normal consensus-validating import. It never adds a balance
// to an indexer response. The only synthetic evidence is a local Bitcoin UTXO.
func precisionSeparationValidatedSource(t *testing.T, precision int) *TxOutput {
	t.Helper()
	previous := _chain
	_chain = "testnet4"
	t.Cleanup(func() { _chain = previous })
	manager, _, imported, _, rpc := newRGB11ValidContractSpendFixture(t)
	point := rgb11GenericFixtureSource(t, manager, imported)
	if precision != 0 {
		for candidate := range rpc.outputs {
			if candidate != point {
				point = candidate
				break
			}
		}
		allocations, err := rgb11IssueAllocations([]string{point}, []uint64{1000})
		if err != nil {
			t.Fatal(err)
		}
		issued, err := coreissuance.Issue(coreissuance.Spec{
			Kind: schemas.NIA, Network: rgb11IssuanceNetwork(GetChainParam()),
			Ticker: "PRECISN", Name: "Precision separation", Precision: uint8(precision), Allocations: allocations,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := manager.rgbManager.ImportRGB11Contract(context.Background(), []byte(issued.Armor)); err != nil {
			t.Fatal(err)
		}
	}
	output, err := manager.rgbManager.projectionStore.LoadOutput(point)
	if err != nil {
		t.Fatal(err)
	}
	if len(output.Assets) != 1 || output.Assets[0].BindingSat != 0 || output.Assets[0].Amount.Precision != precision {
		t.Fatalf("validated genesis source has unexpected metadata: %+v", output.Assets)
	}
	return output
}

func TestRGBPrecisionSeparationMetadataRestore(t *testing.T) {
	for _, precision := range []int{0, 2} {
		t.Run(fmt.Sprint(precision), func(t *testing.T) {
			p := precisionSeparationRuntime(t)
			output := precisionSeparationValidatedSource(t, precision)
			p.AssetName = output.Assets[0].Name
			p.RGBPrecision = precision
			if err := p.initializeNativePublicMetadata(output); err != nil {
				t.Fatal(err)
			}
			raw, err := p.db.Read(p.nativePublicMetadataKey())
			if err != nil {
				t.Fatal(err)
			}
			if p.N != 0 || p.Divisibility != precision || !p.nativeMetadataInitialized || p.GetAssetNameV2().N != 0 {
				t.Fatalf("new RGB metadata initialization mixed binding and precision: N=%d precision=%d known=%t", p.N, p.Divisibility, p.nativeMetadataInitialized)
			}
			oldRoot := CalcContractRuntimeBaseMerkleRoot(&p.ContractRuntimeBase)
			encoded, err := EncodeToBytes(&p.LaunchPoolContractRunTimeInDB)
			if err != nil {
				t.Fatal(err)
			}
			var restored LaunchPoolContractRunTime
			if err := DecodeFromBytes(encoded, &restored.LaunchPoolContractRunTimeInDB); err != nil {
				t.Fatal(err)
			}
			restored.init()
			restored.db = p.db
			if restored.nativeMetadataInitialized {
				t.Fatal("gob unexpectedly persisted the transient known-precision marker")
			}
			if err := restored.restoreNativePublicMetadata(); err != nil {
				t.Fatal(err)
			}
			p = &restored
			if p.N != 0 || p.Divisibility != precision || !p.nativeMetadataInitialized || p.GetAssetNameV2().N != 0 {
				t.Errorf("restored RGB binding/precision/known state is wrong: N=%d precision=%d known=%t", p.N, p.Divisibility, p.nativeMetadataInitialized)
			}
			if !bytes.Equal(oldRoot, CalcContractRuntimeBaseMerkleRoot(&p.ContractRuntimeBase)) {
				t.Error("new RGB metadata restore changed the authenticated runtime root")
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

func TestRGBPrecisionSeparationNewGobLookup(t *testing.T) {
	created := AssetName{AssetName: precisionSeparationAsset(), N: 0}
	canonical := AssetName{AssetName: created.AssetName, N: 0}
	legacy := AssetName{AssetName: indexer.AssetName{Protocol: "ordx", Type: "f", Ticker: "token"}, N: 100}
	output := indexer.NewTxOutput(1000)
	output.Assets = indexer.TxAssets{{Name: created.AssetName, Amount: *indexer.NewDecimal(123, 2), BindingSat: 0}}
	channel := NewChannelInDB()
	channel.Status = CS_CLOSED
	channel.ChannelId = "new-rgb-precision"
	channel.FundingUtxos[created] = []*TxOutput{output}
	channel.LocalCommitment, channel.RemoteCommitment = NewChannelCommitment(), NewChannelCommitment()
	channel.LocalCommitment.LocalBalance[created] = indexer.NewDecimal(100, 2)
	channel.LocalCommitment.RemoteBalance[created] = indexer.NewDecimal(23, 2)
	channel.RemoteCommitment.RemoteBalance[created] = indexer.NewDecimal(100, 2)
	channel.RemoteCommitment.LocalBalance[created] = indexer.NewDecimal(23, 2)
	channel.LocalCommitment.LocalBalance[legacy] = indexer.NewDefaultDecimal(7)
	channel.LocalCommitment.CommitTx = wire.NewMsgTx(2)
	channel.LocalCommitment.CommitTx.LockTime = 42
	var txBefore bytes.Buffer
	if err := channel.LocalCommitment.CommitTx.Serialize(&txBefore); err != nil {
		t.Fatal(err)
	}
	// Use ordinary gob with the supported new RGB identity; no normalizer
	// or compatibility decoder participates in the round trip.
	var encoded bytes.Buffer
	if err := gob.NewEncoder(&encoded).Encode(channel); err != nil {
		t.Fatal(err)
	}
	var restored ChannelInDB
	if err := DecodeFromBytes(encoded.Bytes(), &restored); err != nil {
		t.Fatal(err)
	}
	if got := restored.GetFundingOutputs(&canonical); len(got) != 1 || got[0].OutValue.Value != 1000 {
		t.Errorf("new RGB identity lost funding after gob round trip: %d", len(got))
	}
	if got := restored.GetCommitLocalValue(&canonical); got == nil || got.Cmp(indexer.NewDecimal(100, 2)) != 0 {
		t.Errorf("new RGB identity lost local balance after gob round trip: %v", got)
	}
	if got := restored.GetCommitRemoteValue(&canonical); got == nil || got.Cmp(indexer.NewDecimal(23, 2)) != 0 {
		t.Errorf("new RGB identity lost remote balance after gob round trip: %v", got)
	}
	if _, ok := restored.FundingUtxos[created]; !ok {
		t.Error("new RGB gob round trip lost its binding-correct key")
	}
	if len(restored.FundingUtxos) != 1 {
		t.Error("new RGB gob round trip inserted a second identity")
	}
	wrongLegacy := legacy
	wrongLegacy.N = 0
	if restored.GetCommitLocalValue(&wrongLegacy).Sign() != 0 {
		t.Error("RGB key handling changed legacy N identity")
	}
	var txAfter bytes.Buffer
	if err := restored.LocalCommitment.CommitTx.Serialize(&txAfter); err != nil || !bytes.Equal(txBefore.Bytes(), txAfter.Bytes()) {
		t.Fatalf("new RGB gob round trip changed retained transaction bytes: %v", err)
	}
}

func TestRGBPrecisionSeparationLegacyAssetInfoBinding(t *testing.T) {
	old := AssetName{AssetName: precisionSeparationAsset(), N: 2}
	got := newAssetInfo(&old, indexer.NewDecimal(123, 2))
	if got.BindingSat != 0 || got.Amount.Precision != 2 {
		t.Fatalf("historical RGB key leaked precision into BindingSat: binding=%d precision=%d", got.BindingSat, got.Amount.Precision)
	}
	legacy := AssetName{AssetName: indexer.AssetName{Protocol: "ordx", Type: "f", Ticker: "token"}, N: 100}
	if got := newAssetInfo(&legacy, indexer.NewDefaultDecimal(7)); got.BindingSat != 100 {
		t.Fatalf("legacy binding changed: %d", got.BindingSat)
	}
}
