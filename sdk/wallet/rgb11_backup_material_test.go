package wallet

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	coreconsignment "github.com/sat20-labs/rgb11/consignment"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

// The only shared boundary across the cold restore is Bitcoin evidence. The
// original database is closed before importing the account provider payload;
// subsequent transfers are built, signed and natively validated by the SDK.
func TestRGB11BackupMaterialsColdRestoreCanSpend(t *testing.T) {
	previousChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = previousChain })
	for _, accountIndex := range []uint32{0, 1} {
		for _, stage := range []string{"issued", "received", "sent-change"} {
			t.Run(fmt.Sprintf("account-%d/%s", accountIndex, stage), func(t *testing.T) {
				ctx := context.Background()
				chain := newCoreE2EChain()
				rpc := &coreE2EL1Indexer{chain: chain}
				wallet := NewInternalWalletWithMnemonic(coreE2ESenderMnemonic, "", &chaincfg.TestNet4Params)
				wallet.SetSubAccount(accountIndex)
				peerWallet := NewInternalWalletWithMnemonic(coreE2EReceiverMnemonic, "", &chaincfg.TestNet4Params)
				peerWallet.SetSubAccount(accountIndex)
				chain.fund(t, wallet.GetAddress(), 6)
				chain.fund(t, peerWallet.GetAddress(), 6)
				source := newRGB11FlowManager(t, wallet, rpc, chain, 940)
				peer := newRGB11FlowManager(t, peerWallet, rpc, chain, 941)
				issuer := source
				if stage == "received" {
					issuer = peer
				}
				issued, err := issuer.rgbManager.IssueRGB11Asset(ctx, RGB11IssueRequest{
					Schema: "IFA", Ticker: "BACKUP", Name: "Cold backup material fixture",
					Amounts: []uint64{100}, InflationAmounts: []uint64{900},
				})
				if err != nil {
					t.Fatal(err)
				}
				wantBalance, wantControls := uint64(100), 1
				var completed *rgb11wallet.PendingTransfer
				var settledRecipient []byte
				if stage == "received" {
					completed = completeRGB11BackupMaterialSend(t, peer, source, chain, issued.ContractID, "40", 1)
					wantBalance, wantControls = 40, 0
				} else if stage == "sent-change" {
					completed = completeRGB11BackupMaterialSend(t, source, peer, chain, issued.ContractID, "40", 1)
					wantBalance = 60
					settledRecipient, err = source.rgbManager.projectionStore.LoadObject(completed.RecipientObjectHash)
					if err != nil || len(settledRecipient) == 0 {
						t.Fatalf("settled recipient object unavailable before backup: %v", err)
					}
				}
				if _, err := source.rgbManager.RefreshRGB11State(ctx); err != nil {
					t.Fatal(err)
				}
				catalog, err := source.accountManagedDataCatalog()
				if err != nil || len(catalog.Scopes) != int(accountIndex)+1 {
					t.Fatalf("source account catalog scopes=%d err=%v", len(catalog.Scopes), err)
				}
				provider := &rgb11AccountManagedDataProvider{owner: source}
				payloads, err := provider.Export(catalog)
				if err != nil {
					t.Fatal(err)
				}
				// A settled transfer's polling/relay bookkeeping must not rewrite an
				// otherwise identical recovery package or reach the restored device.
				if stage == "sent-change" {
					completed.State.AckStatus = "polled-again"
					completed.State.RelayDurability = "temporary-cache-status"
					completed.State.RelayExpiry++
					completed.CreatedAt++
					if err := source.rgbManager.projectionStore.SavePendingTransferState(completed); err != nil {
						t.Fatal(err)
					}
				}
				afterPolling, err := provider.Export(catalog)
				if err != nil {
					t.Fatal(err)
				}
				source.rgbManager.scopeStates.stopReconciliations()
				if err := source.db.Close(); err != nil {
					t.Fatal(err)
				}
				source.db = nil
				restoredWallet := NewInternalWalletWithMnemonic(coreE2ESenderMnemonic, "", &chaincfg.TestNet4Params)
				restoredWallet.SetSubAccount(accountIndex)
				restored := newRGB11FlowManager(t, restoredWallet, rpc, chain, 942)
				// This test drives each post-restore refresh explicitly; keep the
				// independent chain reconciler from racing that same candidate.
				restored.rgbManager.scopeStates.stopReconciliations()
				if err := (&rgb11AccountManagedDataProvider{owner: restored}).Import(catalog, payloads); err != nil {
					t.Fatalf("cold provider import: %v", err)
				}
				if stage == "sent-change" {
					packageForReceiver, exportErr := restored.ExportRGB11SettledConsignment(completed.State.TransferID)
					if exportErr != nil || packageForReceiver == nil ||
						packageForReceiver.State.Status != "settled" ||
						packageForReceiver.TxID != completed.State.WitnessTxID ||
						!bytes.Equal([]byte(packageForReceiver.RecipientConsignment), settledRecipient) ||
						packageForReceiver.RecipientConsignmentBase64 == "" {
						t.Fatalf("cold settled out-of-band export lost the exact recipient object: package=%v err=%v", packageForReceiver, exportErr)
					}
					if _, resumeErr := restored.ResumeRGB11PreparedTransfer(completed.State.TransferID); resumeErr == nil {
						t.Fatal("settled export must not reopen prepared-transfer continuation")
					}
				}
				balance, err := restored.GetRGB11AssetBalance(&issued.AssetName)
				if err != nil || balance == nil || balance.Value.Uint64() != wantBalance {
					t.Fatalf("restored balance=%v want=%d err=%v", balance, wantBalance, err)
				}
				proofs, err := restored.rgbManager.projectionStore.ListProofs()
				if err != nil {
					t.Fatal(err)
				}
				controls := 0
				for _, proof := range proofs {
					if proof.AssetName.Type == "control" && proof.Status == "settled" {
						controls++
						if !restored.utxoLockerL1.IsLocked(proof.OutPoint) {
							t.Fatal("restored IFA control carrier is unprotected")
						}
					}
				}
				if controls != wantControls {
					t.Fatalf("restored IFA controls=%d want=%d", controls, wantControls)
				}
				beforeBroadcasts := chain.broadcastCount()
				resent := completeRGB11BackupMaterialSend(t, restored, peer, chain, issued.ContractID, "5", 1)
				var signed wire.MsgTx
				if signed.Deserialize(bytes.NewReader(resent.SignedTx)) != nil || !signed.HasWitness() ||
					chain.broadcastCount() != beforeBroadcasts+1 {
					t.Fatal("cold recovery did not construct, sign and broadcast the next transfer")
				}
				if wantControls != 0 {
					completeRGB11BackupMaterialInflation(t, restored, peer, chain, issued.ContractID, issued.AssetName, 30)
					if accountIndex == 0 && stage == "issued" {
						completeRGB11BackupMaterialInflation(t, restored, peer, chain, issued.ContractID, issued.AssetName, 20)
					}
				}
				// Assert the new material contract only after proving actual use.
				if len(payloads) != len(afterPolling) {
					t.Fatal("execution bookkeeping changed backup scope count")
				}
				for i, payload := range payloads {
					if payload.Scope != afterPolling[i].Scope || !bytes.Equal(payload.Payload, afterPolling[i].Payload) {
						t.Fatal("execution bookkeeping changed recovery materials")
					}
					pkg, err := rgb11wallet.DecodeRecoveryPackage(payload.Payload)
					if err != nil {
						t.Fatal(err)
					}
					pending, err := rgb11wallet.PendingTransfersFromProjectionSnapshot(pkg.ProjectionRecords)
					if err != nil {
						t.Fatal(err)
					}
					for _, value := range pending {
						if value.State.Status == "settled" && (value.CreatedAt != 0 ||
							value.State.AckStatus != "" || value.State.RelayDurability != "" || value.State.RelayExpiry != 0) {
							t.Fatal("settled backup still contains polling bookkeeping")
						}
						if stage == "sent-change" && value.State.TransferID == completed.State.TransferID &&
							value.ReservationID != completed.ReservationID {
							t.Fatal("settled backup lost the reservation identity needed after confirmation loss")
						}
					}
					// A received proof already carries its exact derivation path in
					// CarrierBinding; deleting its redundant ReceiveKey alone does not
					// prove loss of signing material. Remove referenced dependencies.
					for _, missing := range []string{"object-", "wallet/receive/"} {
						broken := *pkg
						broken.ProjectionRecords = append([]rgb11wallet.SnapshotRecord(nil), pkg.ProjectionRecords...)
						broken.EngineRecords = append([]rgb11wallet.SnapshotRecord(nil), pkg.EngineRecords...)
						removed := false
						if missing == "wallet/receive/" {
							removed = len(broken.EngineRecords) > 0
							broken.EngineRecords = nil
						} else {
							for j, record := range broken.ProjectionRecords {
								if strings.HasPrefix(record.Key, missing) {
									broken.ProjectionRecords = append(broken.ProjectionRecords[:j], broken.ProjectionRecords[j+1:]...)
									removed = true
									break
								}
							}
						}
						if removed && rgb11wallet.ValidateRecoveryPackage(&broken) == nil {
							t.Fatalf("incomplete recovery material accepted after removing %s", missing)
						}
					}
				}
			})
		}
	}
}

// A recovered 4010 must do more than remain locked and visible: the normal
// production API must consume it, sign and broadcast the 8000 transition, and
// deliver a newly issued 4000 allocation to the invoice recipient.
func completeRGB11BackupMaterialInflation(t *testing.T, issuer, recipient *Manager,
	chain *coreE2EChain, contractID string, assetName indexer.AssetName, amount uint64) {
	t.Helper()
	ctx := context.Background()
	before, err := recipient.GetRGB11AssetBalance(&assetName)
	if err != nil {
		t.Fatal(err)
	}
	beforeAmount := uint64(0)
	if before != nil {
		beforeAmount = before.Value.Uint64()
	}
	invoice, err := recipient.rgbManager.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode: "witness", TransportMode: "out-of-band", ContractID: contractID,
		AmountRaw: fmt.Sprintf("%d", amount), WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := issuer.rgbManager.PrepareRGB11Inflation(ctx, RGB11InflationRequest{
		Invoice: invoice.Invoice, FeeRate: 2, MinConfirmations: 1,
	})
	if err != nil {
		t.Fatalf("recovered IFA control cannot prepare another inflation: %v", err)
	}
	beforeBroadcasts := chain.broadcastCount()
	txid, err := issuer.rgbManager.BroadcastRGB11OutOfBand([]string{prepared.State.TransferID})
	if err != nil {
		t.Fatalf("recovered IFA control cannot sign and broadcast another inflation: %v", err)
	}
	chain.confirm(txid)
	pending, err := issuer.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	var signed wire.MsgTx
	if signed.Deserialize(bytes.NewReader(pending.SignedTx)) != nil || !signed.HasWitness() ||
		chain.broadcastCount() != beforeBroadcasts+1 {
		t.Fatal("recovered inflation did not sign and broadcast a witness transaction")
	}
	if _, err := recipient.rgbManager.AcceptRGB11Consignment(ctx, invoice.RequestID, pending.RecipientConsignment); err != nil {
		t.Fatalf("recipient cannot accept recovered issuer's inflation: %v", err)
	}
	for _, manager := range []*Manager{issuer, recipient} {
		if _, err := manager.rgbManager.RefreshRGB11State(ctx); err != nil {
			t.Fatal(err)
		}
	}
	after, err := recipient.GetRGB11AssetBalance(&assetName)
	if err != nil || after == nil || after.Value.Uint64() != beforeAmount+amount {
		t.Fatalf("inflation recipient balance=%v want=%d err=%v", after, beforeAmount+amount, err)
	}
}

func TestRGB11InflationPreservesFungibleStateOnTheSameControlCarrier(t *testing.T) {
	previousChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = previousChain })
	ctx := context.Background()
	chain := newCoreE2EChain()
	rpc := &coreE2EL1Indexer{chain: chain}
	issuerWallet := NewInternalWalletWithMnemonic(coreE2ESenderMnemonic, "", &chaincfg.TestNet4Params)
	recipientWallet := NewInternalWalletWithMnemonic(coreE2EReceiverMnemonic, "", &chaincfg.TestNet4Params)
	chain.fund(t, issuerWallet.GetAddress(), 6)
	chain.fund(t, recipientWallet.GetAddress(), 6)
	issuer := newRGB11FlowManager(t, issuerWallet, rpc, chain, 950)
	recipient := newRGB11FlowManager(t, recipientWallet, rpc, chain, 951)
	issued, err := issuer.rgbManager.IssueRGB11Asset(ctx, RGB11IssueRequest{
		Schema: "IFA", Ticker: "MIXIF", Name: "mixed carrier inflation",
		Amounts: []uint64{100}, InflationAmounts: []uint64{900},
	})
	if err != nil {
		t.Fatal(err)
	}
	completeRGB11BackupMaterialInflation(t, issuer, recipient, chain, issued.ContractID, issued.AssetName, 30)
	owned, err := issuer.GetRGB11AssetBalance(&issued.AssetName)
	if err != nil || owned == nil || owned.Value.Uint64() != 100 {
		t.Fatalf("original fungible allocation was not preserved after mixed IFA: balance=%v err=%v", owned, err)
	}
	control, err := rgb11wallet.NewContractAssetKey(issued.ContractID, "control")
	if err != nil {
		t.Fatal(err)
	}
	remaining, err := issuer.GetRGB11AssetBalance(&control)
	if err != nil || remaining == nil || remaining.Value.Uint64() != 870 {
		t.Fatalf("remaining control allowance was not preserved: balance=%v err=%v", remaining, err)
	}
	completeRGB11BackupMaterialSend(t, issuer, recipient, chain, issued.ContractID, "5", 1)
}

func TestRGB11ColdRestoredSettledOutOfBandSenderCanSupplyMissedRecipient(t *testing.T) {
	previousChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = previousChain })
	ctx := context.Background()
	chain := newCoreE2EChain()
	rpc := &coreE2EL1Indexer{chain: chain}
	senderWallet := NewInternalWalletWithMnemonic(coreE2ESenderMnemonic, "", &chaincfg.TestNet4Params)
	receiverWallet := NewInternalWalletWithMnemonic(coreE2EReceiverMnemonic, "", &chaincfg.TestNet4Params)
	chain.fund(t, senderWallet.GetAddress(), 6)
	chain.fund(t, receiverWallet.GetAddress(), 6)
	sender := newRGB11FlowManager(t, senderWallet, rpc, chain, 946)
	receiver := newRGB11FlowManager(t, receiverWallet, rpc, chain, 947)
	// This fixture drives confirmation and projection explicitly. Do not let
	// its own background reconciler race the private refresh under test.
	sender.rgbManager.scopeStates.stopReconciliations()
	receiver.rgbManager.scopeStates.stopReconciliations()
	issued, err := sender.rgbManager.IssueRGB11Asset(ctx, RGB11IssueRequest{
		Schema: "NIA", Ticker: "LATE", Name: "Late recipient recovery", Amounts: []uint64{100},
	})
	if err != nil {
		t.Fatal(err)
	}
	invoice, err := receiver.rgbManager.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode: "witness", TransportMode: "out-of-band", ContractID: issued.ContractID,
		AmountRaw: "40", WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := sender.rgbManager.PrepareRGB11Transfer(ctx, RGB11SendRequest{Invoice: invoice.Invoice, FeeRate: 2, MinConfirmations: 1})
	if err != nil {
		t.Fatal(err)
	}
	txid, err := sender.rgbManager.BroadcastRGB11OutOfBand([]string{prepared.State.TransferID})
	if err != nil {
		t.Fatal(err)
	}
	chain.confirm(txid)
	if _, err := sender.rgbManager.RefreshRGB11State(ctx); err != nil {
		t.Fatal(err)
	}
	settled, err := sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	if err != nil || settled.State.Status != "settled" {
		t.Fatalf("sender must settle before the recipient receives the out-of-band body: pending=%v err=%v", settled, err)
	}
	catalog, err := sender.accountManagedDataCatalog()
	if err != nil {
		t.Fatal(err)
	}
	payloads, err := (&rgb11AccountManagedDataProvider{owner: sender}).Export(catalog)
	if err != nil {
		t.Fatal(err)
	}
	sender.rgbManager.scopeStates.stopReconciliations()
	if err := sender.db.Close(); err != nil {
		t.Fatal(err)
	}
	sender.db = nil
	restored := newRGB11FlowManager(t,
		NewInternalWalletWithMnemonic(coreE2ESenderMnemonic, "", &chaincfg.TestNet4Params), rpc, chain, 948)
	restored.rgbManager.scopeStates.stopReconciliations()
	if err := (&rgb11AccountManagedDataProvider{owner: restored}).Import(catalog, payloads); err != nil {
		t.Fatalf("cold sender import: %v", err)
	}
	packageForReceiver, err := restored.ExportRGB11SettledConsignment(prepared.State.TransferID)
	if err != nil || packageForReceiver == nil || packageForReceiver.State.Status != "settled" {
		t.Fatalf("cold sender cannot export the settled recipient body: package=%v err=%v", packageForReceiver, err)
	}
	if packageForReceiver.State.DeliveryObjectID != "" || packageForReceiver.State.RelayRecordKey != "" ||
		packageForReceiver.State.ReceiveCapabilityKey != "" || packageForReceiver.State.NetworkBackupRef != "" {
		t.Fatal("historical recipient export leaked sender delivery or backup material")
	}
	storedPending, err := restored.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	decodedRecipient, err := coreconsignment.DecodeArmor(packageForReceiver.RecipientConsignment)
	if err != nil || rgb11SettledRecipientMatches(storedPending, decodedRecipient) != nil {
		t.Fatalf("original recipient proof does not match its invoice: %v", err)
	}
	for _, mutation := range []struct {
		name string
		edit func(*rgb11wallet.PendingTransfer)
	}{
		{"invoice", func(p *rgb11wallet.PendingTransfer) { p.State.Invoice = "invalid" }},
		{"recipient", func(p *rgb11wallet.PendingTransfer) { p.State.RecipientID = "another-recipient" }},
		{"contract", func(p *rgb11wallet.PendingTransfer) { p.State.Asset.Name.Ticker = "another-contract" }},
		{"vout", func(p *rgb11wallet.PendingTransfer) { p.State.RecipientVout++ }},
	} {
		t.Run("reject_"+mutation.name, func(t *testing.T) {
			candidate := *storedPending
			mutation.edit(&candidate)
			if err := rgb11SettledRecipientMatches(&candidate, decodedRecipient); err == nil {
				t.Fatal("settled export accepted mismatched recipient proof")
			}
		})
	}
	if _, err := receiver.rgbManager.AcceptRGB11Consignment(ctx, invoice.RequestID, []byte(packageForReceiver.RecipientConsignment)); err != nil {
		t.Fatalf("recipient cannot accept the recovered sender's original consignment: %v", err)
	}
	if _, err := receiver.rgbManager.RefreshRGB11State(ctx); err != nil {
		t.Fatal(err)
	}
	balance, err := receiver.GetRGB11AssetBalance(&issued.AssetName)
	if err != nil || balance == nil || balance.Value.Uint64() != 40 {
		t.Fatalf("late recipient balance=%v err=%v", balance, err)
	}
}

func TestRGB11BackupMaterialsColdRestoreRefreshAfterConfirmationLoss(t *testing.T) {
	previousChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = previousChain })
	for _, test := range []struct {
		name          string
		inMempool     bool
		confirmed     bool
		confirmations int64
	}{
		{name: "mempool", inMempool: true},
		{name: "below-required", confirmed: true, confirmations: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			chain := newCoreE2EChain()
			rpc := &coreE2EL1Indexer{chain: chain}
			wallet := NewInternalWalletWithMnemonic(coreE2ESenderMnemonic, "", &chaincfg.TestNet4Params)
			peerWallet := NewInternalWalletWithMnemonic(coreE2EReceiverMnemonic, "", &chaincfg.TestNet4Params)
			chain.fund(t, wallet.GetAddress(), 6)
			chain.fund(t, peerWallet.GetAddress(), 6)
			source := newRGB11FlowManager(t, wallet, rpc, chain, 943)
			peer := newRGB11FlowManager(t, peerWallet, rpc, chain, 944)
			issued, err := source.rgbManager.IssueRGB11Asset(ctx, RGB11IssueRequest{
				Schema: "NIA", Ticker: "RECONF", Name: "Cold restore confirmation loss",
				Amounts: []uint64{100},
			})
			if err != nil {
				t.Fatal(err)
			}
			completed := completeRGB11BackupMaterialSend(t, source, peer, chain, issued.ContractID, "40", 3)
			if completed.ReservationID == "" {
				t.Fatal("settled source fixture has no reservation identity")
			}
			catalog, err := source.accountManagedDataCatalog()
			if err != nil {
				t.Fatal(err)
			}
			payloads, err := (&rgb11AccountManagedDataProvider{owner: source}).Export(catalog)
			if err != nil {
				t.Fatal(err)
			}
			source.rgbManager.scopeStates.stopReconciliations()
			peer.rgbManager.scopeStates.stopReconciliations()
			if err := source.db.Close(); err != nil {
				t.Fatal(err)
			}
			source.db = nil
			restored := newRGB11FlowManager(t,
				NewInternalWalletWithMnemonic(coreE2ESenderMnemonic, "", &chaincfg.TestNet4Params), rpc, chain, 945)
			restored.rgbManager.scopeStates.stopReconciliations()
			if err := (&rgb11AccountManagedDataProvider{owner: restored}).Import(catalog, payloads); err != nil {
				t.Fatalf("cold provider import while settled: %v", err)
			}

			// Change only fresh Bitcoin evidence after a successful cold import.
			// The recovery package and the original database remain untouched.
			chain.mu.Lock()
			chain.status[completed.State.WitnessTxID] = rgb11wallet.BitcoinTxStatus{
				TxID: completed.State.WitnessTxID, InMempool: test.inMempool,
				Confirmed: test.confirmed, Confirmations: test.confirmations,
			}
			chain.mu.Unlock()
			broadcasts := chain.broadcastCount()
			result, err := restored.rgbManager.RefreshRGB11State(ctx)
			if err != nil {
				t.Fatalf("cold recovery cannot reconcile confirmation loss: %v", err)
			}
			if result.Reorged != 1 || result.Pending != 1 {
				t.Fatalf("confirmation loss did not reopen the settled send: %+v", result)
			}
			pending, err := restored.rgbManager.projectionStore.LoadPendingTransfer(completed.State.TransferID)
			if err != nil || pending.State.Status != "pending" || pending.ReservationID != completed.ReservationID {
				t.Fatalf("reopened transfer lost its reservation identity: pending=%+v err=%v", pending, err)
			}
			changes := rgb11PendingChangeOutpoints(pending)
			if len(changes) == 0 {
				t.Fatal("confirmation-loss fixture has no local change carrier")
			}
			locks := restored.utxoLockerL1.GetLockedUtxoList()
			for _, outpoint := range changes {
				lock := locks[outpoint]
				if lock == nil || lock.ReservationID != completed.ReservationID || lock.Reason != rgb11wallet.LockReasonPending {
					t.Fatalf("reopened change carrier lacks its pending owner: outpoint=%s lock=%+v", outpoint, lock)
				}
			}
			chain.confirm(completed.State.WitnessTxID)
			if _, err := restored.rgbManager.RefreshRGB11State(ctx); err != nil {
				t.Fatalf("reconfirm the recovered transfer: %v", err)
			}
			pending, err = restored.rgbManager.projectionStore.LoadPendingTransfer(completed.State.TransferID)
			if err != nil || pending.State.Status != "settled" || pending.ReservationID != completed.ReservationID {
				t.Fatalf("reconfirmed transfer changed its recovery identity: pending=%+v err=%v", pending, err)
			}
			if chain.broadcastCount() != broadcasts {
				t.Fatal("confirmation reconciliation unexpectedly broadcast a transaction")
			}
		})
	}
}

func completeRGB11BackupMaterialSend(t *testing.T, sender, receiver *Manager, chain *coreE2EChain,
	contractID, amount string, minConfirmations uint8) *rgb11wallet.PendingTransfer {
	t.Helper()
	ctx := context.Background()
	invoice, err := receiver.rgbManager.CreateRGB11Invoice(RGB11InvoiceRequest{
		Mode: "witness", TransportMode: "out-of-band", ContractID: contractID,
		AmountRaw: amount, WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := sender.rgbManager.PrepareRGB11Transfer(ctx, RGB11SendRequest{
		Invoice: invoice.Invoice, FeeRate: 2, MinConfirmations: minConfirmations,
	})
	if err != nil {
		t.Fatalf("prepare next transfer: %v", err)
	}
	txid, err := sender.rgbManager.BroadcastRGB11OutOfBand([]string{prepared.State.TransferID})
	if err != nil {
		t.Fatalf("sign and broadcast next transfer: %v", err)
	}
	chain.confirm(txid)
	pending, err := sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := receiver.rgbManager.AcceptRGB11Consignment(ctx, invoice.RequestID, pending.RecipientConsignment); err != nil {
		t.Fatalf("validate received history from restored sender: %v", err)
	}
	for _, manager := range []*Manager{sender, receiver} {
		if _, err := manager.rgbManager.RefreshRGB11State(ctx); err != nil {
			t.Fatal(err)
		}
	}
	completed, err := sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	if err != nil || completed.State.Status != "settled" {
		t.Fatalf("complete transfer: pending=%v err=%v", completed, err)
	}
	return completed
}
