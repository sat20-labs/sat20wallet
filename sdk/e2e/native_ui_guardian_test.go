//go:build guardian

package e2e

import (
	"context"
	"testing"

	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	"github.com/stretchr/testify/require"
)

// Guardian coverage is explicitly selected, not an unconditional case inside
// the ordinary two-factor journey.
func TestSDKGuardianDisabled(t *testing.T) {
	f := newDKVSNoPluginTemplateFixtureWithArgs(t, map[string]int64{}, nil, nil, dkvsMinerArgs(t))
	waitForDKVSPeerReady(t, f.Network)
	cfg, _ := accountReviewConfig(t, f.Network)
	owner := newNativeUIDevice(t, cfg, accountSyncMnemonic(t, 222))
	ctx := context.Background()
	before := owner.mgr.GetWalletCatalog()
	_, err := owner.mgr.GetGuardianContact(owner.password)
	require.ErrorIs(t, err, wallet.ErrGuardianDisabled)
	_, err = owner.mgr.AcceptGuardianSetup(ctx, owner.password, "")
	require.ErrorIs(t, err, wallet.ErrGuardianDisabled)
	_, err = owner.mgr.ConfirmGuardianSetup(ctx, "", "")
	require.ErrorIs(t, err, wallet.ErrGuardianDisabled)
	_, err = owner.mgr.CreateGuardianRequest(ctx, "")
	require.ErrorIs(t, err, wallet.ErrGuardianDisabled)
	_, err = owner.mgr.CreateGuardianResponse(ctx, owner.password, "")
	require.ErrorIs(t, err, wallet.ErrGuardianDisabled)
	nativeUIConfirm(t, owner.mgr, nativeUIStorage(t, owner.mgr, "temporary", 100))
	request := nativeUISetupReq(owner.mgr, owner.password)
	request.Mode = "2of3"
	_, err = owner.mgr.CreateRecovery(ctx, request)
	require.ErrorIs(t, err, wallet.ErrGuardianDisabled)
	request.Mode = "2of2"
	created, err := owner.mgr.CreateRecovery(ctx, request)
	require.NoError(t, err, "Guardian rejection must not consume storage authorization")
	require.True(t, created.CanActivate)
	require.NoError(t, owner.mgr.AbortRecovery(created.SessionID))
	require.Equal(t, before, owner.mgr.GetWalletCatalog())
	view, err := owner.mgr.GetAccountView(ctx)
	require.NoError(t, err)
	require.Empty(t, view.RecoveryMode)
	require.Nil(t, view.Autopay)
}
