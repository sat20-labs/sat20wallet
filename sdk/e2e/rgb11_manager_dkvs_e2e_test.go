package e2e

import (
	"bytes"
	"errors"
	"testing"

	coresync "github.com/sat20-labs/rgb11/sync"
	"github.com/sat20-labs/sat20wallet/sdk/wallet"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

func TestRealSatoshiNetRGB11ManagerFreeLocalBackupRestoreAndConflict(t *testing.T) {
	fixture := newDKVSNoPluginTemplateFixtureWithArgs(t, map[string]int64{}, nil, nil, dkvsMinerArgs(t))
	waitForDKVSPeerReady(t, fixture.Network)

	primary, _ := newWalletManagerForNode(t, fixture.Network.Bootstrap, dkvsClientMnemonic)

	first, err := primary.CreateRGB11Invoice(wallet.RGB11InvoiceRequest{
		Mode: "witness", AmountRaw: "1", WitnessVout: 1,
	})
	require.NoError(t, err)
	second, err := primary.CreateRGB11Invoice(wallet.RGB11InvoiceRequest{
		Mode: "witness", AmountRaw: "2", WitnessVout: 1,
	})
	require.NoError(t, err)
	require.NotEqual(t, first.RequestID, second.RequestID)
	require.True(t, bytes.Equal(first.WitnessScript, second.WitnessScript))
	activeScript, err := wallet.AddrToPkScript(primary.GetWallet().GetAddress(), wallet.GetChainParam())
	require.NoError(t, err)
	require.Equal(t, activeScript, first.WitnessScript)

	if _, err := primary.CreateRGB11Invoice(wallet.RGB11InvoiceRequest{
		Mode: "unknown", AmountRaw: "1", WitnessVout: 1,
	}); err == nil {
		t.Fatal("unsupported RGB11 receive mode was accepted")
	}
	if _, err := primary.CreateRGB11Invoice(wallet.RGB11InvoiceRequest{
		Mode: "witness", AmountRaw: "not-a-number", WitnessVout: 1,
	}); err == nil {
		t.Fatal("invalid RGB11 amount was accepted")
	}

	// The second public mutation first synchronizes the first receive request,
	// so the explicit sync below advances the head to revision 2.
	head1, err := primary.SyncRGB11WalletState("", dkvsindexer.RecordOptions{})
	require.NoError(t, err)
	require.Equal(t, uint64(2), head1.Seq)
	walletID, err := primary.RGB11WalletID()
	require.NoError(t, err)
	pubKey := primary.GetWallet().GetPubKey().SerializeCompressed()
	accountID := dkvsindexer.AccountID(pubKey)
	headKey, err := dkvsindexer.PersonalKey(pubKey, wallet.RGB11WalletHeadPath(walletID))
	require.NoError(t, err)
	snapshotKey, err := dkvsindexer.BlobKey(accountID, wallet.RGB11WalletSnapshotBlobKey(walletID))
	require.NoError(t, err)
	bootstrapClient := dkvsClientForNode(t, fixture.Network.Bootstrap)
	for _, key := range []string{headKey, snapshotKey} {
		record, getErr := bootstrapClient.GetRecord(key)
		require.NoError(t, getErr)
		proof, proofErr := dkvsindexer.ParseFeeProof(record.FeeProof)
		require.NoError(t, proofErr)
		require.Equal(t, dkvsindexer.FeeModeFreeLocal, proof.Mode)
		require.Zero(t, record.PathGeneration)
		requireDKVSAbsent(t, fixture.Network.Core, key)
		requireDKVSAbsent(t, fixture.Network.Miner, key)
	}

	stale, _ := newWalletManagerForNode(t, fixture.Network.Bootstrap, dkvsClientMnemonic)
	activation, err := stale.ActivateRGB11WalletState(dkvsindexer.RecordVerificationOptions{})
	require.NoError(t, err)
	require.True(t, activation.Found)
	require.True(t, activation.Restored)
	require.Equal(t, uint64(2), activation.Head.Seq)
	for _, requestID := range []string{first.RequestID, second.RequestID} {
		request, loadErr := stale.GetRGB11ReceiveRequest(requestID)
		require.NoError(t, loadErr)
		require.Equal(t, requestID, request.RequestID)
	}
	_, err = stale.RestoreLatestRGB11WalletState("wrong-wallet-id",
		dkvsindexer.RecordVerificationOptions{})
	require.ErrorIs(t, err, coresync.ErrHeadWallet)

	// Both devices branch from revision 2. The primary wins revision 3; the
	// second writer must fail closed instead of overwriting the first branch.
	third, err := primary.CreateRGB11Invoice(wallet.RGB11InvoiceRequest{
		Mode: "witness", AmountRaw: "3", WitnessVout: 1,
	})
	require.NoError(t, err)
	fourth, err := stale.CreateRGB11Invoice(wallet.RGB11InvoiceRequest{
		Mode: "witness", AmountRaw: "4", WitnessVout: 1,
	})
	require.NoError(t, err)
	require.NotEmpty(t, third.RequestID)
	require.NotEmpty(t, fourth.RequestID)

	head2, err := primary.SyncRGB11WalletState("", dkvsindexer.RecordOptions{})
	require.NoError(t, err)
	require.Equal(t, uint64(3), head2.Seq)
	_, err = stale.SyncRGB11WalletState("", dkvsindexer.RecordOptions{})
	require.True(t, errors.Is(err, coresync.ErrHeadConflict), "stale RGB11 writer err=%v", err)

	other, _ := newWalletManagerForNode(t, fixture.Network.Core, dkvsClientMnemonic)
	otherActivation, err := other.ActivateRGB11WalletState(dkvsindexer.RecordVerificationOptions{})
	require.NoError(t, err)
	require.False(t, otherActivation.Found)
	require.False(t, otherActivation.Restored)
}
