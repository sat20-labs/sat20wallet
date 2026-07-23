package wallet

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	indexer "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/rgb11/invoicing"
	corerelay "github.com/sat20-labs/rgb11/relay"
	coresync "github.com/sat20-labs/rgb11/sync"
	corewallet "github.com/sat20-labs/rgb11/wallet"
	sdkcommon "github.com/sat20-labs/sat20wallet/sdk/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"github.com/sat20-labs/satoshinet/btcec"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRGB11WalletHeadUsesOwningWalletDKVSSignature(t *testing.T) {
	ownerPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	otherPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	state := sha256.Sum256([]byte("state-1"))
	operation := sha256.Sum256([]byte("operation-1"))
	head, err := NewRGB11WalletHead("wallet-42", state, operation, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyRGB11WalletHead(head, "wallet-42"); err != nil {
		t.Fatalf("owner payload rejected: %v", err)
	}
	if err := VerifyRGB11WalletHead(head, "wallet-other"); err == nil {
		t.Fatal("head was accepted for another wallet id")
	}
	value, err := head.StrictEncode()
	if err != nil {
		t.Fatal(err)
	}
	owner := dkvsTestWalletFromPriv(t, ownerPriv)
	key, err := dkvsindexer.PersonalKey(ownerPriv.PubKey().SerializeCompressed(), RGB11WalletHeadPath(head.WalletID))
	if err != nil {
		t.Fatal(err)
	}
	record, err := NewDKVSSignedRecord(owner, key, value, dkvsindexer.RecordOptions{Seq: head.Seq, TTL: 60_000, ExpiryHeight: 100})
	if err != nil {
		t.Fatal(err)
	}
	if err := dkvsindexer.VerifyRecordForClient(record, dkvsindexer.RecordVerificationOptions{ExpectedKey: key, Height: 1, Now: record.IssueTime}); err != nil {
		t.Fatalf("owner DKVS signature rejected: %v", err)
	}
	if len(record.PubKey) != 0 {
		t.Fatal("account-scoped head record repeated its public key")
	}
	parsed, err := dkvsindexer.ParseKey(record.Key)
	if err != nil {
		t.Fatal(err)
	}
	signerID, err := dkvsindexer.RecordSignerAccountID(record, parsed)
	if err != nil {
		t.Fatal(err)
	}
	ownerID, err := dkvsindexer.CanonicalAccountID(ownerPriv.PubKey().SerializeCompressed())
	if err != nil || signerID != ownerID {
		t.Fatalf("head signer=%s owner=%s err=%v", signerID, ownerID, err)
	}
	otherID, err := dkvsindexer.CanonicalAccountID(otherPriv.PubKey().SerializeCompressed())
	if err != nil {
		t.Fatal(err)
	}
	if signerID == otherID {
		t.Fatal("another wallet appears as the head record signer")
	}

	nextState := sha256.Sum256([]byte("state-2"))
	nextOperation := sha256.Sum256([]byte("operation-2"))
	next, err := NewRGB11WalletHead("wallet-42", nextState, nextOperation, head)
	if err != nil {
		t.Fatal(err)
	}
	if next.Seq != head.Seq+1 {
		t.Fatalf("head sequence did not advance: %d -> %d", head.Seq, next.Seq)
	}
	if err := VerifyRGB11WalletHead(next, "wallet-42"); err != nil {
		t.Fatal(err)
	}
}

func TestRGB11BackupCodecIsCompactDeterministic(t *testing.T) {
	snapshot := &RGB11WalletSnapshot{
		Version: rgb11WalletSnapshotVersion, WalletID: "wallet-42", AccountIndex: 3, EngineBuildID: "rgb11-engine",
		ProjectionRecords: []rgb11wallet.SnapshotRecord{{Key: "output-test:0", Value: []byte("projected allocation")}},
		EngineRecords:     []rgb11wallet.SnapshotRecord{{Key: "receive-test", Value: []byte("invoice state")}},
		TickerInfos:       []*indexer.TickerInfo{{DisplayName: "RGB Test", MaxSupply: "100000", Divisibility: 8}},
	}
	first, err := rgb11wallet.EncodeWalletSnapshotPayload(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	second, err := rgb11wallet.EncodeWalletSnapshotPayload(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) || !bytes.HasPrefix(first, []byte(rgb11wallet.SnapshotPayloadMagic)) || bytes.HasPrefix(first, []byte("{")) {
		t.Fatalf("compact snapshot is not deterministic binary data: %x", first[:min(8, len(first))])
	}
	compactSample := &RGB11WalletSnapshot{
		Version: rgb11WalletSnapshotVersion, WalletID: snapshot.WalletID, EngineBuildID: snapshot.EngineBuildID,
		ProjectionRecords: []rgb11wallet.SnapshotRecord{{Key: "proof-test", Value: bytes.Repeat([]byte("allocation-proof-"), 512)}},
	}
	legacySample, err := json.Marshal(compactSample)
	if err != nil {
		t.Fatal(err)
	}
	compactSampleValue, err := rgb11wallet.EncodeWalletSnapshotPayload(compactSample)
	if err != nil || len(compactSampleValue) >= len(legacySample) {
		t.Fatalf("compact snapshot size=%d legacy JSON size=%d err=%v", len(compactSampleValue), len(legacySample), err)
	}
	decoded, err := rgb11wallet.DecodeWalletSnapshotPayload(first)
	if err != nil || decoded.WalletID != snapshot.WalletID || len(decoded.ProjectionRecords) != 1 ||
		len(decoded.EngineRecords) != 1 || len(decoded.TickerInfos) != 1 || decoded.TickerInfos[0].DisplayName != "RGB Test" {
		t.Fatalf("compact snapshot decode=%+v err=%v", decoded, err)
	}
	if _, err := rgb11wallet.DecodeWalletSnapshotPayload([]byte(`{"wallet_id":"wallet-42"}`)); err == nil {
		t.Fatal("legacy JSON snapshot unexpectedly accepted")
	}

	operation := [32]byte{9}
	envelope, err := rgb11wallet.EncodeEncryptedSnapshot(snapshot.WalletID, operation, []byte("ciphertext"))
	if err != nil || !bytes.HasPrefix(envelope, []byte(rgb11wallet.SnapshotEnvelopeMagic)) {
		t.Fatalf("compact envelope err=%v value=%x", err, envelope)
	}
	walletID, decodedOperation, ciphertext, err := rgb11wallet.DecodeEncryptedSnapshot(envelope)
	if err != nil || walletID != snapshot.WalletID || decodedOperation != operation || string(ciphertext) != "ciphertext" {
		t.Fatalf("compact envelope decode wallet=%s operation=%x ciphertext=%q err=%v", walletID, decodedOperation, ciphertext, err)
	}
	if _, _, _, err := rgb11wallet.DecodeEncryptedSnapshot([]byte(`{"wallet_id":"wallet-42"}`)); err == nil {
		t.Fatal("legacy JSON envelope unexpectedly accepted")
	}
}

func TestRGB11RelayAndAckUseTheirRespectiveWalletSigners(t *testing.T) {
	senderPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	recipientPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	sender := dkvsTestWalletFromPriv(t, senderPriv)
	recipient := dkvsTestWalletFromPriv(t, recipientPriv)
	_, ackKey, err := corerelay.NewTemporaryKeys()
	if err != nil {
		t.Fatal(err)
	}
	record := &corerelay.RelayRecord{
		Version:      corerelay.RecordVersion,
		TransferID:   "transfer-1",
		RecipientID:  "recipient-1",
		ObjectHash:   sha256.Sum256([]byte("consignment")),
		ObjectSize:   11,
		SourcePeerID: "sender-peer",
		AckRecordKey: ackKey,
		Expiry:       4_102_444_800,
	}
	if err := SignRGB11RelayRecord(sender, record); err != nil {
		t.Fatal(err)
	}
	if err := record.Verify(senderPriv.PubKey().SerializeCompressed(), 1_800_000_000, rgb11wallet.VerifyWalletSignature); err != nil {
		t.Fatalf("sender signature rejected: %v", err)
	}
	if err := record.Verify(recipientPriv.PubKey().SerializeCompressed(), 1_800_000_000, rgb11wallet.VerifyWalletSignature); err == nil {
		t.Fatal("recipient was accepted as relay sender")
	}
	recordHash, err := record.Hash()
	if err != nil {
		t.Fatal(err)
	}
	ack := &corerelay.AckRecord{
		Version:         corerelay.RecordVersion,
		TransferID:      record.TransferID,
		RecipientID:     record.RecipientID,
		RelayRecordHash: recordHash,
		ConsignmentHash: record.ObjectHash,
		Accepted:        true,
	}
	if err := SignRGB11AckRecord(recipient, ack); err != nil {
		t.Fatal(err)
	}
	if err := ack.Verify(recipientPriv.PubKey().SerializeCompressed(), rgb11wallet.VerifyWalletSignature); err != nil {
		t.Fatalf("recipient ACK signature rejected: %v", err)
	}
	if err := ack.Verify(senderPriv.PubKey().SerializeCompressed(), rgb11wallet.VerifyWalletSignature); err == nil {
		t.Fatal("sender was accepted as ACK recipient")
	}
}

func TestRGB11RelayAndAckRoundTripThroughDKVS(t *testing.T) {
	senderPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	recipientPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	sender := dkvsTestWalletFromPriv(t, senderPriv)
	recipient := dkvsTestWalletFromPriv(t, recipientPriv)
	relayKey, ackKey, err := corerelay.NewTemporaryKeys()
	if err != nil {
		t.Fatal(err)
	}
	relay := &corerelay.RelayRecord{
		Version: corerelay.RecordVersion, TransferID: "transfer-dkvs-roundtrip",
		RecipientID: "recipient-dkvs-roundtrip", ObjectHash: sha256.Sum256([]byte("consignment")),
		ObjectSize: 11, SourcePeerID: "sender-peer", AckRecordKey: ackKey, Expiry: 4_102_444_800,
	}
	if err := SignRGB11RelayRecord(sender, relay); err != nil {
		t.Fatal(err)
	}
	remote := newRGB11MemoryDKVSHTTP()
	client := NewSatsNetDKVSClient("http", "dkvs.test", "testnet", remote)
	options := dkvsindexer.RecordOptions{Seq: 1, TTL: 60_000}
	outerRelay, err := client.PutRGB11RelayRecord(sender, relayKey, relay, options)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(outerRelay.PubKey, senderPriv.PubKey().SerializeCompressed()) {
		t.Fatal("outer relay DKVS record is not signed by sender wallet")
	}
	verifiedRelay, _, err := client.GetRGB11RelayRecord(relayKey, senderPriv.PubKey().SerializeCompressed(),
		dkvsindexer.RecordVerificationOptions{Now: outerRelay.IssueTime})
	if err != nil {
		t.Fatal(err)
	}
	if verifiedRelay.TransferID != relay.TransferID || verifiedRelay.ObjectHash != relay.ObjectHash {
		t.Fatalf("relay round trip mismatch: %+v", verifiedRelay)
	}
	if _, _, err := client.GetRGB11RelayRecord(relayKey, recipientPriv.PubKey().SerializeCompressed(),
		dkvsindexer.RecordVerificationOptions{Now: outerRelay.IssueTime}); err == nil {
		t.Fatal("relay DKVS record accepted with recipient as sender")
	}

	relayHash, err := relay.Hash()
	if err != nil {
		t.Fatal(err)
	}
	ack := &corerelay.AckRecord{
		Version: corerelay.RecordVersion, TransferID: relay.TransferID, RecipientID: relay.RecipientID,
		RelayRecordHash: relayHash, ConsignmentHash: relay.ObjectHash, Accepted: true,
	}
	if err := SignRGB11AckRecord(recipient, ack); err != nil {
		t.Fatal(err)
	}
	outerAck, err := client.PutRGB11AckRecord(recipient, ackKey, ack, options)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(outerAck.PubKey, recipientPriv.PubKey().SerializeCompressed()) {
		t.Fatal("outer ACK DKVS record is not signed by recipient wallet")
	}
	verifiedAck, _, err := client.GetRGB11AckRecord(ackKey, recipientPriv.PubKey().SerializeCompressed(),
		dkvsindexer.RecordVerificationOptions{Now: outerAck.IssueTime})
	if err != nil {
		t.Fatal(err)
	}
	if !verifiedAck.Accepted || verifiedAck.RelayRecordHash != relayHash {
		t.Fatalf("ACK round trip mismatch: %+v", verifiedAck)
	}
}

// rgb11MemoryDKVSHTTP models the one property the multi-device protocol relies
// on from DKVS: a key may advance only to a strictly newer wallet-signed
// sequence. Immutable snapshot blobs use unique keys and remain independently
// retrievable.
type rgb11MemoryDKVSHTTP struct {
	mu           sync.Mutex
	records      map[string]*swire.DKVSRecord
	postGate     <-chan struct{}
	autopayState *dkvsindexer.AutopayContractState
	autopayError error
}

func newRGB11MemoryDKVSHTTP() *rgb11MemoryDKVSHTTP {
	return &rgb11MemoryDKVSHTTP{records: make(map[string]*swire.DKVSRecord)}
}

func (h *rgb11MemoryDKVSHTTP) SendPostRequest(url *URL, body []byte) ([]byte, error) {
	if h.postGate != nil {
		<-h.postGate
	}
	recordPath := strings.HasSuffix(url.Path, "/v3/dkvs/records")
	tombstonePath := strings.HasSuffix(url.Path, "/v3/dkvs/tombstone")
	if !recordPath && !tombstonePath {
		return nil, fmt.Errorf("unexpected RGB11 DKVS POST path %s", url.Path)
	}
	var record swire.DKVSRecord
	if err := json.Unmarshal(body, &record); err != nil {
		return nil, err
	}
	if err := dkvsindexer.VerifySignature(&record); err != nil {
		return rgb11DKVSResponse(1, err.Error(), nil, 0)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if current := h.records[record.Key]; current != nil && record.Seq <= current.Seq {
		// The production endpoint returns the submitted record even when the
		// selector keeps the existing active record. The client must re-read the
		// key before treating its candidate as the latest wallet head.
		return rgb11DKVSResponse(0, "ok", &record, 0)
	}
	if tombstonePath {
		delete(h.records, record.Key)
		return rgb11DKVSResponse(0, "ok", &record, 0)
	}
	h.records[record.Key] = cloneRGB11DKVSRecord(&record)
	return rgb11DKVSResponse(0, "ok", &record, 0)
}

func (h *rgb11MemoryDKVSHTTP) SendGetRequest(url *URL) ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if strings.Contains(url.Path, "/v3/contracts/") && strings.HasSuffix(url.Path, "/state") {
		if h.autopayError != nil {
			return nil, h.autopayError
		}
		if h.autopayState == nil {
			return rgb11DKVSResponse(1, "contract state not found", nil, 0)
		}
		return rgb11DKVSResponse(0, "ok", h.autopayState, 0)
	}
	if strings.HasSuffix(url.Path, "/v3/dkvs/records/prefix") {
		prefix := url.Query["prefix"]
		records := make([]*swire.DKVSRecord, 0)
		for key, record := range h.records {
			if key == prefix || strings.HasPrefix(key, strings.TrimSuffix(prefix, "/")+"/") {
				records = append(records, cloneRGB11DKVSRecord(record))
			}
		}
		sort.Slice(records, func(i, j int) bool { return records[i].Key < records[j].Key })
		return rgb11DKVSResponse(0, "ok", records, len(records))
	}
	if !strings.HasSuffix(url.Path, "/v3/dkvs/records") {
		return nil, fmt.Errorf("unexpected RGB11 DKVS GET path %s", url.Path)
	}
	record := h.records[url.Query["key"]]
	if record == nil {
		return rgb11DKVSResponse(1, "DKVS record not found", nil, 0)
	}
	return rgb11DKVSResponse(0, "ok", cloneRGB11DKVSRecord(record), 0)
}

func rgb11DKVSResponse(code int, msg string, data interface{}, total int) ([]byte, error) {
	return json.Marshal(map[string]interface{}{"code": code, "msg": msg, "data": data, "total": total})
}

func cloneRGB11DKVSRecord(record *swire.DKVSRecord) *swire.DKVSRecord {
	if record == nil {
		return nil
	}
	copy := *record
	copy.Value = append([]byte(nil), record.Value...)
	copy.PubKey = append([]byte(nil), record.PubKey...)
	copy.Signature = append([]byte(nil), record.Signature...)
	copy.FeeProof = append([]byte(nil), record.FeeProof...)
	return &copy
}

func newRGB11MultiDeviceManager(t *testing.T, priv *btcec.PrivateKey, localWalletID int64) *Manager {
	t.Helper()
	database := indexerdb.NewKVDB(t.TempDir())
	t.Cleanup(func() { database.Close() })
	wallet := dkvsTestWalletFromPriv(t, priv)
	manager := &Manager{
		db: database, wallet: wallet,
		status:        &Status{CurrentWallet: localWalletID, CurrentAccount: 0},
		tickerInfoMap: make(map[string]*indexer.TickerInfo),
	}
	rgbManager, err := newRGB11Manager(manager, database, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	rgbManager.consistencyStatus = "ok"
	manager.rgbManager = rgbManager
	if err := manager.rgbManager.selectRGB11Scope(); err != nil {
		t.Fatal(err)
	}
	// Automatic DKVS backup is asynchronous. Wait for it before the earlier
	// database cleanup closes Pebble.
	t.Cleanup(manager.rgbManager.waitForRGB11AutoBackup)
	return manager
}

func createRGB11MultiDeviceInvoice(t *testing.T, manager *Manager, recipient string) string {
	t.Helper()
	request, err := manager.rgbManager.engine.CreateReceive(corewallet.ReceiveParams{
		Network: invoicing.BitcoinTestnet4, RecipientID: recipient,
		WitnessVout: 1, Expiry: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return request.RequestID
}

func TestRGB11TwoDevicesRestoreLatestAndRejectStaleWriter(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	deviceA := newRGB11MultiDeviceManager(t, priv, 11)
	deviceB := newRGB11MultiDeviceManager(t, priv, 99)
	remote := newRGB11MemoryDKVSHTTP()
	client := NewSatsNetDKVSClient("http", "dkvs.test", "testnet", remote)
	opts := dkvsindexer.RecordOptions{TTL: uint64((24 * time.Hour) / time.Millisecond)}
	verify := dkvsindexer.RecordVerificationOptions{Now: uint64(time.Now().UnixMilli())}

	requestA := createRGB11MultiDeviceInvoice(t, deviceA, "device-a-recipient")
	walletID, err := deviceA.RGB11WalletID()
	if err != nil {
		t.Fatal(err)
	}
	head1, _, err := deviceA.BackupRGB11WalletState(client, walletID, nil, opts)
	if err != nil {
		t.Fatal(err)
	}
	if head1.Seq != 1 {
		t.Fatalf("first head sequence=%d", head1.Seq)
	}

	restored1, err := deviceB.RestoreRGB11WalletState(client, walletID, verify)
	if err != nil {
		t.Fatal(err)
	}
	if restored1.Seq != 1 {
		t.Fatalf("device B restored head sequence=%d", restored1.Seq)
	}
	if _, err := deviceB.rgbManager.engine.LoadReceive(requestA); err != nil {
		t.Fatalf("device B did not restore device A invoice: %v", err)
	}

	requestB := createRGB11MultiDeviceInvoice(t, deviceB, "device-b-recipient")
	head2, _, err := deviceB.BackupRGB11WalletState(client, walletID, restored1, opts)
	if err != nil {
		t.Fatal(err)
	}
	if head2.Seq != 2 {
		t.Fatalf("second head sequence=%d", head2.Seq)
	}

	createRGB11MultiDeviceInvoice(t, deviceA, "stale-device-a-recipient")
	if _, _, err := deviceA.BackupRGB11WalletState(client, walletID, head1, opts); !errors.Is(err, coresync.ErrHeadConflict) {
		t.Fatalf("stale device A write was not rejected: %v", err)
	}
	deviceA.cfg = &sdkcommon.Config{IndexerL2: &sdkcommon.Indexer{Scheme: "http", Host: "dkvs.test", Proxy: "testnet"}}
	deviceA.http = remote
	if err := deviceA.rgbManager.requireLatestRGB11WalletState(); !errors.Is(err, coresync.ErrHeadConflict) {
		t.Fatalf("stale device A external-effect guard error=%v", err)
	}

	restored2, err := deviceA.RestoreRGB11WalletState(client, walletID, verify)
	if err != nil {
		t.Fatal(err)
	}
	if restored2.Seq != 2 {
		t.Fatalf("device A latest restored sequence=%d", restored2.Seq)
	}
	if err := deviceA.rgbManager.requireLatestRGB11WalletState(); err != nil {
		t.Fatalf("restored device A was not accepted as latest: %v", err)
	}
	if _, err := deviceA.rgbManager.engine.LoadReceive(requestB); err != nil {
		t.Fatalf("device A did not converge to device B state: %v", err)
	}
}

func TestRGB11ManualFirstBackupEnablesAutomaticBackupAndActivationRestore(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	remote := newRGB11MemoryDKVSHTTP()
	configure := func(manager *Manager) {
		manager.cfg = &sdkcommon.Config{IndexerL2: &sdkcommon.Indexer{Scheme: "http", Host: "dkvs.test", Proxy: "testnet"}}
		manager.http = remote
	}
	deviceA := newRGB11MultiDeviceManager(t, priv, 501)
	configure(deviceA)

	first, err := deviceA.CreateRGB11Invoice(RGB11InvoiceRequest{AmountRaw: "1", WitnessVout: 1})
	if err != nil {
		t.Fatal(err)
	}
	walletID, err := deviceA.RGB11WalletID()
	if err != nil {
		t.Fatal(err)
	}
	client := NewSatsNetDKVSClient("http", "dkvs.test", "testnet", remote)
	verify := dkvsindexer.RecordVerificationOptions{Now: uint64(time.Now().UnixMilli())}
	if _, _, err := client.GetRGB11WalletHead(priv.PubKey().SerializeCompressed(), walletID, verify); !errors.Is(err, ErrDKVSRecordNotFound) {
		t.Fatalf("first invoice unexpectedly triggered a paid backup: %v", err)
	}

	options := dkvsindexer.RecordOptions{TTL: uint64((24 * time.Hour) / time.Millisecond)}
	head1, err := deviceA.SyncRGB11WalletState("", options)
	if err != nil {
		t.Fatal(err)
	}
	if head1.Seq != 1 || deviceA.rgbManager.autoBackup == nil || !deviceA.rgbManager.autoBackup.Enabled {
		t.Fatalf("manual first backup did not enable automatic backup: head=%+v policy=%+v", head1, deviceA.rgbManager.autoBackup)
	}
	remote.mu.Lock()
	for key, record := range remote.records {
		if !strings.HasPrefix(key, "/blob/") && !strings.HasPrefix(key, "/personal/") {
			continue
		}
		proof, err := dkvsindexer.ParseFeeProof(record.FeeProof)
		if err != nil || proof.Mode != dkvsindexer.FeeModeAutopay || proof.PoolContract == "" {
			remote.mu.Unlock()
			t.Fatalf("RGB11 backup record %s has no valid AUTOPAY proof: proof=%+v err=%v", key, proof, err)
		}
		if err := dkvsindexer.VerifySignature(record); err != nil {
			remote.mu.Unlock()
			t.Fatalf("RGB11 backup record %s was not re-signed after fee proof: %v", key, err)
		}
	}
	remote.mu.Unlock()

	second, err := deviceA.CreateRGB11Invoice(RGB11InvoiceRequest{AmountRaw: "2", WitnessVout: 1})
	if err != nil {
		t.Fatal(err)
	}
	deviceA.rgbManager.waitForRGB11AutoBackup()
	head2, _, err := client.GetRGB11WalletHead(priv.PubKey().SerializeCompressed(), walletID, verify)
	if err != nil {
		t.Fatal(err)
	}
	if head2.Seq != 2 {
		t.Fatalf("post-enrollment invoice did not auto-backup: head sequence=%d", head2.Seq)
	}
	deviceA.rgbManager.autoBackupRGB11AfterMutation()
	deviceA.rgbManager.waitForRGB11AutoBackup()
	unchanged, _, err := client.GetRGB11WalletHead(priv.PubKey().SerializeCompressed(), walletID, verify)
	if err != nil || unchanged.Seq != 2 {
		t.Fatalf("unchanged automatic backup advanced head: head=%+v err=%v", unchanged, err)
	}

	deviceB := newRGB11MultiDeviceManager(t, priv, 777)
	configure(deviceB)
	activation, err := deviceB.ActivateRGB11WalletState(verify)
	if err != nil {
		t.Fatal(err)
	}
	if !activation.Found || !activation.Restored || !activation.AutoBackup || activation.Head == nil || activation.Head.Seq != 2 {
		t.Fatalf("automatic activation restore=%+v", activation)
	}
	if _, err := deviceB.rgbManager.engine.LoadReceive(first.RequestID); err != nil {
		t.Fatalf("first invoice not restored: %v", err)
	}
	if _, err := deviceB.rgbManager.engine.LoadReceive(second.RequestID); err != nil {
		t.Fatalf("automatically backed-up invoice not restored: %v", err)
	}
	if _, err := deviceB.CreateRGB11Invoice(RGB11InvoiceRequest{AmountRaw: "3", WitnessVout: 1}); err != nil {
		t.Fatal(err)
	}
	deviceB.rgbManager.waitForRGB11AutoBackup()
	head3, _, err := client.GetRGB11WalletHead(priv.PubKey().SerializeCompressed(), walletID, verify)
	if err != nil || head3.Seq != 3 {
		t.Fatalf("restored device did not continue automatic backup: head=%+v err=%v", head3, err)
	}

	otherPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	newWallet := newRGB11MultiDeviceManager(t, otherPriv, 888)
	configure(newWallet)
	missing, err := newWallet.ActivateRGB11WalletState(verify)
	if err != nil || missing.Found || missing.Restored || missing.AutoBackup {
		t.Fatalf("wallet without a backup should remain manual-first: result=%+v err=%v", missing, err)
	}
}

func TestRGB11PaidAutopayEnablesAutomaticBackupOnActivation(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	remote := newRGB11MemoryDKVSHTTP()
	manager := newRGB11MultiDeviceManager(t, priv, 889)
	manager.cfg = &sdkcommon.Config{IndexerL2: &sdkcommon.Indexer{Scheme: "http", Host: "dkvs.test", Proxy: "testnet"}}
	manager.http = remote
	l2 := NewIndexerRPCClientMgr()
	l2.Set(NewIndexerClient("http", "dkvs.test", "testnet", remote))
	manager.l2IndexerClient = l2

	defaults := dkvsindexer.NetworkDefaultsForParams(GetChainParam_SatsNet())
	payer := PublicKeyToP2TRAddress_SatsNet(manager.wallet.GetPubKey())
	remote.autopayState = &dkvsindexer.AutopayContractState{
		Contract: defaults.AutopayContract, TemplateName: TEMPLATE_CONTRACT_AUTOPAY,
		ServiceName: defaults.AutopayServiceName, Recipient: defaults.AutopayRecipient,
		FeeAssetName: defaults.AutopayFeeAssetName, Status: "funding", CurrentBlock: 100,
		Delegates: map[string]dkvsindexer.AutopayDelegateState{
			payer: {AmountPerBlock: "100", Balance: "0", LastPayHeight: 100, Status: "funding"},
		},
	}

	activation, err := manager.ActivateRGB11WalletState(dkvsindexer.RecordVerificationOptions{
		Now: uint64(time.Now().UnixMilli()),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !activation.Found || activation.Restored || !activation.AutoBackup || activation.Head == nil || activation.Head.Seq != 1 {
		t.Fatalf("paid AUTOPAY activation=%+v", activation)
	}
	if manager.rgbManager.autoBackup == nil || !manager.rgbManager.autoBackup.Enabled {
		t.Fatalf("paid AUTOPAY did not enable automatic backup: %+v", manager.rgbManager.autoBackup)
	}
	walletID, err := manager.RGB11WalletID()
	if err != nil {
		t.Fatal(err)
	}
	client := NewSatsNetDKVSClient("http", "dkvs.test", "testnet", remote)
	head, _, err := client.GetRGB11WalletHead(priv.PubKey().SerializeCompressed(), walletID,
		dkvsindexer.RecordVerificationOptions{Now: uint64(time.Now().UnixMilli())})
	if err != nil || head.Seq != 1 {
		t.Fatalf("automatic first backup head=%+v err=%v", head, err)
	}
}

func TestRGB11PaidAutopayFirstBackupRestoresAllocation(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	walletA := dkvsTestWalletFromPriv(t, priv)
	walletB := dkvsTestWalletFromPriv(t, priv)
	walletScript, err := AddrToPkScript(walletA.GetAddress(), GetChainParam())
	if err != nil {
		t.Fatal(err)
	}
	const sourceOutpoint = "14295d5bb1a191cdb6286dc0944df938421e3dfcbf0811353ccac4100c2068c5:1"
	evidence := &rgb11FlowEvidence{
		utxos: map[string]*rgb11wallet.BitcoinUTXO{
			sourceOutpoint: {OutPoint: sourceOutpoint, Value: 10_000, PkScript: walletScript, Confirmations: 6},
		},
		rawTx: make(map[string][]byte), spendingTx: make(map[string]string),
	}
	rpc := &rgb11FlowIndexer{outputs: make(map[string]*TxOutput)}
	source := indexer.NewTxOutput(10_000)
	source.OutPointStr = sourceOutpoint
	source.OutValue.PkScript = walletScript
	rpc.outputs[sourceOutpoint] = source
	deviceA := newRGB11FlowManager(t, walletA, rpc, evidence, 11)
	deviceB := newRGB11FlowManager(t, walletB, rpc, evidence, 99)

	contract, err := os.ReadFile("../../../rgb11/testvectors/rc11/nia-example.rgba")
	if err != nil {
		t.Fatal(err)
	}
	imported, err := deviceA.ImportRGB11Contract(context.Background(), contract)
	if err != nil {
		t.Fatal(err)
	}
	if imported.Projected != 1 {
		t.Fatalf("device A projected allocations=%d", imported.Projected)
	}

	remote := newRGB11MemoryDKVSHTTP()
	configure := func(manager *Manager) {
		manager.cfg = &sdkcommon.Config{IndexerL2: &sdkcommon.Indexer{Scheme: "http", Host: "dkvs.test", Proxy: "testnet"}}
		manager.http = remote
		l2 := NewIndexerRPCClientMgr()
		l2.Set(NewIndexerClient("http", "dkvs.test", "testnet", remote))
		manager.l2IndexerClient = l2
	}
	configure(deviceA)
	configure(deviceB)
	defaults := dkvsindexer.NetworkDefaultsForParams(GetChainParam_SatsNet())
	payer := PublicKeyToP2TRAddress_SatsNet(deviceA.wallet.GetPubKey())
	remote.autopayState = &dkvsindexer.AutopayContractState{
		Contract: defaults.AutopayContract, TemplateName: TEMPLATE_CONTRACT_AUTOPAY,
		ServiceName: defaults.AutopayServiceName, Recipient: defaults.AutopayRecipient,
		FeeAssetName: defaults.AutopayFeeAssetName, Status: "funding", CurrentBlock: 100,
		Delegates: map[string]dkvsindexer.AutopayDelegateState{
			payer: {AmountPerBlock: "100", Balance: "0", LastPayHeight: 100, Status: "funding"},
		},
	}
	verify := dkvsindexer.RecordVerificationOptions{Now: uint64(time.Now().UnixMilli())}
	activation, err := deviceA.ActivateRGB11WalletState(verify)
	if err != nil {
		t.Fatal(err)
	}
	if !activation.Found || activation.Restored || !activation.AutoBackup || activation.Head == nil {
		t.Fatalf("paid AUTOPAY activation=%+v", activation)
	}
	restored, err := deviceB.ActivateRGB11WalletState(verify)
	if err != nil {
		t.Fatal(err)
	}
	if !restored.Found || !restored.Restored || !restored.AutoBackup {
		t.Fatalf("second device activation=%+v", restored)
	}
	balance, err := deviceB.GetRGB11AssetBalance(&imported.AssetName)
	if err != nil {
		t.Fatal(err)
	}
	if balance.Value.String() != "100000" || balance.Precision != 8 {
		t.Fatalf("restored RGB11 balance=%+v", balance)
	}
	locked := deviceB.utxoLockerL1.GetLockedUtxoList()[sourceOutpoint]
	if locked == nil || locked.Reason != rgb11wallet.LockReasonRGB {
		t.Fatalf("restored RGB11 carrier lock=%+v", locked)
	}
}

func TestRGB11InactiveAutopayRemainsManualFirst(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	remote := newRGB11MemoryDKVSHTTP()
	manager := newRGB11MultiDeviceManager(t, priv, 890)
	manager.cfg = &sdkcommon.Config{IndexerL2: &sdkcommon.Indexer{Scheme: "http", Host: "dkvs.test", Proxy: "testnet"}}
	manager.http = remote
	l2 := NewIndexerRPCClientMgr()
	l2.Set(NewIndexerClient("http", "dkvs.test", "testnet", remote))
	manager.l2IndexerClient = l2

	defaults := dkvsindexer.NetworkDefaultsForParams(GetChainParam_SatsNet())
	remote.autopayState = &dkvsindexer.AutopayContractState{
		Contract: defaults.AutopayContract, TemplateName: TEMPLATE_CONTRACT_AUTOPAY,
		ServiceName: defaults.AutopayServiceName, Recipient: defaults.AutopayRecipient,
		FeeAssetName: defaults.AutopayFeeAssetName, Status: "active",
		Delegates: map[string]dkvsindexer.AutopayDelegateState{},
	}

	activation, err := manager.ActivateRGB11WalletState(dkvsindexer.RecordVerificationOptions{
		Now: uint64(time.Now().UnixMilli()),
	})
	if err != nil {
		t.Fatal(err)
	}
	if activation.Found || activation.Restored || activation.AutoBackup || activation.Head != nil {
		t.Fatalf("inactive AUTOPAY activation=%+v", activation)
	}
	if manager.rgbManager.autoBackup != nil {
		t.Fatalf("inactive AUTOPAY enabled automatic backup: %+v", manager.rgbManager.autoBackup)
	}
}

func TestRGB11AutopayLookupFailureDoesNotEnableBackup(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	remote := newRGB11MemoryDKVSHTTP()
	remote.autopayError = errors.New("AUTOPAY state unavailable")
	manager := newRGB11MultiDeviceManager(t, priv, 891)
	manager.cfg = &sdkcommon.Config{IndexerL2: &sdkcommon.Indexer{Scheme: "http", Host: "dkvs.test", Proxy: "testnet"}}
	manager.http = remote
	l2 := NewIndexerRPCClientMgr()
	l2.Set(NewIndexerClient("http", "dkvs.test", "testnet", remote))
	manager.l2IndexerClient = l2

	activation, err := manager.ActivateRGB11WalletState(dkvsindexer.RecordVerificationOptions{
		Now: uint64(time.Now().UnixMilli()),
	})
	if err == nil || activation != nil {
		t.Fatalf("AUTOPAY lookup failure activation=%+v err=%v", activation, err)
	}
	if manager.rgbManager.autoBackup != nil {
		t.Fatalf("AUTOPAY lookup failure enabled automatic backup: %+v", manager.rgbManager.autoBackup)
	}
	remote.mu.Lock()
	recordCount := len(remote.records)
	remote.mu.Unlock()
	if recordCount != 0 {
		t.Fatalf("AUTOPAY lookup failure wrote %d DKVS records", recordCount)
	}
}

func TestRGB11AutomaticBackupDoesNotBlockMutation(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	remote := newRGB11MemoryDKVSHTTP()
	manager := newRGB11MultiDeviceManager(t, priv, 901)
	manager.cfg = &sdkcommon.Config{IndexerL2: &sdkcommon.Indexer{Scheme: "http", Host: "dkvs.test", Proxy: "testnet"}}
	manager.http = remote
	options := dkvsindexer.RecordOptions{TTL: uint64((24 * time.Hour) / time.Millisecond)}
	if _, err := manager.SyncRGB11WalletState("", options); err != nil {
		t.Fatal(err)
	}

	gate := make(chan struct{})
	remote.postGate = gate
	started := time.Now()
	if _, err := manager.CreateRGB11Invoice(RGB11InvoiceRequest{AmountRaw: "1", WitnessVout: 1}); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("mutation waited for automatic backup: %v", elapsed)
	}

	waited := make(chan struct{})
	go func() {
		manager.rgbManager.waitForRGB11AutoBackup()
		close(waited)
	}()
	select {
	case <-waited:
		t.Fatal("automatic backup finished while remote writes were blocked")
	case <-time.After(20 * time.Millisecond):
	}
	close(gate)
	select {
	case <-waited:
	case <-time.After(time.Second):
		t.Fatal("automatic backup did not finish after remote writes resumed")
	}
}

func TestRGB11SecondDeviceRestoresAllocationBalanceAndLock(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	walletA := dkvsTestWalletFromPriv(t, priv)
	walletB := dkvsTestWalletFromPriv(t, priv)
	walletScript, err := AddrToPkScript(walletA.GetAddress(), GetChainParam())
	if err != nil {
		t.Fatal(err)
	}
	const sourceOutpoint = "14295d5bb1a191cdb6286dc0944df938421e3dfcbf0811353ccac4100c2068c5:1"
	evidence := &rgb11FlowEvidence{
		utxos: map[string]*rgb11wallet.BitcoinUTXO{
			sourceOutpoint: {OutPoint: sourceOutpoint, Value: 10_000, PkScript: walletScript, Confirmations: 6},
		},
		rawTx:      make(map[string][]byte),
		spendingTx: make(map[string]string),
	}
	rpc := &rgb11FlowIndexer{outputs: make(map[string]*TxOutput)}
	source := indexer.NewTxOutput(10_000)
	source.OutPointStr = sourceOutpoint
	source.OutValue.PkScript = walletScript
	rpc.outputs[sourceOutpoint] = source
	deviceA := newRGB11FlowManager(t, walletA, rpc, evidence, 11)
	deviceB := newRGB11FlowManager(t, walletB, rpc, evidence, 99)

	contract, err := os.ReadFile("../../../rgb11/testvectors/rc11/nia-example.rgba")
	if err != nil {
		t.Fatal(err)
	}
	imported, err := deviceA.ImportRGB11Contract(context.Background(), contract)
	if err != nil {
		t.Fatal(err)
	}
	if imported.Projected != 1 {
		t.Fatalf("device A projected allocations=%d", imported.Projected)
	}

	remote := newRGB11MemoryDKVSHTTP()
	client := NewSatsNetDKVSClient("http", "dkvs.test", "testnet", remote)
	options := dkvsindexer.RecordOptions{TTL: uint64((24 * time.Hour) / time.Millisecond)}
	walletID, err := deviceA.RGB11WalletID()
	if err != nil {
		t.Fatal(err)
	}
	head, _, err := deviceA.BackupRGB11WalletState(client, walletID, nil, options)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := deviceB.RestoreRGB11WalletState(client, walletID,
		dkvsindexer.RecordVerificationOptions{Now: uint64(time.Now().UnixMilli())})
	if err != nil {
		t.Fatal(err)
	}
	if restored.Seq != head.Seq {
		t.Fatalf("restored sequence=%d want=%d", restored.Seq, head.Seq)
	}
	balance, err := deviceB.GetRGB11AssetBalance(&imported.AssetName)
	if err != nil {
		t.Fatal(err)
	}
	if balance.Value.String() != "100000" || balance.Precision != 8 {
		t.Fatalf("restored RGB11 balance=%+v", balance)
	}
	locked := deviceB.utxoLockerL1.GetLockedUtxoList()[sourceOutpoint]
	if locked == nil || locked.Reason != rgb11wallet.LockReasonRGB {
		t.Fatalf("restored RGB11 carrier lock=%+v", locked)
	}
}
