package wallet

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	corewallet "github.com/sat20-labs/rgb11/wallet"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

func r1ManagedPeer(t *testing.T, source *Manager, evidence rgb11wallet.BitcoinEvidenceProvider, rpc IndexerRPCClient) *Manager {
	t.Helper()
	remote := source.http.(*rgb11MemoryDKVSHTTP)
	peer := newAccountManagementAutoTestManager(t)
	configureRGB11DKVSTestManager(peer, remote)
	client := newRGB11MessageNodeClient(remote)
	peer.serverNode = NewNode(client, "message.test", SERVER_NODE, client.CoreNodePubKey(), client.CoreNodePubKey())
	peer.rgbManager.evidence = evidence
	peer.l1IndexerClient = NewIndexerRPCClientMgr()
	peer.l1IndexerClient.Set(rpc)
	peer.utxoLockerL1.rpcClient = rpc
	if err := restoreManagedImportTestWallet(t, peer, source); err != nil {
		t.Fatal(err)
	}
	if err := peer.SyncAccountManagementState(context.Background()); err != nil {
		t.Fatalf("restored peer baseline sync: %v", err)
	}
	peer.rgbManager.scopeStates.stopReconciliations()
	return peer
}

func r1Snapshot(t *testing.T, m *Manager) *RGB11WalletSnapshot {
	t.Helper()
	id, err := m.RGB11WalletID()
	if err != nil {
		t.Fatal(err)
	}
	s, _, err := m.rgbManager.exportRGB11WalletSnapshot(id)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRGB11R1ExpiredBlindPeerSync(t *testing.T) {
	_, a, _, evidence, rpc := newRGB11GenericSendFixture(t)
	a.rgbManager.scopeStates.stopReconciliations()
	script, err := AddrToPkScript(a.wallet.GetAddress(), GetChainParam())
	if err != nil {
		t.Fatal(err)
	}
	point := seedRGB11FlowEvidenceUTXO(t, evidence.rgb11FlowEvidence, script, 100000, 97)
	out := indexer.NewTxOutput(100000)
	out.OutPointStr = point
	out.OutValue.PkScript = script
	rpc.outputs[point] = out
	rpc.plain = append(rpc.plain, &indexerwire.TxOutputInfo{OutPoint: point, Value: 100000, PkScript: script})
	expiry := time.Now().Unix() + 4
	req, err := a.CreateRGB11Invoice(RGB11InvoiceRequest{Mode: "blind", TransportMode: "out-of-band", AmountRaw: "2", Expiry: expiry})
	if err != nil {
		t.Fatal(err)
	}
	b := r1ManagedPeer(t, a, evidence, rpc)
	reservations, err := b.rgbManager.projectionStore.ListReceiveReservations()
	if err != nil || len(reservations) != 1 {
		t.Fatalf("peer did not import blind reservation: %d %v", len(reservations), err)
	}
	const otherPoint = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd:0"
	if err := b.utxoLockerL1.TryReserve([]string{otherPoint}, rgb11wallet.LockReasonPending, "unrelated-owner"); err != nil {
		t.Fatal(err)
	}
	for time.Now().Unix() <= expiry {
		time.Sleep(10 * time.Millisecond)
	}
	if _, err = a.RefreshRGB11State(context.Background()); err != nil {
		t.Fatalf("A expiry publish: %v", err)
	}
	selected := decodeRGB11ProjectionRecoverySnapshot(t, exportRGB11RecoveryForTest(t, a))
	before := r1Snapshot(t, b)
	beforeLocks := b.utxoLockerL1.GetLockedUtxoList()
	originalDB := b.db
	failed := &failNthFlushDB{KVDB: originalDB, failAt: 1}
	b.db = failed
	importErr := b.rgbManager.importSelectedRGB11AccountRecoverySnapshot(selected)
	b.db = originalDB
	if importErr == nil || failed.count != 1 {
		t.Fatalf("injected cleanup commit did not fail: %v count=%d", importErr, failed.count)
	}
	if !reflect.DeepEqual(before, r1Snapshot(t, b)) || !reflect.DeepEqual(beforeLocks, b.utxoLockerL1.GetLockedUtxoList()) {
		t.Fatal("failed cleanup partially changed stores or locks")
	}
	if err = b.SyncAccountManagementState(context.Background()); err != nil {
		t.Fatalf("B must import A expired unused blind cleanup: %v", err)
	}
	if err = b.checkAccountManagedDataImport(); err != nil {
		t.Fatalf("import marker remains: %v", err)
	}
	if _, err = b.GetRGB11State(); err != nil {
		t.Fatalf("public read remains blocked: %v", err)
	}
	if got, err := b.rgbManager.projectionStore.ListReceiveReservations(); err != nil || len(got) != 0 {
		t.Fatalf("reservation remains: %d %v", len(got), err)
	}
	if locked := b.utxoLockerL1.GetLockedUtxoList(); locked[point] != nil || locked[otherPoint] == nil {
		t.Fatalf("cleanup changed wrong owner lock: expired=%+v unrelated=%+v selected=%s actual=%s", locked[point], locked[otherPoint], point, reservations[0].OutPoint)
	}
	history, err := b.rgbManager.engine.LoadReceive(req.RequestID)
	if err != nil || history.Invoice != req.Invoice {
		t.Fatal("cleanup lost immutable request history")
	}
}

func TestRGB11R1WitnessBatchIndependentRequests(t *testing.T) {
	for _, prepare := range []bool{true, false} {
		t.Run(fmt.Sprintf("prepare=%v", prepare), func(t *testing.T) {
			sender, recipient, imported, evidence, rpc := newRGB11GenericSendFixture(t)
			sender.rgbManager.scopeStates.stopReconciliations()
			recipient.rgbManager.scopeStates.stopReconciliations()
			requests := make([]*corewallet.ReceiveRequest, 2)
			invoices := make([]string, 2)
			for i := range requests {
				var err error
				requests[i], err = recipient.CreateRGB11Invoice(RGB11InvoiceRequest{Mode: "witness", TransportMode: "out-of-band", ContractID: imported.ContractID, AmountRaw: fmt.Sprint(1000 + i), Expiry: time.Now().Add(time.Hour).Unix()})
				if err != nil {
					t.Fatal(err)
				}
				invoices[i] = requests[i].Invoice
			}
			if bytes.Equal(requests[0].WitnessScript, requests[1].WitnessScript) {
				t.Fatal("fixture requires independent receive scripts")
			}
			prepared, err := sender.PrepareRGB11Transfer(context.Background(), RGB11SendRequest{Invoices: invoices, FeeRate: 2, MinConfirmations: 1})
			if err != nil {
				t.Fatal(err)
			}
			raw := []byte(prepared.RecipientConsignment)
			if prepare {
				for i, req := range requests {
					for repeat := 0; repeat < 2; repeat++ {
						if _, err = recipient.PrepareRGB11Consignment(context.Background(), req.RequestID, raw); err != nil {
							t.Fatalf("prepare recipient %d repeat %d: %v", i, repeat, err)
						}
					}
				}
			}
			pending, err := sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
			if err != nil {
				t.Fatal(err)
			}
			tx := wire.NewMsgTx(2)
			if err = tx.Deserialize(bytes.NewReader(pending.SignedTx)); err != nil {
				t.Fatal(err)
			}
			if prepare {
				if _, err = sender.BroadcastRGB11OutOfBand(prepared.State.BatchTransferIDs); err != nil {
					t.Fatal(err)
				}
			}
			txid := tx.TxHash().String()
			evidence.mu.Lock()
			evidence.rawTx[txid] = pending.SignedTx
			for _, in := range tx.TxIn {
				evidence.spendingTx[in.PreviousOutPoint.String()] = txid
			}
			for i, out := range tx.TxOut {
				point := fmt.Sprintf("%s:%d", txid, i)
				evidence.utxos[point] = &rgb11wallet.BitcoinUTXO{OutPoint: point, Value: out.Value, PkScript: out.PkScript, Confirmations: 1}
			}
			evidence.mu.Unlock()
			evidence.setStatus(txid, rgb11wallet.BitcoinTxStatus{TxID: txid, Confirmed: true, Confirmations: 1})
			var peer *Manager
			for i, req := range requests {
				for n := 0; n < 2; n++ {
					receipt, acceptErr := recipient.AcceptRGB11Consignment(context.Background(), req.RequestID, raw)
					if acceptErr != nil {
						t.Fatalf("accept recipient %d repeat %d: %v", i, n, acceptErr)
					}
					if receipt.TransferID != prepared.State.BatchID {
						t.Fatal("canonical receipt identity replaced by local transaction key")
					}
				}
				if i == 0 && !prepare {
					peer = r1ManagedPeer(t, recipient, evidence, rpc)
					prior, loadErr := peer.rgbManager.projectionStore.ListTransfers()
					if loadErr != nil || len(prior) != 1 {
						t.Fatalf("set extension peer must start with exactly one receive: %d %v", len(prior), loadErr)
					}
				}
			}
			// A restored ACKed second invoice is itself actionable background work.
			// Restore that branch after both accepts so the two devices are not writers
			// of the same prepared request. The direct-accept branch checks 1 -> 2 sync.
			if peer == nil {
				peer = r1ManagedPeer(t, recipient, evidence, rpc)
			} else if err = peer.SyncAccountManagementState(context.Background()); err != nil {
				t.Fatalf("cross-device receipt set extension: %v", err)
			}
			third, err := recipient.CreateRGB11Invoice(RGB11InvoiceRequest{Mode: "witness", TransportMode: "out-of-band", ContractID: imported.ContractID, AmountRaw: "1002", Expiry: time.Now().Add(time.Hour).Unix()})
			if err != nil {
				t.Fatal(err)
			}
			beforeWrong := r1Snapshot(t, recipient)
			if _, err = recipient.AcceptRGB11Consignment(context.Background(), third.RequestID, raw); err == nil {
				t.Fatal("different unmatched request overwrote batch receive")
			}
			if !reflect.DeepEqual(beforeWrong, r1Snapshot(t, recipient)) {
				t.Fatal("unmatched request partially committed")
			}
			if _, err = recipient.RefreshRGB11State(context.Background()); err != nil {
				t.Fatal(err)
			}
			assert := func(m *Manager) {
				t.Helper()
				state, err := m.GetRGB11State()
				if err != nil {
					t.Fatal(err)
				}
				if len(state.Transfers) != 2 || state.Transfers[0].TransferID == state.Transfers[1].TransferID {
					t.Fatalf("independent transfers lost: %d", len(state.Transfers))
				}
				balance, err := m.GetRGB11AssetBalance(&imported.AssetName)
				if err != nil || balance.Value.Uint64() != 2001 {
					t.Fatalf("balance must equal both outputs once: %v %v", balance, err)
				}
				for _, req := range requests {
					saved, err := m.rgbManager.engine.LoadReceive(req.RequestID)
					if err != nil || saved.TransferID == "" {
						t.Fatal("request transfer binding lost")
					}
				}
			}
			assert(recipient)
			replacement, err := newRGB11Manager(recipient, recipient.db, recipient.utxoLockerL1, evidence)
			if err != nil {
				t.Fatal(err)
			}
			recipient.rgbManager = replacement
			if err = replacement.selectRGB11Scope(); err != nil {
				t.Fatal(err)
			}
			assert(recipient)
			assert(peer)
			cold := r1ManagedPeer(t, recipient, evidence, rpc)
			assert(cold)
		})
	}
}

func TestRGB11R1ReceiptExpansionPeerSync(t *testing.T) {
	sender, _, imported, evidence, rpc := newRGB11GenericSendFixture(t)
	sender.rgbManager.scopeStates.stopReconciliations()
	exported, err := sender.ExportRGB11Contract(imported.ContractID)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(exported.ContractConsignmentBase64)
	if err != nil {
		t.Fatal(err)
	}
	point := rgb11GenericFixtureSource(t, sender, imported)
	original, err := evidence.GetUTXO(point)
	if err != nil {
		t.Fatal(err)
	}
	// A real raw Bitcoin spender makes this static contract metadata-only.
	spender := wire.NewMsgTx(2)
	op, err := wire.NewOutPointFromString(point)
	if err != nil {
		t.Fatal(err)
	}
	spender.AddTxIn(wire.NewTxIn(op, nil, nil))
	spender.AddTxOut(wire.NewTxOut(original.Value-1000, original.PkScript))
	var buf bytes.Buffer
	if err = spender.Serialize(&buf); err != nil {
		t.Fatal(err)
	}
	spentID := spender.TxHash().String()
	evidence.mu.Lock()
	evidence.spendingTx[point] = spentID
	evidence.rawTx[spentID] = buf.Bytes()
	delete(evidence.utxos, point)
	evidence.mu.Unlock()
	evidence.setStatus(spentID, rgb11wallet.BitcoinTxStatus{TxID: spentID, Confirmed: true, Confirmations: 6})
	remote := newRGB11MemoryDKVSHTTP()
	a := newRGB11ActivatedFlowAccount(t, "inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire", remote, newRGB11MessageNodeClient(remote), evidence, rpc)
	a.rgbManager.scopeStates.stopReconciliations()
	zero, err := a.ImportRGB11ContractFile(context.Background(), raw)
	if err != nil || zero.Projected != 0 {
		t.Fatalf("zero allocation import: %v %v", zero, err)
	}
	b := r1ManagedPeer(t, a, evidence, rpc)
	evidence.mu.Lock()
	delete(evidence.spendingTx, point)
	evidence.utxos[point] = original
	evidence.mu.Unlock()
	evidence.setStatus(spentID, rgb11wallet.BitcoinTxStatus{TxID: spentID})
	one, err := a.ImportRGB11ContractFile(context.Background(), raw)
	if err != nil || one.Projected != 1 {
		t.Fatalf("restored allocation import: %v %v", one, err)
	}
	before := r1Snapshot(t, b)
	if err = b.SyncAccountManagementState(context.Background()); err != nil {
		after := r1Snapshot(t, b)
		if !reflect.DeepEqual(before, after) {
			t.Fatal("failed expansion partially changed stores")
		}
		t.Fatalf("B must accept native historical zero-to-one receipt extension: %v", err)
	}
	balance, err := b.GetRGB11AssetBalance(&imported.AssetName)
	if err != nil || balance.Value.Uint64() != 50000 {
		t.Fatalf("restored allocation balance: %v %v", balance, err)
	}
	if err = b.SyncAccountManagementState(context.Background()); err != nil {
		t.Fatal(err)
	}
	r1AssertForgedReceiptExtensionRejected(t, b)
}

// Even a self-consistent, proof-free added receipt allocation needs native
// history. Otherwise an attacker can smuggle facts past proof-only validation.
func r1AssertForgedReceiptExtensionRejected(t *testing.T, manager *Manager) {
	t.Helper()
	before := r1Snapshot(t, manager)
	locks := manager.utxoLockerL1.GetLockedUtxoList()
	receipts, err := rgb11wallet.ValidationReceiptsFromProjectionSnapshot(before.ProjectionRecords)
	if err != nil {
		t.Fatal(err)
	}
	var stored *rgb11wallet.ValidationReceipt
	for _, receipt := range receipts {
		if len(receipt.Allocations) > 0 {
			stored = receipt
			break
		}
	}
	if stored == nil {
		t.Fatal("no receipt for extension forgery control")
	}
	forged := *stored
	forged.Allocations = append([]rgb11wallet.ValidatedAllocation(nil), stored.Allocations...)
	extra := stored.Allocations[0]
	extra.OperationID = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	extra.AssignmentIndex = 19
	extra.OutPoint = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee:1"
	forged.Allocations = append(forged.Allocations, extra)
	normalized, err := rgb11wallet.SelectHistoricalValidationReceipt(&rgb11wallet.HistoricalValidation{MaterialReceipt: &forged}, nil, forged.Allocations)
	if err != nil {
		t.Fatal(err)
	}
	candidate := *before
	candidate.ProjectionRecords, err = rgb11wallet.RebindValidatedConsignmentReceiptSnapshot(before.ProjectionRecords, stored, normalized)
	if err != nil {
		t.Fatal(err)
	}
	pkg, err := rgb11wallet.RecoveryPackageFromSnapshot(&candidate, 0)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := pkg.WalletSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	// This control must cross the structural subset gate and fail in native
	// history authentication, including when the forged item has no proof.
	if _, err = rgb11wallet.MergeSelectedRecoverySnapshot(before, selected); err != nil {
		t.Fatalf("forgery fixture failed before native authentication: %v", err)
	}
	if err = manager.rgbManager.importSelectedRGB11AccountRecoverySnapshot(selected); err == nil {
		t.Fatal("unverified extra receipt allocation accepted")
	}
	if !reflect.DeepEqual(before, r1Snapshot(t, manager)) || !reflect.DeepEqual(locks, manager.utxoLockerL1.GetLockedUtxoList()) {
		t.Fatal("forged receipt partially committed")
	}
}
