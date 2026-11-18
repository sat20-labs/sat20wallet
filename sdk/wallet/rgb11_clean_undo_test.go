package wallet

import (
	"context"
	"fmt"
	"testing"
	"time"

	sdkcommon "github.com/sat20-labs/sat20wallet/sdk/common"
	rgb "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"github.com/stretchr/testify/require"
)

// Unlike the faulted-carrier rollback, A has never observed the peer's spend.
// A failed B must restore A's old journal, not its obsolete spending permission.
func TestRGBUndoSpentCleanScope(t *testing.T) {
	old := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = old })
	ctx := context.Background()
	origin, phrase, err := NewInteralWallet(GetChainParam())
	require.NoError(t, err)
	_, recipient, err := NewInteralWallet(GetChainParam())
	require.NoError(t, err)
	script, err := AddrToPkScript(origin.GetAddress(), GetChainParam())
	require.NoError(t, err)
	base := &rgb11FlowEvidence{utxos: make(map[string]*rgb.BitcoinUTXO),
		rawTx: make(map[string][]byte), spendingTx: make(map[string]string)}
	chain := &rgb11AddressEvidence{rgb11FlowEvidence: base, statuses: make(map[string]*rgb.BitcoinTxStatus)}
	rpc := &rgb11FlowIndexer{}
	for marker := byte(201); marker < 205; marker++ {
		seedRGB11FlowEvidenceUTXO(t, base, script, 100_000, marker)
	}
	rebuildRGB11ProjectionTestIndexerUTXOs(base, rpc)
	accepted := newRGB11AcceptedEvidence(chain, rpc)
	remote := newRGB11MemoryDKVSHTTP()
	node := newRGB11MessageNodeClient(remote)
	owner := newRGB11ActivatedFlowAccount(t, phrase, remote, node, accepted, rpc)
	receiver := newRGB11ActivatedFlowAccount(t, recipient, remote, node, accepted, rpc)
	owner.rgbManager.scopeStates.stopReconciliations()
	receiver.rgbManager.scopeStates.stopReconciliations()
	issued, err := owner.IssueRGB11Asset(ctx, RGB11IssueRequest{
		Schema: "NIA", Ticker: "UNDO", Name: "Clean rollback", Precision: 2, Amounts: []uint64{100},
	})
	require.NoError(t, err)
	installRGB11PublicContractFixture(t, owner, remote, issued.ContractID)
	useRGB11TestCoreAuthority(t, receiver)
	_, err = owner.RefreshRGB11State(ctx)
	require.NoError(t, err)

	// Only the in-process Bitcoin service receives these SDK-signed bytes.
	// Neither device's transfer status, acknowledgment nor proof is patched.
	send := func(sender *Manager, amount string, confirmations int64) (*rgb.PendingTransfer, string) {
		t.Helper()
		invoice, err := receiver.CreateRGB11Invoice(RGB11InvoiceRequest{
			Mode: "witness", TransportMode: "out-of-band", ContractID: issued.ContractID,
			AmountRaw: amount, WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
		})
		require.NoError(t, err)
		prepared, err := sender.PrepareRGB11Transfer(ctx, RGB11SendRequest{
			Invoice: invoice.Invoice, FeeRate: 2, MinConfirmations: 1,
		})
		require.NoError(t, err)
		raw := []byte(prepared.RecipientConsignment)
		_, err = receiver.PrepareRGB11Consignment(ctx, invoice.RequestID, raw)
		require.NoError(t, err)
		txid, err := sender.BroadcastRGB11OutOfBand([]string{prepared.State.TransferID})
		require.NoError(t, err)
		setRGB11ProjectionTestTxConfirmations(t, chain, txid, confirmations)
		_, err = receiver.AcceptRGB11Consignment(ctx, invoice.RequestID, raw)
		require.NoError(t, err)
		_, err = sender.RefreshRGB11State(ctx)
		require.NoError(t, err)
		pending, err := sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
		require.NoError(t, err)
		require.Len(t, pending.ChangeSeals, 1)
		return pending, fmt.Sprintf("%s:%d", txid, pending.ChangeSeals[0].Vout)
	}
	first, p := send(owner, "40", 6)
	require.NoError(t, owner.EnsureAccount(owner.GetCurrentWalletId(), 1, "scope B", ""))
	peer := r1ManagedPeer(t, owner, accepted, rpc)
	second, q := send(peer, "25", 1)
	require.Contains(t, second.State.InputOutPoints, p)
	spend, err := chain.GetOutspend(p)
	require.NoError(t, err)
	require.True(t, spend.Spent)
	require.Equal(t, second.State.WitnessTxID, spend.SpendingTx)
	status, err := chain.GetTxStatus(spend.SpendingTx)
	require.NoError(t, err)
	require.True(t, status.Confirmed)
	require.EqualValues(t, 1, status.Confirmations)
	peerState, err := peer.GetRGB11State()
	require.NoError(t, err)
	require.Equal(t, uint64(35), rgb11AssetAmount(peerState.AvailableAssets, issued.AssetName))
	require.NoError(t, peer.SyncAccountManagementState(ctx))
	payloads, err := accountTestPayloads(peer, rgb11AccountManagedProviderID)
	require.NoError(t, err)
	catalog, err := owner.accountManagedDataCatalog()
	require.NoError(t, err)
	require.Len(t, catalog.Scopes, 2)
	a, b := catalog.Scopes[0], catalog.Scopes[1]
	require.EqualValues(t, 0, a.AccountIndex)
	require.EqualValues(t, 1, b.AccountIndex)
	require.Contains(t, payloads, a.ID())
	require.Contains(t, payloads, b.ID())
	published := accountTestRow(t, peer, rgb11AccountManagedProviderID, a.ID())
	require.NotNil(t, published.Baseline)
	require.Equal(t, published.Generation, published.Confirmed)
	require.False(t, published.Restoring)

	provider := &rgb11AccountManagedDataProvider{owner: owner}
	scopes, err := provider.accountsByScope()
	require.NoError(t, err)
	scopeB, err := owner.newScopedRGB11Manager(scopes[b.ID()])
	require.NoError(t, err)
	snapshot := func(m *rgb11Manager) *RGB11WalletSnapshot {
		t.Helper()
		id, err := m.RGB11WalletID()
		require.NoError(t, err)
		base, err := m.readRGB11SelectedBase(id)
		require.NoError(t, err)
		require.Empty(t, base.faults, "this is the ordinary rollback, not a prior ProofFault")
		return base.snapshot
	}
	lock := func(point, reason string) *LockedUtxo {
		t.Helper()
		row, err := owner.utxoLockerL1.readRGB11LockRow(point)
		require.NoError(t, err)
		require.Equal(t, row, owner.utxoLockerL1.GetLockedUtxoList()[point], "disk/RAM lock for %s", point)
		if reason == "" {
			require.Nil(t, row)
		} else {
			require.NotNil(t, row)
			require.Equal(t, reason, row.Reason)
		}
		return row
	}
	bScript, err := AddrToPkScript(scopes[b.ID()].Wallet.GetAddress(), GetChainParam())
	require.NoError(t, err)
	other := seedRGB11FlowEvidenceUTXO(t, base, bScript, 10_000, 207)
	require.NoError(t, owner.utxoLockerL1.TryReserve([]string{other}, "other-reservation", "scope-b-owner"))
	otherLock := lock(other, "other-reservation")
	require.Equal(t, "scope-b-owner", otherLock.ReservationID)
	beforeA, beforeB := snapshot(owner.rgbManager), snapshot(scopeB)
	before, err := owner.GetRGB11State()
	require.NoError(t, err)
	require.Equal(t, "ok", before.ConsistencyStatus)
	require.Equal(t, uint64(60), rgb11AssetAmount(before.AvailableAssets, issued.AssetName))
	require.Len(t, before.Transfers, 1)
	require.Equal(t, first.State.TransferID, before.Transfers[0].TransferID)
	require.True(t, rgb11ProjectionTestHasSettledProof(t, owner, p))
	lock(p, sdkcommon.UtxoLockRGB)
	require.False(t, rgb11ProjectionTestHasProof(t, owner, q))
	lock(q, "")
	broadcasts := func() int {
		base.mu.Lock()
		defer base.mu.Unlock()
		return base.broadcastCount
	}
	require.Equal(t, 2, broadcasts())

	// The first atomic flush applies A, B's second flush is rejected, the
	// third is A's compensating commit. No owner Refresh between these reads.
	failed := &failNthFlushDB{KVDB: owner.db, failAt: 2}
	func() {
		owner.db = failed
		defer func() { owner.db = failed.KVDB }()
		err = provider.Import(catalog, []AccountManagedDataPayload{payloads[a.ID()], payloads[b.ID()]})
	}()
	require.ErrorContains(t, err, "injected RGB11 post-broadcast persistence failure")
	require.NotContains(t, err.Error(), "rollback failed")
	require.Equal(t, 3, failed.count)
	require.Equal(t, 1, failed.hits)
	require.False(t, rgb11SnapshotChanged(beforeA, snapshot(owner.rgbManager)))
	require.False(t, rgb11SnapshotChanged(beforeB, snapshot(scopeB)))
	after, err := owner.GetRGB11State()
	require.NoError(t, err)
	require.Equal(t, "ok", after.ConsistencyStatus)
	require.Equal(t, before.Transfers, after.Transfers)
	require.Equal(t, uint64(0), rgb11AssetAmount(after.AvailableAssets, issued.AssetName))
	require.True(t, rgb11ProjectionTestHasSettledProof(t, owner, p), "keep the previous proof, not its spending permission")
	lock(p, sdkcommon.UtxoLockRGBPending)
	require.False(t, rgb11ProjectionTestHasProof(t, owner, q))
	for _, output := range after.Outputs {
		require.NotEqual(t, q, output.OutPointStr)
	}
	lock(q, "")
	inputs, _, _, _, _, total, err := owner.rgbManager.selectRGB11AllocationsOnce(issued.ContractID, 1, 1, nil)
	require.ErrorIs(t, err, ErrRGB11InsufficientBalance)
	require.Empty(t, inputs)
	require.Zero(t, total)
	require.Equal(t, otherLock, lock(other, "other-reservation"))
	require.Equal(t, 2, broadcasts(), "failed import or rollback broadcast a transaction")
}
