package wallet

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sat20-labs/sat20wallet/sdk/common"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

const dkvsIdleSyncInterval = 30 * time.Second

// dkvsManager is the SDK's only DKVS coordination layer. Domain modules use
// dkvsStore and never own transport, sequence, generation, replica or outbox
// state directly.
type dkvsManager struct {
	owner *Manager

	mu        sync.Mutex
	runMu     sync.Mutex
	stop      chan struct{}
	done      chan struct{}
	wake      chan struct{}
	stopping  bool
	callback  func()
	observers []func([]string)
	jobs      map[string]func(*dkvsStore) error
	clients   map[string]*SatsNetDKVSClient
	paths     map[string]struct{}
	exactKeys map[string]struct{}
	ready     map[string]struct{}

	verifyMu          sync.RWMutex
	verifyHeight      uint64
	verifyHeightKnown bool
}

type dkvsSignatureMode uint8

const (
	dkvsSignatureAccount dkvsSignatureMode = iota
	dkvsSignatureLegacy
)

type dkvsStoragePolicy struct {
	TTL       uint64
	FreeLocal bool
	Autopay   *DKVSAutopayOptions
}

type dkvsValue struct {
	Key         string
	Value       []byte
	Seq         uint64
	IssueHeight uint64
	TTL         uint64
	Flags       uint32
	Hash        string
	Signer      []byte
	record      *swire.DKVSRecord
}

type dkvsValueMutation struct {
	Key         string
	Value       []byte
	BuildValue  func(nextSeq uint64) ([]byte, error)
	Owner       common.Wallet
	Policy      dkvsStoragePolicy
	Signature   dkvsSignatureMode
	Tombstone   bool
	IssueHeight uint64
}

type dkvsUpdateBuilder func(current map[string]*dkvsValue,
	nextSequence map[string]uint64) ([]dkvsValueMutation, error)

// dkvsStore is the only interface wallet features use for DKVS data. The
// transport client remains private to the manager layer.
type dkvsStore struct {
	manager *dkvsManager
	client  *SatsNetDKVSClient
}

var ErrDKVSVerificationHeightRequired = errors.New(
	"DKVS record verification requires a trusted L2 height")

func (m *dkvsManager) observeVerificationHeight(height uint64, known bool) {
	if m == nil || !known {
		return
	}
	m.verifyMu.Lock()
	if !m.verifyHeightKnown || height > m.verifyHeight {
		m.verifyHeight = height
	}
	m.verifyHeightKnown = true
	m.verifyMu.Unlock()
}

func (m *dkvsManager) observeVerificationOptions(options dkvsindexer.RecordVerificationOptions) {
	if options.Height != 0 {
		m.observeVerificationHeight(options.Height, true)
	}
}

func (m *dkvsManager) verificationHeight() (uint64, bool) {
	if m == nil {
		return 0, false
	}
	m.verifyMu.RLock()
	height, known := m.verifyHeight, m.verifyHeightKnown
	m.verifyMu.RUnlock()
	if m.owner != nil && m.owner.status != nil {
		m.owner.status.RLock()
		statusHeight := m.owner.status.SyncHeightL2
		m.owner.status.RUnlock()
		if statusHeight >= 0 {
			value := uint64(statusHeight)
			if !known || value > height {
				height = value
			}
			known = true
		}
	}
	if known {
		m.observeVerificationHeight(height, true)
	}
	return height, known
}

func normalizeDKVSRecordVerification(record *swire.DKVSRecord, key string,
	options dkvsindexer.RecordVerificationOptions, fallbackHeight uint64, heightKnown bool) (
	dkvsindexer.RecordVerificationOptions, error) {

	options.ExpectedKey = key
	if options.Height != 0 {
		fallbackHeight, heightKnown = options.Height, true
	}
	expiry := dkvsindexer.RecordExpiryHeight(record)
	if expiry != 0 {
		if !heightKnown {
			return options, ErrDKVSVerificationHeightRequired
		}
		options.Height = fallbackHeight
		if fallbackHeight >= expiry {
			return options, dkvsindexer.ErrExpiredRecord
		}
	} else if heightKnown {
		options.Height = fallbackHeight
	}
	return options, nil
}

func (s *dkvsStore) ObserveVerificationOptions(options dkvsindexer.RecordVerificationOptions) {
	if s != nil && s.manager != nil {
		s.manager.observeVerificationOptions(options)
	}
}

func (s *dkvsStore) verificationOptions(record *swire.DKVSRecord, key string,
	options dkvsindexer.RecordVerificationOptions) (dkvsindexer.RecordVerificationOptions, error) {
	if s == nil || s.manager == nil {
		return options, ErrDKVSPathNotSynced
	}
	s.ObserveVerificationOptions(options)
	height, known := s.manager.verificationHeight()
	return normalizeDKVSRecordVerification(record, key, options, height, known)
}

func (s *dkvsStore) IsReady(keys ...string) bool {
	return s != nil && s.manager != nil && s.client != nil &&
		s.manager.pathsReady(s.client, keys)
}

func (s *dkvsStore) WaitReady(keys ...string) error {
	if s == nil || s.manager == nil || s.client == nil {
		return ErrDKVSPathNotSynced
	}
	return s.manager.waitPathsReady(s.client, keys)
}

func (m *dkvsManager) primaryStore() (*dkvsStore, error) {
	client, err := m.primaryClient()
	if err != nil {
		return nil, err
	}
	return &dkvsStore{manager: m, client: client}, nil
}

func (m *dkvsManager) storeFor(scheme, host, proxy string, http HttpClient) (*dkvsStore, error) {
	client, err := m.clientFor(scheme, host, proxy, http)
	if err != nil {
		return nil, err
	}
	client.manager = m
	client.replicaNamespace = m.owner.dkvsReplicaNamespaceFor(scheme, host, proxy)
	return &dkvsStore{manager: m, client: client}, nil
}

func cloneDKVSValue(record *swire.DKVSRecord) *dkvsValue {
	if record == nil {
		return nil
	}
	recordCopy := *record
	recordCopy.Value = append([]byte(nil), record.Value...)
	recordCopy.PubKey = append([]byte(nil), record.PubKey...)
	recordCopy.Signature = append([]byte(nil), record.Signature...)
	recordCopy.FeeProof = append([]byte(nil), record.FeeProof...)
	return &dkvsValue{
		Key: record.Key, Value: append([]byte(nil), record.Value...),
		Seq: record.Seq, IssueHeight: record.IssueHeight, TTL: record.TTL,
		Flags: record.Flags, Hash: dkvsindexer.RecordHash(record).String(),
		Signer: append([]byte(nil), record.PubKey...), record: &recordCopy,
	}
}

func (s *dkvsStore) Get(key string) (*dkvsValue, error) {
	return s.GetVerified(key, dkvsindexer.RecordVerificationOptions{})
}

func (s *dkvsStore) GetVerified(key string, options dkvsindexer.RecordVerificationOptions) (*dkvsValue, error) {
	if s == nil || s.manager == nil || s.client == nil {
		return nil, ErrDKVSPathNotSynced
	}
	if err := s.WaitReady(key); err != nil {
		return nil, err
	}
	record, err := s.manager.confirmedRecord(s.client, key)
	if err != nil {
		return nil, err
	}
	if record == nil {
		return nil, ErrDKVSRecordNotFound
	}
	options, err = s.verificationOptions(record, key, options)
	if err != nil {
		return nil, err
	}
	if err := dkvsindexer.VerifyRecordForClient(record, options); err != nil {
		return nil, err
	}
	return cloneDKVSValue(record), nil
}

func (s *dkvsStore) Put(mutation dkvsValueMutation) (*dkvsValue, error) {
	values, err := s.PutBatch([]dkvsValueMutation{mutation})
	if err != nil {
		return nil, err
	}
	if len(values) != 1 {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	return values[0], nil
}

func (s *dkvsStore) PutBatch(mutations []dkvsValueMutation) ([]*dkvsValue, error) {
	if s == nil || s.manager == nil || s.client == nil {
		return nil, ErrDKVSPathNotSynced
	}
	return s.manager.putValues(s.client, mutations)
}

func (s *dkvsStore) Update(keys []string, builder dkvsUpdateBuilder) ([]*dkvsValue, error) {
	if s == nil || s.manager == nil || s.client == nil {
		return nil, ErrDKVSPathNotSynced
	}
	return s.manager.updateValues(s.client, keys, builder)
}

func (s *dkvsStore) Config() (*AccountFreeLocalPolicy, error) {
	if s == nil || s.client == nil {
		return nil, ErrDKVSPathNotSynced
	}
	return s.client.GetConfig()
}

func (s *dkvsStore) List(prefix string) ([]*dkvsValue, error) {
	return s.ListVerified(prefix, dkvsindexer.RecordVerificationOptions{})
}

func (s *dkvsStore) ListVerified(prefix string, options dkvsindexer.RecordVerificationOptions) ([]*dkvsValue, error) {
	if s == nil || s.manager == nil || s.client == nil {
		return nil, ErrDKVSPathNotSynced
	}
	prefix = strings.TrimSuffix(strings.TrimSpace(prefix), "/")
	if _, err := dkvsindexer.ParsePrefix(prefix); err != nil {
		return nil, err
	}
	if err := s.manager.waitDirectoriesReady(s.client, []string{prefix}); err != nil {
		return nil, err
	}
	filters := []dkvsindexer.Subscription{{Type: dkvsindexer.SubscriptionPrefix, Target: prefix}}
	scope := dkvsReplicaScope(s.client.replicaNamespace, filters)
	records, err := newDKVSReplicaStore(s.manager.owner.db).loadConfirmed(scope)
	if err != nil {
		return nil, err
	}
	values := make([]*dkvsValue, 0, len(records))
	for _, record := range records {
		if record == nil || (record.Key != prefix && !strings.HasPrefix(record.Key, prefix+"/")) {
			continue
		}
		verify, verifyErr := s.verificationOptions(record, record.Key, options)
		if errors.Is(verifyErr, dkvsindexer.ErrExpiredRecord) {
			continue
		}
		if verifyErr != nil {
			return nil, verifyErr
		}
		if verifyErr = dkvsindexer.VerifyRecordForClient(record, verify); errors.Is(verifyErr, dkvsindexer.ErrExpiredRecord) {
			continue
		} else if verifyErr != nil {
			return nil, verifyErr
		}
		values = append(values, cloneDKVSValue(record))
	}
	return values, nil
}

func (s *dkvsStore) Refresh(keys ...string) error {
	if s == nil || s.manager == nil || s.client == nil {
		return ErrDKVSPathNotSynced
	}
	return s.manager.refreshPaths(s.client, keys)
}

func dkvsManagedPathForKey(key string) (string, bool, error) {
	if _, err := dkvsindexer.ParseKey(key); err != nil {
		return "", false, err
	}
	path, err := dkvsindexer.CollectionPathForKey(key)
	if err != nil {
		if errors.Is(err, dkvsindexer.ErrInvalidKey) {
			return key, false, nil
		}
		return "", false, err
	}
	return path, true, nil
}

func (m *dkvsManager) managesKey(key string) bool {
	_, _, err := dkvsManagedPathForKey(key)
	return err == nil
}

func (m *dkvsManager) managesMutations(mutations []dkvsindexer.CASMutation) bool {
	if len(mutations) == 0 {
		return false
	}
	for _, mutation := range mutations {
		if mutation.Record == nil || !m.managesKey(mutation.Record.Key) {
			return false
		}
	}
	return true
}

func (m *dkvsManager) putRecord(client *SatsNetDKVSClient,
	record *swire.DKVSRecord) (*swire.DKVSRecord, error) {

	if m == nil || record == nil {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	if err := m.waitPathsReady(client, []string{record.Key}); err != nil {
		return nil, err
	}
	unlock, err := m.lockPathsForKeys([]string{record.Key})
	if err != nil {
		return nil, err
	}
	defer unlock()

	existing, err := m.confirmedRecord(client, record.Key)
	if err != nil {
		return nil, err
	}
	if existing != nil && dkvsindexer.RecordHash(existing) == dkvsindexer.RecordHash(record) {
		return existing, nil
	}
	prepared := *record
	prepared.Value = append([]byte(nil), record.Value...)
	prepared.PubKey = append([]byte(nil), record.PubKey...)
	prepared.FeeProof = append([]byte(nil), record.FeeProof...)
	prepared.Signature = append([]byte(nil), record.Signature...)
	if prepared.IssueHeight == 0 {
		if dkvsindexer.RecordRequiresPathPrecondition(&prepared) {
			context, contextErr := m.owner.syncedPathWriteContext(client, []string{prepared.Key})
			if contextErr != nil {
				return nil, contextErr
			}
			path, pathErr := dkvsindexer.CollectionPathForKey(prepared.Key)
			if pathErr != nil {
				return nil, pathErr
			}
			meta := context.PathMeta[path]
			if meta == nil {
				return nil, dkvsindexer.ErrStaleGeneration
			}
			prepared.IssueHeight = meta.ViewHeight
		} else {
			height, known := m.verificationHeight()
			if !known {
				return nil, ErrDKVSVerificationHeightRequired
			}
			prepared.IssueHeight = height
		}
		prepared.Signature = nil
		if m.owner == nil || m.owner.wallet == nil {
			return nil, dkvsindexer.ErrInvalidSignature
		}
		if err := SignDKVSRecord(m.owner.wallet, &prepared); err != nil {
			return nil, err
		}
	}
	precondition := dkvsindexer.WritePrecondition{ExpectAbsent: true}
	if existing != nil {
		hash := dkvsindexer.RecordHash(existing)
		precondition = dkvsindexer.WritePrecondition{ExpectedHash: &hash}
	}
	result, err := m.putBatchCASLocked(client, []dkvsindexer.CASMutation{{
		Record: &prepared, Precondition: precondition,
	}})
	if err != nil {
		return nil, err
	}
	if len(result.Records) != 1 {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	return result.Records[0], nil
}

func (m *dkvsManager) confirmedRecord(client *SatsNetDKVSClient, key string) (*swire.DKVSRecord, error) {
	if m == nil || m.owner == nil || m.owner.db == nil || client == nil {
		return nil, ErrDKVSPathNotSynced
	}
	target, managed, err := dkvsManagedPathForKey(key)
	if err != nil {
		return nil, err
	}
	filterType := dkvsindexer.SubscriptionKey
	if managed {
		filterType = dkvsindexer.SubscriptionPrefix
	}
	filters := []dkvsindexer.Subscription{{Type: filterType, Target: target}}
	scope := dkvsReplicaScope(client.replicaNamespace, filters)
	if !m.scopeReady(scope) {
		return nil, ErrDKVSPathNotSynced
	}
	store := newDKVSReplicaStore(m.owner.db)
	if _, err := store.loadBaseline(scope); err != nil {
		return nil, ErrDKVSPathNotSynced
	}
	records, err := store.loadConfirmed(scope)
	if err != nil {
		return nil, err
	}
	for _, candidate := range records {
		if candidate.Key == key {
			return candidate, nil
		}
	}
	return nil, nil
}

func (m *dkvsManager) putBatchCAS(client *SatsNetDKVSClient,
	mutations []dkvsindexer.CASMutation) (*DKVSBatchCASResult, error) {

	if m == nil || m.owner == nil || client == nil || len(mutations) == 0 {
		return nil, fmt.Errorf("DKVS manager write is unavailable")
	}
	keys := make([]string, 0, len(mutations))
	for _, mutation := range mutations {
		if mutation.Record == nil {
			return nil, dkvsindexer.ErrInvalidRecord
		}
		keys = append(keys, mutation.Record.Key)
	}
	if err := m.waitPathsReady(client, keys); err != nil {
		return nil, err
	}
	unlock, err := m.lockPathsForKeys(keys)
	if err != nil {
		return nil, err
	}
	defer unlock()
	return m.putBatchCASLocked(client, mutations)
}

func (m *dkvsManager) putBatchCASLocked(client *SatsNetDKVSClient,
	mutations []dkvsindexer.CASMutation) (*DKVSBatchCASResult, error) {

	keys := make([]string, 0, len(mutations))
	relayableKeys := make([]string, 0, len(mutations))
	records := make([]*swire.DKVSRecord, 0, len(mutations))
	for _, mutation := range mutations {
		if mutation.Record == nil {
			return nil, dkvsindexer.ErrInvalidRecord
		}
		keys = append(keys, mutation.Record.Key)
		records = append(records, mutation.Record)
		if dkvsindexer.RecordRequiresPathPrecondition(mutation.Record) {
			relayableKeys = append(relayableKeys, mutation.Record.Key)
		}
	}
	context := &dkvsPathWriteContext{
		ServerTimeMS: make(map[string]uint64),
		PathMeta:     make(map[string]*dkvsindexer.PathMeta),
	}
	var err error
	if len(relayableKeys) != 0 {
		context, err = m.owner.syncedPathWriteContext(client, relayableKeys)
		if err != nil {
			return nil, err
		}
	}
	confirmedByKey := make(map[string]*swire.DKVSRecord, len(keys))
	exactCount := 0
	for _, key := range keys {
		existing, loadErr := m.confirmedRecord(client, key)
		if loadErr != nil {
			return nil, loadErr
		}
		if existing != nil {
			confirmedByKey[key] = existing
		}
	}
	for _, mutation := range mutations {
		existing := confirmedByKey[mutation.Record.Key]
		if existing != nil && dkvsindexer.RecordHash(existing) == dkvsindexer.RecordHash(mutation.Record) {
			exactCount++
			continue
		}
		if existing == nil {
			if mutation.Record.Seq != 1 {
				return nil, dkvsindexer.ErrInvalidSequence
			}
		} else if mutation.Record.Seq != existing.Seq+1 {
			return nil, dkvsindexer.ErrInvalidSequence
		}
	}
	if exactCount != 0 {
		if exactCount != len(mutations) {
			return nil, dkvsindexer.ErrWriteConflict
		}
		result := &DKVSBatchCASResult{Applied: 0}
		for _, mutation := range mutations {
			record := confirmedByKey[mutation.Record.Key]
			result.Records = append(result.Records, record)
			result.Hashes = append(result.Hashes, dkvsindexer.RecordHash(record).String())
		}
		return result, nil
	}
	if err := m.ensureEndpointAffinity(client, records); err != nil {
		return nil, err
	}
	// PutRecordBatchCASV1 persists the complete signed batch, all CAS
	// preconditions and endpoint identity as one durable outbox entry. A second
	// per-record outbox would split the atomic retry state and is prohibited.
	store := newDKVSReplicaStore(m.owner.db)
	writeResult, err := client.PutRecordBatchCASV1(mutations, context.Conditions)
	if err != nil {
		return nil, err
	}
	paths := make(map[string]struct{})
	for _, mutation := range mutations {
		path, pathErr := dkvsindexer.CollectionPathForKey(mutation.Record.Key)
		if pathErr != nil {
			return nil, pathErr
		}
		paths[path] = struct{}{}
	}
	orderedPaths := make([]string, 0, len(paths))
	for path := range paths {
		orderedPaths = append(orderedPaths, path)
	}
	sort.Strings(orderedPaths)
	for _, path := range orderedPaths {
		scope := pathReplicaScope(client, path)
		if writeResult.PathMeta[path] != nil {
			if err := store.applyWriteResult(scope, path, writeResult); err != nil {
				return nil, err
			}
		}
		if writeResult.LocalOnly {
			if err := store.applyLocalWrite(scope, path, writeResult); err != nil {
				return nil, err
			}
		}
		m.markReady(scope)
	}
	m.wakeSync()
	return &DKVSBatchCASResult{
		Applied: writeResult.Applied,
		Records: writeResult.Records,
		Hashes:  writeResult.Hashes,
	}, nil
}

func (m *dkvsManager) buildValueRecord(mutation dkvsValueMutation, seq uint64) (*swire.DKVSRecord, error) {
	if mutation.Owner == nil || mutation.Key == "" || seq == 0 {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	value := append([]byte(nil), mutation.Value...)
	if mutation.BuildValue != nil {
		var err error
		value, err = mutation.BuildValue(seq)
		if err != nil {
			return nil, err
		}
	}
	options := dkvsindexer.RecordOptions{
		Seq: seq, IssueHeight: mutation.IssueHeight, TTL: mutation.Policy.TTL,
	}
	if mutation.Tombstone {
		options.Flags |= dkvsindexer.FlagTombstone
		value = nil
	}
	switch mutation.Signature {
	case dkvsSignatureAccount:
		switch {
		case mutation.Policy.FreeLocal:
			return newDKVSAccountSignedRecordWithFreeLocal(mutation.Owner, mutation.Key, value, options)
		case mutation.Policy.Autopay != nil:
			return newDKVSAccountSignedRecordWithAutopay(mutation.Owner, mutation.Key, value, options, *mutation.Policy.Autopay)
		default:
			return NewDKVSAccountSignedRecord(mutation.Owner, mutation.Key, value, options)
		}
	case dkvsSignatureLegacy:
		switch {
		case mutation.Policy.FreeLocal:
			record, err := NewDKVSSignedRecord(mutation.Owner, mutation.Key, value, options)
			if err != nil {
				return nil, err
			}
			parsed, err := dkvsindexer.ParseKey(record.Key)
			if err != nil {
				return nil, err
			}
			proof, err := dkvsindexer.NewFreeLocalFeeProof(record.Key, parsed.Namespace,
				uint32(dkvsindexer.RecordSize(record)), dkvsindexer.RecordExpiryHeight(record))
			if err != nil {
				return nil, err
			}
			if err := AttachDKVSFeeProof(record, proof); err != nil {
				return nil, err
			}
			if err := SignDKVSRecord(mutation.Owner, record); err != nil {
				return nil, err
			}
			return record, nil
		case mutation.Policy.Autopay != nil:
			return newSignedRecordWithAutopay(mutation.Owner, mutation.Key, value, options, *mutation.Policy.Autopay)
		default:
			if mutation.Tombstone {
				return NewDKVSSignedTombstone(mutation.Owner, mutation.Key, options)
			}
			return NewDKVSSignedRecord(mutation.Owner, mutation.Key, value, options)
		}
	default:
		return nil, dkvsindexer.ErrInvalidSignature
	}
}

func (m *dkvsManager) putValues(client *SatsNetDKVSClient,
	values []dkvsValueMutation) ([]*dkvsValue, error) {

	if m == nil || m.owner == nil || client == nil || len(values) == 0 {
		return nil, ErrDKVSPathNotSynced
	}
	keys := make([]string, 0, len(values))
	for _, value := range values {
		if value.Key == "" || value.Owner == nil {
			return nil, dkvsindexer.ErrInvalidRecord
		}
		keys = append(keys, value.Key)
	}
	return m.updateValues(client, keys, func(_ map[string]*dkvsValue,
		_ map[string]uint64) ([]dkvsValueMutation, error) {
		return values, nil
	})
}

func (m *dkvsManager) updateValues(client *SatsNetDKVSClient, keys []string,
	builder dkvsUpdateBuilder) ([]*dkvsValue, error) {

	if m == nil || m.owner == nil || client == nil || len(keys) == 0 || builder == nil {
		return nil, ErrDKVSPathNotSynced
	}
	if err := m.waitPathsReady(client, keys); err != nil {
		return nil, err
	}
	unlock, err := m.lockPathsForKeys(keys)
	if err != nil {
		return nil, err
	}
	defer unlock()

	current := make(map[string]*dkvsValue, len(keys))
	nextSequence := make(map[string]uint64, len(keys))
	confirmed := make(map[string]*swire.DKVSRecord, len(keys))
	for _, key := range keys {
		existing, loadErr := m.confirmedRecord(client, key)
		if loadErr != nil {
			return nil, loadErr
		}
		confirmed[key] = existing
		current[key] = cloneDKVSValue(existing)
		nextSequence[key] = 1
		if existing != nil {
			nextSequence[key] = existing.Seq + 1
		}
	}
	values, err := builder(current, nextSequence)
	if err != nil {
		return nil, err
	}
	if len(values) == 0 {
		output := make([]*dkvsValue, 0, len(keys))
		for _, key := range keys {
			if current[key] != nil {
				output = append(output, current[key])
			}
		}
		return output, nil
	}
	allowed := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		allowed[key] = struct{}{}
	}
	relayableKeys := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := allowed[value.Key]; !ok {
			return nil, fmt.Errorf("DKVS update returned unregistered key %s", value.Key)
		}
		if !value.Policy.FreeLocal {
			relayableKeys = append(relayableKeys, value.Key)
		}
	}
	writeContext := &dkvsPathWriteContext{
		ServerTimeMS: make(map[string]uint64),
		PathMeta:     make(map[string]*dkvsindexer.PathMeta),
	}
	if len(relayableKeys) != 0 {
		writeContext, err = m.owner.syncedPathWriteContext(client, relayableKeys)
		if err != nil {
			return nil, err
		}
	}
	cas := make([]dkvsindexer.CASMutation, 0, len(values))
	for _, value := range values {
		existing := confirmed[value.Key]
		seq := nextSequence[value.Key]
		precondition := dkvsindexer.WritePrecondition{ExpectAbsent: true}
		if existing != nil {
			hash := dkvsindexer.RecordHash(existing)
			precondition = dkvsindexer.WritePrecondition{ExpectedHash: &hash}
		}
		if value.IssueHeight == 0 {
			if value.Policy.FreeLocal {
				height, known := m.verificationHeight()
				if !known {
					return nil, ErrDKVSVerificationHeightRequired
				}
				value.IssueHeight = height
			} else {
				path, pathErr := dkvsindexer.CollectionPathForKey(value.Key)
				if pathErr != nil {
					return nil, pathErr
				}
				meta := writeContext.PathMeta[path]
				if meta == nil {
					return nil, dkvsindexer.ErrStaleGeneration
				}
				value.IssueHeight = meta.ViewHeight
			}
		}
		record, buildErr := m.buildValueRecord(value, seq)
		if buildErr != nil {
			return nil, buildErr
		}
		cas = append(cas, dkvsindexer.CASMutation{Record: record, Precondition: precondition})
	}
	result, err := m.putBatchCASLocked(client, cas)
	if err != nil {
		return nil, err
	}
	output := make([]*dkvsValue, 0, len(result.Records))
	for _, record := range result.Records {
		output = append(output, cloneDKVSValue(record))
	}
	return output, nil
}

func (m *dkvsManager) syncExactKey(client *SatsNetDKVSClient,
	store *dkvsReplicaStore, key string) error {
	_, err := m.syncExactKeyState(client, store, key)
	return err
}

func (m *dkvsManager) syncExactKeyState(client *SatsNetDKVSClient,
	store *dkvsReplicaStore, key string) (dkvsDirectoryState, error) {
	filters := []dkvsindexer.Subscription{{Type: dkvsindexer.SubscriptionKey, Target: key}}
	scope := dkvsReplicaScope(client.replicaNamespace, filters)
	m.markNotReady(scope)
	verify := dkvsindexer.RecordVerificationOptions{}
	if height, known := m.verificationHeight(); known {
		verify.Height = height
	}
	records, root, err := client.SyncFilteredAll(filters, verify)
	if err != nil {
		return dkvsDirectoryState{}, err
	}
	if err := store.applyConfirmed(scope, filters, records, root, chainhash.Hash{}, 0); err != nil {
		return dkvsDirectoryState{}, err
	}
	state := dkvsDirectoryState{Prefix: key, Root: root, Scope: scope, Filters: filters}
	m.markReady(state.Scope)
	return state, nil
}

func (m *dkvsManager) registerKeysLocked(client *SatsNetDKVSClient,
	store *dkvsReplicaStore, keys []string) error {

	paths := make(map[string]struct{})
	exact := make(map[string]struct{})
	for _, key := range keys {
		target, managed, err := dkvsManagedPathForKey(key)
		if err != nil {
			return err
		}
		if managed {
			paths[target] = struct{}{}
		} else {
			exact[target] = struct{}{}
		}
	}
	orderedPaths := make([]string, 0, len(paths))
	for path := range paths {
		orderedPaths = append(orderedPaths, path)
	}
	if err := m.registerDirectoriesLocked(client, store, orderedPaths); err != nil {
		return err
	}
	orderedKeys := make([]string, 0, len(exact))
	for key := range exact {
		orderedKeys = append(orderedKeys, key)
	}
	sort.Strings(orderedKeys)
	for _, key := range orderedKeys {
		if err := m.syncExactKey(client, store, key); err != nil {
			return err
		}
	}
	m.rememberPaths(keys)
	return nil
}

func (m *dkvsManager) refreshPaths(client *SatsNetDKVSClient, keys []string) error {
	if m == nil || m.owner == nil || client == nil {
		return ErrDKVSPathNotSynced
	}
	m.runMu.Lock()
	defer m.runMu.Unlock()
	unlock, err := m.lockPathsForKeys(keys)
	if err != nil {
		return err
	}
	defer unlock()
	return m.registerKeysLocked(client, newDKVSReplicaStore(m.owner.db), keys)
}

func (m *dkvsManager) waitPathsReady(client *SatsNetDKVSClient, keys []string) error {
	if m == nil || m.owner == nil || client == nil || len(keys) == 0 {
		return ErrDKVSPathNotSynced
	}
	if m.pathsReady(client, keys) {
		m.rememberPaths(keys)
		return nil
	}
	m.runMu.Lock()
	defer m.runMu.Unlock()
	if m.pathsReady(client, keys) {
		m.rememberPaths(keys)
		return nil
	}
	unlock, err := m.lockPathsForKeys(keys)
	if err != nil {
		return err
	}
	defer unlock()
	return m.registerKeysLocked(client, newDKVSReplicaStore(m.owner.db), keys)
}

func (m *dkvsManager) registerDirectoriesLocked(client *SatsNetDKVSClient,
	store *dkvsReplicaStore, directories []string) error {

	sort.Strings(directories)
	for _, path := range directories {
		state, err := m.owner.syncDKVSDirectory(client, store, path)
		if err != nil {
			return err
		}
		m.markReady(state.Scope)
	}
	m.rememberDirectories(directories)
	return nil
}

func (m *dkvsManager) waitDirectoriesReady(client *SatsNetDKVSClient, directories []string) error {
	if m == nil || m.owner == nil || client == nil || len(directories) == 0 {
		return ErrDKVSPathNotSynced
	}
	if m.directoriesReady(client, directories) {
		m.rememberDirectories(directories)
		return nil
	}
	m.runMu.Lock()
	defer m.runMu.Unlock()
	if m.directoriesReady(client, directories) {
		m.rememberDirectories(directories)
		return nil
	}
	unlock, err := m.lockPathsForKeys(directories)
	if err != nil {
		return err
	}
	defer unlock()
	return m.registerDirectoriesLocked(client, newDKVSReplicaStore(m.owner.db), directories)
}

func (m *dkvsManager) markReady(scope string) {
	if m == nil || scope == "" {
		return
	}
	m.mu.Lock()
	m.ready[scope] = struct{}{}
	m.mu.Unlock()
}

func (m *dkvsManager) markNotReady(scope string) {
	if m == nil || scope == "" {
		return
	}
	m.mu.Lock()
	delete(m.ready, scope)
	m.mu.Unlock()
}

func (m *dkvsManager) markStatesNotReady(states []dkvsDirectoryState) {
	for _, state := range states {
		m.markNotReady(state.Scope)
	}
}

func (m *dkvsManager) scopeReady(scope string) bool {
	if m == nil || scope == "" {
		return false
	}
	m.mu.Lock()
	_, ok := m.ready[scope]
	m.mu.Unlock()
	return ok
}

func (m *dkvsManager) pathsReady(client *SatsNetDKVSClient, keys []string) bool {
	if m == nil || client == nil || len(keys) == 0 {
		return false
	}
	for _, key := range keys {
		target, managed, err := dkvsManagedPathForKey(key)
		if err != nil {
			return false
		}
		filterType := dkvsindexer.SubscriptionKey
		if managed {
			filterType = dkvsindexer.SubscriptionPrefix
		}
		scope := dkvsReplicaScope(client.replicaNamespace, []dkvsindexer.Subscription{{Type: filterType, Target: target}})
		if !m.scopeReady(scope) {
			return false
		}
	}
	return true
}

func (m *dkvsManager) directoriesReady(client *SatsNetDKVSClient, directories []string) bool {
	if m == nil || client == nil || len(directories) == 0 {
		return false
	}
	for _, path := range directories {
		if _, err := dkvsindexer.ParsePrefix(path); err != nil {
			return false
		}
		scope := dkvsReplicaScope(client.replicaNamespace, []dkvsindexer.Subscription{{
			Type: dkvsindexer.SubscriptionPrefix, Target: path,
		}})
		if !m.scopeReady(scope) {
			return false
		}
	}
	return true
}

func (m *dkvsManager) rememberDirectories(directories []string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, path := range directories {
		if _, err := dkvsindexer.ParsePrefix(path); err == nil {
			m.paths[path] = struct{}{}
		}
	}
}

func (m *dkvsManager) rememberPaths(keys []string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, key := range keys {
		path, managed, err := dkvsManagedPathForKey(key)
		if err != nil {
			continue
		}
		if managed {
			m.paths[path] = struct{}{}
		} else {
			m.exactKeys[path] = struct{}{}
		}
	}
}

func (m *dkvsManager) managedPaths() []string {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]string, 0, len(m.paths))
	for path := range m.paths {
		result = append(result, path)
	}
	sort.Strings(result)
	return result
}

func (m *dkvsManager) managedExactKeys() []string {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]string, 0, len(m.exactKeys))
	for key := range m.exactKeys {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func newDKVSManager(owner *Manager) *dkvsManager {
	manager := &dkvsManager{
		owner: owner, clients: make(map[string]*SatsNetDKVSClient),
		paths: make(map[string]struct{}), exactKeys: make(map[string]struct{}),
		jobs: make(map[string]func(*dkvsStore) error), ready: make(map[string]struct{}),
	}
	runtimeForDKVSManager(manager)
	return manager
}

func (p *Manager) ensureDKVSManager() *dkvsManager {
	if p == nil {
		return nil
	}
	p.dkvsInitMu.Lock()
	defer p.dkvsInitMu.Unlock()
	if p.dkvs == nil {
		p.dkvs = newDKVSManager(p)
	}
	return p.dkvs
}

func dkvsEndpointKey(scheme, host, proxy string) string {
	return strings.Join([]string{strings.TrimSpace(scheme), strings.TrimSpace(host), strings.TrimSpace(proxy)}, "\x00")
}

func (m *dkvsManager) clientFor(scheme, host, proxy string, http HttpClient) (*SatsNetDKVSClient, error) {
	if m == nil || strings.TrimSpace(host) == "" {
		return nil, fmt.Errorf("DKVS endpoint is not configured")
	}
	key := dkvsEndpointKey(scheme, host, proxy)
	m.mu.Lock()
	defer m.mu.Unlock()
	if client := m.clients[key]; client != nil {
		return client, nil
	}
	client := NewSatsNetDKVSClient(scheme, host, proxy, http)
	client.manager = m
	client.replicaNamespace = m.owner.dkvsReplicaNamespaceFor(scheme, host, proxy)
	m.clients[key] = client
	return client, nil
}

func (m *dkvsManager) primaryClient() (*SatsNetDKVSClient, error) {
	if m == nil || m.owner == nil || m.owner.cfg == nil || m.owner.cfg.IndexerL2 == nil {
		return nil, fmt.Errorf("SatoshiNet DKVS indexer is not configured")
	}
	indexerConfig := m.owner.cfg.IndexerL2
	if strings.TrimSpace(indexerConfig.Host) == "" {
		return nil, fmt.Errorf("DKVS endpoint is not configured")
	}
	key := dkvsEndpointKey(indexerConfig.Scheme, indexerConfig.Host, indexerConfig.Proxy)
	m.mu.Lock()
	defer m.mu.Unlock()
	client := m.clients[key]
	if client == nil {
		client = NewSatsNetDKVSClient(indexerConfig.Scheme, indexerConfig.Host, indexerConfig.Proxy, m.owner.http)
		client.manager = m
		client.replicaNamespace = m.owner.dkvsReplicaNamespace()
		m.clients[key] = client
	}
	return client, nil
}

func (m *dkvsManager) setCallback(callback func()) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.callback = callback
	m.mu.Unlock()
}

func (m *dkvsManager) notifyCallback() {
	if m == nil {
		return
	}
	m.mu.Lock()
	callback := m.callback
	m.mu.Unlock()
	if callback != nil {
		callback()
	}
}

func (m *dkvsManager) addObserver(observer func([]string)) {
	if m == nil || observer == nil {
		return
	}
	m.mu.Lock()
	m.observers = append(m.observers, observer)
	m.mu.Unlock()
}

func (m *dkvsManager) notifyObservers(paths []string) {
	if m == nil || len(paths) == 0 {
		return
	}
	m.mu.Lock()
	observers := append([]func([]string){}, m.observers...)
	m.mu.Unlock()
	for _, observer := range observers {
		observer(append([]string(nil), paths...))
	}
}

func (m *dkvsManager) schedule(id string, job func(*dkvsStore) error) {
	if m == nil || strings.TrimSpace(id) == "" || job == nil {
		return
	}
	m.mu.Lock()
	m.jobs[id] = job
	m.mu.Unlock()
	m.wakeSync()
}

func (m *dkvsManager) runPendingJobs(store *dkvsStore) error {
	if m == nil || store == nil {
		return nil
	}
	m.mu.Lock()
	jobs := m.jobs
	m.jobs = make(map[string]func(*dkvsStore) error)
	m.mu.Unlock()
	for id, job := range jobs {
		if err := job(store); err != nil {
			m.mu.Lock()
			if _, replaced := m.jobs[id]; !replaced {
				m.jobs[id] = job
			}
			m.mu.Unlock()
			return err
		}
	}
	return nil
}

func (m *dkvsManager) start() {
	if m == nil {
		return
	}
	m.mu.Lock()
	if m.stop != nil {
		m.mu.Unlock()
		return
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	wake := make(chan struct{}, 1)
	m.ready = make(map[string]struct{})
	m.stop, m.done, m.wake = stop, done, wake
	m.mu.Unlock()
	go m.run(stop, done, wake)
}

func (m *dkvsManager) stopAndWait() {
	if m == nil {
		return
	}
	m.mu.Lock()
	stop := m.stop
	done := m.done
	if stop == nil {
		m.mu.Unlock()
		return
	}
	if !m.stopping {
		m.stopping = true
		close(stop)
	}
	m.mu.Unlock()
	<-done
	m.mu.Lock()
	if m.done == done {
		m.stop, m.done, m.wake = nil, nil, nil
		m.stopping = false
	}
	m.mu.Unlock()
	releaseDKVSManagerRuntime(m)
}

func (m *dkvsManager) wakeSync() {
	if m == nil {
		return
	}
	m.mu.Lock()
	wake := m.wake
	m.mu.Unlock()
	if wake == nil {
		return
	}
	select {
	case wake <- struct{}{}:
	default:
	}
}

func (p *Manager) markDKVSStateDirty() {
	if manager := p.ensureDKVSManager(); manager != nil {
		manager.wakeSync()
	}
}

func (m *dkvsManager) wait(stop, wake <-chan struct{}, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-stop:
		return false
	case <-wake:
		return true
	case <-timer.C:
		return true
	}
}

func (m *dkvsManager) run(stop <-chan struct{}, done chan<- struct{}, wake <-chan struct{}) {
	defer close(done)
	for {
		select {
		case <-stop:
			return
		default:
		}
		states, err := m.owner.syncDKVSOnce()
		if err != nil {
			Log.Warningf("DKVS background sync failed: %v", err)
			if !m.wait(stop, wake, dkvsSyncRetryDelay) {
				return
			}
			continue
		}
		if len(states) == 0 {
			if !m.wait(stop, wake, dkvsIdleSyncInterval) {
				return
			}
			continue
		}
		if !m.watch(states, stop) {
			return
		}
	}
}

func (m *dkvsManager) watch(states []dkvsDirectoryState, stop <-chan struct{}) bool {
	client, err := m.primaryClient()
	if err != nil {
		m.markStatesNotReady(states)
		return m.wait(stop, nil, dkvsSyncRetryDelay)
	}
	watchSeconds := dkvsWatchTimeoutSeconds / len(states)
	if watchSeconds < 1 {
		watchSeconds = 1
	}
	for _, state := range states {
		select {
		case <-stop:
			return false
		default:
		}
		if state.Root == "" {
			continue
		}
		if len(state.Filters) == 1 && state.Filters[0].Type == dkvsindexer.SubscriptionKey {
			watch, watchErr := client.WatchFiltered(DKVSWatchRequest{
				Filters: state.Filters, Root: state.Root, TimeoutSeconds: watchSeconds,
			})
			if watchErr != nil {
				m.markNotReady(state.Scope)
				Log.Warningf("DKVS watch failed: %v", watchErr)
				return m.wait(stop, nil, dkvsSyncRetryDelay)
			}
			if watch == nil || watch.Changed || watch.Root != state.Root {
				m.markNotReady(state.Scope)
				return true
			}
			continue
		}
		watch, watchErr := client.WatchPath(DKVSPathWatchRequest{
			Path: state.Prefix, Generation: state.Generation,
			StateRoot: state.Root, ViewHeight: state.ViewHeight,
			TimeoutSeconds: watchSeconds,
		})
		if watchErr != nil {
			m.markNotReady(state.Scope)
			Log.Warningf("DKVS path watch failed: %v", watchErr)
			return m.wait(stop, nil, dkvsSyncRetryDelay)
		}
		if watch == nil || watch.PathMeta == nil || watch.Changed ||
			watch.PathMeta.Generation != state.Generation ||
			watch.PathMeta.StateRoot.String() != state.Root {
			m.markNotReady(state.Scope)
			return true
		}
	}
	return true
}
