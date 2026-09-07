package wallet

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	dkvscore "github.com/sat20-labs/sat20wallet/sdk/wallet/dkvs"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

// dkvsManager is the SDK's only DKVS coordination layer. paths are explicit
// managed collection prefixes, including active-account mailboxes. Ordinary
// reads never add a prefix implicitly.
type dkvsManager struct {
	owner *Manager

	mu                 sync.Mutex
	runMu              sync.Mutex
	runActive          bool
	runDone            chan struct{}
	stop               chan struct{}
	done               chan struct{}
	wake               chan struct{}
	requestCtx         context.Context
	cancelRequests     context.CancelFunc
	stopping           bool
	callback           func()
	observers          []func([]string)
	pendingNotifies    map[string]struct{}
	notifyQueued       bool
	jobs               map[string]func(*dkvsStore) error
	clients            map[string]*SatsNetDKVSClient
	paths              map[string]struct{}
	exactKeys          map[string]struct{}
	ready              map[string]struct{}
	lastSyncErrorCode  string
	lastSyncError      string
	lastSyncErrorAt    int64
	readCacheMu        sync.Mutex
	readCache          map[string]dkvsReadCacheEntry
	prefixReadCache    map[string]dkvsPrefixReadCacheEntry
	mailboxPollAt      time.Time
	mailboxPollAccount string

	verifyMu                 sync.RWMutex
	verifyHeight             uint64
	verifyHeightKnown        bool
	verifyHeightFromEndpoint bool
}

type dkvsReadCacheEntry struct {
	record    *swire.DKVSRecord
	fetchedAt time.Time
}

type dkvsPrefixReadCacheEntry struct {
	records   []*swire.DKVSRecord
	fetchedAt time.Time
}

const (
	dkvsUnmanagedReadCacheTTL = time.Minute
	dkvsUnmanagedReadTimeout  = 5 * time.Second
)

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
	if !m.verifyHeightFromEndpoint && (!m.verifyHeightKnown || height > m.verifyHeight) {
		m.verifyHeight = height
	}
	if !m.verifyHeightFromEndpoint {
		m.verifyHeightKnown = true
	}
	m.verifyMu.Unlock()
}

func (m *dkvsManager) setEndpointVerificationHeight(height uint64, known bool) {
	if m == nil || !known {
		return
	}
	m.verifyMu.Lock()
	m.verifyHeight = height
	m.verifyHeightKnown = true
	m.verifyHeightFromEndpoint = true
	m.verifyMu.Unlock()
}

func (m *dkvsManager) endpointVerificationHeight() (uint64, bool) {
	if m == nil {
		return 0, false
	}
	m.verifyMu.RLock()
	height, known := m.verifyHeight, m.verifyHeightKnown && m.verifyHeightFromEndpoint
	m.verifyMu.RUnlock()
	return height, known
}

func (m *dkvsManager) refreshVerificationBestHeight(client *SatsNetDKVSClient) (uint64, error) {
	if m == nil || client == nil {
		return 0, ErrDKVSPathNotSynced
	}
	height, err := client.GetBestHeight()
	if err != nil {
		return 0, err
	}
	m.setEndpointVerificationHeight(height, true)
	return height, nil
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

func cloneDKVSRecord(record *swire.DKVSRecord) *swire.DKVSRecord {
	if record == nil {
		return nil
	}
	copyRecord := *record
	copyRecord.Value = append([]byte(nil), record.Value...)
	copyRecord.PubKey = append([]byte(nil), record.PubKey...)
	copyRecord.Signature = append([]byte(nil), record.Signature...)
	copyRecord.FeeProof = append([]byte(nil), record.FeeProof...)
	return &copyRecord
}

func (m *dkvsManager) cachedUnmanagedRecord(namespace, key string) (*swire.DKVSRecord, bool) {
	if m == nil {
		return nil, false
	}
	cacheKey := namespace + "\x00" + key
	m.readCacheMu.Lock()
	defer m.readCacheMu.Unlock()
	entry, ok := m.readCache[cacheKey]
	if !ok || time.Since(entry.fetchedAt) >= dkvsUnmanagedReadCacheTTL {
		delete(m.readCache, cacheKey)
		return nil, false
	}
	return cloneDKVSRecord(entry.record), true
}

func (m *dkvsManager) cacheUnmanagedRecord(namespace string, record *swire.DKVSRecord) {
	if m == nil || record == nil {
		return
	}
	m.readCacheMu.Lock()
	m.readCache[namespace+"\x00"+record.Key] = dkvsReadCacheEntry{
		record: cloneDKVSRecord(record), fetchedAt: time.Now(),
	}
	m.readCacheMu.Unlock()
}

func (m *dkvsManager) invalidateUnmanagedRecords(namespace string, keys []string) {
	if m == nil || len(keys) == 0 {
		return
	}
	m.readCacheMu.Lock()
	for _, key := range keys {
		delete(m.readCache, namespace+"\x00"+key)
		for cacheKey, entry := range m.prefixReadCache {
			if !strings.HasPrefix(cacheKey, namespace+"\x00") {
				continue
			}
			for _, record := range entry.records {
				if record != nil && record.Key == key {
					delete(m.prefixReadCache, cacheKey)
					break
				}
			}
		}
	}
	m.readCacheMu.Unlock()
}

func cloneDKVSRecords(records []*swire.DKVSRecord) []*swire.DKVSRecord {
	result := make([]*swire.DKVSRecord, 0, len(records))
	for _, record := range records {
		if record != nil {
			result = append(result, cloneDKVSRecord(record))
		}
	}
	return result
}

func (m *dkvsManager) cachedPrefixRead(namespace, prefix string) ([]*swire.DKVSRecord, bool) {
	if m == nil {
		return nil, false
	}
	cacheKey := namespace + "\x00" + prefix
	m.readCacheMu.Lock()
	defer m.readCacheMu.Unlock()
	entry, ok := m.prefixReadCache[cacheKey]
	if !ok || time.Since(entry.fetchedAt) >= dkvsUnmanagedReadCacheTTL {
		delete(m.prefixReadCache, cacheKey)
		return nil, false
	}
	return cloneDKVSRecords(entry.records), true
}

func (m *dkvsManager) cachePrefixRead(namespace, prefix string, records []*swire.DKVSRecord) {
	if m == nil {
		return
	}
	m.readCacheMu.Lock()
	m.prefixReadCache[namespace+"\x00"+prefix] = dkvsPrefixReadCacheEntry{
		records: cloneDKVSRecords(records), fetchedAt: time.Now(),
	}
	m.readCacheMu.Unlock()
}

func dkvsManagedPathForKey(key string) (string, bool, error) {
	if _, err := dkvsindexer.ParseKey(key); err != nil {
		return "", false, err
	}
	path, err := dkvsindexer.CollectionPathForKey(key)
	if err == nil && path != "" {
		return path, true, nil
	}
	if err != nil && !errors.Is(err, dkvsindexer.ErrInvalidKey) {
		return "", false, err
	}
	return key, true, nil
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

func (m *dkvsManager) desiredPrefixContainsKey(client *SatsNetDKVSClient, key string) bool {
	if m == nil || client == nil || m.owner == nil || m.owner.db == nil {
		return false
	}
	store := newDKVSReplicaStore(m.owner.db)
	if subscribed, err := store.KeyIsSubscribed(client.replicaNamespace, key); err == nil && subscribed {
		return true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for prefix := range m.paths {
		if walletSubscriptionMatches(prefix, key) {
			return true
		}
	}
	return false
}

func (m *dkvsManager) subscriptionStateForClient(client *SatsNetDKVSClient) (*DKVSSubscriptionState, error) {
	if m == nil || m.owner == nil || m.owner.db == nil || client == nil {
		return nil, ErrDKVSPathNotSynced
	}
	return newDKVSReplicaStore(m.owner.db).LoadSubscriptionState(client.replicaNamespace)
}

func (m *dkvsManager) pathsReady(client *SatsNetDKVSClient, keys []string) bool {
	if m == nil || client == nil || len(keys) == 0 {
		return false
	}
	needsReplica := false
	for _, key := range keys {
		if m.desiredPrefixContainsKey(client, key) {
			needsReplica = true
		}
	}
	if !needsReplica {
		return true
	}
	state, err := m.subscriptionStateForClient(client)
	if err != nil {
		return false
	}
	return state.Status == DKVSSubscriptionReady || state.Status == DKVSSubscriptionOfflineReady
}

func (s *dkvsStore) IsReady(keys ...string) bool {
	return s != nil && s.manager != nil && s.client != nil && s.manager.pathsReady(s.client, keys)
}

func (m *dkvsManager) waitPathsReady(client *SatsNetDKVSClient, keys []string) error {
	if m == nil || client == nil || len(keys) == 0 {
		return ErrDKVSPathNotSynced
	}
	for _, key := range keys {
		if !m.desiredPrefixContainsKey(client, key) {
			return ErrDKVSPathNotSynced
		}
	}
	if m.pathsReady(client, keys) {
		return nil
	}
	return m.ensureCurrentSubscription(client)
}

func (s *dkvsStore) WaitReady(keys ...string) error {
	if s == nil || s.manager == nil || s.client == nil {
		return ErrDKVSPathNotSynced
	}
	return s.manager.waitPathsReady(s.client, keys)
}

// SyncCurrent performs a non-forced freshness check for managed keys. A
// current prefix costs only a status request; a snapshot is downloaded only
// when the server's PathMeta generation differs from the acknowledged local
// generation.
func (s *dkvsStore) SyncCurrent(keys ...string) error {
	if s == nil || s.manager == nil || s.client == nil || len(keys) == 0 {
		return ErrDKVSPathNotSynced
	}
	for _, key := range keys {
		if !s.manager.desiredPrefixContainsKey(s.client, key) {
			return ErrDKVSPathNotSynced
		}
	}
	return s.manager.ensureCurrentSubscription(s.client)
}

func (s *dkvsStore) hasLocalRecordEvidence(keys ...string) (bool, error) {
	if s == nil || s.manager == nil || s.manager.owner == nil ||
		s.manager.owner.db == nil || s.client == nil {
		return false, ErrDKVSPathNotSynced
	}
	store := newDKVSReplicaStore(s.manager.owner.db)
	wanted := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if _, err := dkvsindexer.ParseKey(key); err != nil {
			return false, err
		}
		wanted[key] = struct{}{}
		if _, err := store.LoadLocalKeyState(s.client.replicaNamespace, key); err == nil {
			return true, nil
		} else if !errors.Is(err, indexercommon.ErrKeyNotFound) {
			return false, err
		}
	}
	entries, err := store.LoadOutbox(s.client.replicaNamespace)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		mutations, err := entry.DecodeMutations()
		if err != nil {
			return false, err
		}
		for _, mutation := range mutations {
			if mutation.Record != nil {
				if _, ok := wanted[mutation.Record.Key]; ok {
					return true, nil
				}
			}
		}
	}
	return false, nil
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

func (s *dkvsStore) Get(key string) (*dkvsValue, error) {
	return s.GetVerified(key, dkvsindexer.RecordVerificationOptions{})
}

func (s *dkvsStore) GetVerified(key string, options dkvsindexer.RecordVerificationOptions) (*dkvsValue, error) {
	if s == nil || s.manager == nil || s.client == nil {
		return nil, ErrDKVSPathNotSynced
	}
	var record *swire.DKVSRecord
	var err error
	if s.manager.desiredPrefixContainsKey(s.client, key) {
		if err = s.WaitReady(key); err != nil {
			return nil, err
		}
		record, err = newDKVSReplicaStore(s.manager.owner.db).LoadSubscriptionRecord(s.client.replicaNamespace, key)
		if errors.Is(err, indexercommon.ErrKeyNotFound) {
			return nil, ErrDKVSRecordNotFound
		}
	} else {
		if cached, ok := s.manager.cachedUnmanagedRecord(s.client.unmanagedReadCacheNamespace(), key); ok {
			record = cached
		} else {
			ctx, cancel := context.WithTimeout(s.client.requestContext(), dkvsUnmanagedReadTimeout)
			record, err = s.client.GetRecordDirectContext(ctx, key)
			cancel()
			if err == nil {
				s.manager.cacheUnmanagedRecord(s.client.unmanagedReadCacheNamespace(), record)
			}
		}
		if err == nil && record != nil && dkvsindexer.RecordExpiryHeight(record) != 0 {
			if _, heightErr := s.manager.refreshVerificationBestHeight(s.client); heightErr != nil {
				return nil, heightErr
			}
		}
	}
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

// GetAuthoritativeVerified reads one key directly from the trusted CoreNode.
// It is intentionally not written into the materialized prefix replica: a
// single-key read cannot prove that no other key in the prefix changed.
func (s *dkvsStore) GetAuthoritativeVerified(key string,
	options dkvsindexer.RecordVerificationOptions) (*dkvsValue, error) {
	if s == nil || s.manager == nil || s.client == nil {
		return nil, ErrDKVSPathNotSynced
	}
	_, record, err := s.manager.authoritativeKeyState(s.client, key, options)
	if err != nil {
		return nil, err
	}
	if record == nil {
		return nil, ErrDKVSRecordNotFound
	}
	return cloneDKVSValue(record), nil
}

func (s *dkvsStore) GetAuthoritative(key string) (*dkvsValue, error) {
	return s.GetAuthoritativeVerified(key, dkvsindexer.RecordVerificationOptions{})
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

func (s *dkvsStore) updateWithOutboxOrigin(keys []string, builder dkvsUpdateBuilder,
	origin dkvsOutboxOrigin) ([]*dkvsValue, error) {
	if s == nil || s.manager == nil || s.client == nil {
		return nil, ErrDKVSPathNotSynced
	}
	return s.manager.updateValuesWithOrigin(s.client, keys, builder, origin, false)
}

func (s *dkvsStore) updateAuthoritativeWithOutboxOrigin(keys []string,
	builder dkvsUpdateBuilder, origin dkvsOutboxOrigin) ([]*dkvsValue, error) {
	if s == nil || s.manager == nil || s.client == nil {
		return nil, ErrDKVSPathNotSynced
	}
	return s.manager.updateValuesWithOrigin(s.client, keys, builder, origin, true)
}

func (s *dkvsStore) Config() (*AccountFreeLocalPolicy, error) {
	if s == nil || s.client == nil {
		return nil, ErrDKVSPathNotSynced
	}
	return s.client.GetConfig()
}

func (s *dkvsStore) ConfigWithVerificationHeight() (*AccountFreeLocalPolicy, uint64, bool, error) {
	policy, err := s.Config()
	if err != nil {
		return nil, 0, false, err
	}
	if s.manager == nil {
		return policy, 0, false, nil
	}
	if height, refreshErr := s.manager.refreshVerificationBestHeight(s.client); refreshErr == nil {
		return policy, height, true, nil
	}
	height, known := s.manager.verificationHeight()
	return policy, height, known, nil
}

func (s *dkvsStore) ConfigureFreeLocalRetention(options *dkvsindexer.RecordOptions) (*AccountFreeLocalPolicy, error) {
	policy, err := s.Config()
	if err != nil {
		return nil, err
	}
	if err := dkvscore.ApplyFreeLocalRetention(policy, options); err != nil {
		return nil, err
	}
	return policy, nil
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
	if !s.manager.desiredPrefixContainsKey(s.client, prefix) {
		return nil, ErrDKVSPathNotSynced
	}
	if err := s.manager.waitDirectoriesReady(s.client, []string{prefix}); err != nil {
		return nil, err
	}
	records, err := newDKVSReplicaStore(s.manager.owner.db).ListSubscriptionRecords(s.client.replicaNamespace)
	if err != nil {
		return nil, err
	}
	values := make([]*dkvsValue, 0)
	for _, record := range records {
		if record == nil || !walletSubscriptionMatches(prefix, record.Key) {
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

func (s *dkvsStore) ListMailboxVerified(accountID string,
	options dkvsindexer.RecordVerificationOptions) ([]*dkvsValue, error) {
	if s == nil || s.manager == nil || s.client == nil {
		return nil, ErrDKVSPathNotSynced
	}
	target, err := mailboxSubscriptionTarget(accountID)
	if err != nil {
		return nil, err
	}
	values, err := s.ListVerified(target, options)
	if err != nil {
		return nil, err
	}
	messagePrefix := target + "/msg/"
	filtered := values[:0]
	for _, value := range values {
		if value != nil && strings.HasPrefix(value.Key, messagePrefix) {
			filtered = append(filtered, value)
		}
	}
	return filtered, nil
}

func (s *dkvsStore) Refresh(keys ...string) error {
	if s == nil || s.manager == nil || s.client == nil {
		return ErrDKVSPathNotSynced
	}
	return s.manager.refreshPaths(s.client, keys)
}

func statePrecondition(state *dkvsindexer.DKVSKeyState) (dkvsindexer.WritePrecondition, error) {
	if state == nil || state.Status == dkvsindexer.KeyStateNeverSeen {
		return dkvsindexer.WritePrecondition{ExpectAbsent: true}, nil
	}
	if state.ETag == "" {
		return dkvsindexer.WritePrecondition{}, dkvsindexer.ErrInvalidRecord
	}
	hash, err := chainhash.NewHashFromStr(state.ETag)
	if err != nil {
		return dkvsindexer.WritePrecondition{}, dkvsindexer.ErrInvalidRecord
	}
	return dkvsindexer.WritePrecondition{ExpectedHash: hash}, nil
}

func (m *dkvsManager) currentKeyState(client *SatsNetDKVSClient,
	key string) (*dkvsindexer.DKVSKeyState, *swire.DKVSRecord, error) {
	if m == nil || m.owner == nil || m.owner.db == nil || client == nil {
		return nil, nil, ErrDKVSPathNotSynced
	}
	store := newDKVSReplicaStore(m.owner.db)
	if m.desiredPrefixContainsKey(client, key) {
		if local, err := store.LoadLocalKeyState(client.replicaNamespace, key); err == nil {
			state := &dkvsindexer.DKVSKeyState{
				Key: key, Seq: local.Seq, ETag: local.ETag,
				ExpiryHeight: local.ExpiryHeight, StorageMode: local.StorageMode,
			}
			if local.Deleted {
				state.Status = dkvsindexer.KeyStateDeleted
				return state, nil, nil
			}
			state.Status = dkvsindexer.KeyStateActive
			record, readErr := store.LoadSubscriptionRecord(client.replicaNamespace, key)
			if readErr != nil {
				return nil, nil, readErr
			}
			state.Record = record
			return state, record, nil
		} else if !errors.Is(err, indexercommon.ErrKeyNotFound) {
			return nil, nil, err
		}
	}
	state, err := client.GetKeyState(key)
	if err != nil {
		return nil, nil, err
	}
	var record *swire.DKVSRecord
	if state.Status == dkvsindexer.KeyStateActive {
		record = state.Record
		if record == nil {
			record, err = client.GetRecordDirect(key)
			if err != nil {
				return nil, nil, err
			}
		}
	}
	return state, record, nil
}

func (m *dkvsManager) authoritativeKeyState(client *SatsNetDKVSClient, key string,
	options dkvsindexer.RecordVerificationOptions) (*dkvsindexer.DKVSKeyState,
	*swire.DKVSRecord, error) {
	if m == nil || client == nil {
		return nil, nil, ErrDKVSPathNotSynced
	}
	state, err := client.GetKeyState(key)
	if err != nil {
		return nil, nil, err
	}
	if state == nil || state.Key != key {
		return nil, nil, dkvsindexer.ErrInvalidRecord
	}
	if state.Status != dkvsindexer.KeyStateActive {
		if state.Status != dkvsindexer.KeyStateDeleted &&
			state.Status != dkvsindexer.KeyStateNeverSeen {
			return nil, nil, dkvsindexer.ErrInvalidRecord
		}
		return state, nil, nil
	}
	record := state.Record
	if record == nil {
		record, err = client.GetRecordDirect(key)
		if err != nil {
			return nil, nil, err
		}
	}
	if record == nil || record.Key != key || record.Seq != state.Seq ||
		strings.TrimSpace(state.ETag) == "" ||
		dkvsindexer.RecordHash(record).String() != state.ETag {
		return nil, nil, dkvsindexer.ErrInvalidRecord
	}
	if dkvsindexer.RecordExpiryHeight(record) != 0 {
		if _, err := m.refreshVerificationBestHeight(client); err != nil {
			return nil, nil, err
		}
	}
	store := &dkvsStore{manager: m, client: client}
	options, err = store.verificationOptions(record, key, options)
	if err != nil {
		return nil, nil, err
	}
	if err := dkvsindexer.VerifyRecordForClient(record, options); err != nil {
		return nil, nil, err
	}
	return state, record, nil
}

func nextDKVSRecordSequence(existing *swire.DKVSRecord, floorSeq uint64) (uint64, error) {
	current := floorSeq
	if existing != nil && existing.Seq > current {
		current = existing.Seq
	}
	if current == ^uint64(0) {
		return 0, dkvsindexer.ErrInvalidSequence
	}
	return current + 1, nil
}

func (m *dkvsManager) confirmedRecordState(client *SatsNetDKVSClient,
	key string) (*swire.DKVSRecord, uint64, error) {
	state, record, err := m.currentKeyState(client, key)
	if err != nil {
		return nil, 0, err
	}
	if state == nil || state.Status == dkvsindexer.KeyStateNeverSeen {
		return nil, 0, nil
	}
	return record, state.Seq, nil
}

func (m *dkvsManager) confirmedRecord(client *SatsNetDKVSClient, key string) (*swire.DKVSRecord, error) {
	record, _, err := m.confirmedRecordState(client, key)
	return record, err
}

func (m *dkvsManager) putRecord(client *SatsNetDKVSClient,
	record *swire.DKVSRecord) (*swire.DKVSRecord, error) {
	if m == nil || record == nil || client == nil {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	unlock, err := m.lockPathsForKeys([]string{record.Key})
	if err != nil {
		return nil, err
	}
	defer unlock()
	state, existing, err := m.currentKeyState(client, record.Key)
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
		height, heightErr := m.refreshVerificationBestHeight(client)
		if heightErr != nil {
			return nil, heightErr
		}
		prepared.IssueHeight = height
		prepared.Signature = nil
		if m.owner == nil {
			return nil, dkvsindexer.ErrInvalidSignature
		}
		m.owner.mutex.RLock()
		var signer common.Wallet
		if m.owner.wallet != nil {
			signer = m.owner.wallet.Clone()
		}
		m.owner.mutex.RUnlock()
		if signer == nil {
			return nil, dkvsindexer.ErrInvalidSignature
		}
		if err := SignDKVSRecord(signer, &prepared); err != nil {
			return nil, err
		}
	}
	next := uint64(1)
	if state != nil && state.Status != dkvsindexer.KeyStateNeverSeen {
		if state.Seq == ^uint64(0) {
			return nil, dkvsindexer.ErrInvalidSequence
		}
		next = state.Seq + 1
	}
	if prepared.Seq != next {
		return nil, dkvsindexer.ErrInvalidSequence
	}
	precondition, err := statePrecondition(state)
	if err != nil {
		return nil, err
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

func (m *dkvsManager) putBatchCAS(client *SatsNetDKVSClient,
	mutations []dkvsindexer.CASMutation) (*DKVSBatchCASResult, error) {
	if m == nil || client == nil || len(mutations) == 0 {
		return nil, fmt.Errorf("DKVS manager write is unavailable")
	}
	keys := make([]string, 0, len(mutations))
	for _, mutation := range mutations {
		if mutation.Record == nil {
			return nil, dkvsindexer.ErrInvalidRecord
		}
		keys = append(keys, mutation.Record.Key)
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
	if len(mutations) == 0 || mutations[0].Record == nil {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	return m.putBatchCASLockedWithOrigin(client, mutations, dkvsOutboxOrigin{Key: mutations[0].Record.Key})
}

func (m *dkvsManager) putBatchCASLockedWithOrigin(client *SatsNetDKVSClient,
	mutations []dkvsindexer.CASMutation, origin dkvsOutboxOrigin) (*DKVSBatchCASResult, error) {
	if m == nil || client == nil || len(mutations) == 0 {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	writeResult, err := client.putRecordBatchCASWithOrigin(mutations, origin)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(mutations))
	for _, mutation := range mutations {
		if mutation.Record != nil {
			keys = append(keys, mutation.Record.Key)
		}
	}
	m.invalidateUnmanagedRecords(client.unmanagedReadCacheNamespace(), keys)
	m.wakeSync()
	return writeResultToBatchResult(writeResult), nil
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
	options := dkvsindexer.RecordOptions{Seq: seq, IssueHeight: mutation.IssueHeight, TTL: mutation.Policy.TTL}
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
	if m == nil || client == nil || len(values) == 0 {
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
	if len(keys) == 0 {
		return nil, ErrDKVSPathNotSynced
	}
	return m.updateValuesWithOrigin(client, keys, builder, dkvsOutboxOrigin{Key: keys[0]}, false)
}

func (m *dkvsManager) updateValuesWithOrigin(client *SatsNetDKVSClient, keys []string,
	builder dkvsUpdateBuilder, origin dkvsOutboxOrigin, authoritativeFirst bool) ([]*dkvsValue, error) {
	if m == nil || client == nil || len(keys) == 0 || builder == nil {
		return nil, ErrDKVSPathNotSynced
	}
	managedKeys := make([]string, 0, len(keys))
	for _, key := range keys {
		if m.desiredPrefixContainsKey(client, key) {
			managedKeys = append(managedKeys, key)
		}
	}
	if len(managedKeys) != 0 {
		if err := m.waitPathsReady(client, managedKeys); err != nil {
			return nil, err
		}
	}
	unlock, err := m.lockPathsForKeys(keys)
	if err != nil {
		return nil, err
	}
	defer unlock()
	allowed := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		allowed[key] = struct{}{}
	}
	for attempt := 0; attempt < 3; attempt++ {
		current := make(map[string]*dkvsValue, len(keys))
		nextSequence := make(map[string]uint64, len(keys))
		states := make(map[string]*dkvsindexer.DKVSKeyState, len(keys))
		for _, key := range keys {
			var state *dkvsindexer.DKVSKeyState
			var record *swire.DKVSRecord
			var loadErr error
			if attempt == 0 && !authoritativeFirst {
				state, record, loadErr = m.currentKeyState(client, key)
			} else {
				state, record, loadErr = m.authoritativeKeyState(client, key,
					dkvsindexer.RecordVerificationOptions{})
			}
			if loadErr != nil {
				return nil, loadErr
			}
			states[key] = state
			current[key] = cloneDKVSValue(record)
			if state == nil || state.Status == dkvsindexer.KeyStateNeverSeen {
				nextSequence[key] = 1
			} else {
				if state.Seq == ^uint64(0) {
					return nil, dkvsindexer.ErrInvalidSequence
				}
				nextSequence[key] = state.Seq + 1
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
		writeHeight := uint64(0)
		for _, value := range values {
			if _, ok := allowed[value.Key]; !ok {
				return nil, fmt.Errorf("DKVS update returned unregistered key %s", value.Key)
			}
			if value.IssueHeight == 0 && writeHeight == 0 {
				writeHeight, err = m.refreshVerificationBestHeight(client)
				if err != nil {
					return nil, err
				}
			}
		}
		cas := make([]dkvsindexer.CASMutation, 0, len(values))
		for _, value := range values {
			seq := nextSequence[value.Key]
			if seq == 0 {
				return nil, dkvsindexer.ErrInvalidSequence
			}
			if value.IssueHeight == 0 {
				value.IssueHeight = writeHeight
			}
			record, buildErr := m.buildValueRecord(value, seq)
			if buildErr != nil {
				return nil, buildErr
			}
			precondition, conditionErr := statePrecondition(states[value.Key])
			if conditionErr != nil {
				return nil, conditionErr
			}
			cas = append(cas, dkvsindexer.CASMutation{Record: record, Precondition: precondition})
		}
		result, submitErr := m.putBatchCASLockedWithOrigin(client, cas, origin)
		if submitErr != nil {
			if attempt < 2 && isDKVSRebaseError(submitErr) {
				continue
			}
			return nil, submitErr
		}
		output := make([]*dkvsValue, 0, len(result.Records))
		for _, record := range result.Records {
			output = append(output, cloneDKVSValue(record))
		}
		return output, nil
	}
	return nil, dkvsindexer.ErrWriteConflict
}

func (m *dkvsManager) registerDirectoriesLocked(client *SatsNetDKVSClient,
	store *dkvsReplicaStore, directories []string) error {
	if m == nil || client == nil || store == nil {
		return ErrDKVSPathNotSynced
	}
	prefixes, err := normalizeWalletSubscriptionPrefixes(directories)
	if err != nil {
		return err
	}
	m.mu.Lock()
	for _, prefix := range prefixes {
		m.paths[prefix] = struct{}{}
	}
	m.mu.Unlock()
	desired, err := m.desiredPrefixes(store, client.replicaNamespace)
	if err != nil {
		return err
	}
	if len(desired) == 0 {
		return nil
	}
	return m.forceCurrentPrefixes(client, desired)
}

func (m *dkvsManager) registerKeysLocked(client *SatsNetDKVSClient,
	store *dkvsReplicaStore, keys []string) error {
	prefixes := make([]string, 0, len(keys))
	for _, key := range keys {
		prefix, _, err := dkvsManagedPathForKey(key)
		if err != nil {
			return err
		}
		prefixes = append(prefixes, prefix)
	}
	return m.registerDirectoriesLocked(client, store, prefixes)
}

func (m *dkvsManager) refreshPaths(client *SatsNetDKVSClient, keys []string) error {
	if m == nil || m.owner == nil || m.owner.db == nil || client == nil {
		return ErrDKVSPathNotSynced
	}
	return m.registerKeysLocked(client, newDKVSReplicaStore(m.owner.db), keys)
}

func (m *dkvsManager) directoriesReady(client *SatsNetDKVSClient, directories []string) bool {
	if m == nil || client == nil || len(directories) == 0 {
		return false
	}
	state, err := m.subscriptionStateForClient(client)
	if err != nil || (state.Status != DKVSSubscriptionReady && state.Status != DKVSSubscriptionOfflineReady) {
		return false
	}
	for _, directory := range directories {
		if _, found := state.Generations[directory]; !found {
			return false
		}
	}
	return true
}

// coveringManagedDirectories resolves read-only child prefixes to the
// registered managed prefixes whose snapshots contain them. Only registered
// canonical prefixes own a server PathMeta generation; query prefixes such as
// /mail/<account>/msg are filters over that local snapshot and must not be
// treated as independent synchronization roots.
func (m *dkvsManager) coveringManagedDirectories(client *SatsNetDKVSClient,
	directories []string) ([]string, error) {

	if m == nil || m.owner == nil || m.owner.db == nil || client == nil || len(directories) == 0 {
		return nil, ErrDKVSPathNotSynced
	}
	desired, err := m.desiredPrefixes(newDKVSReplicaStore(m.owner.db), client.replicaNamespace)
	if err != nil {
		return nil, err
	}
	covered := make([]string, 0, len(directories))
	for _, directory := range directories {
		directory = strings.TrimSuffix(strings.TrimSpace(directory), "/")
		if _, err := dkvsindexer.ParsePrefix(directory); err != nil {
			return nil, err
		}
		covering := ""
		for _, prefix := range desired {
			if walletSubscriptionMatches(prefix, directory) && len(prefix) > len(covering) {
				covering = prefix
			}
		}
		if covering == "" {
			return nil, ErrDKVSPathNotSynced
		}
		covered = append(covered, covering)
	}
	return normalizeWalletSubscriptionPrefixes(covered)
}

func (m *dkvsManager) waitDirectoriesReady(client *SatsNetDKVSClient, directories []string) error {
	if m == nil || m.owner == nil || m.owner.db == nil || client == nil || len(directories) == 0 {
		return ErrDKVSPathNotSynced
	}
	managed, err := m.coveringManagedDirectories(client, directories)
	if err != nil {
		return err
	}
	if m.directoriesReady(client, managed) {
		return nil
	}
	return m.ensureCurrentSubscription(client)
}

func (m *dkvsManager) rememberDirectories(directories []string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, prefix := range directories {
		prefix = strings.TrimSuffix(strings.TrimSpace(prefix), "/")
		if _, err := dkvsindexer.ParsePrefix(prefix); err == nil {
			m.paths[prefix] = struct{}{}
		}
	}
}

func (m *dkvsManager) rememberPaths(keys []string) {
	if m == nil {
		return
	}
	prefixes := make([]string, 0, len(keys))
	for _, key := range keys {
		if prefix, _, err := dkvsManagedPathForKey(key); err == nil {
			prefixes = append(prefixes, prefix)
		}
	}
	m.rememberDirectories(prefixes)
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
	return nil
}

const dkvsBackgroundSyncErrorCode = "DKVS_SYNC_ERROR"

func dkvsSyncErrorCode(err error) string {
	if err == nil {
		return ""
	}
	code := dkvsindexer.ErrorCodeOf(err)
	if code != dkvsindexer.ErrorCodeInvalidRecord {
		return string(code)
	}
	switch {
	case errors.Is(err, dkvsindexer.ErrInvalidRecord),
		errors.Is(err, dkvsindexer.ErrInvalidKey),
		errors.Is(err, dkvsindexer.ErrInvalidNamespace),
		errors.Is(err, dkvsindexer.ErrInvalidSignature),
		errors.Is(err, dkvsindexer.ErrInvalidCheckpoint),
		errors.Is(err, dkvsindexer.ErrInvalidSnapshot):
		return string(code)
	default:
		return dkvsBackgroundSyncErrorCode
	}
}

func (m *dkvsManager) setLastSyncError(err error) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err == nil {
		m.lastSyncErrorCode, m.lastSyncError, m.lastSyncErrorAt = "", "", 0
		return
	}
	m.lastSyncErrorCode = dkvsSyncErrorCode(err)
	m.lastSyncError = err.Error()
	m.lastSyncErrorAt = time.Now().UnixMilli()
}

func (m *dkvsManager) lastSyncErrorStatus() (string, string, int64) {
	if m == nil {
		return "", "", 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastSyncErrorCode, m.lastSyncError, m.lastSyncErrorAt
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

func (m *dkvsManager) scopeReady(scope string) bool {
	if m == nil || scope == "" {
		return false
	}
	m.mu.Lock()
	_, ok := m.ready[scope]
	m.mu.Unlock()
	return ok
}

func newDKVSManager(owner *Manager) *dkvsManager {
	manager := &dkvsManager{
		owner: owner, clients: make(map[string]*SatsNetDKVSClient),
		paths: make(map[string]struct{}), exactKeys: make(map[string]struct{}),
		jobs: make(map[string]func(*dkvsStore) error), ready: make(map[string]struct{}),
		pendingNotifies: make(map[string]struct{}),
		readCache:       make(map[string]dkvsReadCacheEntry), prefixReadCache: make(map[string]dkvsPrefixReadCacheEntry),
	}
	runtimeForDKVSManager(manager)
	return manager
}

// runTransport serializes replica/outbox operations without holding a mutex
// while HTTP, verification, or Pebble work is in progress. Waiters sleep on the
// completed operation's channel, so local state locks remain available.
func (m *dkvsManager) runTransport(operation func() error) error {
	if m == nil || operation == nil {
		return ErrDKVSPathNotSynced
	}
	for {
		m.runMu.Lock()
		if !m.runActive {
			m.runActive = true
			m.runDone = make(chan struct{})
			done := m.runDone
			m.runMu.Unlock()

			err := operation()
			m.runMu.Lock()
			if m.runDone == done {
				m.runActive = false
				m.runDone = nil
				close(done)
			}
			m.runMu.Unlock()
			return err
		}
		done := m.runDone
		m.runMu.Unlock()
		if done == nil {
			continue
		}
		select {
		case <-done:
		case <-m.requestContext().Done():
			return m.requestContext().Err()
		}
	}
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
	config := m.owner.cfg.IndexerL2
	if strings.TrimSpace(config.Host) == "" {
		return nil, fmt.Errorf("DKVS endpoint is not configured")
	}
	key := dkvsEndpointKey(config.Scheme, config.Host, config.Proxy)
	m.mu.Lock()
	defer m.mu.Unlock()
	client := m.clients[key]
	if client == nil {
		client = NewSatsNetDKVSClient(config.Scheme, config.Host, config.Proxy, m.owner.http)
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

const dkvsNotificationJobID = "dkvs-notifications"

var errDKVSJobDeferred = errors.New("DKVS job dependency is not ready")

// enqueueNotifications reliably coalesces replica changes behind the DKVS
// worker. Neither domain observers nor the application callback run on a
// foreground Refresh stack or while an internal coordination mutex is held.
func (m *dkvsManager) enqueueNotifications(changes []string) {
	if m == nil {
		return
	}
	targets := notificationTargets(changes)
	if len(targets) == 0 {
		return
	}
	m.mu.Lock()
	for _, target := range targets {
		m.pendingNotifies[target] = struct{}{}
	}
	if !m.notifyQueued {
		m.notifyQueued = true
		m.jobs[dkvsNotificationJobID] = func(*dkvsStore) error {
			m.deliverNotifications()
			return nil
		}
	}
	m.mu.Unlock()
	m.wakeSync()
}

func (m *dkvsManager) deliverNotifications() {
	if m == nil {
		return
	}
	m.mu.Lock()
	paths := make([]string, 0, len(m.pendingNotifies))
	for path := range m.pendingNotifies {
		paths = append(paths, path)
	}
	m.pendingNotifies = make(map[string]struct{})
	m.notifyQueued = false
	observers := append([]func([]string){}, m.observers...)
	callback := m.callback
	m.mu.Unlock()

	sort.Strings(paths)
	for _, observer := range observers {
		observer(append([]string(nil), paths...))
	}
	if callback != nil {
		callback()
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
	ids := make([]string, 0, len(jobs))
	for id := range jobs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		job := jobs[id]
		delete(jobs, id)
		if err := job(store); err != nil {
			deferred := errors.Is(err, errDKVSJobDeferred)
			m.mu.Lock()
			if _, replaced := m.jobs[id]; !replaced {
				m.jobs[id] = job
			}
			if !deferred {
				for pendingID, pendingJob := range jobs {
					if _, replaced := m.jobs[pendingID]; !replaced {
						m.jobs[pendingID] = pendingJob
					}
				}
			}
			m.mu.Unlock()
			if deferred {
				continue
			}
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
	requestCtx, cancelRequests := context.WithCancel(context.Background())
	m.ready = make(map[string]struct{})
	m.stop, m.done, m.wake = stop, done, wake
	m.requestCtx, m.cancelRequests = requestCtx, cancelRequests
	m.stopping = false
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
	cancelRequests := m.cancelRequests
	if stop == nil {
		m.mu.Unlock()
		return
	}
	if !m.stopping {
		m.stopping = true
		if cancelRequests != nil {
			cancelRequests()
		}
		close(stop)
	}
	m.mu.Unlock()
	<-done
	m.mu.Lock()
	if m.done == done {
		m.stop, m.done, m.wake = nil, nil, nil
		m.requestCtx, m.cancelRequests = nil, nil
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
	stopping := m.stopping
	m.mu.Unlock()
	if wake == nil || stopping {
		return
	}
	select {
	case wake <- struct{}{}:
	default:
	}
}

func (m *dkvsManager) requestContext() context.Context {
	if m == nil {
		return context.Background()
	}
	m.mu.Lock()
	ctx := m.requestCtx
	m.mu.Unlock()
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func (p *Manager) markDKVSStateDirty() {
	if manager := p.ensureDKVSManager(); manager != nil {
		manager.wakeSync()
	}
}
