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

// dkvsManager is the only coordination layer for domain data, the confirmed
// subscription replica, and the durable client outbox. Reads never subscribe
// implicitly; background transport does not own business-state mutexes.
type dkvsManager struct {
	owner                    *Manager
	mu                       sync.Mutex
	runMu                    sync.Mutex
	runActive                bool
	runDone                  chan struct{}
	stop                     chan struct{}
	done                     chan struct{}
	wake                     chan struct{}
	requestCtx               context.Context
	cancelRequests           context.CancelFunc
	stopping                 bool
	callback                 func()
	observers                []func([]string)
	pendingNotifies          map[string]struct{}
	notifyQueued             bool
	jobs                     map[string]func(*dkvsStore) error
	clients                  map[string]*SatsNetDKVSClient
	paths                    map[string]struct{}
	ready                    map[string]struct{}
	lastSyncErrorCode        string
	lastSyncError            string
	lastSyncErrorAt          int64
	mailboxPollAt            time.Time
	mailboxPollAccount       string
	verifyMu                 sync.RWMutex
	verifyHeight             uint64
	verifyHeightKnown        bool
	verifyHeightFromEndpoint bool
}

const dkvsUnmanagedReadTimeout = 5 * time.Second

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
type dkvsUpdateBuilder func(current map[string]*dkvsValue, nextSequence map[string]uint64) ([]dkvsValueMutation, error)
type dkvsStore struct {
	manager *dkvsManager
	client  *SatsNetDKVSClient
}

var ErrDKVSVerificationHeightRequired = errors.New("DKVS record verification requires a trusted L2 height")

func (m *dkvsManager) observeVerificationHeight(height uint64, known bool) {
	if m == nil || !known {
		return
	}
	m.verifyMu.Lock()
	defer m.verifyMu.Unlock()
	if !m.verifyHeightFromEndpoint && (!m.verifyHeightKnown || height > m.verifyHeight) {
		m.verifyHeight = height
	}
	if !m.verifyHeightFromEndpoint {
		m.verifyHeightKnown = true
	}
}
func (m *dkvsManager) setEndpointVerificationHeight(height uint64, known bool) {
	if m == nil || !known {
		return
	}
	m.verifyMu.Lock()
	m.verifyHeight, m.verifyHeightKnown, m.verifyHeightFromEndpoint = height, true, true
	m.verifyMu.Unlock()
}

// A collection view is an observation, not a current-chain-height query.
// Only an authoritative best-height refresh may lower the endpoint height.
func (m *dkvsManager) observeEndpointVerificationHeight(height uint64) {
	if m == nil {
		return
	}
	m.verifyMu.Lock()
	defer m.verifyMu.Unlock()
	if !m.verifyHeightFromEndpoint || !m.verifyHeightKnown || height > m.verifyHeight {
		m.verifyHeight = height
	}
	m.verifyHeightKnown, m.verifyHeightFromEndpoint = true, true
}
func (m *dkvsManager) endpointVerificationHeight() (uint64, bool) {
	if m == nil {
		return 0, false
	}
	m.verifyMu.RLock()
	defer m.verifyMu.RUnlock()
	return m.verifyHeight, m.verifyHeightKnown && m.verifyHeightFromEndpoint
}
func (m *dkvsManager) refreshVerificationBestHeight(client *SatsNetDKVSClient) (uint64, error) {
	if m == nil || client == nil {
		return 0, ErrDKVSPathNotSynced
	}
	config, err := m.subscriptionEndpointConfig(client)
	if err != nil {
		return 0, err
	}
	if err := m.validateVerificationEndpoint(client, config.EndpointID); err != nil {
		return 0, err
	}
	height, err := client.GetBestHeight()
	if err != nil {
		return 0, err
	}
	m.setEndpointVerificationHeight(height, true)
	return height, nil
}
func (m *dkvsManager) validateVerificationEndpoint(client *SatsNetDKVSClient, endpointID string) error {
	if m.owner == nil || m.owner.db == nil || client.replicaNamespace == "" {
		return nil
	}
	state, err := m.subscriptionStateForClient(client)
	if errors.Is(err, indexercommon.ErrKeyNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return validateDKVSSubscriptionEndpoint(state, endpointID)
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
	defer m.verifyMu.RUnlock()
	return m.verifyHeight, m.verifyHeightKnown
}
func normalizeDKVSRecordVerification(record *swire.DKVSRecord, key string, options dkvsindexer.RecordVerificationOptions,
	fallbackHeight uint64, heightKnown bool) (dkvsindexer.RecordVerificationOptions, error) {
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
func (s *dkvsStore) verificationOptions(record *swire.DKVSRecord, key string, options dkvsindexer.RecordVerificationOptions) (dkvsindexer.RecordVerificationOptions, error) {
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
	copyRecord := cloneDKVSRecord(record)
	return &dkvsValue{Key: record.Key, Value: append([]byte(nil), record.Value...), Seq: record.Seq,
		IssueHeight: record.IssueHeight, TTL: record.TTL, Flags: record.Flags,
		Hash: dkvsindexer.RecordHash(record).String(), Signer: append([]byte(nil), record.PubKey...), record: copyRecord}
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

// Confirmed reads share the synchronization source and completeness boundary.
// An unknown client identity can read the last confirmed offline view, but an
// observed different identity cannot claim that view as its own.
func (m *dkvsManager) requirePathsReady(client *SatsNetDKVSClient, keys []string) error {
	if m == nil || client == nil || len(keys) == 0 {
		return ErrDKVSPathNotSynced
	}
	state, err := m.subscriptionStateForClient(client)
	if errors.Is(err, indexercommon.ErrKeyNotFound) {
		return ErrDKVSPathNotSynced
	}
	if err != nil {
		return err
	}
	client.endpointMu.RLock()
	endpointID := client.endpointID
	client.endpointMu.RUnlock()
	if endpointID != "" {
		if err := validateDKVSSubscriptionEndpoint(state, endpointID); err != nil {
			return err
		}
	}
	registered, err := newDKVSReplicaStore(m.owner.db).LoadRegisteredPrefixes(client.replicaNamespace)
	if err != nil {
		return err
	}
	prefixes, err := mergeSubscriptionPrefixes(registered, m.managedPaths())
	if err != nil {
		return err
	}
	if !prefixStateReady(state, prefixes) {
		return ErrDKVSPathNotSynced
	}
	for _, key := range keys {
		covered := false
		for _, prefix := range prefixes {
			if walletSubscriptionMatches(prefix, key) {
				covered = true
				break
			}
		}
		if !covered {
			return ErrDKVSPathNotSynced
		}
	}
	// Restore only an eligible persisted view, including height zero. A newer
	// online observation (or an authoritative reorg height) takes precedence.
	m.verifyMu.Lock()
	if !m.verifyHeightKnown {
		m.verifyHeight, m.verifyHeightKnown, m.verifyHeightFromEndpoint = state.ViewHeight, true, true
	}
	m.verifyMu.Unlock()
	return nil
}
func (m *dkvsManager) pathsReady(client *SatsNetDKVSClient, keys []string) bool {
	if m == nil || client == nil || len(keys) == 0 {
		return false
	}
	var managed []string
	for _, key := range keys {
		if m.desiredPrefixContainsKey(client, key) {
			managed = append(managed, key)
		}
	}
	if len(managed) == 0 {
		return true
	}
	return m.requirePathsReady(client, managed) == nil
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
	err := m.requirePathsReady(client, keys)
	if err == nil {
		return nil
	}
	if !errors.Is(err, ErrDKVSPathNotSynced) {
		return err
	}
	if err := m.ensureCurrentSubscription(client); err != nil {
		return err
	}
	return m.requirePathsReady(client, keys)
}
func (s *dkvsStore) WaitReady(keys ...string) error {
	if s == nil || s.manager == nil || s.client == nil {
		return ErrDKVSPathNotSynced
	}
	return s.manager.waitPathsReady(s.client, keys)
}
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
	if s == nil || s.manager == nil || s.manager.owner == nil || s.manager.owner.db == nil || s.client == nil {
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
	if s.manager.desiredPrefixContainsKey(s.client, key) {
		if err := s.WaitReady(key); err != nil {
			return nil, err
		}
		return s.getConfirmedVerified(key, options)
	}
	ctx, cancel := context.WithTimeout(s.client.requestContext(), dkvsUnmanagedReadTimeout)
	defer cancel()
	record, err := s.client.GetRecordDirectContext(ctx, key)
	if err != nil {
		return nil, err
	}
	height, err := s.client.getBestHeightContext(ctx)
	if err != nil {
		return nil, err
	}
	return verifiedDKVSValueAtHeight(record, key, options, height, true)
}

// Local readiness checks must never fall through to on-demand HTTP or wait on
// the shared transport. Recheck eligibility even if subscriptions changed.
func (s *dkvsStore) getConfirmedVerified(key string, options dkvsindexer.RecordVerificationOptions) (*dkvsValue, error) {
	if s == nil || s.manager == nil || s.client == nil {
		return nil, ErrDKVSPathNotSynced
	}
	if err := s.manager.requirePathsReady(s.client, []string{key}); err != nil {
		return nil, err
	}
	record, err := newDKVSReplicaStore(s.manager.owner.db).LoadSubscriptionRecord(s.client.replicaNamespace, key)
	if errors.Is(err, indexercommon.ErrKeyNotFound) {
		return nil, ErrDKVSRecordNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.verifiedValue(record, key, options)
}
func (s *dkvsStore) verifiedValue(record *swire.DKVSRecord, key string, options dkvsindexer.RecordVerificationOptions) (*dkvsValue, error) {
	s.ObserveVerificationOptions(options)
	height, known := s.manager.verificationHeight()
	return verifiedDKVSValueAtHeight(record, key, options, height, known)
}

// A direct read's height belongs to its selected source and this request. It
// cannot update the manager's confirmed-replica verification authority.
func verifiedDKVSValueAtHeight(record *swire.DKVSRecord, key string, options dkvsindexer.RecordVerificationOptions,
	height uint64, known bool) (*dkvsValue, error) {
	if record == nil {
		return nil, ErrDKVSRecordNotFound
	}
	options, err := normalizeDKVSRecordVerification(record, key, options, height, known)
	if err != nil {
		return nil, err
	}
	if err := dkvsindexer.VerifyRecordForClient(record, options); err != nil {
		return nil, err
	}
	return cloneDKVSValue(record), nil
}

// A direct authoritative read does not advance or replace an entire prefix.
func (s *dkvsStore) GetAuthoritativeVerified(key string, options dkvsindexer.RecordVerificationOptions) (*dkvsValue, error) {
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
func (s *dkvsStore) updateWithOutboxOrigin(keys []string, builder dkvsUpdateBuilder, origin dkvsOutboxOrigin) ([]*dkvsValue, error) {
	if s == nil || s.manager == nil || s.client == nil {
		return nil, ErrDKVSPathNotSynced
	}
	return s.manager.updateValuesWithOrigin(s.client, keys, builder, origin, false)
}
func (s *dkvsStore) updateAuthoritativeWithOutboxOrigin(keys []string, builder dkvsUpdateBuilder, origin dkvsOutboxOrigin) ([]*dkvsValue, error) {
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
	// Config has already identified the policy's source. Check it before a
	// transient refresh failure can fall back to the replica's trusted height.
	s.client.endpointMu.RLock()
	endpointID := s.client.endpointID
	s.client.endpointMu.RUnlock()
	if err := s.manager.validateVerificationEndpoint(s.client, endpointID); err != nil {
		return nil, 0, false, err
	}
	if height, err := s.manager.refreshVerificationBestHeight(s.client); err == nil {
		return policy, height, true, nil
	} else if errors.Is(err, dkvsindexer.ErrEndpointMismatch) {
		return nil, 0, false, err
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
		verify, err := s.verificationOptions(record, record.Key, options)
		if errors.Is(err, dkvsindexer.ErrExpiredRecord) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if err := dkvsindexer.VerifyRecordForClient(record, verify); errors.Is(err, dkvsindexer.ErrExpiredRecord) {
			continue
		} else if err != nil {
			return nil, err
		}
		values = append(values, cloneDKVSValue(record))
	}
	return values, nil
}
func (s *dkvsStore) ListMailboxVerified(accountID string, options dkvsindexer.RecordVerificationOptions) ([]*dkvsValue, error) {
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
	filtered := values[:0]
	for _, value := range values {
		if value != nil && strings.HasPrefix(value.Key, target+"/msg/") {
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
func (m *dkvsManager) currentKeyState(client *SatsNetDKVSClient, key string) (*dkvsindexer.DKVSKeyState, *swire.DKVSRecord, error) {
	if m == nil || m.owner == nil || m.owner.db == nil || client == nil {
		return nil, nil, ErrDKVSPathNotSynced
	}
	store := newDKVSReplicaStore(m.owner.db)
	if m.desiredPrefixContainsKey(client, key) {
		if local, err := store.LoadLocalKeyState(client.replicaNamespace, key); err == nil {
			height, known := m.verificationHeight()
			if !local.Deleted && (!known || local.ExpiryHeight == 0 || local.ExpiryHeight > height) {
				record, err := store.LoadSubscriptionRecord(client.replicaNamespace, key)
				if err != nil {
					return nil, nil, err
				}
				return &dkvsindexer.DKVSKeyState{Key: key, Seq: local.Seq, ETag: local.ETag,
					ExpiryHeight: local.ExpiryHeight, StorageMode: local.StorageMode, Status: dkvsindexer.KeyStateActive, Record: record}, record, nil
			}
			// A local expired value is history, not a permanent CAS floor. Ask
			// the source for the current lifetime before building a new write.
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
func (m *dkvsManager) authoritativeKeyState(client *SatsNetDKVSClient, key string, options dkvsindexer.RecordVerificationOptions) (*dkvsindexer.DKVSKeyState, *swire.DKVSRecord, error) {
	if m == nil || client == nil {
		return nil, nil, ErrDKVSPathNotSynced
	}
	ctx, cancel := context.WithTimeout(client.requestContext(), dkvsUnmanagedReadTimeout)
	defer cancel()
	state, err := client.getKeyStateContext(ctx, key)
	if err != nil {
		return nil, nil, err
	}
	if state == nil || state.Key != key {
		return nil, nil, dkvsindexer.ErrInvalidRecord
	}
	if state.Status != dkvsindexer.KeyStateActive {
		if state.Status != dkvsindexer.KeyStateNeverSeen {
			return nil, nil, dkvsindexer.ErrInvalidRecord
		}
		return state, nil, nil
	}
	record := state.Record
	if record == nil {
		record, err = client.GetRecordDirectContext(ctx, key)
		if err != nil {
			return nil, nil, err
		}
	}
	if record == nil || record.Key != key || record.Seq != state.Seq || state.ETag == "" || dkvsindexer.RecordHash(record).String() != state.ETag {
		return nil, nil, dkvsindexer.ErrInvalidRecord
	}
	height, err := client.getBestHeightContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	options, err = normalizeDKVSRecordVerification(record, key, options, height, true)
	if err != nil {
		return nil, nil, err
	}
	if err := dkvsindexer.VerifyRecordForClient(record, options); err != nil {
		return nil, nil, err
	}
	return state, record, nil
}
func nextDKVSRecordSequence(existing *swire.DKVSRecord, currentSeq uint64) (uint64, error) {
	if existing != nil && existing.Seq > currentSeq {
		currentSeq = existing.Seq
	}
	if currentSeq == ^uint64(0) {
		return 0, dkvsindexer.ErrInvalidSequence
	}
	return currentSeq + 1, nil
}
func (m *dkvsManager) confirmedRecordState(client *SatsNetDKVSClient, key string) (*swire.DKVSRecord, uint64, error) {
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
func (m *dkvsManager) putRecord(client *SatsNetDKVSClient, record *swire.DKVSRecord) (*swire.DKVSRecord, error) {
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
	prepared := cloneDKVSRecord(record)
	if prepared.IssueHeight == 0 {
		height, err := m.refreshVerificationBestHeight(client)
		if err != nil {
			return nil, err
		}
		prepared.IssueHeight, prepared.Signature = height, nil
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
		if err := SignDKVSRecord(signer, prepared); err != nil {
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
	result, err := m.putBatchCASLocked(client, []dkvsindexer.CASMutation{{Record: prepared, Precondition: precondition}})
	if err != nil {
		return nil, err
	}
	if len(result.Records) != 1 {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	return result.Records[0], nil
}
func (m *dkvsManager) putBatchCAS(client *SatsNetDKVSClient, mutations []dkvsindexer.CASMutation) (*DKVSBatchCASResult, error) {
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
func (m *dkvsManager) putBatchCASLocked(client *SatsNetDKVSClient, mutations []dkvsindexer.CASMutation) (*DKVSBatchCASResult, error) {
	if len(mutations) == 0 || mutations[0].Record == nil {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	return m.putBatchCASLockedWithOrigin(client, mutations, dkvsOutboxOrigin{Key: mutations[0].Record.Key})
}
func (m *dkvsManager) putBatchCASLockedWithOrigin(client *SatsNetDKVSClient, mutations []dkvsindexer.CASMutation, origin dkvsOutboxOrigin) (*DKVSBatchCASResult, error) {
	if m == nil || client == nil || len(mutations) == 0 {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	result, err := client.putRecordBatchCASWithOrigin(mutations, origin)
	if err != nil {
		return nil, err
	}
	m.wakeSync()
	return writeResultToBatchResult(result), nil
}

func (m *dkvsManager) buildValueRecord(mutation dkvsValueMutation, seq uint64) (*swire.DKVSRecord, error) {
	if mutation.Owner == nil || mutation.Key == "" || seq == 0 {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	if mutation.Tombstone {
		// The caller supplies the captured current record hash. Deletion is a
		// signed target-bound operation, without storage TTL or a fee proof.
		if len(mutation.Value) != chainhash.HashSize {
			return nil, dkvsindexer.ErrInvalidRecord
		}
		return NewDKVSSignedRecord(mutation.Owner, mutation.Key, mutation.Value,
			dkvsindexer.RecordOptions{Seq: seq, IssueHeight: mutation.IssueHeight, Flags: dkvsindexer.FlagTombstone})
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
			proof, err := dkvsindexer.NewFreeLocalFeeProof(record.Key, parsed.Namespace, uint32(dkvsindexer.RecordSize(record)), dkvsindexer.RecordExpiryHeight(record))
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
			return NewDKVSSignedRecord(mutation.Owner, mutation.Key, value, options)
		}
	default:
		return nil, dkvsindexer.ErrInvalidSignature
	}
}
func (m *dkvsManager) putValues(client *SatsNetDKVSClient, values []dkvsValueMutation) ([]*dkvsValue, error) {
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
	return m.updateValues(client, keys, func(_ map[string]*dkvsValue, _ map[string]uint64) ([]dkvsValueMutation, error) { return values, nil })
}
func (m *dkvsManager) updateValues(client *SatsNetDKVSClient, keys []string, builder dkvsUpdateBuilder) ([]*dkvsValue, error) {
	if len(keys) == 0 {
		return nil, ErrDKVSPathNotSynced
	}
	return m.updateValuesWithOrigin(client, keys, builder, dkvsOutboxOrigin{Key: keys[0]}, false)
}
func (m *dkvsManager) updateValuesWithOrigin(client *SatsNetDKVSClient, keys []string, builder dkvsUpdateBuilder, origin dkvsOutboxOrigin, authoritativeFirst bool) ([]*dkvsValue, error) {
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
			var err error
			if attempt == 0 && !authoritativeFirst {
				state, record, err = m.currentKeyState(client, key)
			} else {
				state, record, err = m.authoritativeKeyState(client, key, dkvsindexer.RecordVerificationOptions{})
			}
			if err != nil {
				return nil, err
			}
			states[key], current[key] = state, cloneDKVSValue(record)
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
			if value.Tombstone {
				if current[value.Key] == nil || current[value.Key].record == nil {
					return nil, dkvsindexer.ErrRecordNotFound
				}
				target := dkvsindexer.RecordHash(current[value.Key].record)
				value.Value = append([]byte(nil), target[:]...)
			}
			record, err := m.buildValueRecord(value, seq)
			if err != nil {
				return nil, err
			}
			precondition, err := statePrecondition(states[value.Key])
			if err != nil {
				return nil, err
			}
			cas = append(cas, dkvsindexer.CASMutation{Record: record, Precondition: precondition})
		}
		result, err := m.putBatchCASLockedWithOrigin(client, cas, origin)
		if err != nil {
			if attempt < 2 && isDKVSRebaseError(err) {
				if (errors.Is(err, dkvsindexer.ErrStaleGeneration) || IsDKVSErrorCode(err, dkvsindexer.ErrorCodeStaleGeneration)) && len(managedKeys) != 0 {
					prefixes := make([]string, 0, len(managedKeys))
					for _, key := range managedKeys {
						prefix, _, pathErr := dkvsManagedPathForKey(key)
						if pathErr != nil {
							return nil, pathErr
						}
						prefixes = append(prefixes, prefix)
					}
					prefixes, pathErr := normalizeWalletSubscriptionPrefixes(prefixes)
					if pathErr != nil {
						return nil, pathErr
					}
					if pathErr = m.forceCurrentPrefixes(client, prefixes); pathErr != nil {
						return nil, pathErr
					}
				}
				continue
			}
			return nil, err
		}
		output := make([]*dkvsValue, 0, len(result.Records))
		for _, record := range result.Records {
			output = append(output, cloneDKVSValue(record))
		}
		return output, nil
	}
	return nil, dkvsindexer.ErrWriteConflict
}
func (m *dkvsManager) registerDirectoriesLocked(client *SatsNetDKVSClient, store *dkvsReplicaStore, directories []string) error {
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
func (m *dkvsManager) registerKeysLocked(client *SatsNetDKVSClient, store *dkvsReplicaStore, keys []string) error {
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
func (m *dkvsManager) waitDirectoriesReady(client *SatsNetDKVSClient, directories []string) error {
	for _, directory := range directories {
		if _, err := dkvsindexer.ParsePrefix(directory); err != nil {
			return err
		}
	}
	// Child prefixes filter their registered collection; they do not own a
	// separate generation. Keys and directories use the same readiness gate.
	return m.waitPathsReady(client, directories)
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
	var prefixes []string
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
	case errors.Is(err, dkvsindexer.ErrInvalidRecord), errors.Is(err, dkvsindexer.ErrInvalidKey),
		errors.Is(err, dkvsindexer.ErrInvalidNamespace), errors.Is(err, dkvsindexer.ErrInvalidSignature),
		errors.Is(err, dkvsindexer.ErrInvalidCheckpoint), errors.Is(err, dkvsindexer.ErrInvalidSnapshot):
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
	m.lastSyncErrorCode, m.lastSyncError, m.lastSyncErrorAt = dkvsSyncErrorCode(err), err.Error(), time.Now().UnixMilli()
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
	manager := &dkvsManager{owner: owner, clients: make(map[string]*SatsNetDKVSClient), paths: make(map[string]struct{}),
		jobs: make(map[string]func(*dkvsStore) error), ready: make(map[string]struct{}),
		pendingNotifies: make(map[string]struct{})}
	runtimeForDKVSManager(manager)
	return manager
}

// Only the coordinator slot is held across transport operations. Data-state
// mutexes remain available to the UI, cancellation, and domain callbacks.
func (m *dkvsManager) runTransport(operation func() error) error {
	if m == nil || operation == nil {
		return ErrDKVSPathNotSynced
	}
	for {
		m.runMu.Lock()
		if !m.runActive {
			m.runActive, m.runDone = true, make(chan struct{})
			done := m.runDone
			m.runMu.Unlock()
			defer func() {
				m.runMu.Lock()
				defer m.runMu.Unlock()
				if m.runDone == done {
					m.runActive, m.runDone = false, nil
					close(done)
				}
			}()
			return operation()
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
	client.manager, client.replicaNamespace = m, m.owner.dkvsReplicaNamespaceFor(scheme, host, proxy)
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
		client.manager, client.replicaNamespace = m, m.owner.dkvsReplicaNamespace()
		m.clients[key] = client
	}
	return client, nil
}
func (m *dkvsManager) setCallback(callback func()) {
	if m != nil {
		m.mu.Lock()
		m.callback = callback
		m.mu.Unlock()
	}
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
		m.jobs[dkvsNotificationJobID] = func(*dkvsStore) error { m.deliverNotifications(); return nil }
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
	m.pendingNotifies, m.notifyQueued = make(map[string]struct{}), false
	observers, callback := append([]func([]string){}, m.observers...), m.callback
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
	var jobErr error
	for _, id := range ids {
		job := jobs[id]
		if err := job(store); err != nil {
			deferred := errors.Is(err, errDKVSJobDeferred)
			m.mu.Lock()
			if _, replaced := m.jobs[id]; !replaced {
				m.jobs[id] = job
			}
			m.mu.Unlock()
			if !deferred {
				jobErr = errors.Join(jobErr, err)
			}
			// Keep the failed task, but let independent tasks (especially
			// confirmed-change notifications) finish this round.
		}
	}
	return jobErr
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
	stop, done, wake := make(chan struct{}), make(chan struct{}), make(chan struct{}, 1)
	requestCtx, cancelRequests := context.WithCancel(context.Background())
	m.ready = make(map[string]struct{})
	m.stop, m.done, m.wake, m.requestCtx, m.cancelRequests, m.stopping = stop, done, wake, requestCtx, cancelRequests, false
	m.mu.Unlock()
	go m.run(stop, done, wake)
}
func (m *dkvsManager) stopAndWait() {
	if m == nil {
		return
	}
	m.mu.Lock()
	stop, done, cancelRequests := m.stop, m.done, m.cancelRequests
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
		m.stop, m.done, m.wake, m.requestCtx, m.cancelRequests, m.stopping = nil, nil, nil, nil, nil, false
	}
	m.mu.Unlock()
	releaseDKVSManagerRuntime(m)
}
func (m *dkvsManager) wakeSync() {
	if m == nil {
		return
	}
	m.mu.Lock()
	wake, stopping := m.wake, m.stopping
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
