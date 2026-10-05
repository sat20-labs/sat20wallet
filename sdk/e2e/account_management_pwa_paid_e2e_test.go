package e2e

import (
	"testing"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	"github.com/sat20-labs/satoshinet/chaincfg"
	contractcommon "github.com/sat20-labs/satoshinet/contract"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

// The SDK constructs, signs and submits the actual funding transaction. Only
// pool deployment and block production use the existing node fixture helpers.
func TestSDKAccountPWAPaidFundingWithChildSelected(t *testing.T) {
	defaults := dkvs.NetworkDefaultsForParams(&chaincfg.TestNetParams)
	fixture := newDKVSNoPluginTemplateFixtureWithArgs(t,
		map[string]int64{defaults.AutopayFeeAssetName: 50000}, nil, nil, dkvsMinerArgs(t))
	network := fixture.Network
	waitForDKVSPeerReady(t, network)
	gas := contractcommon.GetGasAssetName()
	owner := newDKVSKeyPathActor(t, keyFromMnemonic(t, dkvsClientMnemonic, 0))
	gasOuts := splitToDKVSKeyPathActors(t, fixture, fixture.gasAnchor, gas,
		[]int64{300000, 300000, 300000}, []int64{10000, 10000, 10000},
		[]*dkvsKeyPathActor{owner, owner, owner})
	feeOuts := splitToDKVSKeyPathActors(t, fixture, fixture.assetAnchors[defaults.AutopayFeeAssetName],
		defaults.AutopayFeeAssetName, []int64{5000, 45000}, []int64{10000, 10000},
		[]*dkvsKeyPathActor{owner, owner})
	content, err := defaults.AutopayContent()
	require.NoError(t, err)
	assets := txAsset(gas, 290000)
	require.NoError(t, assets.Merge(txAsset(defaults.AutopayFeeAssetName, 5000)))
	deploy, address := buildDKVSKeyPathTemplateDeploy(t, owner, contractcommon.TemplateAutopay,
		content, owner.Address, defaults.AutopayDeployNonce, []dkvsPrevOut{gasOuts[0], feeOuts[0]},
		wire.TxOut{Value: 10000, Assets: assets})
	network.sendManyAndMine(t, []*wire.MsgTx{deploy}, 0)
	param, err := (&contractcommon.TemplateAutopayConfigInvokeParam{
		AmountPerBlock: "1", GasFundingAmount: "280000",
	}).Encode()
	require.NoError(t, err)
	reserve := buildDKVSKeyPathTemplateInvoke(t, owner, address, 1, contractcommon.TemplateInvokeAPIConfig,
		param, []dkvsPrevOut{gasOuts[1]}, wire.TxOut{Value: 9000, Assets: txAsset(gas, 290000)})
	network.sendManyAndMine(t, []*wire.MsgTx{reserve}, 0)
	f := prepareAccountReviewWithMnemonic(t, network, dkvsClientMnemonic, true)
	childID, _, err := f.manager.CreateWallet(accountReviewPassword)
	require.NoError(t, err)
	require.Equal(t, childID, f.manager.GetCurrentWalletId())

	// The fixture's POS miner mines nonempty blocks. Submit one ordinary
	// transfer after the SDK funding transaction to confirm its first payment.
	type fundingResult struct {
		authorization *wallet.AccountStorageAuthorization
		err           error
	}
	done := make(chan fundingResult, 1)
	go func() {
		authorization, err := f.manager.ConfirmAccountStorage(wallet.AccountStoragePaid, 100)
		done <- fundingResult{authorization, err}
	}()
	require.Eventually(t, func() bool {
		state := fetchTemplateAutopayView(t, network.Core, address.MustEncode())
		return state.Delegates[owner.Address].AmountPerBlock == "10"
	}, 30*time.Second, 200*time.Millisecond, "the root wallet's actual funding transaction must confirm")
	heartbeat := buildDKVSKeyPathAssetTransfer(t, owner, gasOuts[2], gas, 290000, 9000, owner)
	network.sendManyAndMine(t, []*wire.MsgTx{heartbeat}, 0)
	funded := <-done
	authorization, err := funded.authorization, funded.err
	require.NoError(t, err)
	require.NotEmpty(t, authorization.TransactionID)
	require.Equal(t, childID, f.manager.GetCurrentWalletId(), "funding must preserve the PWA selection")
	state := fetchTemplateAutopayView(t, network.Core, address.MustEncode())
	require.Equal(t, "10", state.Delegates[owner.Address].AmountPerBlock)
	childAddress := wallet.PublicKeyToP2TRAddress_SatsNet(f.manager.GetWallet().GetPubKey())
	_, childPaid := state.Delegates[childAddress]
	require.False(t, childPaid, "the root account pays even when another wallet is selected")
	f.activate(t)
	status, err := f.manager.GetAccountAutopayFundingStatus()
	require.NoError(t, err)
	require.True(t, status.Required)
	require.True(t, status.Ready)
	reused, err := f.manager.FundAccountAutopay()
	require.NoError(t, err)
	require.True(t, reused.Reused)
	require.Empty(t, reused.TransactionID)
}
