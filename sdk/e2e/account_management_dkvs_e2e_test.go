package e2e

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	"github.com/sat20-labs/satoshinet/chaincfg"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	templateruntime "github.com/sat20-labs/satoshinet/contract/template"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

// TestRealSatoshiNetAccountManagementAutopaySync verifies the network-facing
// account-management storage contract on top of a real public three-node DKVS
// fixture. Cryptographic account-envelope and Shamir reconstruction tests live
// in wallet-sdk; this test proves that the package-versioned encrypted records
// are owner-signed, paid through AUTOPAY, propagated, indexed and retrievable.
func TestRealSatoshiNetAccountManagementAutopaySync(t *testing.T) {
	defaults := dkvsindexer.NetworkDefaultsForParams(&chaincfg.TestNetParams)
	f := newDKVSNoPluginTemplateFixtureWithArgs(t,
		map[string]int64{defaults.AutopayFeeAssetName: 20000}, nil, nil, dkvsMinerArgs(t))
	waitForDKVSPeerReady(t, f.Network)

	gas := contractcommon.GetGasAssetName()
	owner := newDKVSKeyPathActor(t, keyFromMnemonic(t, dkvsClientMnemonic, 0))
	require.Equal(t, defaults.AutopayDeployer, owner.Address)

	gasOuts := splitToDKVSKeyPathActors(t, f, f.gasAnchor, gas,
		[]int64{300000, 300000},
		[]int64{10000, 10000},
		[]*dkvsKeyPathActor{owner, owner})
	feeOuts := splitToDKVSKeyPathActors(t, f, f.assetAnchors[defaults.AutopayFeeAssetName],
		defaults.AutopayFeeAssetName,
		[]int64{5000},
		[]int64{10000},
		[]*dkvsKeyPathActor{owner})

	content, err := defaults.AutopayContent()
	require.NoError(t, err)
	deployAssets := txAsset(gas, 290000)
	deployAssets = append(deployAssets, txAsset(defaults.AutopayFeeAssetName, 5000)...)
	deploy, contractAddress := buildDKVSKeyPathTemplateDeploy(t, owner,
		contractcommon.TemplateAutopay, content, owner.Address, defaults.AutopayDeployNonce,
		[]dkvsPrevOut{gasOuts[0], feeOuts[0]},
		wire.TxOut{Value: 10000, Assets: deployAssets})
	f.Network.sendManyAndMine(t, []*wire.MsgTx{deploy}, 0)

	heartbeat := buildDKVSKeyPathAssetTransfer(t, owner, gasOuts[1], gas, 290000, 9000, owner)
	f.Network.sendManyAndMine(t, []*wire.MsgTx{heartbeat}, 0)
	state := fetchTemplateAutopayView(t, f.Network.Bootstrap, contractAddress.MustEncode())
	require.Equal(t, templateruntime.AutopayStatusActive, state.Status)
	require.Contains(t, state.Delegates, owner.Address)

	pubKey := owner.Wallet.GetPubKey().SerializeCompressed()
	accountID := dkvsindexer.AccountID(pubKey)
	packageID := strings.Repeat("a1", 16)
	basePath := "account/recovery/" + packageID
	prefix, err := dkvsindexer.PersonalKey(pubKey, basePath)
	require.NoError(t, err)

	minerClient := dkvsClientForNode(t, f.Network.Miner)
	_, _, err = minerClient.SubscribePrefix(prefix)
	require.NoError(t, err)
	require.NoError(t, connectNode(f.Network.Miner, f.Network.Core))

	type locator struct {
		Version      int    `json:"version"`
		AccountID    string `json:"account_id"`
		PackageID    string `json:"package_id"`
		RecoveryMode string `json:"recovery_mode"`
	}
	loc := locator{Version: 1, AccountID: accountID, PackageID: packageID, RecoveryMode: "2of3"}
	envelope, err := json.Marshal(map[string]interface{}{
		"version": 1,
		"locator": loc,
		"encrypted_backup": map[string]string{
			"algorithm":  "aes-256-gcm",
			"iv":         "fixture-iv",
			"auth_tag":   "fixture-auth-tag",
			"ciphertext": "fixture-encrypted-account-backup",
		},
	})
	require.NoError(t, err)
	manifest, err := json.Marshal(map[string]interface{}{
		"version":       1,
		"locator":       loc,
		"threshold":     2,
		"total":         3,
		"envelope_hash": "fixture-envelope-hash",
	})
	require.NoError(t, err)

	values := []struct {
		path  string
		value []byte
	}{
		{path: basePath + "/envelope", value: envelope},
		{path: basePath + "/share/dkvs", value: []byte("fixture-encrypted-dkvs-share-capsule")},
		{path: basePath + "/questions", value: []byte("fixture-encrypted-private-question-set")},
		// Manifest is the application-level commit marker and is intentionally written last.
		{path: basePath + "/manifest", value: manifest},
	}

	client := dkvsClientForNode(t, f.Network.Bootstrap)
	autopay := wallet.DKVSAutopayOptions{
		AddressParams: &chaincfg.TestNetParams,
		PoolContract:  contractAddress.MustEncode(),
	}
	for _, item := range values {
		_, err := client.PutPersonalRecordWithAutopay(owner.Wallet, item.path, item.value,
			dkvsindexer.RecordOptions{Seq: 1, TTL: 60_000, ExpiryHeight: 1000}, autopay)
		require.NoError(t, err)
	}

	for _, item := range values {
		key, err := dkvsindexer.PersonalKey(pubKey, item.path)
		require.NoError(t, err)
		requireDKVSValue(t, f.Network.Bootstrap, key, item.value)
		requireDKVSValue(t, f.Network.Core, key, item.value)
		requireDKVSValue(t, f.Network.Miner, key, item.value)
	}

	records, total, err := dkvsClientForNode(t, f.Network.Core).ListRecords(prefix, 0, 10)
	require.NoError(t, err)
	require.Equal(t, len(values), total)
	require.Len(t, records, len(values))

	usage, err := dkvsClientForNode(t, f.Network.Core).GetUsage(prefix)
	require.NoError(t, err)
	require.NotNil(t, usage)
	require.Equal(t, uint64(len(values)), usage.ActiveRecords)
	require.Greater(t, usage.ActiveTotalSize, uint64(0))
}
