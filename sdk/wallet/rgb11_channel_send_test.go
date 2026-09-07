package wallet

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
	indexerwire "github.com/sat20-labs/indexer/rpcserver/wire"
	coreissuance "github.com/sat20-labs/rgb11/issuance"
	"github.com/sat20-labs/rgb11/schemas"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

// rgb11ChannelRecordingPeer keeps the existing AccountBound message service
// while replacing only SendSigReq. Its signing path is cryptographic test
// coverage for a channel peer; it is not an STP contract authorization mock.
type rgb11ChannelRecordingPeer struct {
	*rgb11MessageNodeClient
	peerWallet  common.Wallet
	rebuild     *Manager
	prev        txscript.PrevOutputFetcher
	witness     []byte
	afterSign   func(*wire.MsgTx)
	responseErr error

	expectedChannel string
	expectedReason  string
	expectedTxID    string

	mu           sync.Mutex
	signRequests int
	lastRequest  *wwire.SignRequest
}

func (p *rgb11ChannelRecordingPeer) SendSigReq(req *wwire.SignRequest,
	sig []byte) ([][][]byte, error) {
	if req == nil {
		return nil, errors.New("missing channel sign request")
	}
	p.mu.Lock()
	p.signRequests++
	copyReq := *req
	copyReq.MoreData = append([]byte(nil), req.MoreData...)
	p.lastRequest = &copyReq
	p.mu.Unlock()

	raw, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	if err := VerifySignOfMessage(raw, sig, req.PubKey); err != nil {
		return nil, fmt.Errorf("verify channel request signature: %w", err)
	}
	if req.ChannelId != p.expectedChannel || req.Reason != p.expectedReason || req.CommitHeight != -1 {
		return nil, fmt.Errorf("unexpected channel request channel=%q reason=%q height=%d",
			req.ChannelId, req.Reason, req.CommitHeight)
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(req.MoreData, &fields); err != nil {
		return nil, fmt.Errorf("decode channel request fields: %w", err)
	}
	var txs []*wwire.TxSignInfo
	if err := json.Unmarshal(fields["tx1"], &txs); err != nil || len(txs) != 1 || txs[0] == nil {
		return nil, fmt.Errorf("channel request has no transaction: %w", err)
	}
	var witness []byte
	if err := json.Unmarshal(fields["witness"], &witness); err != nil || !bytes.Equal(witness, p.witness) {
		return nil, fmt.Errorf("channel request witness mismatch: %w", err)
	}
	var proof wwire.RGB11SigningProof
	if err := json.Unmarshal(fields["rgb11"], &proof); err != nil || len(proof.Consignment) == 0 {
		return nil, fmt.Errorf("channel request RGB11 proof missing: %w", err)
	}

	tx, err := DecodeMsgTx(txs[0].Tx)
	if err != nil {
		return nil, err
	}
	if tx.TxID() != p.expectedTxID || !txs[0].L1Tx {
		return nil, fmt.Errorf("channel request txid=%s expected=%s l1=%t",
			tx.TxID(), p.expectedTxID, txs[0].L1Tx)
	}
	if p.rebuild == nil {
		return nil, errors.New("missing RGB11 proof validator")
	}
	// RebuildRGB11TxOutput is the same read-only proof check used by the STP
	// peer path. The surrounding STP contract AllowPeerAction authorization is
	// intentionally outside this SDK mock and is reported as uncovered.
	if _, _, err := p.rebuild.RebuildRGB11TxOutput(tx, &proof); err != nil {
		return nil, fmt.Errorf("validate RGB11 proof: %w", err)
	}
	if p.prev == nil {
		return nil, errors.New("missing channel previous-output fetcher")
	}
	peerSigs, err := FinalSignTxWithWallet(p.peerWallet, tx, p.prev, p.witness,
		false, req.PubKey, txs[0].LocalSigs)
	if err != nil {
		return nil, fmt.Errorf("peer final sign: %w", err)
	}
	if err := VerifySignedTx(tx, p.prev); err != nil {
		return nil, fmt.Errorf("verify peer-signed transaction: %w", err)
	}
	if len(peerSigs) != len(tx.TxIn) || len(peerSigs) == 0 || len(peerSigs[0]) == 0 {
		return nil, fmt.Errorf("peer returned incomplete signatures: %d", len(peerSigs))
	}
	if p.afterSign != nil {
		p.afterSign(tx)
	}
	if p.responseErr != nil {
		return nil, p.responseErr
	}
	return [][][]byte{peerSigs}, nil
}

// rgb11ChannelRecoveryEvidence models the narrow race where a peer has
// signed and the Bitcoin node has accepted the transaction, but the signing
// response is lost before the sender can observe the acceptance. The one-shot
// hidden status makes the first broadcast attempt retain its durable intent;
// ResumeRGB11Send then sees the same txid and recovers the witness from rawTx.
type rgb11ChannelRecoveryEvidence struct {
	*rgb11AddressEvidence
	mu             sync.Mutex
	hideNextStatus map[string]bool
	broadcastCalls int
}

var _ rgb11wallet.BitcoinEvidenceProvider = (*rgb11ChannelRecoveryEvidence)(nil)

func (e *rgb11ChannelRecoveryEvidence) GetTxStatus(txid string) (*rgb11wallet.BitcoinTxStatus, error) {
	e.mu.Lock()
	if e.hideNextStatus[txid] {
		delete(e.hideNextStatus, txid)
		e.mu.Unlock()
		return nil, nil
	}
	e.mu.Unlock()
	return e.rgb11AddressEvidence.GetTxStatus(txid)
}

func (e *rgb11ChannelRecoveryEvidence) Broadcast(raw []byte) (string, error) {
	e.mu.Lock()
	e.broadcastCalls++
	e.mu.Unlock()
	return e.rgb11AddressEvidence.Broadcast(raw)
}

func (e *rgb11ChannelRecoveryEvidence) recordSignedBroadcast(txid string, tx *wire.MsgTx) error {
	if tx == nil || tx.TxID() != txid {
		return fmt.Errorf("signed broadcast txid mismatch: got %v want %s", tx, txid)
	}
	var raw bytes.Buffer
	if err := tx.Serialize(&raw); err != nil {
		return err
	}
	e.rgb11FlowEvidence.mu.Lock()
	e.rgb11FlowEvidence.rawTx[txid] = append([]byte(nil), raw.Bytes()...)
	e.rgb11FlowEvidence.mu.Unlock()
	e.rgb11AddressEvidence.setStatus(txid, rgb11wallet.BitcoinTxStatus{TxID: txid, InMempool: true})
	e.mu.Lock()
	e.hideNextStatus[txid] = true
	e.mu.Unlock()
	return nil
}

func (e *rgb11ChannelRecoveryEvidence) calls() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.broadcastCalls
}

type rgb11ChannelSendCase struct {
	sender          *Manager
	recipient       *Manager
	existing        *RGB11ImportResult
	channelAsset    *RGB11ImportResult
	evidence        *rgb11AddressEvidence
	rpc             *rgb11FlowIndexer
	peer            *rgb11ChannelRecordingPeer
	prepared        *RGB11PreparedTransfer
	witness         []byte
	channelOutpoint string
}

func newRGB11ChannelSendCase(t *testing.T) *rgb11ChannelSendCase {
	t.Helper()
	sender, recipient, existing, evidence, rpc := newRGB11GenericSendFixture(t)

	peerWallet := NewInternalWalletWithMnemonic(
		"abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
		"", GetChainParam(),
	)
	if peerWallet == nil {
		t.Fatal("create channel peer wallet")
	}
	localPub := sender.wallet.GetPaymentPubKey().SerializeCompressed()
	peerPub := peerWallet.GetPaymentPubKey().SerializeCompressed()
	witness, channelPkScript, err := GetP2WSHscript(localPub, peerPub)
	if err != nil {
		t.Fatal(err)
	}
	if !txscript.IsPayToWitnessScriptHash(channelPkScript) {
		t.Fatal("channel funding output is not P2WSH")
	}
	channelID, err := GetP2WSHaddress(localPub, peerPub)
	if err != nil {
		t.Fatal(err)
	}

	// Add a real, independently serialized funding transaction to the existing
	// Bitcoin evidence fake. The RGB genesis below is sealed to this exact
	// channel-owned outpoint; no L1 indexer asset balance is synthesized.
	fundingHash := chainhash.DoubleHashH([]byte("rgb11-channel-funding-v1"))
	funding := wire.NewMsgTx(2)
	funding.AddTxIn(wire.NewTxIn(wire.NewOutPoint(&fundingHash, 0), nil, nil))
	funding.AddTxOut(wire.NewTxOut(100_000, channelPkScript))
	var fundingRaw bytes.Buffer
	if err := funding.Serialize(&fundingRaw); err != nil {
		t.Fatal(err)
	}
	fundingTxID := funding.TxHash().String()
	channelOutpoint := fmt.Sprintf("%s:0", fundingTxID)
	evidence.rgb11FlowEvidence.mu.Lock()
	evidence.rgb11FlowEvidence.rawTx[fundingTxID] = fundingRaw.Bytes()
	evidence.rgb11FlowEvidence.utxos[channelOutpoint] = &rgb11wallet.BitcoinUTXO{
		OutPoint: channelOutpoint, Value: 100_000,
		PkScript: append([]byte(nil), channelPkScript...), Confirmations: 6,
	}
	evidence.rgb11FlowEvidence.mu.Unlock()
	evidence.setStatus(fundingTxID, rgb11wallet.BitcoinTxStatus{
		TxID: fundingTxID, Confirmed: true, BlockHeight: 100, Confirmations: 6,
	})
	channelOutput := indexer.NewTxOutput(100_000)
	channelOutput.OutPointStr = channelOutpoint
	channelOutput.OutValue.PkScript = append([]byte(nil), channelPkScript...)
	rpc.outputs[channelOutpoint] = channelOutput
	rpc.plain = append(rpc.plain, &indexerwire.TxOutputInfo{
		OutPoint: channelOutpoint, Value: 100_000,
		PkScript: append([]byte(nil), channelPkScript...),
	})

	messageClient, ok := sender.serverNode.client.(*rgb11MessageNodeClient)
	if !ok || messageClient == nil {
		t.Fatal("generic fixture did not provide message client")
	}
	peer := &rgb11ChannelRecordingPeer{
		rgb11MessageNodeClient: messageClient,
		peerWallet:             peerWallet,
		expectedChannel:        channelID,
		expectedReason:         "rgb11-channel-test",
		witness:                append([]byte(nil), witness...),
	}
	sender.serverNode = NewNode(peer, "peer.test", SERVER_NODE,
		messageClient.CoreNodePubKey(), peerWallet.GetPaymentPubKey())
	peer.rebuild = sender

	// Issue a real RGB11 NIA genesis against the P2WSH funding output and make
	// the wallet prove ownership through its channel binding during import.
	allocations, err := rgb11IssueAllocations([]string{channelOutpoint}, []uint64{100})
	if err != nil {
		t.Fatal(err)
	}
	issued, err := coreissuance.Issue(coreissuance.Spec{
		Kind: schemas.NIA, Network: rgb11IssuanceNetwork(GetChainParam()),
		Ticker: "CHSEND", Name: "Channel RGB11 send", Precision: 0,
		Allocations: allocations,
	})
	if err != nil {
		t.Fatal(err)
	}
	channelAsset, err := sender.ImportRGB11Contract(context.Background(), []byte(issued.Armor))
	if err != nil || channelAsset == nil || channelAsset.Projected != 1 {
		t.Fatalf("channel genesis import=%+v err=%v", channelAsset, err)
	}

	endpoint, err := recipient.EnableConfiguredRGB11AddressReceive(
		RGB11ReceiveCapabilityOptions{RecordOptions: dkvsindexer.RecordOptions{TTL: testRGB11FreeLocalTTL}},
	)
	if err != nil {
		t.Fatal(err)
	}
	mailbox, err := mailboxSubscriptionTarget(endpoint.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	if err := recipient.SubscribeDKVSPrefix(mailbox); err != nil {
		t.Fatal(err)
	}

	moreData := []byte(`{"test":"rgb11-channel"}`)
	channelCtx, err := sender.newRGB11ChannelSendContext(sender.wallet, channelID,
		peer.expectedReason, moreData, false)
	if err != nil {
		t.Fatal(err)
	}
	request := RGB11AddressSendRequest{
		ReceiverAddress: recipient.wallet.GetAddress(), AssetName: channelAsset.AssetName,
		AmountRaw: "20", FeeRate: 1, MinConfirmations: 1,
	}
	// A source confirmed above the caller's cutoff must be rejected before any
	// pending transfer is persisted. The real source status is used so this
	// checks the boundary rather than a hard-coded fixture height.
	sourceStatus, err := evidence.GetTxStatus(fundingTxID)
	if err != nil || sourceStatus == nil || sourceStatus.BlockHeight <= 1 {
		t.Fatalf("funding status for height boundary=%+v err=%v", sourceStatus, err)
	}
	transfersBefore, err := sender.rgbManager.projectionStore.ListTransfers()
	if err != nil {
		t.Fatal(err)
	}
	channelCtx.maxConfirmedInputHeight = int(sourceStatus.BlockHeight - 1)
	blocked, blockedErr := runRootRGB11ManagedOperation(sender, context.Background(),
		rgb11ManagedOperationNew, func(manager *rgb11Manager) (*RGB11PreparedTransfer, error) {
			manager.channelSend = channelCtx
			defer func() { manager.channelSend = nil }()
			return manager.prepareRGB11AddressBatch(context.Background(),
				[]RGB11AddressSendRequest{request, request}, dkvsindexer.RecordVerificationOptions{})
		})
	if blockedErr == nil || blocked != nil {
		t.Fatalf("height cutoff accepted source: prepared=%+v err=%v", blocked, blockedErr)
	}
	transfersAfter, err := sender.rgbManager.projectionStore.ListTransfers()
	if err != nil {
		t.Fatal(err)
	}
	if len(transfersAfter) != len(transfersBefore) {
		t.Fatalf("height cutoff persisted pending transfer: before=%d after=%d", len(transfersBefore), len(transfersAfter))
	}
	channelCtx.maxConfirmedInputHeight = 0
	prepared, err := runRootRGB11ManagedOperation(sender, context.Background(),
		rgb11ManagedOperationNew, func(manager *rgb11Manager) (*RGB11PreparedTransfer, error) {
			manager.channelSend = channelCtx
			defer func() { manager.channelSend = nil }()
			return manager.prepareRGB11AddressBatch(context.Background(),
				[]RGB11AddressSendRequest{request, request}, dkvsindexer.RecordVerificationOptions{})
		})
	if err != nil {
		t.Fatal(err)
	}
	if prepared == nil || len(prepared.States) != 2 || prepared.State == nil ||
		prepared.State.WitnessTxID == "" {
		t.Fatalf("invalid two-output preparation: %+v", prepared)
	}
	peer.expectedTxID = prepared.State.WitnessTxID
	if channelAsset.AssetName == existing.AssetName {
		t.Fatal("channel genesis unexpectedly reused the existing RGB asset")
	}
	encodedPSBT, err := hex.DecodeString(prepared.SignedPSBT)
	if err != nil {
		t.Fatal(err)
	}
	packet, err := psbt.NewFromRawBytes(bytes.NewReader(encodedPSBT), false)
	if err != nil {
		t.Fatal(err)
	}
	prev := txscript.NewMultiPrevOutFetcher(nil)
	for i, input := range packet.UnsignedTx.TxIn {
		if packet.Inputs[i].WitnessUtxo == nil {
			t.Fatalf("prepared input %d has no witness UTXO", i)
		}
		prev.AddPrevOut(input.PreviousOutPoint, packet.Inputs[i].WitnessUtxo)
	}
	peer.prev = prev

	// The generic fixture has one pre-existing RGB contract. A channel send for
	// the newly issued asset must select only the P2WSH carrier, never that
	// unrelated carrier.
	for _, input := range prepared.State.InputOutPoints {
		if len(rpc.plain) > 0 && input == rpc.plain[0].OutPoint {
			t.Fatal("channel RGB send selected the unrelated RGB carrier")
		}
		if input != channelOutpoint {
			t.Fatalf("channel RGB send selected unexpected input %s", input)
		}
	}
	if len(prepared.State.InputOutPoints) != 1 {
		t.Fatalf("channel RGB send selected %d inputs", len(prepared.State.InputOutPoints))
	}

	return &rgb11ChannelSendCase{
		sender: sender, recipient: recipient, existing: existing,
		channelAsset: channelAsset, evidence: evidence, rpc: rpc,
		peer: peer, prepared: prepared, witness: witness,
		channelOutpoint: channelOutpoint,
	}
}

func acknowledgeRGB11ChannelSend(t *testing.T, c *rgb11ChannelSendCase) {
	t.Helper()
	sender, recipient, prepared, peer := c.sender, c.recipient, c.prepared, c.peer
	if _, err := sender.BroadcastRGB11AddressTransfer(prepared.State.TransferID); !errors.Is(err, ErrRGB11AddressDeliveryRequired) {
		t.Fatalf("broadcast before delivery err=%v", err)
	}
	peer.mu.Lock()
	if peer.signRequests != 0 {
		t.Fatalf("peer was asked to sign before ACKs: %d", peer.signRequests)
	}
	peer.mu.Unlock()

	for i, state := range prepared.States {
		if _, err := sender.rgbManager.deliverRGB11AddressTransferStore(
			mustRGB11ConfiguredStore(t, sender), state.TransferID, RGB11AddressDeliveryOptions{},
		); err != nil {
			t.Fatal(err)
		}
		syncResult, err := recipient.SyncConfiguredRGB11AddressMailbox(
			context.Background(), dkvsindexer.RecordVerificationOptions{}, RGB11AddressDeliveryOptions{},
		)
		if err != nil || syncResult.Received != 1 || syncResult.Invalid != 0 {
			t.Fatalf("output %d mailbox sync=%+v err=%v", i, syncResult, err)
		}
		if _, err := sender.SyncConfiguredRGB11AddressMailbox(
			context.Background(), dkvsindexer.RecordVerificationOptions{}, RGB11AddressDeliveryOptions{},
		); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if _, err := sender.BroadcastRGB11AddressTransfer(state.TransferID); !errors.Is(err, ErrRGB11AddressDeliveryRequired) {
				t.Fatalf("broadcast with one of two ACKs err=%v", err)
			}
			peer.mu.Lock()
			if peer.signRequests != 0 {
				t.Fatalf("peer was asked to sign after only one ACK: %d", peer.signRequests)
			}
			peer.mu.Unlock()
		}
	}
}

func TestRGB11ChannelSendRequiresACKAndCoSignsRealP2WSH(t *testing.T) {
	c := newRGB11ChannelSendCase(t)
	sender, prepared, peer, evidence := c.sender, c.prepared, c.peer, c.evidence
	witness := c.witness
	acknowledgeRGB11ChannelSend(t, c)

	// Delivery snapshots retain the channel PSBT until the deferred peer
	// signature is complete. Importing the snapshot must therefore preserve the
	// exact transaction needed by the post-ACK signing boundary.
	snapshot, err := sender.rgbManager.projectionStore.ExportSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if err := sender.rgbManager.projectionStore.ImportSnapshot(snapshot); err != nil {
		t.Fatal(err)
	}
	pending, err := sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	if pending.ChannelSend == nil || pending.ChannelSend.Signed || len(pending.SignedPSBT) == 0 {
		t.Fatalf("channel pending state lost deferred PSBT: channel=%+v psbt=%d", pending.ChannelSend, len(pending.SignedPSBT))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	txID, fee, err := sender.ResumeRGB11Send(ctx, prepared.State.TransferID)
	if err != nil || txID != prepared.TxID || fee <= 0 {
		t.Fatalf("resume tx=%s fee=%d err=%v", txID, fee, err)
	}
	peer.mu.Lock()
	if peer.signRequests != 1 || peer.lastRequest == nil {
		t.Fatalf("peer signature requests=%d last=%v", peer.signRequests, peer.lastRequest != nil)
	}
	peer.mu.Unlock()
	pending, err = sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	if pending.ChannelSend == nil || !pending.ChannelSend.Signed || len(pending.SignedTx) == 0 {
		t.Fatalf("channel pending state was not signed: channel=%+v tx=%d", pending.ChannelSend, len(pending.SignedTx))
	}
	finalTx := wire.NewMsgTx(2)
	if err := finalTx.Deserialize(bytes.NewReader(pending.SignedTx)); err != nil {
		t.Fatal(err)
	}
	if finalTx.TxID() != txID {
		t.Fatalf("final channel transaction txid=%s expected=%s", finalTx.TxID(), txID)
	}
	if err := VerifySignedTx(finalTx, peer.prev); err != nil {
		t.Fatalf("verify final channel transaction: %v", err)
	}
	if len(finalTx.TxIn) != 1 || len(finalTx.TxIn[0].Witness) != 4 ||
		len(finalTx.TxIn[0].Witness[1]) == 0 || len(finalTx.TxIn[0].Witness[2]) == 0 ||
		!bytes.Equal(finalTx.TxIn[0].Witness[3], witness) {
		t.Fatalf("final P2WSH witness is not two-signature: %+v", finalTx.TxIn[0].Witness)
	}
	evidence.mu.Lock()
	broadcasted := len(evidence.broadcasted) != 0
	evidence.mu.Unlock()
	if !broadcasted {
		t.Fatal("RGB11 channel transaction was not broadcast by the evidence backend")
	}
}

func TestRGB11ChannelSendRecoversAfterPeerResponseLoss(t *testing.T) {
	c := newRGB11ChannelSendCase(t)
	sender, prepared, peer := c.sender, c.prepared, c.peer
	recoveryEvidence := &rgb11ChannelRecoveryEvidence{
		rgb11AddressEvidence: c.evidence,
		hideNextStatus:       make(map[string]bool),
	}
	sender.rgbManager.evidence = recoveryEvidence
	peer.afterSign = func(tx *wire.MsgTx) {
		if err := recoveryEvidence.recordSignedBroadcast(prepared.State.WitnessTxID, tx); err != nil {
			t.Fatalf("record signed broadcast: %v", err)
		}
	}
	peer.responseErr = errors.New("simulated peer connection interruption after broadcast")
	acknowledgeRGB11ChannelSend(t, c)

	txID, err := sender.BroadcastRGB11AddressTransfer(prepared.State.TransferID)
	var unknown *RGB11BroadcastResultUnknownError
	if txID != prepared.TxID || !errors.As(err, &unknown) {
		t.Fatalf("lost peer response tx=%s err=%v", txID, err)
	}
	if unknown.TxID != prepared.TxID {
		t.Fatalf("unknown response txid=%s expected=%s", unknown.TxID, prepared.TxID)
	}
	pending, err := sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	if pending.State.Status != rgb11StatusBroadcastAttempted || pending.ChannelSend == nil || pending.ChannelSend.Signed {
		t.Fatalf("lost response did not retain unsigned durable intent: state=%+v channel=%+v", pending.State, pending.ChannelSend)
	}
	if len(pending.SignedTx) == 0 || len(pending.SignedPSBT) == 0 {
		t.Fatal("lost response discarded the fixed transaction material")
	}
	recoveryEvidence.rgb11FlowEvidence.mu.Lock()
	raw := append([]byte(nil), recoveryEvidence.rgb11FlowEvidence.rawTx[prepared.TxID]...)
	recoveryEvidence.rgb11FlowEvidence.mu.Unlock()
	if len(raw) == 0 {
		t.Fatal("peer did not leave the complete raw transaction in evidence")
	}
	status, err := recoveryEvidence.rgb11AddressEvidence.GetTxStatus(prepared.TxID)
	if err != nil || status == nil || !status.InMempool {
		t.Fatalf("broadcast evidence status=%+v err=%v", status, err)
	}
	peer.mu.Lock()
	if peer.signRequests != 1 {
		t.Fatalf("peer signature requests before resume=%d", peer.signRequests)
	}
	peer.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	recoveredTxID, fee, err := sender.ResumeRGB11Send(ctx, prepared.State.TransferID)
	if err != nil || recoveredTxID != prepared.TxID || fee <= 0 {
		t.Fatalf("resume after lost response tx=%s fee=%d err=%v", recoveredTxID, fee, err)
	}
	pending, err = sender.rgbManager.projectionStore.LoadPendingTransfer(prepared.State.TransferID)
	if err != nil {
		t.Fatal(err)
	}
	if pending.ChannelSend == nil || !pending.ChannelSend.Signed || !bytes.Equal(pending.SignedTx, raw) {
		t.Fatalf("resume did not persist recovered witness: channel=%+v signed=%d", pending.ChannelSend, len(pending.SignedTx))
	}
	finalTx := wire.NewMsgTx(2)
	if err := finalTx.Deserialize(bytes.NewReader(pending.SignedTx)); err != nil {
		t.Fatal(err)
	}
	if finalTx.TxID() != prepared.TxID || len(finalTx.TxIn) != 1 || len(finalTx.TxIn[0].Witness) != 4 ||
		len(finalTx.TxIn[0].Witness[1]) == 0 || len(finalTx.TxIn[0].Witness[2]) == 0 ||
		!bytes.Equal(finalTx.TxIn[0].Witness[3], c.witness) {
		t.Fatalf("recovered P2WSH witness is incomplete: txid=%s witness=%+v", finalTx.TxID(), finalTx.TxIn[0].Witness)
	}
	if err := VerifySignedTx(finalTx, peer.prev); err != nil {
		t.Fatalf("verify recovered channel transaction: %v", err)
	}
	if got := recoveryEvidence.calls(); got != 0 {
		t.Fatalf("resume rebroadcast transaction: evidence broadcast calls=%d", got)
	}
	peer.mu.Lock()
	if peer.signRequests != 1 {
		t.Fatalf("resume requested a second peer signature: %d", peer.signRequests)
	}
	peer.mu.Unlock()
}
