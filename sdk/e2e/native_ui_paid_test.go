package e2e

import (
	"context"
	"testing"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	"github.com/sat20-labs/satoshinet/chaincfg"
	contract "github.com/sat20-labs/satoshinet/contract"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func runNativePaidUI(t *testing.T) {
	ctx := context.Background()
	defaults := dkvs.NetworkDefaultsForParams(&chaincfg.TestNetParams)
	f := newDKVSNoPluginTemplateFixtureWithArgs(t,
		map[string]int64{defaults.AutopayFeeAssetName: 50000}, nil, nil, dkvsMinerArgs(t))
	network := f.Network
	waitForDKVSPeerReady(t, network)
	gas := contract.GetGasAssetName()
	actor := newDKVSKeyPathActor(t, keyFromMnemonic(t, dkvsClientMnemonic, 0))
	clockActor := newDKVSKeyPathActor(t, keyFromMnemonic(t, coreMnemonic, 3))
	gasOuts := splitToDKVSKeyPathActors(t, f, f.gasAnchor, gas,
		[]int64{300000, 300000, 280000, 10000, 10000}, []int64{10000, 10000, 10000, 10000, 10000},
		[]*dkvsKeyPathActor{actor, actor, actor, clockActor, clockActor})
	feeOuts := splitToDKVSKeyPathActors(t, f, f.assetAnchors[defaults.AutopayFeeAssetName],
		defaults.AutopayFeeAssetName, []int64{5000, 45000}, []int64{10000, 10000}, []*dkvsKeyPathActor{actor, actor})
	content, err := defaults.AutopayContent()
	require.NoError(t, err)
	assets := txAsset(gas, 290000)
	require.NoError(t, assets.Merge(txAsset(defaults.AutopayFeeAssetName, 5000)))
	deploy, pool := buildDKVSKeyPathTemplateDeploy(t, actor, contract.TemplateAutopay,
		content, actor.Address, defaults.AutopayDeployNonce, []dkvsPrevOut{gasOuts[0], feeOuts[0]}, wire.TxOut{Value: 10000, Assets: assets})
	network.sendManyAndMine(t, []*wire.MsgTx{deploy}, 0)
	param, err := (&contract.TemplateAutopayConfigInvokeParam{AmountPerBlock: "1", GasFundingAmount: "280000"}).Encode()
	require.NoError(t, err)
	reserve := buildDKVSKeyPathTemplateInvoke(t, actor, pool, 1, contract.TemplateInvokeAPIConfig,
		param, []dkvsPrevOut{gasOuts[1]}, wire.TxOut{Value: 9000, Assets: txAsset(gas, 290000)})
	network.sendManyAndMine(t, []*wire.MsgTx{reserve}, 0)
	cfg, _ := accountReviewConfig(t, network)
	owner := newNativeUIInactive(t, cfg, dkvsClientMnemonic)
	nativeUILocalBackup(t, owner)
	before := owner.mgr.GetWalletCatalog()
	identity := owner.mgr.GetAccountManagementStatus().AccountID
	key, err := dkvs.AccountPersonalKey(identity, "account/state")
	require.NoError(t, err)
	client := dkvsClientForNode(t, network.Core)
	temporary, err := client.GetRecordDirect(key)
	require.NoError(t, err)
	require.NoError(t, dkvs.VerifySignature(temporary))
	require.Positive(t, temporary.TTL)
	option := nativeUIStorage(t, owner.mgr, "paid", 200)
	require.EqualValues(t, 200, option.RecordCount)
	bad := option
	badPayment := *option.Payment
	badPayment.Amount.Amount = "1"
	bad.Payment = &badPayment
	_, err = owner.mgr.ConfirmStorage(ctx, bad)
	require.ErrorIs(t, err, wallet.ErrAccountAutopayFundingQuoteChanged)
	option = nativeUIStorage(t, owner.mgr, "paid", 100)
	paid, err := owner.mgr.ConfirmStorage(ctx, option)
	require.NoError(t, err)
	require.Len(t, paid.TxID, 64, "the SDK must submit an actual first payment")
	paymentID := paid.TxID
	require.Eventually(t, func() bool {
		view := fetchTemplateAutopayView(t, network.Core, pool.MustEncode())
		return view.Delegates[actor.Address].AmountPerBlock == "10"
	}, 60*time.Second, 200*time.Millisecond)
	// Advance the real POS chain with an independent actor. This cannot spend
	// the SDK payment's change or substitute a synthetic receipt.
	pulse := buildDKVSKeyPathAssetTransfer(t, clockActor, gasOuts[3], gas, 9000, 9000, clockActor)
	network.sendManyAndMine(t, []*wire.MsgTx{pulse}, 0)
	if paid.Status == "pending" {
		owner.reopen(t)
		nativeUIReady(t, owner.mgr)
		paid = nativeUIConfirm(t, owner.mgr, option)
		if paid.TxID != "" {
			require.Equal(t, paymentID, paid.TxID)
		}
	}
	require.Equal(t, "ready", paid.Status)
	created, err := owner.mgr.CreateRecovery(ctx, nativeUISetupReq(owner.mgr, owner.password))
	require.NoError(t, err)
	_, err = owner.mgr.ActivateRecovery(ctx, created.SessionID, wallet.ActivationInput{
		Password: owner.password, RecoveryFile: "not-the-saved-code", Factors: nativeUIFactors()})
	require.Error(t, err)
	nativeUIActivate(t, owner, created)
	require.Equal(t, before, owner.mgr.GetWalletCatalog())
	signed, err := client.GetRecordDirect(key)
	require.NoError(t, err)
	require.NoError(t, dkvs.VerifySignature(signed))
	proof, err := dkvs.ParseFeeProof(signed.FeeProof)
	require.NoError(t, err)
	require.Equal(t, dkvs.FeeModeAutopay, proof.Mode)
	require.Zero(t, signed.TTL)
	require.Equal(t, temporary.Value, signed.Value, "policy upgrade preserves encrypted catalog bytes")
	require.Greater(t, signed.Seq, temporary.Seq)
	require.NotEqual(t, temporary.FeeProof, signed.FeeProof)
	view, err := owner.mgr.GetAccountView(ctx)
	require.NoError(t, err)
	require.Equal(t, "paid", view.Storage.Option.ID)
	require.True(t, view.Autopay.CanFund)
	require.NotNil(t, view.Autopay.Quote)
	renewalQuote := *view.Autopay.Quote
	badQuote := renewalQuote
	badQuote.Amount.Amount = "1"
	_, err = owner.mgr.FundAutopay(ctx, badQuote)
	require.ErrorIs(t, err, wallet.ErrAccountAutopayFundingQuoteChanged)
	renewal, err := owner.mgr.FundAutopay(ctx, renewalQuote)
	require.NoError(t, err)
	require.Len(t, renewal.TxID, 64)
	require.NotEqual(t, paymentID, renewal.TxID)
	pulse = buildDKVSKeyPathAssetTransfer(t, clockActor, gasOuts[4], gas, 9000, 9000, clockActor)
	network.sendManyAndMine(t, []*wire.MsgTx{pulse}, 0)
	owner.reopen(t)
	nativeUIReady(t, owner.mgr)
	require.Eventually(t, func() bool {
		v, e := owner.mgr.RefreshAccount(ctx)
		return e == nil && v.Autopay != nil && v.Autopay.Status == "ready"
	}, 60*time.Second, 200*time.Millisecond)
	rehearsal, err := owner.mgr.LoadRecovery(ctx, nativeUIRecoveryFile(t, created), true)
	require.NoError(t, err)
	preview, err := owner.mgr.PreviewRecovery(ctx, rehearsal.SessionID, nativeUIFactors())
	require.NoError(t, err)
	require.False(t, preview.CanCommit)
	require.Len(t, preview.Summary.Wallets, len(before))
	_, err = owner.mgr.CommitRecovery(ctx, rehearsal.SessionID, owner.password)
	require.EqualError(t, err, "rehearsal cannot import wallets")
	require.NoError(t, owner.mgr.AbortRecovery(rehearsal.SessionID))
	owner.close()
	restored := newNativeUIDevice(t, cfg, "")
	restored.password += "-restored"
	result := nativeUIRestore(t, restored, created)
	require.True(t, result.Complete)
	require.Equal(t, identity, result.AccountID)
	restored.reopen(t)
	nativeUIReady(t, restored.mgr)
	nativeUICatalog(t, restored.mgr, before)
	view, err = restored.mgr.GetAccountView(ctx)
	require.NoError(t, err)
	require.Equal(t, identity, view.AccountID)
	require.NotNil(t, view.Autopay)
	require.Equal(t, "ready", view.Autopay.Status)
	final, err := client.GetRecordDirect(key)
	require.NoError(t, err)
	require.NoError(t, dkvs.VerifySignature(final))
	proof, err = dkvs.ParseFeeProof(final.FeeProof)
	require.NoError(t, err)
	require.Equal(t, dkvs.FeeModeAutopay, proof.Mode)
	require.Zero(t, final.TTL)
	require.Equal(t, temporary.Value, final.Value)
}
