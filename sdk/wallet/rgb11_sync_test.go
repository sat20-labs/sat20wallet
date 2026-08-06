package wallet

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	indexer "github.com/sat20-labs/indexer/common"
	indexerdb "github.com/sat20-labs/indexer/indexer/db"
	"github.com/sat20-labs/rgb11/invoicing"
	corerelay "github.com/sat20-labs/rgb11/relay"
	corewallet "github.com/sat20-labs/rgb11/wallet"
	sdkcommon "github.com/sat20-labs/sat20wallet/sdk/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRGB11SnapshotDoesNotCopyGlobalTickerCatalog(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	manager := newRGB11MultiDeviceManager(t, priv, 42)
	globalName := indexer.AssetName{Protocol: rgb11wallet.Protocol, Type: indexer.ASSET_TYPE_FT, Ticker: "global_test"}
	manager.tickerInfoMap[globalName.String()] = &indexer.TickerInfo{
		AssetName: globalName, DisplayName: "Global RGB Test",
	}
	walletID, err := manager.RGB11WalletID()
	if err != nil {
		t.Fatal(err)
	}
	wantWalletID := "rgb11-" + hex.EncodeToString(manager.wallet.GetPubKey().SerializeCompressed())
	if walletID != wantWalletID {
		t.Fatalf("RGB11 wallet key contains format version: got=%s want=%s", walletID, wantWalletID)
	}
	snapshot, _, err := manager.rgbManager.exportRGB11WalletSnapshot(walletID)
	if err != nil {
		t.Fatal(err)
	}
	if rgb11SnapshotHasState(snapshot) {
		t.Fatal("empty wallet inherited global RGB ticker metadata")
	}
}

func TestRGB11SnapshotPreflightDoesNotPartiallyImportEngineState(t *testing.T) {
	sourcePriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	source := newRGB11MultiDeviceManager(t, sourcePriv, 42)
	createRGB11MultiDeviceInvoice(t, source, "recipient-preflight")
	engineRecords, err := source.rgbManager.engineStore.ExportSnapshot()
	if err != nil || len(engineRecords) != 1 {
		t.Fatalf("source engine records=%d err=%v", len(engineRecords), err)
	}

	targetPriv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	target := newRGB11MultiDeviceManager(t, targetPriv, 43)
	snapshot := &RGB11WalletSnapshot{
		Version: rgb11WalletSnapshotVersion, EngineRecords: engineRecords,
		ProjectionRecords: []rgb11wallet.SnapshotRecord{{Key: "invalid-record", Value: []byte{1}}},
	}
	if err := target.rgbManager.importRGB11WalletSnapshot(snapshot); err == nil {
		t.Fatal("invalid projection snapshot was accepted")
	}
	restoredEngine, err := target.rgbManager.engineStore.ExportSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(restoredEngine) != 0 {
		t.Fatalf("engine state was imported before projection preflight: %+v", restoredEngine)
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
		ExpectedKey: relayKey,
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
		ExpectedKey: ackKey,
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

func TestDKVSManagerObserverCanRefresh(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	manager := newRGB11MultiDeviceManager(t, priv, 992)
	remote := newRGB11MemoryDKVSHTTP()
	configureRGB11DKVSTestManager(manager, remote)
	dkvs := manager.ensureDKVSManager()
	const key = "/tmp/observer-refresh"
	dkvs.rememberPaths([]string{key})

	record, err := NewDKVSSignedRecord(manager.wallet, key, []byte("value"),
		dkvsindexer.RecordOptions{Seq: 1, TTL: 60_000})
	if err != nil {
		t.Fatal(err)
	}
	remote.mu.Lock()
	remote.records[key] = cloneRGB11DKVSRecord(record)
	remote.mu.Unlock()

	observerDone := make(chan error, 1)
	dkvs.addObserver(func(_ []string) {
		store, storeErr := dkvs.primaryStore()
		if storeErr == nil {
			storeErr = store.Refresh(key)
		}
		observerDone <- storeErr
	})
	syncDone := make(chan error, 1)
	go func() {
		_, syncErr := manager.syncDKVSOnce()
		syncDone <- syncErr
	}()

	select {
	case err := <-syncDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("DKVS synchronization deadlocked while notifying an observer")
	}
	select {
	case err := <-observerDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("DKVS observer refresh did not complete")
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
		status:        &Status{CurrentWallet: localWalletID, CurrentAccount: 0, SyncHeightL2: 1},
		tickerInfoMap: make(map[string]*indexer.TickerInfo),
		utxoLockerL1:  NewUtxoLocker(database, nil, L1_NETWORK_BITCOIN),
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
	t.Cleanup(func() { manager.rgbManager.scopeStates.stopReconciliations() })
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
