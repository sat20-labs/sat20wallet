package wallet

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/rgb11/issuance"
	"github.com/sat20-labs/rgb11/rejectlist"
	"github.com/sat20-labs/rgb11/schemas"
	"github.com/stretchr/testify/require"
)

func TestRGB11SharedChangeE2E(t *testing.T) {
	ctx := context.Background()
	oldChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = oldChain })

	chain := newCoreE2EChain()
	remote := newRGB11MemoryDKVSHTTP()
	client := newRGB11MessageNodeClient(remote)
	cfg := coreE2EConfig{Core: AccountIndexerLocation{Scheme: "http", Host: "dkvs.test", Proxy: "testnet"}}
	directory := t.TempDir()
	openSender := func() (*Manager, func()) {
		database := indexerdb.NewKVDB(directory)
		manager := coreNewE2EManagerWithDB(t, cfg, chain, database, "", remote)
		manager.SetServerNode(NewNode(client, "message.test", SERVER_NODE, client.CoreNodePubKey(), client.CoreNodePubKey()))
		useRGB11TestCoreAuthority(t, manager)
		closeWallet := sync.OnceFunc(func() {
			manager.Close()
			require.NoError(t, database.Close())
		})
		t.Cleanup(closeWallet)
		return manager, closeWallet
	}

	sender, closeSender := openSender()
	_, _, secret := activateRGB11AccountScopeRecovery(t, sender, coreE2ESenderMnemonic)
	defer zeroBytes(secret)
	receiver := newRGB11ActivatedFlowAccount(t, coreE2EReceiverMnemonic, remote, client, chain, &coreE2EL1Indexer{chain: chain})
	receiver.rgbManager.scopeStates.stopReconciliations()
	useRGB11TestCoreAuthority(t, receiver)
	chain.fund(t, sender.wallet.GetAddress(), 3)

	a, err := sender.IssueRGB11Asset(ctx, RGB11IssueRequest{
		Schema: "NIA", Ticker: "DUALA", Name: "Change test asset A", Amounts: []uint64{10},
	})
	require.NoError(t, err)
	require.Len(t, a.OutPoints, 1)
	source := a.OutPoints[0]
	installRGB11PublicContractFixture(t, sender, remote, a.ContractID)
	allocations, err := rgb11IssueAllocations([]string{source}, []uint64{10})
	require.NoError(t, err)
	second, err := issuance.Issue(issuance.Spec{
		Kind: schemas.NIA, Network: rgb11IssuanceNetwork(GetChainParam()),
		Ticker: "DUALB", Name: "Change test asset B", Allocations: allocations,
	})
	require.NoError(t, err)
	b, err := sender.ImportRGB11Contract(ctx, []byte(second.Armor))
	require.NoError(t, err)
	require.Equal(t, 1, b.Projected)
	require.NotEqual(t, a.ContractID, b.ContractID)
	installRGB11PublicContractFixture(t, sender, remote, b.ContractID)
	_, err = sender.RefreshRGB11State(ctx)
	require.NoError(t, err)

	assertOnOutput := func(wallet *Manager, outpoint string, asset common.AssetName, want uint64) string {
		t.Helper()
		store := wallet.rgbManager.projectionStore
		output, err := store.LoadOutput(outpoint)
		require.NoError(t, err)
		amount := output.GetAsset(&asset)
		require.NotNil(t, amount, "missing %s on %s", asset.String(), outpoint)
		require.Equal(t, want, amount.Value.Uint64(), "asset %s on %s", asset.String(), outpoint)
		require.NoError(t, store.AssertConsistent(outpoint, asset))
		proof, err := store.LoadProof(outpoint, asset)
		require.NoError(t, err)
		require.Contains(t, []string{"valid", "settled"}, proof.Status)
		return proof.ValidationHash
	}
	assertBalance := func(wallet *Manager, asset common.AssetName, want uint64) {
		t.Helper()
		value, err := wallet.GetRGB11AssetBalance(&asset)
		require.NoError(t, err)
		if want == 0 {
			require.True(t, value == nil || value.Sign() == 0, "unexpected balance for %s", asset.String())
			return
		}
		require.NotNil(t, value)
		require.Equal(t, want, value.Value.Uint64(), "balance for %s", asset.String())
	}
	assertOnOutput(sender, source, a.AssetName, 10)
	assertOnOutput(sender, source, b.AssetName, 10)

	invoice, err := receiver.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode: "witness", TransportMode: "out-of-band", ContractID: a.ContractID,
		AmountRaw: "1", WitnessVout: 1,
	})
	require.NoError(t, err)
	prepared, err := sender.PrepareRGB11Transfer(ctx, RGB11SendRequest{Invoice: invoice.Invoice, FeeRate: 2, MinConfirmations: 1})
	require.NoError(t, err)
	require.Contains(t, prepared.State.InputOutPoints, source)
	pending, err := sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	require.NoError(t, err)
	require.Len(t, pending.PreservedChanges, 1)
	require.Len(t, pending.ChangeSeals, 1)
	require.Len(t, pending.PreservedChanges[0].Seals, 1)
	require.Equal(t, pending.ChangeSeals[0].Vout, pending.PreservedChanges[0].Seals[0].Vout)
	change := rgb11PendingChangeOutpoints(pending)
	require.Len(t, change, 1)
	_, err = receiver.PrepareRGB11Consignment(ctx, invoice.RequestID, []byte(prepared.RecipientConsignment))
	require.NoError(t, err)
	txid, err := sender.BroadcastRGB11OutOfBand([]string{prepared.State.TransferID})
	require.NoError(t, err)
	require.Equal(t, prepared.TxID, txid)
	require.Equal(t, 1, chain.broadcastCount())
	chain.confirm(txid)
	_, err = receiver.AcceptRGB11Consignment(ctx, invoice.RequestID, []byte(prepared.RecipientConsignment))
	require.NoError(t, err)
	_, err = sender.RefreshRGB11State(ctx)
	require.NoError(t, err)
	_, err = receiver.RefreshRGB11State(ctx)
	require.NoError(t, err)
	spend, err := chain.GetOutspend(source)
	require.NoError(t, err)
	require.True(t, spend.Spent)
	rawTx, err := chain.GetRawTx(txid)
	require.NoError(t, err)
	witness, err := decodeRGB11BroadcastTx(rawTx)
	require.NoError(t, err)
	inputs := 0
	for _, input := range witness.TxIn {
		if input.PreviousOutPoint.String() == source {
			inputs++
		}
	}
	require.Equal(t, 1, inputs, "the shared UTXO is consumed exactly once")
	assertBalance(receiver, a.AssetName, 1)
	assertBalance(receiver, b.AssetName, 0)
	assertOnOutput(receiver, prepared.State.OutputOutPoints[0], a.AssetName, 1)
	assertBalance(sender, a.AssetName, 9)
	assertBalance(sender, b.AssetName, 10)
	proofA := assertOnOutput(sender, change[0], a.AssetName, 9)
	proofB := assertOnOutput(sender, change[0], b.AssetName, 10)
	t.Logf("Confirmed %s with recipient A=1 and A=9 B=10 on change %s", txid, change[0])

	walletID, err := sender.RGB11WalletID()
	require.NoError(t, err)
	closeSender()
	reloaded, _ := openSender()
	_, err = reloaded.UnlockWallet("password")
	require.NoError(t, err)
	reloadedID, err := reloaded.RGB11WalletID()
	require.NoError(t, err)
	require.Equal(t, walletID, reloadedID)
	require.Equal(t, proofA, assertOnOutput(reloaded, change[0], a.AssetName, 9))
	require.Equal(t, proofB, assertOnOutput(reloaded, change[0], b.AssetName, 10))
	require.NoError(t, reloaded.SyncAccountManagementState(ctx))
	_, err = reloaded.RefreshRGB11State(ctx)
	require.NoError(t, err)
	assertBalance(reloaded, a.AssetName, 9)
	assertBalance(reloaded, b.AssetName, 10)
	require.Equal(t, proofA, assertOnOutput(reloaded, change[0], a.AssetName, 9))
	require.Equal(t, proofB, assertOnOutput(reloaded, change[0], b.AssetName, 10))
	t.Logf("Reopened wallet and validated the original A=9 and B=10 proofs on %s", change[0])
}

func TestRGB11SharedRejectList(t *testing.T) {
	oldChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = oldChain })
	ctx := context.Background()
	chain := newCoreE2EChain()
	rpc := &coreE2EL1Indexer{chain: chain}
	wallet := NewInternalWalletWithMnemonic(coreE2ESenderMnemonic, "", &chaincfg.TestNet4Params)
	sender := newRGB11FlowManager(t, wallet, rpc, chain, 950).rgbManager
	chain.fund(t, wallet.GetAddress(), 3)
	a, err := sender.IssueRGB11Asset(ctx, RGB11IssueRequest{
		Schema: "NIA", Ticker: "DUALA", Name: "Target asset", Amounts: []uint64{10, 10},
	})
	require.NoError(t, err)
	require.Len(t, a.OutPoints, 2)
	shared, clean := a.OutPoints[0], a.OutPoints[1]
	require.Less(t, shared, clean, "selection tries the shared output first")
	allocations, err := rgb11IssueAllocations([]string{shared}, []uint64{7})
	require.NoError(t, err)
	rights, err := rgb11IssueAllocations([]string{shared}, []uint64{1})
	require.NoError(t, err)
	issued, err := issuance.Issue(issuance.Spec{
		Kind: schemas.IFA, Network: rgb11IssuanceNetwork(GetChainParam()), Ticker: "DUALB", Name: "Preserved asset",
		Allocations: allocations, InflationRights: rights, RejectListURL: "https://issuer.example/reject-list",
	})
	require.NoError(t, err)
	b, err := sender.ImportRGB11Contract(ctx, []byte(issued.Armor))
	require.NoError(t, err)
	proof, err := sender.projectionStore.LoadProof(shared, b.AssetName)
	require.NoError(t, err)
	opout, err := rgb11ProofOpout(proof)
	require.NoError(t, err)

	receiverKey := NewInternalWalletWithMnemonic(coreE2EReceiverMnemonic, "", &chaincfg.TestNet4Params)
	receiver := newRGB11FlowManager(t, receiverKey, rpc, chain, 951).rgbManager
	prepare := func(amount string) (*RGB11PreparedTransfer, error) {
		invoice, err := receiver.CreateRGB11Invoice(RGB11InvoiceRequest{
			Mode: "witness", TransportMode: "out-of-band", ContractID: a.ContractID, AmountRaw: amount, WitnessVout: 1,
		})
		require.NoError(t, err)
		return sender.PrepareRGB11Transfer(ctx, RGB11SendRequest{Invoice: invoice.Invoice, FeeRate: 2})
	}
	sender.rejectLists = rgb11StaticRejectLists{err: errors.New("offline")}
	_, err = prepare("1")
	require.ErrorIs(t, err, ErrRGB11RejectListUnavailable)

	sender.rejectLists = rgb11StaticRejectLists{list: rejectlist.Parse([]byte(opout.String()))}
	_, err = prepare("11")
	require.ErrorIs(t, err, ErrRGB11InsufficientBalance)
	prepared, err := prepare("1")
	require.NoError(t, err)
	require.Contains(t, prepared.State.InputOutPoints, clean)
	require.NotContains(t, prepared.State.InputOutPoints, shared)
}
