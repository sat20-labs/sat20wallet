package e2e

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"

	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	"github.com/stretchr/testify/require"
)

func runNativeIfaceInvalid(t *testing.T, f *nativeIfaceChain) {
	t.Helper()
	// Each operation runs on a funded, writable wallet. An unrelated locked or
	// unsynchronized-wallet error cannot satisfy these validation assertions.
	for _, group := range []string{"22_amounts_channel_targets", "23_contract_boundaries", "24_issuance_referrer", "25_rgb_boundaries"} {
		t.Run(group, func(t *testing.T) {
			d := f.device(t, dkvsClientMnemonic)
			m := d.mgr
			if group == "22_amounts_channel_targets" {
				attachNativeIfaceWallet(t, m, f.network)
				require.True(t, m.IsReady())
			}
			require.NoError(t, m.CheckAccountWrite("wallets", wallet.AccountManagedDataGlobalScope))
			view, err := m.GetRGBView()
			require.NoError(t, err)
			require.True(t, view.CanWrite, view.ErrorCode)
			address := m.GetWallet().GetAddress()
			require.Positive(t, nativeIfaceBalance(t, m, address))
			catalog, chain := nativeIfaceCatalog(t, m), f.snapshot(t)
			unchanged := func() {
				require.Equal(t, catalog, nativeIfaceCatalog(t, m))
				require.Equal(t, chain, f.snapshot(t), "invalid request changed a real chain or its mempool")
				require.Equal(t, address, m.GetWallet().GetAddress())
			}
			check := func(err error) {
				require.Error(t, err)
				require.NotErrorIs(t, err, wallet.ErrDKVSPathNotSynced)
				unchanged()
			}
			switch group {
			case "22_amounts_channel_targets":
				for _, amount := range []string{"0", "-1", "0.1", "18446744073709551616"} {
					_, _, err = m.BatchSendAssets(address, "::", amount, 2, 1, nil)
					check(err)
					_, err = m.BatchSendAssets_SatsNet(address, "::", amount, 2)
					check(err)
					_, err = m.BatchSendAssetsV2_SatsNet([]string{address}, "::", []string{amount}, nil)
					check(err)
				}
				// Fractional/NaN/overflowing JS counts do not exist in this typed
				// int API and remain covered by the tagged bridge gate.
				for _, count := range []int{0, -1} {
					_, _, err = m.BatchSendAssets(address, "::", "1000", count, 1, nil)
					check(err)
					_, err = m.BatchSendAssets_SatsNet(address, "::", "1000", count)
					check(err)
				}
				_, err = m.BatchSendAssetsV2_SatsNet([]string{address}, "::", []string{"1000", "2000"}, nil)
				check(err)
				_, err = m.SafetySnapshot("missing-channel")
				check(err)
				_, err = m.CommitmentExport("missing-channel")
				check(err)
				_, err = m.ForceClosePlan("missing-channel")
				check(err)
				_, err = m.BuildSweepTx("missing-channel", 0)
				check(err)
				_, err = m.BuildPunishTx("missing-channel", "0000000000000000000000000000000000000000000000000000000000000000")
				check(err)
				_, err = m.ReopenChannel(false)
				check(err)
				_, err = m.RebuildChannel()
				check(err)
				status, err := m.PunishStatus("missing-channel")
				require.NoError(t, err)
				require.Empty(t, status)
				unchanged()
			case "23_contract_boundaries":
				content := contractcommon.TemplateExchangeContract{AssetAName: contractcommon.GetGasAssetName(), AssetBName: "::", PriceMode: contractcommon.ExchangePriceModeHeight, Steps: []contractcommon.TemplateExchangePriceStep{{Threshold: "0", BPerA: "0.001"}}}
				raw, err := json.Marshal(content)
				require.NoError(t, err)
				want, err := contractcommon.EncodeTemplateExchangeContent(content)
				require.NoError(t, err)
				encoded, err := wallet.BuildUnifiedContractContent("template", "exchange.tc", string(raw))
				require.NoError(t, err)
				require.Equal(t, base64.StdEncoding.EncodeToString(want), encoded)
				_, err = wallet.BuildUnifiedContractContent("unknown-module", "unknown-template", "{}")
				check(err)
				_, err = wallet.BuildUnifiedContractContent("template", "exchange.tc", "{invalid")
				check(err)
				_, err = m.QueryParamForInvokeUnifiedContract("template", "unknown-template", "unknown-action")
				check(err)
				_, err = m.QueryContract(&wallet.ContractQueryRequest{Query: "state", Contract: "invalid"})
				check(err)
				_, err = m.EstimateDeployUnifiedContract(nil)
				check(err)
				_, err = m.DeployUnifiedContract(nil)
				check(err)
				_, err = m.InvokeUnifiedContract(nil)
				check(err)
				_, err = m.QueryFeeForInvokeUnifiedContract(nil)
				check(err)
			case "24_issuance_referrer":
				_, err = m.DeployTicker_ordx("SDKBAD", -1, 0, 1, 1)
				check(err)
				_, err = m.DeployTicker_brc20("BAD", 0, -1, 19, 1)
				check(err)
				_, err = m.MintAsset_brc20(address, indexercommon.NewAssetNameFromString("brc20:f:missing"), indexercommon.NewDecimal(-1, 0), nil, 1)
				check(err)
				// The old Runes case rejects an absent indexed ticker before the
				// typed mint method. The ORDX fractional amount is bridge-only.
				require.Nil(t, m.GetIndexerClient().GetTickInfo(wallet.ParseAssetString("runes:f:missing")))
				unchanged()
				_, err = m.InscribeName("invalid/name", 1)
				check(err)
				_, err = m.BindReferrerForServer("missing-referrer", f.network.Core.nodePubKey)
				check(err)
			case "25_rgb_boundaries":
				ctx := context.Background()
				locks, err := m.GetLockedUtxoList(address)
				require.NoError(t, err)
				proofs, err := m.GetRGB11State()
				require.NoError(t, err)
				operations := []func() error{
					func() error { _, e := m.AcceptRGBConsignment(ctx, "missing-request"); return e },
					func() error { _, e := m.PrepareRGBConsignment(ctx, "missing-request", []byte("invalid")); return e },
					func() error { _, e := m.ImportRGBContract(ctx, []byte("invalid-contract")); return e },
					func() error {
						_, e := m.PrepareRGBTransfer(ctx, wallet.RGBSendRequest{Recipients: []wallet.RGBRecipient{{Destination: "invalid", Asset: "missing", Amount: "-1"}}, FeeRate: 1})
						return e
					},
					func() error {
						_, e := m.CreateRGBInvoice(ctx, wallet.RGBInvoiceRequest{Asset: "missing", Amount: "0", ReceiveMode: "witness", Transport: "invalid"})
						return e
					},
					func() error { _, e := m.ConfirmRGBTransfer(ctx, "missing-request", nil); return e },
					func() error { _, e := m.CancelRGBTransfer(ctx, "missing-request"); return e },
				}
				for _, op := range operations {
					check(op())
					after, err := m.GetRGBView()
					require.NoError(t, err)
					require.Equal(t, view, after)
					afterLocks, err := m.GetLockedUtxoList(address)
					require.NoError(t, err)
					require.Equal(t, locks, afterLocks)
					afterProofs, err := m.GetRGB11State()
					require.NoError(t, err)
					require.Equal(t, proofs, afterProofs)
				}
			}
		})
	}
}
