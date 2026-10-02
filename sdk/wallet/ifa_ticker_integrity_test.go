package wallet

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/chaincfg"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

func TestIFATickerObjectTamperingRejected(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	chain := newCoreE2EChain()
	wallet := NewInternalWalletWithMnemonic(coreE2ESenderMnemonic, "", &chaincfg.TestNet4Params)
	chain.fund(t, wallet.GetAddress(), 4)
	manager := newRGB11FlowManager(t, wallet, &coreE2EL1Indexer{chain: chain}, chain, 910)
	issued, err := manager.IssueRGB11Asset(context.Background(), RGB11IssueRequest{
		Schema: "IFA", Ticker: "IFAC", Name: "IFA object integrity", Amounts: []uint64{100}, InflationAmounts: []uint64{900},
	})
	if err != nil { t.Fatal(err) }
	id, err := manager.RGB11WalletID()
	if err != nil { t.Fatal(err) }
	snapshot, _, err := manager.rgbManager.exportRGB11WalletSnapshot(id)
	if err != nil { t.Fatal(err) }
	ref := rgb11wallet.SnapshotTickerRef{ContractID: issued.ContractID, AssetName: issued.AssetName.String()}
	if raw, _, err := rgb11wallet.ContractObjectForTickerRef(snapshot.ProjectionRecords, ref); err != nil || len(raw) == 0 {
		t.Fatalf("valid mixed IFA receipt was rejected: %v", err)
	}
	for _, variant := range []string{"missing_object", "changed_object"} {
		t.Run(variant, func(t *testing.T) {
			var records []rgb11wallet.SnapshotRecord
			for _, record := range snapshot.ProjectionRecords {
				copy := rgb11wallet.SnapshotRecord{Key: record.Key, Value: append([]byte(nil), record.Value...)}
				if strings.HasPrefix(copy.Key, "object-") {
					if variant == "missing_object" { continue }
					copy.Value[0] ^= 1
				}
				records = append(records, copy)
			}
			if _, _, err := rgb11wallet.ContractObjectForTickerRef(records, ref); !errors.Is(err, rgb11wallet.ErrValidationReceipt) {
				t.Fatalf("ticker lookup accepted %s instead of verifying referenced object hash: %v", variant, err)
			}
		})
	}
}
