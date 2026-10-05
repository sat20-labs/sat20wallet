package e2e

import (
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	"github.com/sat20-labs/satoshinet/chaincfg"
	contract "github.com/sat20-labs/satoshinet/contract"
	templateruntime "github.com/sat20-labs/satoshinet/contract/template"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

// Funding and L1 ownership are isolated fixtures. Business calls use bound
// wallet SDK interfaces, production CoreNode RPC and real P2P replication.
func TestSDKDKVSModulePaid(t *testing.T) {
	f, owner, successor, payment := sdkDKVSReviewPaidFixture(t)
	client := dkvsClientForNode(t, f.Network.Core)
	peer := dkvsClientForNode(t, f.Network.Bootstrap)
	personal := func(t *testing.T, path string) string {
		t.Helper()
		key, err := dkvs.PersonalKey(owner.Wallet.GetPubKey().SerializeCompressed(), path)
		require.NoError(t, err)
		return key
	}
	put := func(t *testing.T, actor *dkvsKeyPathActor, key, value string) *wire.DKVSRecord {
		t.Helper()
		record, err := client.PutSignedRecordWithAutopay(actor.Wallet, key, []byte(value),
			dkvs.RecordOptions{IssueHeight: sdkDKVSReviewHeight(t, client)}, payment)
		require.NoError(t, err)
		return record
	}

	t.Run("ReplicatedLifecycleAndDowngradeProtection", func(t *testing.T) {
		key := personal(t, "review-paid/value")
		first := put(t, owner, key, "paid-v1")
		require.Equal(t, uint64(1), first.Seq)
		require.Zero(t, first.TTL)
		requireDKVSValue(t, f.Network.Bootstrap, key, first.Value)
		second := put(t, owner, key, "paid-v2")
		require.Equal(t, uint64(2), second.Seq)
		requireDKVSValue(t, f.Network.Bootstrap, key, second.Value)
		_, err := client.PutSignedRecordFreeLocal(owner.Wallet, key, []byte("illegal-downgrade"),
			dkvs.RecordOptions{IssueHeight: sdkDKVSReviewHeight(t, client), TTL: 600})
		require.ErrorIs(t, err, dkvs.ErrStorageModeDowngrade)
		sdkDKVSReviewUnchanged(t, client, second)
		sdkDKVSReviewUnchanged(t, peer, second)
		removed, err := client.DeleteCurrentRecord(owner.Wallet, key, sdkDKVSReviewHeight(t, client))
		require.NoError(t, err)
		require.Equal(t, uint64(3), removed.Seq)
		requireDKVSAbsent(t, f.Network.Bootstrap, key)
		remoteState, err := peer.GetKeyState(key)
		require.NoError(t, err)
		require.Equal(t, dkvs.KeyStateNeverSeen, remoteState.Status)
		require.Zero(t, remoteState.Seq)
		require.Empty(t, remoteState.ETag)
		recreated := put(t, owner, key, "paid-recreated")
		require.Equal(t, uint64(1), recreated.Seq)
		requireDKVSValue(t, f.Network.Bootstrap, key, recreated.Value)
	})

	t.Run("PaidDeleteAckReplayIsIdempotent", func(t *testing.T) {
		original := put(t, owner, personal(t, "review-paid-ack/value"), "paid-delete")
		requireDKVSValue(t, f.Network.Bootstrap, original.Key, original.Value)
		command, err := wallet.NewDKVSDeleteCommand(owner.Wallet, original, sdkDKVSReviewHeight(t, client))
		require.NoError(t, err)
		retry, transport := sdkDKVSReviewReplayClient(client, owner.Wallet)
		result, err := retry.PutRecordBatchCAS([]dkvs.CASMutation{sdkDKVSReviewExpected(command, original)})
		require.NoError(t, err)
		require.Equal(t, 2, transport.attempts)
		require.Equal(t, 1, transport.firstApplied)
		require.Zero(t, result.Applied)
		requireDKVSAbsent(t, f.Network.Bootstrap, original.Key)
	})

	t.Run("FreeLocalUpgradeBecomesReplicated", func(t *testing.T) {
		key := personal(t, "review-upgrade/value")
		local := sdkDKVSReviewPutFree(t, client, owner, key, []byte("temporary"))
		_, err := peer.GetRecord(key)
		require.ErrorIs(t, err, dkvs.ErrRecordNotFound)
		paid := put(t, owner, key, "durable")
		require.Equal(t, local.Seq+1, paid.Seq)
		require.Zero(t, paid.TTL)
		requireDKVSValue(t, f.Network.Bootstrap, key, paid.Value)
		state, err := peer.GetKeyState(key)
		require.NoError(t, err)
		require.Equal(t, dkvs.StorageModeAutopay, state.StorageMode)
	})

	t.Run("AutopayHasNoRecordLevelRenewal", func(t *testing.T) {
		record := put(t, owner, personal(t, "review-paid-renew/value"), "durable")
		requireDKVSValue(t, f.Network.Bootstrap, record.Key, record.Value)
		_, err := client.RenewRecord(owner.Wallet, record, dkvs.RecordOptions{IssueHeight: sdkDKVSReviewHeight(t, client), TTL: 100})
		require.ErrorIs(t, err, dkvs.ErrInvalidRecord)
		sdkDKVSReviewUnchanged(t, client, record)
		sdkDKVSReviewUnchanged(t, peer, record)
	})

	t.Run("UnfundedSignerCannotCreatePaidData", func(t *testing.T) {
		// Bind this distinct signer so the negative result is due to payment,
		// not the new RPC admission gate short-circuiting the original test.
		bindDKVSReviewWallet(t, f.Network.Core, minerMnemonic)
		unfunded := newDKVSKeyPathActor(t, keyFromMnemonic(t, minerMnemonic, 0))
		key, err := dkvs.PersonalKey(unfunded.Wallet.GetPubKey().SerializeCompressed(), "review-unfunded/value")
		require.NoError(t, err)
		_, err = client.PutSignedRecordWithAutopay(unfunded.Wallet, key, []byte("not-funded"),
			dkvs.RecordOptions{IssueHeight: sdkDKVSReviewHeight(t, client)}, payment)
		require.Error(t, err)
		require.NotErrorIs(t, err, dkvs.ErrPermissionDenied)
		state, err := client.GetKeyState(key)
		require.NoError(t, err)
		require.Equal(t, dkvs.KeyStateNeverSeen, state.Status)
		_, err = peer.GetRecord(key)
		require.ErrorIs(t, err, dkvs.ErrRecordNotFound)
	})

	t.Run("TransferredServiceRejectsPreviousOwner", func(t *testing.T) {
		const service = "reviewsvc.btc"
		f.NetworkFakeL1().setNameOwner(service, owner.Address)
		key, err := dkvs.ServiceKey(service, "authenticity/pwa")
		require.NoError(t, err)
		original := put(t, owner, key, "original-release")
		requireDKVSValue(t, f.Network.Bootstrap, key, original.Value)
		f.NetworkFakeL1().setNameOwner(service, successor.Address)
		deniedKey, err := dkvs.ServiceKey(service, "previous-owner-fresh")
		require.NoError(t, err)
		_, denied := client.PutSignedRecordWithAutopay(owner.Wallet, deniedKey, []byte("denied"),
			dkvs.RecordOptions{IssueHeight: sdkDKVSReviewHeight(t, client)}, payment)
		require.ErrorIs(t, denied, dkvs.ErrPermissionDenied)
		freshKey, err := dkvs.ServiceKey(service, "successor-fresh")
		require.NoError(t, err)
		fresh := put(t, successor, freshKey, "new-owner-authorized")
		sdkDKVSReviewUnchanged(t, client, fresh)
		_, updateErr := client.PutSignedRecordWithAutopay(owner.Wallet, original.Key, []byte("revoked-owner-update"),
			dkvs.RecordOptions{IssueHeight: sdkDKVSReviewHeight(t, client)}, payment)
		require.ErrorIs(t, updateErr, dkvs.ErrPermissionDenied)
		sdkDKVSReviewUnchanged(t, client, original)
		_, deleteErr := client.DeleteCurrentRecord(owner.Wallet, original.Key, sdkDKVSReviewHeight(t, client))
		require.ErrorIs(t, deleteErr, dkvs.ErrPermissionDenied)
		sdkDKVSReviewUnchanged(t, client, original)

		successorUpdate := put(t, successor, original.Key, "successor-release")
		require.Equal(t, original.Seq+1, successorUpdate.Seq)
		requireDKVSValue(t, f.Network.Bootstrap, original.Key, successorUpdate.Value)
		requireDKVSValue(t, f.Network.Bootstrap, fresh.Key, fresh.Value)
		successorDelete, err := client.DeleteCurrentRecord(successor.Wallet, original.Key, sdkDKVSReviewHeight(t, client))
		require.NoError(t, err)
		require.Equal(t, successorUpdate.Seq+1, successorDelete.Seq)
		requireDKVSAbsent(t, f.Network.Bootstrap, original.Key)
	})
}

func sdkDKVSReviewPaidFixture(t *testing.T) (*templateFixture, *dkvsKeyPathActor, *dkvsKeyPathActor, wallet.DKVSAutopayOptions) {
	t.Helper()
	defaults := dkvs.NetworkDefaultsForParams(&chaincfg.TestNetParams)
	f := newDKVSNoPluginTemplateFixtureWithArgs(t, map[string]int64{defaults.AutopayFeeAssetName: 20000}, nil, nil, dkvsMinerArgs(t))
	waitForDKVSPeerReady(t, f.Network)
	bindDKVSReviewWallet(t, f.Network.Core, dkvsClientMnemonic)
	bindDKVSReviewWallet(t, f.Network.Core, bootstrapMnemonic)
	gas := contract.GetGasAssetName()
	a := newDKVSKeyPathActor(t, keyFromMnemonic(t, dkvsClientMnemonic, 0))
	b := newDKVSKeyPathActor(t, keyFromMnemonic(t, bootstrapMnemonic, 0))
	require.Equal(t, defaults.AutopayDeployer, a.Address)
	gasOuts := splitToDKVSKeyPathActors(t, f, f.gasAnchor, gas,
		[]int64{300000, 300000, 300000, 300000}, []int64{10000, 10000, 10000, 10000},
		[]*dkvsKeyPathActor{a, a, b, b})
	feeOuts := splitToDKVSKeyPathActors(t, f, f.assetAnchors[defaults.AutopayFeeAssetName], defaults.AutopayFeeAssetName,
		[]int64{5000, 5000}, []int64{10000, 10000}, []*dkvsKeyPathActor{a, b})
	content, err := defaults.AutopayContent()
	require.NoError(t, err)
	assets := txAsset(gas, 290000)
	require.NoError(t, assets.Merge(txAsset(defaults.AutopayFeeAssetName, 5000)))
	deploy, pool := buildDKVSKeyPathTemplateDeploy(t, a, contract.TemplateAutopay, content, a.Address,
		defaults.AutopayDeployNonce, []dkvsPrevOut{gasOuts[0], feeOuts[0]}, wire.TxOut{Value: 10000, Assets: assets})
	f.Network.sendManyAndMine(t, []*wire.MsgTx{deploy}, 0)
	fundB := buildDKVSKeyPathTemplateDefaultInvoke(t, b, pool, []dkvsPrevOut{feeOuts[1]}, wire.TxOut{Value: 10000, Assets: txAsset(defaults.AutopayFeeAssetName, 5000)})
	f.Network.sendManyAndMine(t, []*wire.MsgTx{fundB}, 0)
	param, err := (&contract.TemplateAutopayConfigInvokeParam{GasFundingAmount: "289950"}).Encode()
	require.NoError(t, err)
	reserve := buildDKVSKeyPathTemplateInvoke(t, a, pool, 1, contract.TemplateInvokeAPIConfig, param,
		[]dkvsPrevOut{gasOuts[1]}, wire.TxOut{Value: 9000, Assets: txAsset(gas, 290000)})
	heartbeat := buildDKVSKeyPathAssetTransfer(t, b, gasOuts[3], gas, 290000, 9000, b)
	f.Network.sendManyAndMine(t, []*wire.MsgTx{reserve, heartbeat}, 0)
	for _, node := range []*testHarness{f.Network.Bootstrap, f.Network.Core} {
		state := fetchTemplateAutopayView(t, node, pool.MustEncode())
		require.Equal(t, templateruntime.AutopayStatusActive, state.Status)
		require.Contains(t, state.Delegates, a.Address)
		require.Contains(t, state.Delegates, b.Address)
		require.NotEqual(t, "0", state.GasBalance)
	}
	return f, a, b, wallet.DKVSAutopayOptions{AddressParams: &chaincfg.TestNetParams, PoolContract: pool.MustEncode()}
}
