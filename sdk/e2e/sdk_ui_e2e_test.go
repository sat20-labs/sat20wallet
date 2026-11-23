package e2e

import (
	"fmt"
	"testing"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

// Real temporary Bitcoin/indexer/Core nodes, one group, independent wallets.
// Business calls use the same Manager API as the UI without WASM or Node.
func TestSDKUIFlows(t *testing.T) {
	chain := newNativeIfaceChain(t)
	for _, item := range []struct {
		name string
		run  func(*testing.T, *nativeIfaceChain)
	}{
		{"ServiceWalletProfile", runServiceWalletProfile},
		{"RGBManual", runNativeRGBManual},
		{"RGBDirectOffline", runNativeRGBDirect},
		{"RGBBlindSelfReceiveFirst", runNativeRGBBlindSelf},
		{"RGBBlindUDA", runNativeRGBBlind},
		{"RGBProxy", runNativeRGBProxy},
		{"DIDHistory", runNativeDID},
	} {
		t.Run(item.name, func(t *testing.T) { item.run(t, chain) })
	}
	t.Run("AccountReview", func(t *testing.T) { runNativeAccountReview(t, chain) })
}

func runServiceWalletProfile(t *testing.T, chain *nativeIfaceChain) {
	for _, mode := range []string{wallet.SERVER_NODE, wallet.BOOTSTRAP_NODE} {
		t.Run(mode, func(t *testing.T) {
			cfg := *chain.cfg
			cfg.Mode = mode
			device := newNativeUIDevice(t, &cfg, "")
			id, _, err := device.mgr.CreateWallet(device.password)
			require.NoError(t, err)
			address := device.mgr.GetWallet().GetAddress()
			key := []byte(wallet.GetDBKeyPrefix() + "account-management-profile-v1")
			obsolete := []byte("incompatible account profile")
			require.NoError(t, device.db.Write(key, obsolete))

			device.reopen(t)
			device.mgr.Start()
			require.Equal(t, address, device.mgr.GetWallet().GetAddress())
			status := device.mgr.GetAccountManagementStatus()
			require.True(t, status.LocalOnly)
			require.True(t, status.Active)
			require.False(t, status.RecoveryConfigured)
			require.Equal(t, id, status.RootWalletID)
			require.Equal(t, dkvsindexer.AccountID(device.mgr.GetWallet().GetPubKey().SerializeCompressed()), status.AccountID)
			receiver, err := device.mgr.ResolveConfiguredRGB11AddressEndpoint(address, dkvsindexer.RecordVerificationOptions{})
			require.NoError(t, err)
			require.Equal(t, status.AccountID, receiver.AccountID)
			body := []byte("service wallet Direct after restart")
			sent, err := device.mgr.SendAccountDirectMessage("service-self", wallet.AccountMessageKindGeneric, status.AccountID, body)
			require.NoError(t, err)
			deadline := time.Now().Add(30 * time.Second)
			for {
				messages, _, err := device.mgr.ReadAccountDirectMessages(0, 10)
				if err == nil && len(messages) != 0 {
					require.Len(t, messages, 1)
					require.Equal(t, sent.MessageID, messages[0].Direct.MessageID)
					require.Equal(t, body, messages[0].Payload.Body)
					break
				}
				require.True(t, time.Now().Before(deadline), "waiting for service wallet Direct, last read: %v", err)
				time.Sleep(100 * time.Millisecond)
			}

			require.Equal(t, "inactive", status.DiscoveryStatus)
			require.EqualError(t, device.mgr.InitializeAccountManagement(device.password),
				"personal account backup is disabled for STP service wallets")
			require.NoError(t, device.mgr.UpdateWalletName(id, "Local service wallet"))
			device.reopen(t)
			catalog := device.mgr.GetWalletCatalog()
			require.Len(t, catalog, 1)
			require.Equal(t, "Local service wallet", catalog[0].Name)
			raw, err := device.db.Read(key)
			require.NoError(t, err)
			require.Equal(t, obsolete, raw)
		})
	}
}

func prepareSDKUIActors(t *testing.T, source *wallet.Manager, network *realSatoshiNet,
	mine func(string, int)) map[string]map[string]string {
	t.Helper()
	// Check the working network and paid service before funding fresh test wallets.
	require.NotNil(t, network.Bootstrap)
	require.NotNil(t, network.Core)
	_, height, err := network.Core.Client.GetBestBlock()
	require.NoError(t, err)
	require.GreaterOrEqual(t, height, int32(32), "RGB requires the real Core to pass POSv2 activation")
	defaults := dkvsindexer.NetworkDefaultsForParams(wallet.GetChainParam_SatsNet())
	raw, err := source.QueryContract(&wallet.ContractQueryRequest{Query: wallet.ContractQueryState, Contract: defaults.AutopayContract})
	require.NoError(t, err)
	state, err := dkvsindexer.DecodeAutopayContractState([]byte(raw), defaults.AutopayContract)
	require.NoError(t, err)
	require.NotNil(t, state, "prepare the real AUTOPAY service before this suite; no simulated paid storage")
	require.NotEmpty(t, state.GasBalance, "real AUTOPAY needs an operating reserve")
	actors := map[string]map[string]string{}
	runID := fmt.Sprintf("sdk-ui-%d", time.Now().UnixNano())
	for _, role := range []string{"manual_owner", "manual_receiver", "direct_owner", "direct_receiver", "account_owner", "guardian"} {
		actor := sdkPWARegtestSyntheticIdentity(t, runID+"-"+role)
		txid, _, err := source.BatchSendPlainSats(actor["address"], 1_000_000, 8, 2, nil)
		require.NoError(t, err)
		confirmSDKPWARegtestL1SDKTransaction(t, source, txid, mine)
		actors[role] = actor
	}
	// Only the paid-account journey needs real SGAS and L2 fees. Funding uses
	// the SDK's existing transfer, never seeded indexer or contract state.
	for _, amount := range []struct{ asset, value string }{{wallet.PLAIN_ASSET.String(), "1000000"}, {defaults.AutopayFeeAssetName, "2000000"}} {
		tx, err := source.SendAssets_SatsNet(actors["account_owner"]["address"], amount.asset, amount.value, nil)
		require.NoError(t, err, "real funding wallet must cover the paid account journey")
		waitRGB11RegtestL2Transaction(t, source, network, tx.TxID())
	}
	return actors
}
