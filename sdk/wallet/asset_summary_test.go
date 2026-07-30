package wallet

import (
	"context"
	"fmt"
	"testing"

	"github.com/btcsuite/btcd/chaincfg"
	indexer "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

type rgb11SummaryIndexer struct {
	*rgb11FlowIndexer
	summaries map[string]*indexerwire.AssetSummary
}

func (s *rgb11SummaryIndexer) GetAssetSummaryWithAddress(address string) *indexerwire.AssetSummary {
	return cloneAssetSummary(s.summaries[address])
}

func TestGetAssetSummaryMergesLocalRGB11AndExcludesCarrierSats(t *testing.T) {
	wallet := NewInternalWalletWithMnemonic(
		"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire", "", &chaincfg.TestNet4Params,
	)
	if wallet == nil {
		t.Fatal("create RGB11 summary wallet")
	}
	walletScript, err := AddrToPkScript(wallet.GetAddress(), &chaincfg.TestNet4Params)
	if err != nil {
		t.Fatal(err)
	}

	flow := &rgb11FlowIndexer{outputs: make(map[string]*TxOutput)}
	rpc := &rgb11SummaryIndexer{
		rgb11FlowIndexer: flow,
		summaries:        make(map[string]*indexerwire.AssetSummary),
	}
	evidence := &rgb11FlowEvidence{
		utxos: make(map[string]*rgb11wallet.BitcoinUTXO), rawTx: make(map[string][]byte), spendingTx: make(map[string]string),
	}
	for index := 0; index < 3; index++ {
		outpoint := fmt.Sprintf("%064x:0", 6000+index)
		flow.plain = append(flow.plain, &indexerwire.TxOutputInfo{
			OutPoint: outpoint, Value: 10_000, PkScript: walletScript,
		})
		evidence.utxos[outpoint] = &rgb11wallet.BitcoinUTXO{
			OutPoint: outpoint, Value: 10_000, PkScript: walletScript, Confirmations: 6,
		}
	}
	rpc.summaries[wallet.GetAddress()] = &indexerwire.AssetSummary{
		ListResp: indexerwire.ListResp{Total: 2},
		Data: []*indexer.AssetInfo{
			{Name: indexer.ASSET_PLAIN_SAT, Amount: *indexer.NewDefaultDecimal(30_000), BindingSat: 1},
			{Name: indexer.ASSET_ALL_SAT, Amount: *indexer.NewDefaultDecimal(30_000), BindingSat: 1},
		},
	}

	manager := newRGB11FlowManager(t, wallet, rpc, evidence, 60)
	manager.walletInfoMap = map[int64]*WalletInfo{
		60: {
			WalletInDB: WalletInDB{Id: 60, Accounts: 2},
			Wallet:     wallet,
		},
	}
	issued, err := manager.IssueRGB11Asset(context.Background(), RGB11IssueRequest{
		Schema: "NIA", Ticker: "SUMMARY", Name: "Summary Asset", Amounts: []uint64{100},
	})
	if err != nil {
		t.Fatal(err)
	}
	state, err := manager.GetRGB11State()
	if err != nil {
		t.Fatal(err)
	}
	var carrierSats int64
	for _, output := range state.Outputs {
		stored, loadErr := manager.rgbManager.projectionStore.LoadOutput(output.OutPointStr)
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		carrierSats += stored.OutValue.Value
	}

	summary, err := manager.GetAssetSummary(wallet.GetAddress())
	if err != nil {
		t.Fatal(err)
	}
	var plain, total, rgb *indexer.AssetInfo
	for _, asset := range summary.Data {
		switch {
		case asset.Name == indexer.ASSET_PLAIN_SAT:
			plain = asset
		case asset.Name == indexer.ASSET_ALL_SAT:
			total = asset
		case asset.Name == issued.AssetName:
			rgb = asset
		}
	}
	if plain == nil || plain.Amount.Int64() != 30_000-carrierSats {
		t.Fatalf("plain sats=%v want=%d", plain, 30_000-carrierSats)
	}
	if total == nil || total.Amount.Int64() != 30_000 {
		t.Fatalf("physical BTC total changed: %v", total)
	}
	if rgb == nil || rgb.Amount.Value.Uint64() != 100 {
		t.Fatalf("RGB11 summary=%v", rgb)
	}
	proofs, err := manager.rgbManager.projectionStore.ListProofs()
	if err != nil || len(proofs) != 1 {
		t.Fatalf("RGB11 proofs=%d err=%v", len(proofs), err)
	}
	proofs[0].Status = "valid"
	proofs[0].Confirmations = 0
	if err := manager.rgbManager.projectionStore.SaveProofState(proofs[0]); err != nil {
		t.Fatal(err)
	}
	pendingSummary, err := manager.GetAssetSummary(wallet.GetAddress())
	if err != nil {
		t.Fatal(err)
	}
	for _, asset := range pendingSummary.Data {
		if asset.Name == issued.AssetName {
			t.Fatalf("unconfirmed RGB11 asset leaked into available summary: %+v", asset)
		}
		if asset.Name == indexer.ASSET_PLAIN_SAT && asset.Amount.Int64() != 30_000-carrierSats {
			t.Fatalf("pending RGB11 carrier sats became spendable: %+v", asset)
		}
	}
	pendingState, err := manager.GetRGB11State()
	if err != nil {
		t.Fatal(err)
	}
	if len(pendingState.AvailableAssets) != 0 || len(pendingState.PendingAssets) != 1 ||
		pendingState.PendingAssets[0].Amount.Value.Uint64() != 100 {
		t.Fatalf("unexpected pending RGB11 split: available=%+v pending=%+v",
			pendingState.AvailableAssets, pendingState.PendingAssets)
	}
	secondAddress := wallet.GetAddressByIndex(1)
	rpc.summaries[secondAddress] = &indexerwire.AssetSummary{
		ListResp: indexerwire.ListResp{Total: 1},
		Data: []*indexer.AssetInfo{
			{Name: indexer.ASSET_PLAIN_SAT, Amount: *indexer.NewDefaultDecimal(5_000), BindingSat: 1},
		},
	}
	second, err := manager.GetAssetSummary(secondAddress)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Data) != 1 || second.Data[0].Amount.Int64() != 5_000 {
		t.Fatalf("account scope leaked into second account: %+v", second.Data)
	}
}

func TestGetAssetSummaryPreservesIndexerAssetsWhenRGB11ProofIsMissing(t *testing.T) {
	wallet := NewInternalWalletWithMnemonic(
		"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire", "", &chaincfg.TestNet4Params,
	)
	if wallet == nil {
		t.Fatal("create RGB11 summary wallet")
	}
	walletScript, err := AddrToPkScript(wallet.GetAddress(), &chaincfg.TestNet4Params)
	if err != nil {
		t.Fatal(err)
	}

	flow := &rgb11FlowIndexer{outputs: make(map[string]*TxOutput)}
	rpc := &rgb11SummaryIndexer{
		rgb11FlowIndexer: flow,
		summaries:        make(map[string]*indexerwire.AssetSummary),
	}
	evidence := &rgb11FlowEvidence{
		utxos: make(map[string]*rgb11wallet.BitcoinUTXO), rawTx: make(map[string][]byte), spendingTx: make(map[string]string),
	}
	for index := 0; index < 2; index++ {
		outpoint := fmt.Sprintf("%064x:0", 7000+index)
		flow.plain = append(flow.plain, &indexerwire.TxOutputInfo{
			OutPoint: outpoint, Value: 10_000, PkScript: walletScript,
		})
		evidence.utxos[outpoint] = &rgb11wallet.BitcoinUTXO{
			OutPoint: outpoint, Value: 10_000, PkScript: walletScript, Confirmations: 6,
		}
	}
	rpc.summaries[wallet.GetAddress()] = &indexerwire.AssetSummary{
		ListResp: indexerwire.ListResp{Total: 3},
		Data: []*indexer.AssetInfo{
			{Name: indexer.ASSET_PLAIN_SAT, Amount: *indexer.NewDefaultDecimal(20_000), BindingSat: 1},
			{Name: indexer.ASSET_ALL_SAT, Amount: *indexer.NewDefaultDecimal(20_000), BindingSat: 1},
			{
				Name:       indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_ORDX, Type: indexer.ASSET_TYPE_FT, Ticker: "dogcoin"},
				Amount:     *indexer.NewDefaultDecimal(1_000),
				BindingSat: 5,
			},
		},
	}

	manager := newRGB11FlowManager(t, wallet, rpc, evidence, 61)
	manager.walletInfoMap = map[int64]*WalletInfo{
		61: {
			WalletInDB: WalletInDB{Id: 61, Accounts: 1},
			Wallet:     wallet,
		},
	}
	issued, err := manager.IssueRGB11Asset(context.Background(), RGB11IssueRequest{
		Schema: "NIA", Ticker: "BROKEN", Name: "Broken Projection", Amounts: []uint64{100},
	})
	if err != nil {
		t.Fatal(err)
	}
	state, err := manager.GetRGB11State()
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Outputs) != 1 {
		t.Fatalf("RGB11 outputs=%d want=1", len(state.Outputs))
	}
	proofKey := []byte(fmt.Sprintf(
		"rgb11-%s-proof-%s-%s",
		rgb11StorageScope(61, 0),
		state.Outputs[0].OutPointStr,
		issued.AssetName.String(),
	))
	if err := manager.db.Delete(proofKey); err != nil {
		t.Fatal(err)
	}

	summary, err := manager.GetAssetSummary(wallet.GetAddress())
	if err != nil {
		t.Fatal(err)
	}
	if summary.Total != 3 || len(summary.Data) != 3 {
		t.Fatalf("summary total=%d assets=%d want=3", summary.Total, len(summary.Data))
	}
	for _, asset := range summary.Data {
		if asset.Name.Protocol == rgb11wallet.Protocol {
			t.Fatalf("damaged RGB11 asset leaked into summary: %s", asset.Name.String())
		}
		switch asset.Name {
		case indexer.ASSET_PLAIN_SAT, indexer.ASSET_ALL_SAT:
			if asset.Amount.Int64() != 20_000 {
				t.Fatalf("%s amount=%d want=20000", asset.Name.String(), asset.Amount.Int64())
			}
		default:
			if asset.Name.Protocol != indexer.PROTOCOL_NAME_ORDX || asset.Name.Ticker != "dogcoin" ||
				asset.Amount.Int64() != 1_000 {
				t.Fatalf("unexpected Indexer asset after RGB11 failure: %+v", asset)
			}
		}
	}
}
