package wallet

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/account"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/dkvs"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
)

// AccountDKVSInspection is a local observation of the confirmed replica. It
// does not fetch remote records and never mixes unacknowledged outbox values
// with the raw record bytes shown here.
type AccountDKVSInspection struct {
	Scope      AccountDKVSInspectionScope    `json:"scope"`
	Snapshot   AccountDKVSInspectionSnapshot `json:"snapshot"`
	Records    []AccountDKVSInspectionRecord `json:"records"`
	NextCursor string                        `json:"next_cursor,omitempty"`
}

type AccountDKVSInspectionScope struct {
	AccountID string   `json:"account_id"`
	Wallets   []string `json:"wallet_subscopes,omitempty"`
}

type AccountDKVSInspectionSnapshot struct {
	ID               string   `json:"id"`
	Status           string   `json:"status"`
	EndpointID       string   `json:"endpoint_id,omitempty"`
	VerifiedAtMS     uint64   `json:"verified_at_ms,omitempty"`
	CacheHeight      uint64   `json:"cache_height,omitempty"`
	CoveredPrefixes  []string `json:"covered_prefixes"`
	Complete         bool     `json:"complete"`
	RemoteCurrent    bool     `json:"remote_current"`
	LocalDirty       bool     `json:"local_dirty"`
	PendingChanges   int      `json:"pending_changes"`
	TotalCachedCount int      `json:"total_cached_count"`
}

type AccountDKVSInspectionRecord struct {
	Key             string                       `json:"key"`
	RawHex          string                       `json:"raw_hex,omitempty"`
	RawHidden       bool                         `json:"raw_hidden,omitempty"`
	RawHiddenReason string                       `json:"raw_hidden_reason,omitempty"`
	RecordHash      string                       `json:"record_hash"`
	Seq             uint64                       `json:"seq"`
	Decoded         AccountDKVSInspectionDecoded `json:"decoded"`
}

type AccountDKVSInspectionDecoded struct {
	Type         string   `json:"type"`
	Status       string   `json:"status"`
	Revision     uint64   `json:"revision,omitempty"`
	DataRevision uint64   `json:"data_revision,omitempty"`
	WalletCount  int      `json:"wallet_count,omitempty"`
	ItemCount    int      `json:"item_count,omitempty"`
	Providers    []string `json:"providers,omitempty"`
	Subscopes    []string `json:"subscopes,omitempty"`
}

type AccountDKVSInspectOptions struct {
	Cursor string `json:"cursor,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

type accountDKVSInspectionCursor struct {
	AccountID string `json:"account_id"`
	Snapshot  string `json:"snapshot"`
	Offset    int    `json:"offset"`
}

// The replica's state and all of its records must be read from one immutable
// database version. Embedding KVDB supplies only the unused write methods;
// the replica's read methods are pinned to view below.
type accountDKVSInspectionDB struct {
	indexer.KVDB
	view indexer.DBReadView
}

func (db accountDKVSInspectionDB) Read(key []byte) ([]byte, error) {
	return db.view.Read(key)
}

func (db accountDKVSInspectionDB) Scan(options indexer.ScanOptions, visit func([]byte, []byte) error) error {
	return db.view.Scan(options, visit)
}

func (db accountDKVSInspectionDB) BatchRead(prefix []byte, reverse bool, visit func([]byte, []byte) error) error {
	return db.view.Scan(indexer.ScanOptions{Prefix: prefix, Reverse: reverse, CopyKey: true, CopyValue: true}, visit)
}

// InspectCurrentAccountDKVS lists only records in the currently unlocked
// account's verified local subscription cache. Its cursor cannot select a
// different account or combine pages from two cache generations.
func (p *Manager) InspectCurrentAccountDKVS(options AccountDKVSInspectOptions) (*AccountDKVSInspection, error) {
	if p == nil || p.db == nil {
		return nil, ErrDKVSPathNotSynced
	}
	p.mutex.RLock()
	root, err := p.accountManagementRootWalletLocked()
	if err != nil {
		p.mutex.RUnlock()
		return nil, err
	}
	rootWallet := cloneWalletAtAccountZero(root.Wallet)
	if rootWallet == nil {
		p.mutex.RUnlock()
		return nil, ErrAccountManagementWalletUnavailable
	}
	accountID, err := dkvsAccountID(rootWallet)
	if err != nil || p.accountProfile == nil || accountID != p.accountProfile.AccountID ||
		walletFingerprint(rootWallet) != p.accountProfile.RootFingerprint || len(p.accountSecret) != 32 {
		p.mutex.RUnlock()
		return nil, ErrAccountManagementWalletUnavailable
	}
	secret := append([]byte(nil), p.accountSecret...)
	address := rootWallet.GetAddress()
	network := _chain
	dirty, pending := p.accountProfile.ManagedDataDirty, len(p.accountProfile.Pending)
	p.mutex.RUnlock()
	defer func() {
		for i := range secret {
			secret[i] = 0
		}
	}()

	stateKey, err := dkvsindexer.AccountPersonalKey(accountID, accountManagedStatePath)
	if err != nil {
		return nil, err
	}
	indexKey, err := account.ManagedIndexKey(accountID)
	if err != nil {
		return nil, err
	}
	wrapperKey, err := dkvsindexer.AccountPersonalKey(accountID, accountRootWrapperPath)
	if err != nil {
		return nil, err
	}
	mailPrefix, err := mailboxSubscriptionTarget(accountID)
	if err != nil {
		return nil, err
	}
	mappingKey := ""
	if address != "" {
		mappingKey, err = dkvsindexer.AccountMappingKey(GetChainParam().Name, address)
		if err != nil {
			return nil, err
		}
	}
	allowedPrefixes := []string{"/personal/" + accountID, "/blob/" + accountID, mailPrefix}
	namespace := p.dkvsReplicaNamespace()
	// Inspection only needs an immutable read view. Requiring SnapshotDB would
	// also demand a write-durability Sync method from browser IndexedDB, which
	// cannot be inferred from its read transaction.
	snapshotDB, ok := p.db.(interface {
		NewReadView() (indexer.DBReadView, error)
	})
	if !ok {
		return nil, ErrDKVSPathNotSynced
	}
	view, err := snapshotDB.NewReadView()
	if err != nil {
		return nil, err
	}
	defer view.Close()
	replica := newDKVSReplicaStore(accountDKVSInspectionDB{KVDB: p.db, view: view})
	state, err := replica.LoadSubscriptionState(namespace)
	if errors.Is(err, indexer.ErrKeyNotFound) {
		state, err = &dkvs.SubscriptionState{Status: DKVSSubscriptionSyncing}, nil
	}
	if err != nil {
		return nil, err
	}
	all, err := replica.ListSubscriptionRecords(namespace)
	if err != nil {
		return nil, err
	}
	height, heightKnown := state.ViewHeight, state.LastSyncAtMS != 0
	selected := make([]*wire.DKVSRecord, 0)
	hashes := make([]string, 0)
	for _, record := range all {
		if record == nil || !accountDKVSInspectionOwns(record.Key, allowedPrefixes, mappingKey) {
			continue
		}
		if _, err := verifiedDKVSValueAtHeight(record, record.Key,
			dkvsindexer.RecordVerificationOptions{}, height, heightKnown); err != nil {
			return nil, fmt.Errorf("verify cached account record %q: %w", record.Key, err)
		}
		selected = append(selected, record)
		hashes = append(hashes, dkvsindexer.RecordHash(record).String())
	}
	meta, _ := json.Marshal(struct {
		AccountID string
		Namespace string
		State     *dkvs.SubscriptionState
	}{accountID, namespace, state})
	digest := sha256.New()
	digest.Write(meta)
	for i, record := range selected {
		digest.Write([]byte(record.Key))
		digest.Write([]byte{0})
		digest.Write([]byte(hashes[i]))
	}
	snapshotID := hex.EncodeToString(digest.Sum(nil))
	offset := 0
	if options.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(options.Cursor)
		if err != nil || len(raw) > 512 {
			return nil, ErrDKVSPathNotSynced
		}
		var cursor accountDKVSInspectionCursor
		if json.Unmarshal(raw, &cursor) != nil || cursor.AccountID != accountID ||
			cursor.Snapshot != snapshotID || cursor.Offset < 0 || cursor.Offset > len(selected) {
			return nil, ErrDKVSPathNotSynced
		}
		offset = cursor.Offset
	}
	limit := options.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}
	end := offset + limit
	if end > len(selected) {
		end = len(selected)
	}
	coverage := make([]string, 0, len(state.Prefixes))
	for _, prefix := range state.Prefixes {
		if accountDKVSInspectionOwns(prefix+"/", allowedPrefixes, mappingKey) ||
			(mappingKey != "" && walletSubscriptionMatches(prefix, mappingKey)) {
			coverage = append(coverage, prefix)
		}
	}
	sort.Strings(coverage)
	complete := state.Status == DKVSSubscriptionReady || state.Status == DKVSSubscriptionOfflineReady
	for _, rootPrefix := range allowedPrefixes {
		covered := false
		for _, prefix := range coverage {
			if prefix == rootPrefix {
				covered = true
				break
			}
		}
		complete = complete && covered
	}
	if mappingKey != "" {
		mappingCovered := false
		for _, prefix := range coverage {
			mappingCovered = mappingCovered || walletSubscriptionMatches(prefix, mappingKey)
		}
		complete = complete && mappingCovered
	}
	result := &AccountDKVSInspection{
		Scope: AccountDKVSInspectionScope{AccountID: accountID},
		Snapshot: AccountDKVSInspectionSnapshot{
			ID: snapshotID, Status: state.Status, EndpointID: state.EndpointID,
			VerifiedAtMS: state.LastSyncAtMS, CacheHeight: state.ViewHeight,
			CoveredPrefixes: coverage, Complete: complete,
			// A cache read does not contact the authority and cannot prove current
			// remote state, even immediately after a previous synchronization.
			RemoteCurrent: false, LocalDirty: dirty, PendingChanges: pending,
			TotalCachedCount: len(selected),
		},
		Records: make([]AccountDKVSInspectionRecord, 0, end-offset),
	}
	entries := make(map[string]account.ManagedEntry)
	subscopeSet := make(map[string]struct{})
	for _, record := range selected {
		if record.Key != indexKey {
			continue
		}
		index, err := account.OpenManagedIndex(secret, accountID, network, record.Value)
		if err != nil {
			break
		}
		for _, entry := range index.Entries {
			if !entry.Deleted {
				entries[entry.Key] = entry
				subscopeSet[entry.Scope] = struct{}{}
			}
		}
	}
	for index := offset; index < end; index++ {
		record := selected[index]
		view := AccountDKVSInspectionRecord{
			Key: record.Key, RecordHash: hashes[index], Seq: record.Seq,
			RawHidden: true, RawHiddenReason: "unverified_value_type",
			Decoded: AccountDKVSInspectionDecoded{Type: "unknown", Status: "raw_hidden"},
		}
		verified := false
		switch record.Key {
		case stateKey:
			view.Decoded = AccountDKVSInspectionDecoded{Type: "managed_state", Status: "invalid"}
			state, err := account.OpenManagedState(secret, accountID, record.Value)
			if err == nil {
				verified = true
				view.Decoded.WalletCount = len(state.Wallets)
			}
		case indexKey:
			view.Decoded = AccountDKVSInspectionDecoded{Type: "managed_index", Status: "invalid"}
			index, err := account.OpenManagedIndex(secret, accountID, network, record.Value)
			if err == nil {
				verified = true
				view.Decoded.ItemCount = len(index.Entries)
			}
		case wrapperKey:
			view.Decoded = AccountDKVSInspectionDecoded{Type: "root_key_wrapper", Status: "invalid"}
			payload, err := openAccountRootWrapper(rootWallet, network, accountID, record.Value)
			zeroBytes(payload.Secret)
			verified = err == nil
		default:
			if entry, ok := entries[record.Key]; ok {
				view.Decoded = AccountDKVSInspectionDecoded{Type: "managed_module", Status: "invalid", Providers: []string{entry.Provider}, Subscopes: []string{entry.Scope}}
				payload, err := openAccountPayload(secret, accountID, network, entry, record.Value)
				zeroBytes(payload)
				verified = err == nil
			}
		}
		if verified {
			view.RawHex = hex.EncodeToString(record.Value)
			view.RawHidden = false
			view.RawHiddenReason = ""
			view.Decoded.Status = "verified"
		}
		result.Records = append(result.Records, view)
	}
	for scope := range subscopeSet {
		result.Scope.Wallets = append(result.Scope.Wallets, scope)
	}
	sort.Strings(result.Scope.Wallets)
	if end < len(selected) {
		cursor, _ := json.Marshal(accountDKVSInspectionCursor{AccountID: accountID, Snapshot: snapshotID, Offset: end})
		result.NextCursor = base64.RawURLEncoding.EncodeToString(cursor)
	}
	p.mutex.RLock()
	currentRoot, currentErr := p.accountManagementRootWalletLocked()
	stillCurrent := currentErr == nil && currentRoot != nil && currentRoot.Wallet != nil && _chain == network &&
		p.accountProfile != nil && p.accountProfile.AccountID == accountID &&
		p.accountProfile.RootFingerprint == walletFingerprint(rootWallet) &&
		bytes.Equal(p.accountSecret, secret) &&
		walletFingerprint(cloneWalletAtAccountZero(currentRoot.Wallet)) == walletFingerprint(rootWallet)
	p.mutex.RUnlock()
	if !stillCurrent {
		return nil, ErrDKVSPathNotSynced
	}
	return result, nil
}

func accountDKVSInspectionOwns(key string, prefixes []string, mappingKey string) bool {
	if key == mappingKey && mappingKey != "" {
		return true
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(key, prefix+"/") {
			return true
		}
	}
	return false
}
