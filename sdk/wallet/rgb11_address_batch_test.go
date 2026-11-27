package wallet

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	coreissuance "github.com/sat20-labs/rgb11/issuance"
	"github.com/sat20-labs/rgb11/schemas"
	"github.com/sat20-labs/rgb11/seals"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

func newRGB11ActivatedFlowAccount(t *testing.T, mnemonic string, remote *rgb11MemoryDKVSHTTP,
	client *rgb11MessageNodeClient, evidence rgb11wallet.BitcoinEvidenceProvider, rpc IndexerRPCClient) *Manager {
	t.Helper()
	manager := newAccountManagementAutoTestManager(t)
	configureRGB11DKVSTestManager(manager, remote)
	useRGB11TestCoreAuthority(t, manager)
	manager.serverNode = NewNode(client, "message.test", SERVER_NODE, client.CoreNodePubKey(), client.CoreNodePubKey())
	manager.rgbManager.evidence = evidence
	manager.l1IndexerClient = NewIndexerRPCClientMgr()
	manager.l1IndexerClient.Set(rpc)
	manager.utxoLockerL1.rpcClient = rpc
	if _, err := manager.ImportWallet(mnemonic, "password"); err != nil {
		t.Fatal(err)
	}
	if err := manager.InitializeAccountManagement("password"); err != nil {
		t.Fatal(err)
	}
	pkg, authorization, secret := rereviewRecoveryPackage(t, manager)
	if err := manager.ActivateAccountManagement(secret, "password", *authorization, pkg.Envelope.Locator, "account://"+pkg.Envelope.Locator.PackageID); err != nil {
		t.Fatal(err)
	}
	if err := manager.SyncAccountManagementState(context.Background()); err != nil {
		t.Fatal(err)
	}
	return manager
}

func newRGB11GenericSendFixture(t *testing.T) (*Manager, *Manager, *RGB11ImportResult, *rgb11AddressEvidence, *rgb11FlowIndexer) {
	return newRGB11GenericSendFixtureWithCarrier(t, 10_000)
}

// Public send cases use genuine account activation and stable synchronization.
// The local-only maintenance fixture below remains independent of this setup.
func newRGB11GenericSendFixtureWithCarrier(t *testing.T, carrierValue int64, prepare ...rgbColdPrepare) (*Manager, *Manager, *RGB11ImportResult, *rgb11AddressEvidence, *rgb11FlowIndexer) {
	t.Helper()
	return rgb11SendFixture(t, carrierValue, 0, prepare...)
}

func rgb11SendFixture(t *testing.T, carrierValue int64, precision uint8, prepare ...rgbColdPrepare) (*Manager, *Manager, *RGB11ImportResult, *rgb11AddressEvidence, *rgb11FlowIndexer) {
	t.Helper()
	if len(prepare) > 1 {
		t.Fatal("RGB source group accepts one preparation phase")
	}
	oldChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = oldChain })
	base := &rgb11FlowEvidence{utxos: make(map[string]*rgb11wallet.BitcoinUTXO), rawTx: make(map[string][]byte), spendingTx: make(map[string]string)}
	evidence := &rgb11AddressEvidence{rgb11FlowEvidence: base, statuses: make(map[string]*rgb11wallet.BitcoinTxStatus)}
	rpc := &rgb11FlowIndexer{outputs: make(map[string]*TxOutput)}
	remote := newRGB11MemoryDKVSHTTP()
	client := newRGB11MessageNodeClient(remote)

	sender := newRGB11ActivatedFlowAccount(t, "inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire", remote, client, evidence, rpc)
	recipient := newRGB11ActivatedFlowAccount(t, "comfort very add tuition senior run eight snap burst appear exile dutch", remote, client, evidence, rpc)
	if len(prepare) == 1 && prepare[0] != nil {
		prepare[0](sender, remote, evidence, rpc)
	}
	script, err := AddrToPkScript(sender.wallet.GetAddress(), GetChainParam())
	if err != nil {
		t.Fatal(err)
	}
	addOutput := func(value int64, marker byte) string {
		point := seedRGB11FlowEvidenceUTXO(t, base, script, value, marker)
		output := indexer.NewTxOutput(value)
		output.OutPointStr, output.OutValue.PkScript = point, append([]byte(nil), script...)
		rpc.outputs[point] = output
		rpc.plain = append(rpc.plain, &indexerwire.TxOutputInfo{OutPoint: point, Value: value, PkScript: append([]byte(nil), script...)})
		return point
	}
	// Only the intended carrier is available at issuance; selection order
	// cannot silently choose the later fee output instead.
	source := addOutput(carrierValue, 91)
	issued, err := sender.IssueRGB11Asset(context.Background(), RGB11IssueRequest{Schema: "NIA", Ticker: "SEND", Name: "RGB11 public send fixture", Precision: precision, Amounts: []uint64{50000}})
	if err != nil || issued == nil || len(issued.OutPoints) != 1 || issued.OutPoints[0] != source {
		t.Fatalf("issue exact controlled carrier: issued=%v err=%v", issued, err)
	}
	// Public account backups reference the shared, authenticated genesis object.
	installRGB11PublicContractFixture(t, sender, remote, issued.ContractID)
	useRGB11TestCoreAuthority(t, recipient)
	addOutput(100_000, 92)
	endpoint, err := recipient.EnableConfiguredRGB11AddressReceive(RGB11ReceiveCapabilityOptions{})
	if err != nil {
		t.Fatal(err)
	}
	target, err := mailboxSubscriptionTarget(endpoint.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	if err := recipient.SubscribeDKVSPrefix(target); err != nil {
		t.Fatal(err)
	}
	return sender, recipient, &RGB11ImportResult{ContractID: issued.ContractID, AssetName: issued.AssetName}, evidence, rpc
}

func rgb11GenericFixtureSource(t *testing.T, sender *Manager, imported *RGB11ImportResult) string {
	t.Helper()
	proofs, err := sender.rgbManager.projectionStore.ListProofs()
	if err != nil {
		t.Fatal(err)
	}
	for _, proof := range proofs {
		if proof.AssetName == imported.AssetName {
			return proof.OutPoint
		}
	}
	t.Fatal("fixture has no controlled RGB carrier")
	return ""
}

func newRGB11SendFixture(t *testing.T, rgb11Data bool) (*Manager, *Manager, *RGB11ImportResult, *rgb11AddressEvidence, *rgb11FlowIndexer) {
	t.Helper()
	oldChain := _chain
	_chain = "testnet"
	t.Cleanup(func() { _chain = oldChain })
	senderWallet := NewInternalWalletWithMnemonic(
		"inflict resource march liquid pigeon salad ankle miracle badge twelve smart wire", "", &chaincfg.TestNet4Params,
	)
	recipientWallet := NewInternalWalletWithMnemonic(
		"comfort very add tuition senior run eight snap burst appear exile dutch", "", &chaincfg.TestNet4Params,
	)
	senderScript, err := AddrToPkScript(senderWallet.GetAddress(), &chaincfg.TestNet4Params)
	if err != nil {
		t.Fatal(err)
	}

	sourceOutpoint := "14295d5bb1a191cdb6286dc0944df938421e3dfcbf0811353ccac4100c2068c5:1"
	plainOutpoint := "3333333333333333333333333333333333333333333333333333333333333333:0"
	baseEvidence := &rgb11FlowEvidence{
		utxos: map[string]*rgb11wallet.BitcoinUTXO{
			sourceOutpoint: {OutPoint: sourceOutpoint, Value: 10_000, PkScript: senderScript, Confirmations: 6},
		},
		rawTx:      make(map[string][]byte),
		spendingTx: make(map[string]string),
	}
	evidence := &rgb11AddressEvidence{rgb11FlowEvidence: baseEvidence, statuses: make(map[string]*rgb11wallet.BitcoinTxStatus)}
	if rgb11Data {
		baseEvidence.utxos = make(map[string]*rgb11wallet.BitcoinUTXO)
		sourceOutpoint = seedRGB11FlowEvidenceUTXO(t, baseEvidence, senderScript, 10_000, 91)
		plainOutpoint = seedRGB11FlowEvidenceUTXO(t, baseEvidence, senderScript, 100_000, 92)
	}
	rpc := &rgb11FlowIndexer{outputs: make(map[string]*TxOutput)}
	sourceOutput := indexer.NewTxOutput(10_000)
	sourceOutput.OutPointStr = sourceOutpoint
	sourceOutput.OutValue.PkScript = senderScript
	rpc.outputs[sourceOutpoint] = sourceOutput
	plainOutput := indexer.NewTxOutput(100_000)
	plainOutput.OutPointStr = plainOutpoint
	plainOutput.OutValue.PkScript = senderScript
	rpc.outputs[plainOutpoint] = plainOutput
	rpc.plain = []*indexerwire.TxOutputInfo{
		{OutPoint: sourceOutpoint, Value: 10_000, PkScript: senderScript},
		{OutPoint: plainOutpoint, Value: 100_000, PkScript: senderScript},
	}

	sender := newRGB11FlowManager(t, senderWallet, rpc, evidence, 101, accountGroupUnprepared)
	recipient := newRGB11FlowManager(t, recipientWallet, rpc, evidence, 102, accountGroupUnprepared)
	remote := newRGB11MemoryDKVSHTTP()
	messageClient := newRGB11MessageNodeClient(remote)
	configure := func(manager *Manager) {
		manager.serverNode = NewNode(
			messageClient, "message.test", SERVER_NODE,
			messageClient.CoreNodePubKey(), messageClient.CoreNodePubKey(),
		)
		prepareRGBEndpoint(t, manager, remote)
	}
	configure(sender)
	configure(recipient)
	var imported *RGB11ImportResult
	if rgb11Data {
		issued, issueErr := sender.rgbManager.IssueRGB11Asset(context.Background(), RGB11IssueRequest{
			Schema: "NIA", Ticker: "MAINT", Name: "RGB11 maintenance fixture", Amounts: []uint64{50000},
		})
		if issueErr != nil {
			t.Fatal(issueErr)
		}
		imported = &RGB11ImportResult{ContractID: issued.ContractID, AssetName: issued.AssetName}
	} else {
		contract, readErr := os.ReadFile("../../../rgb11/testvectors/rc2/nia-example.rgba")
		if readErr != nil {
			t.Fatal(readErr)
		}
		imported, err = sender.rgbManager.ImportRGB11Contract(context.Background(), contract)
	}
	if err != nil {
		t.Fatal(err)
	}

	endpoint, err := recipient.rgbManager.EnableConfiguredRGB11AddressReceive(RGB11ReceiveCapabilityOptions{})
	if err != nil {
		t.Fatal(err)
	}
	target, err := mailboxSubscriptionTarget(endpoint.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	if err := recipient.SubscribeDKVSPrefix(target); err != nil {
		t.Fatal(err)
	}
	return sender, recipient, imported, evidence, rpc
}

func assertRGB11ProjectionProofIdentity(t *testing.T, manager *Manager, stage string) {
	t.Helper()
	outputs, err := manager.rgbManager.projectionStore.ListOutputs()
	if err != nil {
		t.Fatalf("%s list outputs: %v", stage, err)
	}
	proofs, err := manager.rgbManager.projectionStore.ListProofs()
	if err != nil {
		t.Fatalf("%s list proofs: %v", stage, err)
	}
	proofIndex := make(map[string]bool, len(proofs))
	for _, proof := range proofs {
		if proof != nil {
			proofIndex[proof.OutPoint+"|"+proof.AssetName.String()] = true
		}
	}
	for _, output := range outputs {
		if output == nil {
			continue
		}
		for _, asset := range output.Assets {
			if asset.Name.Protocol != rgb11wallet.Protocol {
				continue
			}
			key := output.OutPointStr + "|" + asset.Name.String()
			if !proofIndex[key] {
				names := make([]string, 0, len(proofs))
				for _, proof := range proofs {
					if proof != nil && proof.OutPoint == output.OutPointStr {
						names = append(names, proof.AssetName.String())
					}
				}
				t.Fatalf("%s projection missing proof: output=%s asset=%s proofs_at_outpoint=%v",
					stage, output.OutPointStr, asset.Name.String(), names)
			}
		}
	}
}

func TestRGB11FullContractProjectionIdentityThroughBatchPrepare(t *testing.T) {
	sender, recipient, imported, _, _ := newRGB11GenericSendFixture(t)
	assertRGB11ProjectionProofIdentity(t, sender, "after-import")
	request := RGB11AddressSendRequest{
		ReceiverAddress:  recipient.wallet.GetAddress(),
		AssetName:        imported.AssetName,
		AmountRaw:        "20000",
		FeeRate:          2,
		MinConfirmations: 1,
	}
	if _, err := runRGB11ManagedOperation(sender, context.Background(), rgb11ManagedOperationNew,
		func(m *rgb11Manager) (*RGB11PreparedTransfer, error) {
			return m.prepareRGB11AddressBatch(context.Background(),
				[]RGB11AddressSendRequest{request, request}, dkvsindexer.RecordVerificationOptions{})
		}); err != nil {
		t.Fatal(err)
	}
	assertRGB11ProjectionProofIdentity(t, sender, "after-prepare")
}

func TestDirectBatchMailboxCommits(t *testing.T) {
	sender, recipient, imported, evidence, rpc := newRGB11GenericSendFixture(t)
	request := RGB11AddressSendRequest{ReceiverAddress: recipient.wallet.GetAddress(), AssetName: imported.AssetName, AmountRaw: "20000", FeeRate: 2, MinConfirmations: 1}
	prepared, err := runRGB11ManagedOperation(sender, context.Background(), rgb11ManagedOperationNew, func(m *rgb11Manager) (*RGB11PreparedTransfer, error) {
		return m.prepareRGB11AddressBatch(context.Background(), []RGB11AddressSendRequest{request, request}, dkvsindexer.RecordVerificationOptions{})
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.States) != 2 || prepared.States[0].TransferID == prepared.States[1].TransferID {
		t.Fatal("batch output identity lost")
	}
	for i, state := range prepared.States {
		_, err := sender.rgbManager.deliverRGBAddress(state.TransferID)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if _, err := sender.rgbManager.BroadcastRGB11AddressTransfer(state.TransferID); !errors.Is(err, ErrRGB11AddressDeliveryRequired) {
				t.Fatalf("partial mailbox commitment allowed broadcast: %v", err)
			}
			evidence.mu.Lock()
			sent := len(evidence.broadcasted) != 0
			evidence.mu.Unlock()
			if sent {
				t.Fatal("broadcast before every recipient persisted")
			}
		}
	}
	// A delivered recovery snapshot omits the PSBT. Resuming the same
	// transaction must still work after importing that persisted state.
	snapshot, err := sender.rgbManager.projectionStore.ExportSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	engineSnapshot, err := sender.rgbManager.engineStore.ExportSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	walletID, err := sender.rgbManager.RGB11WalletID()
	if err != nil {
		t.Fatal(err)
	}
	if err := sender.rgbManager.importRGB11WalletSnapshot(&RGB11WalletSnapshot{
		Version: rgb11wallet.WalletSnapshotVersion, WalletID: walletID,
		ProjectionRecords: snapshot, EngineRecords: engineSnapshot,
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	txid, fee, err := sender.ResumeRGB11Send(ctx, prepared.State.TransferID)
	if err != nil || txid != prepared.TxID || fee <= 0 {
		t.Fatalf("resume=%s fee=%d err=%v", txid, fee, err)
	}
	pending, err := sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	if err := acceptRGB11FlowTx(evidence, rpc, pending.SignedTx, txid); err != nil {
		t.Fatal(err)
	}
	evidence.mu.Lock()
	for point, out := range evidence.utxos {
		if allocationOutpointTxID(point) == txid {
			out.Confirmations = 1
		}
	}
	evidence.mu.Unlock()
	evidence.setStatus(txid, rgb11wallet.BitcoinTxStatus{TxID: txid, Confirmed: true, Confirmations: 1})
	received, err := recipient.SyncConfiguredRGB11AddressMailbox(context.Background(), dkvsindexer.RecordVerificationOptions{})
	if err != nil || received.Invalid != 0 || received.Received != 2 {
		t.Fatalf("receive confirmed offline batch: %+v, %v", received, err)
	}
	for _, state := range prepared.States {
		local, err := recipient.rgbManager.projectionStore.LoadTransferState(state.TransferID)
		if err != nil || len(local.OutputOutPoints) != 1 || local.OutputOutPoints[0] != fmt.Sprintf("%s:%d", txid, state.RecipientVout) {
			t.Fatalf("recipient state=%+v err=%v", local, err)
		}
	}
	if _, err := recipient.RefreshRGB11State(context.Background()); err != nil {
		t.Fatal(err)
	}
	balance, err := recipient.GetRGB11AssetBalance(&imported.AssetName)
	if err != nil || balance == nil || balance.Value.Uint64() != 40000 {
		t.Fatalf("batch receive balance=%v err=%v", balance, err)
	}
	if _, err := sender.RefreshRGB11State(context.Background()); err != nil {
		t.Fatal(err)
	}

	assertRGB11FrameworkRecovery(t, sender, recipient)
}

func TestGenericBatchSendAssetsRGB11(t *testing.T) {
	sender, recipient, imported, evidence, _ := newRGB11GenericSendFixture(t)
	type result struct {
		err error
		fee int64
	}
	done := make(chan result, 1)
	go func() {
		tx, fee, err := sender.BatchSendAssets(recipient.wallet.GetAddress(), imported.AssetName.String(), indexer.NewDecimalWithScale(20000, sender.getTickerInfo(&imported.AssetName).Divisibility).String(), 2, 2, nil)
		if err == nil && tx == nil {
			err = fmt.Errorf("missing sent transaction")
		}
		if err == nil && !tx.HasWitness() {
			err = fmt.Errorf("generic RGB send returned an unsigned transaction after broadcast")
		}
		if err == nil {
			var raw bytes.Buffer
			if encodeErr := tx.Serialize(&raw); encodeErr != nil {
				err = encodeErr
			}
			evidence.mu.Lock()
			matches := bytes.Equal(raw.Bytes(), evidence.broadcasted)
			evidence.mu.Unlock()
			if !matches {
				err = fmt.Errorf("generic RGB send did not return the exact broadcast witness")
			}
		}
		done <- result{err, fee}
	}()
	finished := false
	defer func() {
		if !finished {
			<-done
		}
	}()
	deadline := time.NewTimer(130 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case r := <-done:
			finished = true
			if r.err != nil || r.fee <= 0 {
				t.Fatalf("generic RGB batch fee=%d err=%v", r.fee, r.err)
			}
			evidence.mu.Lock()
			sent := len(evidence.broadcasted) > 0
			evidence.mu.Unlock()
			if !sent {
				t.Fatal("generic send succeeded without broadcast")
			}
			return
		case <-deadline.C:
			t.Fatal("generic RGB send did not finish")
		case <-tick.C:
			syncResult, err := recipient.rgbManager.SyncConfiguredRGB11AddressMailbox(context.Background(), dkvsindexer.RecordVerificationOptions{})
			if err != nil || syncResult.Invalid != 0 {
				transfers, transferErr := recipient.rgbManager.projectionStore.ListTransfers()
				reservations, reservationErr := recipient.rgbManager.loadRGB11Reservations()
				engineRecords, engineErr := recipient.rgbManager.engineStore.ExportSnapshot()
				t.Fatalf("receive=%+v err=%v state=%+v transferErr=%v reservations=%+v reservationErr=%v engineRecords=%+v engineErr=%v",
					syncResult, err, transfers, transferErr, reservations, reservationErr, engineRecords, engineErr)
			}
		}
	}
}

func TestRGB11ShortNameCollisionRejectsSecondImport(t *testing.T) {
	sender, recipient, _, evidence, rpc := newRGB11GenericSendFixture(t)
	sender.rgbManager.evidence = newRGB11AcceptedEvidence(evidence, rpc)
	oldChain := indexer.CHAIN
	indexer.CHAIN = "testnet"
	t.Cleanup(func() { indexer.CHAIN = oldChain })
	ctx := context.Background()
	remote := sender.http.(*rgb11MemoryDKVSHTTP)
	script, err := AddrToPkScript(sender.wallet.GetAddress(), GetChainParam())
	require.NoError(t, err)

	// These two genuine genesis consignments deterministically share twin@7c9b.
	var shortName indexer.AssetName
	var firstID string
	for i, timestamp := range []int64{1730000298, 1730000292} {
		source := seedRGB11FlowEvidenceUTXO(t, evidence.rgb11FlowEvidence, script, 10_000, byte(93+i))
		output := indexer.NewTxOutput(10_000)
		output.OutPointStr, output.OutValue.PkScript = source, script
		rpc.outputs[source] = output
		point, err := wire.NewOutPointFromString(source)
		require.NoError(t, err)
		seal, err := seals.NewGraphBlindSeal(point.Hash[:], point.Index, 1)
		require.NoError(t, err)
		issued, err := coreissuance.Issue(coreissuance.Spec{
			Kind: schemas.NIA, Network: coreissuance.BitcoinTestnet4, Ticker: "TWIN",
			Name: "RGB11 short name collision", Precision: uint8(i + 1), Timestamp: timestamp,
			Allocations: []coreissuance.Allocation{{Seal: seal, Amount: 50_000}},
		})
		require.NoError(t, err)
		name, err := rgb11wallet.NewContractAssetKey(issued.ContractID, "f", "TWIN")
		require.NoError(t, err)
		imported, err := sender.ImportRGB11Contract(ctx, []byte(issued.Armor))
		if i == 1 {
			require.Equal(t, shortName, name)
			require.ErrorIs(t, err, dkvsindexer.ErrWriteConflict)
			require.Nil(t, imported)
			info, err := loadTickerInfo(sender.db, &shortName)
			require.NoError(t, err)
			require.Equal(t, firstID, info.ContractID)
			balance, err := sender.GetRGB11AssetBalance(&shortName)
			require.NoError(t, err)
			require.EqualValues(t, 50_000, balance.Value.Uint64())
			sender.RemoveAllTickerInfo()
			imported, err = sender.ImportRGB11Contract(ctx, []byte(issued.Armor))
			require.Error(t, err, "the existing projection must own the name even after cache clear")
			require.Nil(t, imported)
			// The second wallet has never imported this name. The signed
			// registration alone must prevent import of a different contract.
			recipient.rgbManager.evidence = newRGB11AcceptedEvidence(evidence, rpc)
			seedRGB11RegistryName(t, sender, shortName, "TWIN", firstID)
			_, err = recipient.db.Read(rgb11TickerBindingKey(shortName))
			require.ErrorIs(t, err, indexer.ErrKeyNotFound)
			view, err := recipient.ImportRGBContract(ctx, []byte(issued.Armor))
			require.ErrorIs(t, err, dkvsindexer.ErrWriteConflict)
			require.Nil(t, view)
			_, err = recipient.db.Read(rgb11TickerBindingKey(shortName))
			require.ErrorIs(t, err, indexer.ErrKeyNotFound)

			rebuilt := sender.GetTickerInfo(&shortName)
			require.NotNil(t, rebuilt)
			require.Equal(t, firstID, rebuilt.ContractID)
			return
		}
		require.NoError(t, err)
		require.Equal(t, 1, imported.Projected)
		installRGB11PublicContractFixture(t, sender, remote, imported.ContractID)
		shortName = imported.AssetName
		firstID, err = rgb11wallet.RGB11ContractIDHex(imported.ContractID)
		require.NoError(t, err)
	}
}

func TestRGB11SendRejectsCarriersContainingOtherAssets(t *testing.T) {
	for _, protocol := range []string{indexer.PROTOCOL_NAME_ORDX, indexer.PROTOCOL_NAME_RUNES, indexer.PROTOCOL_NAME_BRC20} {
		t.Run(protocol, func(t *testing.T) {
			sender, recipient, imported, evidence, rpc := newRGB11GenericSendFixture(t)
			source := rgb11GenericFixtureSource(t, sender, imported)
			rpc.outputs[source].Assets = indexer.TxAssets{{Name: indexer.AssetName{Protocol: protocol, Type: "f", Ticker: "OTHER"}, Amount: *indexer.NewDefaultDecimal(7)}}
			request := RGB11AddressSendRequest{ReceiverAddress: recipient.wallet.GetAddress(), AssetName: imported.AssetName, AmountRaw: "20000", FeeRate: 2, MinConfirmations: 1}
			_, _, err := sender.rgbManager.PrepareConfiguredRGB11AddressTransfer(context.Background(), request, dkvsindexer.RecordVerificationOptions{})
			if !errors.Is(err, ErrRGB11AssetPreservation) {
				t.Fatalf("mixed carrier was not refused: %v", err)
			}
			evidence.mu.Lock()
			sent := len(evidence.broadcasted) != 0
			evidence.mu.Unlock()
			if sent {
				t.Fatal("mixed carrier broadcast")
			}
			transfers, err := sender.rgbManager.projectionStore.ListTransfers()
			if err != nil || len(transfers) != 0 {
				t.Fatalf("failed preservation check persisted transfer: %v %v", transfers, err)
			}
		})
	}
}

func TestRGB11FeeSelectionRejectsStalePlainAssetView(t *testing.T) {
	sender, recipient, imported, evidence, rpc := newRGB11GenericSendFixtureWithCarrier(t, 330)
	source := rgb11GenericFixtureSource(t, sender, imported)
	unsafe := rpc.plain[len(rpc.plain)-1].OutPoint
	plain := seedRGB11FlowEvidenceUTXO(t, evidence.rgb11FlowEvidence, rpc.outputs[source].OutValue.PkScript, 10_000, 93)
	// Force an extra fee input. The plain-list cache is intentionally stale.
	rpc.outputs[unsafe].Assets = indexer.TxAssets{{Name: indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_ORDX, Type: "f", Ticker: "OTHER"}, Amount: *indexer.NewDefaultDecimal(7)}}
	plainOutput := indexer.NewTxOutput(10000)
	plainOutput.OutPointStr = plain
	plainOutput.OutValue.PkScript = rpc.outputs[source].OutValue.PkScript
	rpc.outputs[plain] = plainOutput
	rpc.plain = append([]*indexerwire.TxOutputInfo{{OutPoint: plain, Value: 10000, PkScript: plainOutput.OutValue.PkScript}}, rpc.plain...)
	request := RGB11AddressSendRequest{ReceiverAddress: recipient.wallet.GetAddress(), AssetName: imported.AssetName, AmountRaw: "20000", FeeRate: 2, MinConfirmations: 1}
	prepared, _, err := sender.rgbManager.PrepareConfiguredRGB11AddressTransfer(context.Background(), request, dkvsindexer.RecordVerificationOptions{})
	if err != nil {
		t.Fatal(err)
	}
	foundPlain := false
	for _, point := range prepared.State.InputOutPoints {
		if point == unsafe {
			t.Fatal("RGB fee selection consumed another asset")
		}
		if point == plain {
			foundPlain = true
		}
	}
	if !foundPlain {
		t.Fatal("test did not force the clean fee input")
	}
}

func TestRGB11SendDoesNotUseAnotherRGBAssetForFees(t *testing.T) {
	sender, recipient, imported, evidence, rpc := newRGB11GenericSendFixtureWithCarrier(t, 330)
	source := rgb11GenericFixtureSource(t, sender, imported)
	other := rpc.plain[len(rpc.plain)-1].OutPoint
	plain := seedRGB11FlowEvidenceUTXO(t, evidence.rgb11FlowEvidence, rpc.outputs[source].OutValue.PkScript, 10_000, 93)
	evidence.utxos[other] = &rgb11wallet.BitcoinUTXO{OutPoint: other, Value: 100000, PkScript: rpc.outputs[other].OutValue.PkScript, Confirmations: 6}
	issued, err := sender.IssueRGB11Asset(context.Background(), RGB11IssueRequest{Schema: "NIA", Ticker: "OTHER", Name: "Other RGB fee protection", Amounts: []uint64{50}})
	if err != nil || len(issued.OutPoints) != 1 || issued.OutPoints[0] != other {
		t.Fatalf("issue second RGB asset: %+v %v", issued, err)
	}
	if err := sender.utxoLockerL1.UnlockUtxo(other); err != nil {
		t.Fatal(err)
	}
	out := indexer.NewTxOutput(10000)
	out.OutPointStr = plain
	out.OutValue.PkScript = rpc.outputs[source].OutValue.PkScript
	rpc.outputs[plain] = out
	rpc.plain = append([]*indexerwire.TxOutputInfo{{OutPoint: plain, Value: 10000, PkScript: out.OutValue.PkScript}}, rpc.plain...)
	request := RGB11AddressSendRequest{ReceiverAddress: recipient.wallet.GetAddress(), AssetName: imported.AssetName, AmountRaw: "20000", FeeRate: 2, MinConfirmations: 1}
	prepared, _, err := sender.rgbManager.PrepareConfiguredRGB11AddressTransfer(context.Background(), request, dkvsindexer.RecordVerificationOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, point := range prepared.State.InputOutPoints {
		if point == other {
			t.Fatal("RGB send used another RGB asset for fees")
		}
	}
	balance, err := sender.GetRGB11AssetBalance(&issued.AssetName)
	if err != nil || balance == nil || balance.Value.Uint64() != 50 {
		t.Fatalf("other RGB balance=%v err=%v", balance, err)
	}
}

func TestRGB11SharedAssetFault(t *testing.T) {
	sender, recipient, imported, evidence, _ := newRGB11GenericSendFixture(t)
	source := rgb11GenericFixtureSource(t, sender, imported)
	allocations, err := rgb11IssueAllocations([]string{source}, []uint64{7})
	if err != nil {
		t.Fatal(err)
	}
	// Import a second real genesis sealed to the same Bitcoin output. This is
	// possible for externally created RGB contracts even though our issuer
	// deliberately selects only plain outputs.
	issued, err := coreissuance.Issue(coreissuance.Spec{
		Kind: schemas.NIA, Network: rgb11IssuanceNetwork(&chaincfg.TestNet4Params),
		Ticker: "SHARED", Name: "Shared carrier asset", Allocations: allocations,
	})
	if err != nil {
		t.Fatal(err)
	}
	other, err := sender.ImportRGB11Contract(context.Background(), []byte(issued.Armor))
	if err != nil || other.Projected != 1 {
		t.Fatalf("import shared carrier: %+v %v", other, err)
	}
	request := RGB11AddressSendRequest{ReceiverAddress: recipient.wallet.GetAddress(), AssetName: imported.AssetName, AmountRaw: "20000", FeeRate: 2, MinConfirmations: 1}
	proof, err := sender.rgbManager.projectionStore.LoadProof(source, other.AssetName)
	if err != nil {
		t.Fatal(err)
	}
	proof.Status = "inconsistent"
	if err := sender.rgbManager.projectionStore.SaveProofState(proof); err != nil {
		t.Fatal(err)
	}
	_, _, err = sender.rgbManager.PrepareConfiguredRGB11AddressTransfer(context.Background(), request, dkvsindexer.RecordVerificationOptions{})
	if !errors.Is(err, ErrRGB11AssetPreservation) {
		t.Fatalf("invalid proof did not prevent spending shared carrier: %v", err)
	}
	transfers, err := sender.rgbManager.projectionStore.ListTransfers()
	if err != nil || len(transfers) != 0 {
		t.Fatalf("failed preservation check persisted transfer: %v %v", transfers, err)
	}
	evidence.mu.Lock()
	sent := len(evidence.broadcasted) != 0
	evidence.mu.Unlock()
	if sent {
		t.Fatal("shared carrier was broadcast")
	}
}

func TestRGB11DirectReservationResumesAfterSDKRestart(t *testing.T) {
	sender, recipient, imported, evidence, rpc := newRGB11GenericSendFixture(t)
	sourceOutpoint := rgb11GenericFixtureSource(t, sender, imported)
	accepted := newRGB11AcceptedEvidence(evidence, rpc)
	sender.rgbManager.evidence = accepted
	request := RGB11AddressSendRequest{
		ReceiverAddress:  recipient.wallet.GetAddress(),
		AssetName:        imported.AssetName,
		AmountRaw:        "20000",
		FeeRate:          2,
		MinConfirmations: 1,
	}
	prepared, _, err := sender.PrepareConfiguredRGB11AddressTransfer(
		context.Background(), request, dkvsindexer.RecordVerificationOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.State == nil || !prepared.State.AddressMode {
		t.Fatalf("prepared Direct state=%+v", prepared.State)
	}
	unsigned := assertRGB11UnsignedPending(t, sender.rgbManager, prepared.State.TransferID)
	_, err = sender.rgbManager.deliverRGBAddress(prepared.State.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	assertRGB11UnsignedPending(t, sender.rgbManager, prepared.State.TransferID)
	// Simulate the RGB SDK module being recreated while keeping only durable DB,
	// reservation, wallet and transport state. Stop the old module's background
	// workers first, just as a real runtime shutdown must do before DB teardown.
	oldRGB := sender.rgbManager
	oldRGB.scopeStates.stopReconciliations()
	restarted, err := newRGB11Manager(sender, sender.db, sender.utxoLockerL1, accepted)
	if err != nil {
		t.Fatal(err)
	}
	sender.rgbManager = restarted
	if err := restarted.selectRGB11Scope(); err != nil {
		t.Fatal(err)
	}
	// A new RGB module starts unverified, like a cold wallet runtime. Obtain
	// fresh chain evidence through the public API before resuming the ACK.
	if _, err := sender.RefreshRGB11State(context.Background()); err != nil {
		t.Fatalf("refresh restarted source before ACK: %v", err)
	}
	sourceProof, err := restarted.projectionStore.LoadProof(sourceOutpoint, imported.AssetName)
	if err != nil || sourceProof == nil || sourceProof.OutPoint != sourceOutpoint {
		t.Fatalf("restarted original source is not verified: proof_present=%t err=%v", sourceProof != nil, err)
	}
	logRGB11BroadcastState(t, "before-restart-ack", sender, prepared.State.TransferID, accepted)
	resumed, err := sender.SyncConfiguredRGB11AddressMailbox(
		context.Background(), dkvsindexer.RecordVerificationOptions{},
	)
	logRGB11BroadcastState(t, "after-restart-ack", sender, prepared.State.TransferID, accepted)
	if err != nil || resumed == nil || resumed.ACKs != 0 || resumed.Invalid != 0 {
		t.Fatalf("restarted module did not consume the original ACK: result=%+v err=%v", resumed, err)
	}

	pending, err := restarted.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	if !validRGB11SigningTransition(unsigned, pending) {
		t.Fatal("restarted module did not sign the exact restored unsigned operation")
	}
	if !rgb11BroadcastCompleteStatus(pending.State.Status) ||
		pending.State.DeliveryAcknowledged || !rgbDeliveryReady(pending) {
		t.Fatalf("durable Direct reservation did not resume: %+v", pending.State)
	}
	reservations, err := restarted.loadRGB11Reservations()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, reservation := range reservations {
		if reservation.State != nil && reservation.State.TransferID == prepared.State.TransferID {
			found = true
			if reservation.State.Status != pending.State.Status ||
				reservation.State.AckStatus != pending.State.AckStatus {
				t.Fatalf("framework reservation diverged from Direct transfer: resv=%+v pending=%+v",
					reservation.State, pending.State)
			}
		}
	}
	if !found {
		t.Fatal("Direct transfer has no framework reservation after restart")
	}
	evidence.mu.Lock()
	broadcastCount := evidence.broadcastCount
	broadcasted := append([]byte(nil), evidence.broadcasted...)
	evidence.mu.Unlock()
	if broadcastCount != 1 || len(broadcasted) == 0 {
		t.Fatalf("Direct transfer broadcast count=%d bytes=%d", broadcastCount, len(broadcasted))
	}
}

// A fresh Direct intent binds its known mailbox policy before publication.
// An offline prepared device must retain the same immutable recovery identity.
func TestRGB11DirectPreparedMailboxPolicyMatchesDelivery(t *testing.T) {
	sender, recipient, imported, _, _ := newRGB11GenericSendFixture(t)
	prepared, _, err := sender.PrepareConfiguredRGB11AddressTransfer(context.Background(), RGB11AddressSendRequest{
		ReceiverAddress: recipient.wallet.GetAddress(), AssetName: imported.AssetName,
		AmountRaw: "20000", FeeRate: 2, MinConfirmations: 1,
	}, dkvsindexer.RecordVerificationOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if prepared == nil || prepared.State == nil {
		t.Fatal("Direct preparation returned no state")
	}
	preparedPolicy := prepared.State.DeliveryTemporary
	preparedRelay, preparedAck := prepared.State.RelayRecordKey, prepared.State.AckRecordKey
	if preparedRelay == "" || preparedAck == "" || preparedRelay == preparedAck ||
		prepared.State.DeliveryRecordKey != "" || prepared.State.DeliveryRecordHash != "" {
		t.Fatal("Direct preparation claimed a transport receipt before delivery")
	}
	transferID, witness := prepared.State.TransferID, prepared.TxID
	client := sender.serverNode.client.(*rgb11MessageNodeClient)
	probe := &directReplyProbe{rgb11MessageNodeClient: client, acceptFirst: true}
	sender.serverNode = NewNode(probe, "message.test", SERVER_NODE, client.CoreNodePubKey(), client.CoreNodePubKey())
	if _, err := sender.DeliverAndBroadcastConfiguredRGB11AddressTransfer(transferID); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected lost Core response: %v", err)
	}
	queued, err := sender.DeliverAndBroadcastConfiguredRGB11AddressTransfer(transferID)
	if err != nil || queued == nil || !queued.AwaitingACK || queued.Broadcast {
		t.Fatalf("Core has not confirmed storage: %+v, %v", queued, err)
	}
	assertRGB11UnsignedPending(t, sender.rgbManager, transferID)
	probe.committed = true
	if _, err := sender.SyncConfiguredRGB11AddressMailbox(context.Background(), dkvsindexer.RecordVerificationOptions{}); err != nil {
		t.Fatal(err)
	}
	result, err := sender.DeliverAndBroadcastConfiguredRGB11AddressTransfer(transferID)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.AwaitingACK || !result.Broadcast || result.TxID != witness {
		t.Fatal("Direct did not broadcast on Core commit with the recipient offline")
	}
	if len(probe.attempts) != 3 || !bytes.Equal(probe.attempts[0].Ciphertext, probe.attempts[2].Ciphertext) || probe.attempts[0].MessageID != probe.attempts[2].MessageID {
		t.Fatal("Core retry changed the Direct message")
	}
	root, err := sender.captureRGB11NetworkScope(true)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := root.rgbManager.projectionStore.LoadPendingTransfer(transferID)
	if err != nil {
		t.Fatal(err)
	}
	if pending.State.TransferID != transferID || pending.State.WitnessTxID != witness {
		t.Fatal("Direct delivery changed the prepared transaction identity")
	}
	if pending.State.RelayRecordKey != preparedRelay || pending.State.AckRecordKey != preparedAck {
		t.Fatal("Direct delivery changed the prepared application identity")
	}
	if result.RecordKey == "" || result.RecordHash == "" || pending.State.DeliveryMode != "direct" ||
		pending.State.DeliveryRecordKey != result.RecordKey || pending.State.DeliveryRecordHash != result.RecordHash {
		t.Fatal("Direct delivery did not bind its actual mailbox receipt")
	}
	if !preparedPolicy || !pending.State.DeliveryTemporary || preparedPolicy != pending.State.DeliveryTemporary {
		t.Fatalf("Direct immutable mailbox policy differs across prepare/deliver: prepared=%t delivered=%t", preparedPolicy, pending.State.DeliveryTemporary)
	}
}
