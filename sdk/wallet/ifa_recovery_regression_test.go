package wallet

import (
	"context"
	"testing"

	"github.com/btcsuite/btcd/chaincfg"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

func TestIFARecoveryPreservesControlAssignments(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	defer func() { _chain = oldChain }()
	chain := newCoreE2EChain()
	signingWallet := NewInternalWalletWithMnemonic(coreE2ESenderMnemonic, "", &chaincfg.TestNet4Params)
	chain.fund(t, signingWallet.GetAddress(), 4)
	indexer := &coreE2EL1Indexer{chain: chain}
	source := newRGB11FlowManager(t, signingWallet, indexer, chain, 900)
	issued, err := source.IssueRGB11Asset(context.Background(), RGB11IssueRequest{
		Schema: "IFA", Ticker: "IFAR", Name: "IFA restore control", Amounts: []uint64{1000}, InflationAmounts: []uint64{9000},
	})
	if err != nil { t.Fatal(err) }
	if issued.Projected != 2 { t.Fatal("fixture must contain both ownership and inflation-right allocations") }
	walletID, err := source.RGB11WalletID()
	if err != nil { t.Fatal(err) }
	full, _, err := source.rgbManager.exportRGB11WalletSnapshot(walletID)
	if err != nil { t.Fatal(err) }
	recovery, err := rgb11wallet.RecoveryPackageFromSnapshot(full, 0)
	if err != nil { t.Fatal(err) }
	snapshot, err := recovery.WalletSnapshot()
	if err != nil { t.Fatal(err) }
	// Start without any copied DB, ticker cache or contract metadata.
	target := newRGB11FlowManager(t,
		NewInternalWalletWithMnemonic(coreE2ESenderMnemonic, "", &chaincfg.TestNet4Params), indexer, chain, 901)
	if err := target.rgbManager.importRGB11WalletSnapshot(snapshot); err != nil {
		t.Fatalf("valid IFA ownership plus inflation rights cannot restore to an empty SDK: %v", err)
	}
	if err := target.rgbManager.rebuildRGB11Locks(); err != nil { t.Fatal(err) }
	balance, err := target.GetRGB11AssetBalance(&issued.AssetName)
	if err != nil || balance == nil || balance.Value.Uint64() != 1000 {
		t.Fatal("IFA control rights must not increase the transferable balance")
	}
	state, err := target.GetRGB11State()
	if err != nil { t.Fatal(err) }
	controls := 0
	for _, proof := range state.Proofs {
		if proof.AssetName.Type == "control" {
			controls++
			if !target.GetUtxoLocker().IsLocked(proof.OutPoint) { t.Fatal("restored inflation-right carrier is not protected") }
		}
	}
	if controls != 1 || len(state.Proofs) != 2 { t.Fatal("restoration omitted IFA control rights or duplicated ownership") }
	exported, err := target.ExportRGB11Contract(issued.ContractID)
	if err != nil || exported == nil || exported.ContractID != issued.ContractID { t.Fatal("recovered IFA cannot export its canonical contract") }
}
