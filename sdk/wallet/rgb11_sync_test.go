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
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
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
	relayValue, err := relay.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	relayRecord, err := NewDKVSSignedRecord(sender, relayKey, relayValue, options)
	if err != nil {
		t.Fatal(err)
	}
	outerRelay, err := client.PutRecord(relayRecord)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(outerRelay.PubKey, senderPriv.PubKey().SerializeCompressed()) {
		t.Fatal("outer relay DKVS record is not signed by sender wallet")
	}
	verifiedRelayRecord, err := client.GetVerifiedRecord(relayKey, dkvsindexer.RecordVerificationOptions{
		ExpectedKey: relayKey, Now: outerRelay.IssueTime,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(verifiedRelayRecord.PubKey, senderPriv.PubKey().SerializeCompressed()) {
		t.Fatal("relay DKVS signer does not match sender")
	}
	verifiedRelay, err := corerelay.UnmarshalRelayRecord(verifiedRelayRecord.Value)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifiedRelay.Verify(senderPriv.PubKey().SerializeCompressed(), time.Now().Unix(),
		rgb11wallet.VerifyWalletSignature); err != nil {
		t.Fatal(err)
	}
	if verifiedRelay.TransferID != relay.TransferID || verifiedRelay.ObjectHash != relay.ObjectHash {
		t.Fatalf("relay round trip mismatch: %+v", verifiedRelay)
	}
	if bytes.Equal(verifiedRelayRecord.PubKey, recipientPriv.PubKey().SerializeCompressed()) {
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
	ackValue, err := ack.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	ackRecord, err := NewDKVSSignedRecord(recipient, ackKey, ackValue, options)
	if err != nil {
		t.Fatal(err)
	}
	outerAck, err := client.PutRecord(ackRecord)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(outerAck.PubKey, recipientPriv.PubKey().SerializeCompressed()) {
		t.Fatal("outer ACK DKVS record is not signed by recipient wallet")
	}
	verifiedAckRecord, err := client.GetVerifiedRecord(ackKey, dkvsindexer.RecordVerificationOptions{
		ExpectedKey: ackKey, Now: outerAck.IssueTime,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(verifiedAckRecord.PubKey, recipientPriv.PubKey().SerializeCompressed()) {
		t.Fatal("ACK DKVS signer does not match recipient")
	}
	verifiedAck, err := corerelay.UnmarshalAckRecord(verifiedAckRecord.Value)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifiedAck.Verify(recipientPriv.PubKey().SerializeCompressed(),
		rgb11wallet.VerifyWalletSignature); err != nil {
		t.Fatal(err)
	}
	if !verifiedAck.Accepted || verifiedAck.RelayRecordHash != relayHash {
		t.Fatalf("ACK round trip mismatch: %+v", verifiedAck)
	}
}

// rgb11MemoryDKVSHTTP models the one property the multi-device protocol relies
// on from DKVS: a key may advance only to a strictly newer wallet-signed
// sequence. Related key updates are committed atomically through batch-CAS.
type rgb11MemoryDKVSHTTP struct {
	mu           sync.Mutex
	records      map[string]*swire.DKVSRecord
	generations  map[string]uint64
	postGate     <-chan struct{}
	autopayState *dkvsindexer.AutopayContractState
	autopayError error
	freeLocal    dkvsindexer.FreeLocalCachePolicy
	maxRecords   int
}

func newRGB11MemoryDKVSHTTP() *rgb11MemoryDKVSHTTP {
	return &rgb11MemoryDKVSHTTP{
		records:     make(map[string]*swire.DKVSRecord),
		generations: make(map[string]uint64),
		freeLocal: dkvsindexer.FreeLocalCachePolicy{
			Enabled:             true,
			MaxTTL:              rgb11AddressTemporaryTTL,
			MaxRecordsPerSigner: 100,
			MaxBytesPerSigner:   1 << 20,
			MaxTotalRecords:     100_000,
			MaxTotalBytes:       1 << 30,
		},
	}
}

func configureRGB11DKVSTestManager(manager *Manager, remote HttpClient) {
	manager.cfg = &sdkcommon.Config{
		Env: "test", Chain: "testnet",
		IndexerL2: &sdkcommon.Indexer{Scheme: "http", Host: "dkvs.test", Proxy: "testnet"},
	}
	manager.http = remote
}

func (h *rgb11MemoryDKVSHTTP) SendPostRequest(url *URL, body []byte) ([]byte, error) {
	if h.postGate != nil {
		<-h.postGate
	}
	switch {
	case strings.HasSuffix(url.Path, "/v3/dkvs/records/batch-cas"):
		var req DKVSBatchCASRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, err
		}
		return h.applyBatchCAS(req.Mutations, req.PathPreconditions)
	case strings.HasSuffix(url.Path, "/v3/dkvs/records/cas"):
		var req DKVSCASMutationRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, err
		}
		result, err := h.applyBatchCAS([]DKVSCASMutationRequest{req}, nil)
		if err != nil {
			return nil, err
		}
		var batch struct {
			Code int                 `json:"code"`
			Msg  string              `json:"msg"`
			Data *DKVSBatchCASResult `json:"data"`
		}
		if err := json.Unmarshal(result, &batch); err != nil {
			return nil, err
		}
		if batch.Code != 0 || batch.Data == nil || len(batch.Data.Records) != 1 {
			return result, nil
		}
		hash := dkvsindexer.RecordHash(batch.Data.Records[0])
		return json.Marshal(map[string]interface{}{
			"code": 0, "msg": "ok", "data": batch.Data.Records[0], "hash": hash.String(),
		})
	case strings.HasSuffix(url.Path, "/v3/dkvs/sync"):
		var req DKVSSyncRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, err
		}
		return h.syncFiltered(req)
	case strings.HasSuffix(url.Path, "/v3/dkvs/sync/directory"):
		var req DKVSDirectorySyncRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, err
		}
		return h.syncDirectory(req)
	case strings.HasSuffix(url.Path, "/v3/dkvs/watch/directory"):
		var req DKVSDirectoryWatchRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return nil, err
		}
		sync, err := h.syncDirectory(DKVSDirectorySyncRequest{Prefix: req.Prefix})
		if err != nil {
			return nil, err
		}
		var response dkvsSyncResp
		if err := json.Unmarshal(sync, &response); err != nil || response.Data == nil {
			return nil, err
		}
		return rgb11DKVSResponse(0, "ok", &DKVSWatchResult{
			Changed: response.Data.Root != req.Root, Root: response.Data.Root,
		}, 0)
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
	if h.maxRecords > 0 && !tombstonePath && h.records[record.Key] == nil &&
		len(h.records) >= h.maxRecords {
		return rgb11DKVSResponse(1, dkvsindexer.ErrFeeCapacityExceeded.Error(), nil, 0)
	}
	if current := h.records[record.Key]; current != nil && record.Seq <= current.Seq {
		return rgb11DKVSResponse(0, "ok", &record, 0)
	}
	if tombstonePath {
		delete(h.records, record.Key)
		return rgb11DKVSResponse(0, "ok", &record, 0)
	}
	h.records[record.Key] = cloneRGB11DKVSRecord(&record)
	return rgb11DKVSResponse(0, "ok", &record, 0)
}

func (h *rgb11MemoryDKVSHTTP) applyBatchCAS(mutations []DKVSCASMutationRequest,
	pathPreconditions []DKVSPathPreconditionRequest) ([]byte, error) {
	if len(mutations) == 0 {
		return rgb11DKVSResponse(1, dkvsindexer.ErrInvalidRecord.Error(), nil, 0)
	}
	for _, mutation := range mutations {
		if mutation.Record == nil {
			return rgb11DKVSResponse(1, dkvsindexer.ErrInvalidRecord.Error(), nil, 0)
		}
		if err := dkvsindexer.VerifySignature(mutation.Record); err != nil {
			return rgb11DKVSResponse(1, err.Error(), nil, 0)
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	already := 0
	for _, mutation := range mutations {
		current := h.records[mutation.Record.Key]
		if current != nil && dkvsindexer.RecordHash(current) == dkvsindexer.RecordHash(mutation.Record) {
			already++
		}
	}
	if already != 0 && already != len(mutations) {
		return rgb11DKVSResponse(1, dkvsindexer.ErrWriteConflict.Error(), nil, 0)
	}
	if already == 0 {
		for _, condition := range pathPreconditions {
			root, err := chainhash.NewHashFromStr(condition.ExpectedRoot)
			if err != nil || h.generations[condition.Path] != condition.ExpectedGeneration ||
				h.pathActiveRootLocked(condition.Path) != *root {
				return rgb11DKVSResponse(1, dkvsindexer.ErrWriteConflict.Error(), nil, 0)
			}
		}
		for _, mutation := range mutations {
			current := h.records[mutation.Record.Key]
			nextSeq := uint64(1)
			if current != nil {
				nextSeq = current.Seq + 1
			}
			if mutation.Record.Seq != nextSeq {
				return rgb11DKVSResponse(1, dkvsindexer.ErrWriteConflict.Error(), nil, 0)
			}
			if mutation.ExpectAbsent {
				if current != nil {
					return rgb11DKVSResponse(1, dkvsindexer.ErrWriteConflict.Error(), nil, 0)
				}
				continue
			}
			want, err := chainhash.NewHashFromStr(mutation.ExpectedHash)
			if err != nil || current == nil || dkvsindexer.RecordHash(current) != *want {
				return rgb11DKVSResponse(1, dkvsindexer.ErrWriteConflict.Error(), nil, 0)
			}
		}
		projected := len(h.records)
		for _, mutation := range mutations {
			_, existed := h.records[mutation.Record.Key]
			if dkvsindexer.IsTombstone(mutation.Record.Flags) {
				if existed {
					projected--
				}
			} else if !existed {
				projected++
			}
		}
		if h.maxRecords > 0 && projected > h.maxRecords {
			return rgb11DKVSResponse(1, dkvsindexer.ErrFeeCapacityExceeded.Error(), nil, 0)
		}
		touchedPaths := make(map[string]struct{})
		for _, mutation := range mutations {
			if dkvsindexer.IsTombstone(mutation.Record.Flags) {
				delete(h.records, mutation.Record.Key)
			} else {
				h.records[mutation.Record.Key] = cloneRGB11DKVSRecord(mutation.Record)
			}
			if path, err := dkvsindexer.CollectionPathForKey(mutation.Record.Key); err == nil {
				touchedPaths[path] = struct{}{}
			}
		}
		for path := range touchedPaths {
			h.generations[path]++
		}
	}
	records := make([]*swire.DKVSRecord, 0, len(mutations))
	hashes := make([]string, 0, len(mutations))
	for _, mutation := range mutations {
		records = append(records, cloneRGB11DKVSRecord(mutation.Record))
		hash := dkvsindexer.RecordHash(mutation.Record)
		hashes = append(hashes, hash.String())
	}
	return rgb11DKVSResponse(0, "ok", &DKVSBatchCASResult{
		Applied: len(mutations) - already, Records: records, Hashes: hashes,
	}, 0)
}

func (h *rgb11MemoryDKVSHTTP) pathActiveRootLocked(path string) chainhash.Hash {
	var root chainhash.Hash
	for key, record := range h.records {
		if key != path && !strings.HasPrefix(key, path+"/") {
			continue
		}
		recordHash := dkvsindexer.RecordHash(record)
		payload := make([]byte, 0, len(key)+chainhash.HashSize)
		payload = append(payload, key...)
		payload = append(payload, recordHash[:]...)
		leaf := chainhash.DoubleHashH(payload)
		for n := range root {
			root[n] ^= leaf[n]
		}
	}
	return root
}

func (h *rgb11MemoryDKVSHTTP) syncFiltered(req DKVSSyncRequest) ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	records := make([]*swire.DKVSRecord, 0)
	for key, record := range h.records {
		for _, filter := range req.Filters {
			if dkvsindexer.SubscriptionMatchesKey(filter, key) {
				records = append(records, cloneRGB11DKVSRecord(record))
				break
			}
		}
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Key < records[j].Key })
	root, err := dkvsindexer.DirectoryRootFromRecords(records, 0)
	if err != nil {
		return nil, err
	}
	return rgb11DKVSResponse(0, "ok", &DKVSSyncPage{
		Records: records, Done: true, Root: root.String(),
	}, 0)
}

func (h *rgb11MemoryDKVSHTTP) syncDirectory(req DKVSDirectorySyncRequest) ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	prefix := strings.TrimSuffix(req.Prefix, "/")
	records := make([]*swire.DKVSRecord, 0)
	for key, record := range h.records {
		if key == prefix || strings.HasPrefix(key, prefix+"/") {
			records = append(records, cloneRGB11DKVSRecord(record))
		}
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Key < records[j].Key })
	root, err := dkvsindexer.DirectoryRootFromRecords(records, 0)
	if err != nil {
		return nil, err
	}
	return rgb11DKVSResponse(0, "ok", &DKVSSyncPage{
		Records: records, Done: true, Root: root.String(),
	}, 0)
}

func (h *rgb11MemoryDKVSHTTP) SendGetRequest(url *URL) ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if strings.HasSuffix(url.Path, "/v3/dkvs/config") {
		return rgb11DKVSResponse(0, "ok", dkvsindexer.ClientConfig{FreeLocal: h.freeLocal, Blob: dkvsindexer.DefaultBlobPolicy(), MaxBatchMutations: dkvsindexer.MaxBatchCASMutations, MaxBatchBytes: dkvsindexer.MaxBatchCASTotalSize}, 0)
	}
	if strings.HasSuffix(url.Path, "/v3/dkvs/path-meta") {
		path := strings.TrimSuffix(url.Query["path"], "/")
		return rgb11DKVSResponse(0, "ok", &dkvsindexer.PathMeta{
			Version:    2,
			Path:       path,
			Generation: h.generations[path],
			ActiveRoot: h.pathActiveRootLocked(path),
		}, 0)
	}
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

func getRGB11RemoteHead(remote *rgb11MemoryDKVSHTTP, walletPubKey []byte,
	walletID string) (*coresync.WalletHead, *swire.DKVSRecord, error) {
	key, err := dkvsindexer.PersonalKey(walletPubKey, RGB11WalletHeadPath(walletID))
	if err != nil {
		return nil, nil, err
	}
	remote.mu.Lock()
	record := cloneRGB11DKVSRecord(remote.records[key])
	remote.mu.Unlock()
	if record == nil {
		return nil, nil, ErrDKVSRecordNotFound
	}
	if err := dkvsindexer.VerifyRecordForClient(record, dkvsindexer.RecordVerificationOptions{
		ExpectedKey: key, Now: record.IssueTime,
	}); err != nil {
		return nil, nil, err
	}
	head, err := rgb11wallet.DecodeWalletHead(record.Value)
	if err != nil {
		return nil, nil, err
	}
	if record.Seq != head.Seq {
		return nil, nil, coresync.ErrHeadSequence
	}
	if err := VerifyRGB11WalletHead(head, walletID); err != nil {
		return nil, nil, err
	}
	return head, record, nil
}

func getRGB11RemoteSnapshot(remote *rgb11MemoryDKVSHTTP, walletPubKey []byte,
	walletID string) ([]byte, [32]byte, *swire.DKVSRecord, error) {
	accountID := dkvsindexer.AccountID(walletPubKey)
	key, err := dkvsindexer.BlobKey(accountID, RGB11WalletSnapshotBlobKey(walletID))
	if err != nil {
		return nil, [32]byte{}, nil, err
	}
	remote.mu.Lock()
	record := cloneRGB11DKVSRecord(remote.records[key])
	remote.mu.Unlock()
	if record == nil {
		return nil, [32]byte{}, nil, ErrDKVSRecordNotFound
	}
	if err := dkvsindexer.VerifyRecordForClient(record, dkvsindexer.RecordVerificationOptions{
		ExpectedKey: key, Now: record.IssueTime,
	}); err != nil {
		return nil, [32]byte{}, nil, err
	}
	blob, err := DecodeDKVSBlobValue(record.Value)
	if err != nil {
		return nil, [32]byte{}, nil, err
	}
	envelopeWalletID, operationID, _, err := rgb11wallet.DecodeEncryptedSnapshot(blob.Data)
	if err != nil || envelopeWalletID != walletID {
		return nil, [32]byte{}, nil, ErrRGB11Inconsistent
	}
	return blob.Data, operationID, record, nil
}

func TestDKVSManagerBackgroundSyncRefreshesExactKey(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	manager := newRGB11MultiDeviceManager(t, priv, 990)
	remote := newRGB11MemoryDKVSHTTP()
	configureRGB11DKVSTestManager(manager, remote)
	dkvs := manager.ensureDKVSManager()
	const key = "/tmp/manager-exact-key"
	dkvs.rememberPaths([]string{key})

	writeRemote := func(seq uint64, value string) {
		record, err := NewDKVSSignedRecord(manager.wallet, key, []byte(value),
			dkvsindexer.RecordOptions{Seq: seq, TTL: 60_000})
		if err != nil {
			t.Fatal(err)
		}
		remote.mu.Lock()
		remote.records[key] = cloneRGB11DKVSRecord(record)
		remote.mu.Unlock()
	}

	writeRemote(1, "first")
	states, err := manager.syncDKVSOnce()
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 1 || len(states[0].Filters) != 1 ||
		states[0].Filters[0].Type != dkvsindexer.SubscriptionKey {
		t.Fatalf("exact-key sync state=%+v", states)
	}
	store, err := dkvs.primaryStore()
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.Get(key)
	if err != nil || string(first.Value) != "first" {
		t.Fatalf("first exact-key value=%+v err=%v", first, err)
	}

	writeRemote(2, "second")
	if _, err := manager.syncDKVSOnce(); err != nil {
		t.Fatal(err)
	}
	second, err := store.Get(key)
	if err != nil || second.Seq != 2 || string(second.Value) != "second" {
		t.Fatalf("updated exact-key value=%+v err=%v", second, err)
	}
}

func TestDKVSManagerReadinessRequiresCurrentSessionSync(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	manager := newRGB11MultiDeviceManager(t, priv, 991)
	remote := newRGB11MemoryDKVSHTTP()
	configureRGB11DKVSTestManager(manager, remote)
	const key = "/tmp/current-session-readiness"

	writeRemote := func(seq uint64, value string) {
		record, err := NewDKVSSignedRecord(manager.wallet, key, []byte(value),
			dkvsindexer.RecordOptions{Seq: seq, TTL: 60_000})
		if err != nil {
			t.Fatal(err)
		}
		remote.mu.Lock()
		remote.records[key] = cloneRGB11DKVSRecord(record)
		remote.mu.Unlock()
	}

	writeRemote(1, "stale")
	firstManager := manager.ensureDKVSManager()
	firstManager.rememberPaths([]string{key})
	if _, err := manager.syncDKVSOnce(); err != nil {
		t.Fatal(err)
	}

	writeRemote(2, "latest")
	manager.dkvs = newDKVSManager(manager)
	store, err := manager.dkvs.primaryStore()
	if err != nil {
		t.Fatal(err)
	}
	if store.IsReady(key) {
		t.Fatal("persisted baseline was treated as current-session readiness")
	}
	value, err := store.Get(key)
	if err != nil {
		t.Fatal(err)
	}
	if value.Seq != 2 || string(value.Value) != "latest" {
		t.Fatalf("current-session sync value=%+v", value)
	}
	if !store.IsReady(key) {
		t.Fatal("successful current-session sync did not mark the key ready")
	}
}

func TestDKVSManagerReadinessIsScopeIsolated(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	manager := newRGB11MultiDeviceManager(t, priv, 992)
	remote := newRGB11MemoryDKVSHTTP()
	configureRGB11DKVSTestManager(manager, remote)
	store, err := manager.ensureDKVSManager().primaryStore()
	if err != nil {
		t.Fatal(err)
	}
	const first = "/tmp/readiness-scope-a"
	const second = "/tmp/readiness-scope-b"
	if err := store.WaitReady(first); err != nil {
		t.Fatal(err)
	}
	if !store.IsReady(first) {
		t.Fatal("first scope was not marked ready")
	}
	if store.IsReady(second) {
		t.Fatal("first scope readiness leaked into the second scope")
	}
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
	return manager
}

func flushRGB11Background(t *testing.T, manager *Manager) {
	t.Helper()
	if _, err := manager.syncDKVSOnce(); err != nil {
		t.Fatal(err)
	}
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

func syncRGB11TestManager(t *testing.T, manager *Manager, client *SatsNetDKVSClient,
	walletID string) {
	t.Helper()
	if err := manager.syncRGB11WalletPaths(client, walletID); err != nil {
		t.Fatal(err)
	}
}

func TestRGB11ScopedManagersSerializeBackupAndReloadHead(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	remote := newRGB11MemoryDKVSHTTP()
	manager := newRGB11MultiDeviceManager(t, priv, 12)
	manager.cfg = &sdkcommon.Config{
		IndexerL2: &sdkcommon.Indexer{Scheme: "http", Host: "dkvs.test", Proxy: "testnet"},
	}
	manager.http = remote
	options := dkvsindexer.RecordOptions{TTL: uint64((24 * time.Hour) / time.Millisecond)}
	head1, err := manager.SyncRGB11WalletState("", options)
	if err != nil {
		t.Fatal(err)
	}
	if head1.Seq != 1 {
		t.Fatalf("initial head sequence=%d", head1.Seq)
	}

	createRGB11MultiDeviceInvoice(t, manager, "shared-scope-recipient")
	scoped, err := manager.newScopedRGB11Manager(localRGB11Account{
		WalletID: manager.status.CurrentWallet, AccountIndex: manager.status.CurrentAccount,
		Address: manager.wallet.GetAddress(), Wallet: manager.wallet,
	})
	if err != nil {
		t.Fatal(err)
	}
	if scoped.backupLock() != manager.rgbManager.backupLock() {
		t.Fatal("scoped RGB11 manager does not share the backup coordinator")
	}

	start := make(chan struct{})
	type backupResult struct {
		head *coresync.WalletHead
		err  error
	}
	results := make(chan backupResult, 2)
	for _, target := range []*rgb11Manager{manager.rgbManager, scoped} {
		go func(target *rgb11Manager) {
			<-start
			head, err := target.SyncRGB11WalletState("", options)
			results <- backupResult{head: head, err: err}
		}(target)
	}
	close(start)

	sequences := make([]uint64, 0, 2)
	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatal(result.err)
		}
		sequences = append(sequences, result.head.Seq)
	}
	sort.Slice(sequences, func(i, j int) bool { return sequences[i] < sequences[j] })
	if sequences[0] != 2 || sequences[1] != 2 {
		t.Fatalf("concurrent backup sequences=%v", sequences)
	}
}

func TestRGB11TwoDevicesRestoreLatestAndRejectStaleWriter(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	deviceA := newRGB11MultiDeviceManager(t, priv, 11)
	deviceB := newRGB11MultiDeviceManager(t, priv, 99)
	remote := newRGB11MemoryDKVSHTTP()
	opts := dkvsindexer.RecordOptions{TTL: uint64((24 * time.Hour) / time.Millisecond)}
	verify := dkvsindexer.RecordVerificationOptions{Now: uint64(time.Now().UnixMilli())}
	configureRGB11DKVSTestManager(deviceA, remote)
	configureRGB11DKVSTestManager(deviceB, remote)

	requestA := createRGB11MultiDeviceInvoice(t, deviceA, "device-a-recipient")
	walletID, err := deviceA.RGB11WalletID()
	if err != nil {
		t.Fatal(err)
	}
	head1, err := deviceA.SyncRGB11WalletState(walletID, opts)
	if err != nil {
		t.Fatal(err)
	}
	if head1.Seq != 1 {
		t.Fatalf("first head sequence=%d", head1.Seq)
	}

	restored1, err := deviceB.RestoreLatestRGB11WalletState(walletID, verify)
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
	head2, err := deviceB.SyncRGB11WalletState(walletID, opts)
	if err != nil {
		t.Fatal(err)
	}
	if head2.Seq != 2 {
		t.Fatalf("second head sequence=%d", head2.Seq)
	}

	createRGB11MultiDeviceInvoice(t, deviceA, "stale-device-a-recipient")
	if _, err := deviceA.SyncRGB11WalletState(walletID, opts); !errors.Is(err, coresync.ErrHeadConflict) {
		t.Fatalf("stale device A write was not rejected: %v", err)
	}
	if err := deviceA.rgbManager.requireLatestRGB11WalletState(); !errors.Is(err, coresync.ErrHeadConflict) {
		t.Fatalf("stale device A external-effect guard error=%v", err)
	}

	// Remote refresh belongs to the DKVS manager, not the RGB11 domain.
	client, err := deviceA.ensureDKVSManager().primaryClient()
	if err != nil {
		t.Fatal(err)
	}
	syncRGB11TestManager(t, deviceA, client, walletID)
	restored2, err := deviceA.RestoreLatestRGB11WalletState(walletID, verify)
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

func TestRGB11PublicMutationWaitsForRemoteState(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	deviceA := newRGB11MultiDeviceManager(t, priv, 993)
	deviceB := newRGB11MultiDeviceManager(t, priv, 994)
	remote := newRGB11MemoryDKVSHTTP()
	configureRGB11DKVSTestManager(deviceA, remote)
	configureRGB11DKVSTestManager(deviceB, remote)

	requestA := createRGB11MultiDeviceInvoice(t, deviceA, "device-a-before-sync")
	head1, err := deviceA.SyncRGB11WalletState("", dkvsindexer.RecordOptions{
		TTL: uint64((24 * time.Hour) / time.Millisecond),
	})
	if err != nil {
		t.Fatal(err)
	}
	if head1.Seq != 1 {
		t.Fatalf("device A head sequence=%d", head1.Seq)
	}

	requestB, err := deviceB.CreateRGB11Invoice(RGB11InvoiceRequest{
		AmountRaw: "2", WitnessVout: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := deviceB.rgbManager.engine.LoadReceive(requestA); err != nil {
		t.Fatalf("public mutation did not restore remote state first: %v", err)
	}
	if _, err := deviceB.rgbManager.engine.LoadReceive(requestB.RequestID); err != nil {
		t.Fatalf("device B mutation was not retained: %v", err)
	}
	flushRGB11Background(t, deviceB)

	walletID, err := deviceB.RGB11WalletID()
	if err != nil {
		t.Fatal(err)
	}
	head2, _, err := getRGB11RemoteHead(remote, priv.PubKey().SerializeCompressed(), walletID)
	if err != nil {
		t.Fatal(err)
	}
	if head2.Seq != 2 {
		t.Fatalf("merged remote head sequence=%d", head2.Seq)
	}
}

func TestRGB11BackgroundBackupIsBoundToScheduledScope(t *testing.T) {
	database := indexerdb.NewKVDB(t.TempDir())
	t.Cleanup(func() { database.Close() })
	walletA, _, err := NewInteralWallet(GetChainParam())
	if err != nil {
		t.Fatal(err)
	}
	walletB, _, err := NewInteralWallet(GetChainParam())
	if err != nil {
		t.Fatal(err)
	}
	manager := &Manager{
		db: database, wallet: walletA,
		status:        &Status{CurrentWallet: walletA.GetId(), CurrentAccount: 0},
		tickerInfoMap: make(map[string]*indexer.TickerInfo),
		walletInfoMap: map[int64]*WalletInfo{
			walletA.GetId(): {
				WalletInDB: WalletInDB{Id: walletA.GetId(), Accounts: 2},
				Wallet:     walletA,
			},
			walletB.GetId(): {
				WalletInDB: WalletInDB{Id: walletB.GetId(), Accounts: 1},
				Wallet:     walletB,
			},
		},
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
	remote := newRGB11MemoryDKVSHTTP()
	configureRGB11DKVSTestManager(manager, remote)
	options := dkvsindexer.RecordOptions{TTL: uint64((24 * time.Hour) / time.Millisecond)}

	scopeWalletID := func(wallet sdkcommon.Wallet, walletID int64, accountIndex uint32) string {
		t.Helper()
		fixed := wallet.Clone()
		fixed.SetSubAccount(accountIndex)
		scoped, err := manager.newScopedRGB11Manager(localRGB11Account{
			WalletID: walletID, AccountIndex: accountIndex,
			Address: fixed.GetAddress(), Wallet: fixed,
		})
		if err != nil {
			t.Fatal(err)
		}
		id, err := scoped.RGB11WalletID()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	selectScope := func(wallet sdkcommon.Wallet, walletID int64, accountIndex uint32) {
		t.Helper()
		wallet.SetSubAccount(accountIndex)
		manager.wallet = wallet
		manager.status.CurrentWallet = walletID
		manager.status.CurrentAccount = accountIndex
		if err := manager.rgbManager.selectRGB11Scope(); err != nil {
			t.Fatal(err)
		}
	}

	walletAAccount0ID := scopeWalletID(walletA, walletA.GetId(), 0)
	if _, err := manager.SyncRGB11WalletState("", options); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.CreateRGB11Invoice(RGB11InvoiceRequest{
		AmountRaw: "1", WitnessVout: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if status := manager.rgbManager.scopeStates.load(
		rgb11StorageScope(walletA.GetId(), 0)).Status; status != "pending" {
		t.Fatalf("wallet A queued status=%q", status)
	}

	selectScope(walletB, walletB.GetId(), 0)
	if state := manager.rgbManager.rgb11ScopeState(); state.Status != "offline" || state.AutoBackup != nil {
		t.Fatalf("wallet B inherited wallet A backup state: %+v", state)
	}
	flushRGB11Background(t, manager)
	if state := manager.rgbManager.rgb11ScopeState(); state.Status != "offline" {
		t.Fatalf("wallet B status changed after wallet A backup: %+v", state)
	}
	if state := manager.rgbManager.scopeStates.load(
		rgb11StorageScope(walletA.GetId(), 0)); state.Status != "synced" {
		t.Fatalf("wallet A backup status=%+v", state)
	}
	headA0, _, err := getRGB11RemoteHead(
		remote, walletA.GetPubKeyByIndex(0).SerializeCompressed(), walletAAccount0ID)
	if err != nil || headA0.Seq != 2 {
		t.Fatalf("wallet A remote head=%+v err=%v", headA0, err)
	}
	walletBAccount0ID := scopeWalletID(walletB, walletB.GetId(), 0)
	if _, _, err := getRGB11RemoteHead(
		remote, walletB.GetPubKeyByIndex(0).SerializeCompressed(), walletBAccount0ID,
	); !errors.Is(err, ErrDKVSRecordNotFound) {
		t.Fatalf("wallet A backup wrote wallet B scope: %v", err)
	}

	selectScope(walletA, walletA.GetId(), 1)
	walletAAccount1ID := scopeWalletID(walletA, walletA.GetId(), 1)
	if _, err := manager.SyncRGB11WalletState("", options); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.CreateRGB11Invoice(RGB11InvoiceRequest{
		AmountRaw: "2", WitnessVout: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if status := manager.rgbManager.rgb11ScopeState().Status; status != "pending" {
		t.Fatalf("wallet A account 1 queued status=%q", status)
	}

	selectScope(walletA, walletA.GetId(), 0)
	if state := manager.rgbManager.rgb11ScopeState(); state.Status != "synced" {
		t.Fatalf("wallet A account 0 inherited account 1 status: %+v", state)
	}
	flushRGB11Background(t, manager)
	headA1, _, err := getRGB11RemoteHead(
		remote, walletA.GetPubKeyByIndex(1).SerializeCompressed(), walletAAccount1ID)
	if err != nil || headA1.Seq != 2 {
		t.Fatalf("wallet A account 1 remote head=%+v err=%v", headA1, err)
	}
	headA0After, _, err := getRGB11RemoteHead(
		remote, walletA.GetPubKeyByIndex(0).SerializeCompressed(), walletAAccount0ID)
	if err != nil || headA0After.Seq != headA0.Seq {
		t.Fatalf("account 1 backup changed account 0 head: before=%+v after=%+v err=%v",
			headA0, headA0After, err)
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
	verify := dkvsindexer.RecordVerificationOptions{Now: uint64(time.Now().UnixMilli())}
	if _, _, err := getRGB11RemoteHead(remote, priv.PubKey().SerializeCompressed(), walletID); !errors.Is(err, ErrDKVSRecordNotFound) {
		t.Fatalf("first invoice unexpectedly triggered a paid backup: %v", err)
	}

	options := dkvsindexer.RecordOptions{TTL: uint64((24 * time.Hour) / time.Millisecond)}
	head1, err := deviceA.SyncRGB11WalletState("", options)
	if err != nil {
		t.Fatal(err)
	}
	policy := deviceA.rgbManager.rgb11AutoBackupPolicy()
	if head1.Seq != 1 || policy == nil || !policy.Enabled {
		t.Fatalf("manual first backup did not enable automatic backup: head=%+v policy=%+v", head1, policy)
	}
	remote.mu.Lock()
	for key, record := range remote.records {
		if !strings.HasPrefix(key, "/blob/") && !strings.HasPrefix(key, "/personal/") {
			continue
		}
		proof, err := dkvsindexer.ParseFeeProof(record.FeeProof)
		if err != nil || proof.Mode != dkvsindexer.FeeModeFreeLocal {
			remote.mu.Unlock()
			t.Fatalf("RGB11 backup record %s has no valid FREE_LOCAL proof: proof=%+v err=%v", key, proof, err)
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
	flushRGB11Background(t, deviceA)
	head2, _, err := getRGB11RemoteHead(remote, priv.PubKey().SerializeCompressed(), walletID)
	if err != nil {
		t.Fatal(err)
	}
	if head2.Seq != 2 {
		t.Fatalf("post-enrollment invoice did not auto-backup: head sequence=%d", head2.Seq)
	}
	deviceA.rgbManager.autoBackupRGB11AfterMutation()
	flushRGB11Background(t, deviceA)
	unchanged, _, err := getRGB11RemoteHead(remote, priv.PubKey().SerializeCompressed(), walletID)
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
	flushRGB11Background(t, deviceB)
	head3, _, err := getRGB11RemoteHead(remote, priv.PubKey().SerializeCompressed(), walletID)
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
	if err != nil || missing.Found || missing.Restored || !missing.AutoBackup || missing.Head != nil {
		t.Fatalf("wallet without a backup should enable temporary automatic backup: result=%+v err=%v", missing, err)
	}
}

func TestRGB11ActivationPublishesHigherUnsyncedLocalState(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	remote := newRGB11MemoryDKVSHTTP()
	manager := newRGB11MultiDeviceManager(t, priv, 778)
	manager.cfg = &sdkcommon.Config{IndexerL2: &sdkcommon.Indexer{Scheme: "http", Host: "dkvs.test", Proxy: "testnet"}}
	manager.http = remote

	first, err := manager.CreateRGB11Invoice(RGB11InvoiceRequest{AmountRaw: "1", WitnessVout: 1})
	if err != nil {
		t.Fatal(err)
	}
	options := dkvsindexer.RecordOptions{TTL: uint64((24 * time.Hour) / time.Millisecond)}
	head1, err := manager.SyncRGB11WalletState("", options)
	if err != nil {
		t.Fatal(err)
	}
	manager.rgbManager.updateRGB11ScopeState(func(state *rgb11ScopeBackupState) {
		state.AutoBackup = nil
	})
	second, err := manager.CreateRGB11Invoice(RGB11InvoiceRequest{AmountRaw: "2", WitnessVout: 1})
	if err != nil {
		t.Fatal(err)
	}

	activation, err := manager.ActivateRGB11WalletState(dkvsindexer.RecordVerificationOptions{
		Now: uint64(time.Now().UnixMilli()),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !activation.Found || activation.Restored || !activation.AutoBackup ||
		activation.Head == nil || activation.Head.Seq != head1.Seq+1 {
		t.Fatalf("unsynced local activation=%+v", activation)
	}
	if _, err := manager.rgbManager.engine.LoadReceive(first.RequestID); err != nil {
		t.Fatalf("first local state missing after activation: %v", err)
	}
	if _, err := manager.rgbManager.engine.LoadReceive(second.RequestID); err != nil {
		t.Fatalf("newer local state missing after activation: %v", err)
	}

	walletID, err := manager.RGB11WalletID()
	if err != nil {
		t.Fatal(err)
	}
	active, _, err := getRGB11RemoteHead(remote, priv.PubKey().SerializeCompressed(), walletID)
	if err != nil || active.Seq != head1.Seq+1 {
		t.Fatalf("remote head was not advanced from local state: head=%+v err=%v", active, err)
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
	if activation.Found || activation.Restored || !activation.AutoBackup || activation.Head != nil {
		t.Fatalf("paid AUTOPAY activation=%+v", activation)
	}
	policy := manager.rgbManager.rgb11AutoBackupPolicy()
	if policy == nil || !policy.Enabled {
		t.Fatalf("paid AUTOPAY did not enable automatic backup: %+v", policy)
	}
	remote.mu.Lock()
	recordCount := len(remote.records)
	remote.mu.Unlock()
	if recordCount != 0 {
		t.Fatalf("empty wallet activation wrote %d DKVS records", recordCount)
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
	if activation.Found || activation.Restored || !activation.AutoBackup || activation.Head != nil {
		t.Fatalf("inactive AUTOPAY activation=%+v", activation)
	}
	if policy := manager.rgbManager.rgb11AutoBackupPolicy(); policy == nil || !policy.Enabled {
		t.Fatalf("inactive AUTOPAY did not enable temporary automatic backup: %+v", policy)
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
	if policy := manager.rgbManager.rgb11AutoBackupPolicy(); policy != nil {
		t.Fatalf("AUTOPAY lookup failure enabled automatic backup: %+v", policy)
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
		_, _ = manager.syncDKVSOnce()
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
	options := dkvsindexer.RecordOptions{TTL: uint64((24 * time.Hour) / time.Millisecond)}
	walletID, err := deviceA.RGB11WalletID()
	if err != nil {
		t.Fatal(err)
	}
	configureRGB11DKVSTestManager(deviceA, remote)
	configureRGB11DKVSTestManager(deviceB, remote)
	head, err := deviceA.SyncRGB11WalletState(walletID, options)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := deviceB.RestoreLatestRGB11WalletState(walletID,
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
