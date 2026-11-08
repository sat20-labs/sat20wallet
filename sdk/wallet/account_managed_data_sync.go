package wallet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/account"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

const accountManagedDataReadyTimeout = 30 * time.Second

const accountManagedOutboxDomain = "account-managed"

var ErrAccountManagedDataImportIncomplete = errors.New("account-managed data import is incomplete; restore the wallet before uploading")

func accountManagedDataImportKey() []byte {
	return []byte(GetDBKeyPrefix() + "account-managed-data-import-pending")
}

const (
	accountManagedImportMarkerVersion = 1

	accountManagedImportOriginUnknown     = "unknown"
	accountManagedImportOriginRestore     = "restore"
	accountManagedImportOriginRemoteApply = "remote-apply"

	accountManagedImportStageUnknown        = "unknown"
	accountManagedImportStageLocalCommit    = "local-commit"
	accountManagedImportStageProviderImport = "provider-import"
	accountManagedImportStageScopeRebuild   = "scope-rebuild"
	accountManagedImportStageMarkerDelete   = "marker-delete"
)

type accountManagedDataImportMarker struct {
	Version             uint32 `json:"version"`
	Origin              string `json:"origin"`
	Stage               string `json:"stage"`
	TargetStateRevision uint64 `json:"target_state_revision,omitempty"`
	TargetStateHash     string `json:"target_state_hash,omitempty"`
	TargetDataRevision  uint64 `json:"target_data_revision,omitempty"`
	TargetDataHash      string `json:"target_data_hash,omitempty"`
	CreatedAtUnix       int64  `json:"created_at_unix,omitempty"`
	UpdatedAtUnix       int64  `json:"updated_at_unix,omitempty"`
	// Only a rebase needs these: the profile retains the confirmed remote
	// baseline, while the authenticated merge must survive a partial import.
	ConfirmedStateHash  string `json:"confirmed_state_hash,omitempty"`
	ReplayStateEnvelope []byte `json:"replay_state_envelope,omitempty"`
	ReplayDataEnvelope  []byte `json:"replay_data_envelope,omitempty"`
}

func accountManagedImportMarkerForProfile(origin, stage string,
	profile *accountManagementProfile) accountManagedDataImportMarker {

	now := time.Now().Unix()
	marker := accountManagedDataImportMarker{
		Version: accountManagedImportMarkerVersion,
		Origin:  strings.TrimSpace(origin), Stage: strings.TrimSpace(stage),
		CreatedAtUnix: now, UpdatedAtUnix: now,
	}
	if marker.Origin == "" {
		marker.Origin = accountManagedImportOriginUnknown
	}
	if marker.Stage == "" {
		marker.Stage = accountManagedImportStageUnknown
	}
	if profile != nil {
		marker.TargetStateRevision = profile.StateSeq
		marker.TargetStateHash = profile.StateHash
		marker.TargetDataRevision = profile.ManagedDataRevision
		marker.TargetDataHash = profile.ManagedDataHash
	}
	return marker
}

func (p *Manager) writeAccountManagedDataImportMarker(marker accountManagedDataImportMarker) error {
	if p == nil || p.db == nil {
		return fmt.Errorf("wallet database is unavailable")
	}
	if marker.Version == 0 {
		marker.Version = accountManagedImportMarkerVersion
	}
	if strings.TrimSpace(marker.Origin) == "" {
		marker.Origin = accountManagedImportOriginUnknown
	}
	if strings.TrimSpace(marker.Stage) == "" {
		marker.Stage = accountManagedImportStageUnknown
	}
	now := time.Now().Unix()
	if marker.CreatedAtUnix == 0 {
		marker.CreatedAtUnix = now
	}
	marker.UpdatedAtUnix = now
	encoded, err := json.Marshal(marker)
	if err != nil {
		return err
	}
	return p.db.Write(accountManagedDataImportKey(), encoded)
}

func (p *Manager) updateAccountManagedDataImportStage(stage string) error {
	marker, err := p.readAccountManagedDataImportMarker()
	if err != nil {
		return err
	}
	if marker == nil {
		return ErrAccountManagedDataImportIncomplete
	}
	marker.Stage = strings.TrimSpace(stage)
	if marker.Stage == "" {
		marker.Stage = accountManagedImportStageUnknown
	}
	return p.writeAccountManagedDataImportMarker(*marker)
}

func (p *Manager) readAccountManagedDataImportMarker() (*accountManagedDataImportMarker, error) {
	if p == nil || p.db == nil {
		return nil, fmt.Errorf("wallet database is unavailable")
	}
	encoded, err := p.db.Read(accountManagedDataImportKey())
	if errors.Is(err, indexer.ErrKeyNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var marker accountManagedDataImportMarker
	if err := json.Unmarshal(encoded, &marker); err != nil ||
		marker.Version != accountManagedImportMarkerVersion {
		// A malformed marker is still an incomplete-import barrier. Diagnostics
		// may be unavailable, but corruption must never turn into permission to
		// upload or activate.
		return &accountManagedDataImportMarker{
			Version: 0, Origin: accountManagedImportOriginUnknown,
			Stage: accountManagedImportStageUnknown,
		}, nil
	}
	if strings.TrimSpace(marker.Origin) == "" {
		marker.Origin = accountManagedImportOriginUnknown
	}
	if strings.TrimSpace(marker.Stage) == "" {
		marker.Stage = accountManagedImportStageUnknown
	}
	return &marker, nil
}

// This persistent marker is only a crash boundary for a real remote recovery
// import. Runtime concurrency is excluded by the application sync gate; normal
// PUT/ACK processing must never create it. Only a fully successful import
// removes the marker. Remote apply resumes its authenticated committed target
// before any export; restore-origin markers require the same explicit recovery.
func (p *Manager) checkAccountManagedDataImport() error {
	marker, err := p.readAccountManagedDataImportMarker()
	if err != nil {
		return err
	}
	if marker == nil {
		return nil
	}
	return ErrAccountManagedDataImportIncomplete
}

type accountManagedOutboxPlan struct {
	Pending bool
}

func (p accountManagedOutboxPlan) origin(key string, generation uint64) dkvsOutboxOrigin {
	return dkvsOutboxOrigin{
		Key: key, Domain: accountManagedOutboxDomain, Generation: generation,
	}
}

func applyAccountManagedOutboxPlan(mutations []dkvsValueMutation, _ string,
	_ accountManagedOutboxPlan) []dkvsValueMutation {
	return mutations
}

func accountManagedEntryRecords(entry *DKVSBatchOutboxEntry,
	stateKey, dataKey string) ([]dkvsindexer.CASMutation, bool) {
	if entry == nil {
		return nil, false
	}
	mutations, err := entry.DecodeMutations()
	if err != nil || len(mutations) == 0 {
		return nil, false
	}
	wrapperKey := ""
	if strings.HasSuffix(stateKey, "/"+accountManagedStatePath) {
		wrapperKey = strings.TrimSuffix(stateKey, accountManagedStatePath) + accountRootWrapperPath
	}
	seenState := false
	for _, mutation := range mutations {
		if mutation.Record == nil ||
			(mutation.Record.Key != stateKey && mutation.Record.Key != dataKey && (wrapperKey == "" || mutation.Record.Key != wrapperKey)) {
			return nil, false
		}
		seenState = seenState || mutation.Record.Key == stateKey
	}
	return mutations, seenState
}

func (p *Manager) currentAccountManagedOutboxPlan() (accountManagedOutboxPlan, error) {
	plan := accountManagedOutboxPlan{}
	if p == nil {
		return plan, ErrDKVSPathNotSynced
	}
	p.mutex.RLock()
	if p.accountProfile == nil {
		p.mutex.RUnlock()
		return plan, nil
	}
	accountID := p.accountProfile.AccountID
	p.mutex.RUnlock()
	store, err := p.accountDKVSStore()
	if err != nil {
		return plan, err
	}
	if store == nil || store.client == nil || p.db == nil ||
		strings.TrimSpace(store.client.replicaNamespace) == "" {
		return plan, nil
	}
	entries, err := newDKVSReplicaStore(p.db).LoadOutbox(store.client.replicaNamespace)
	if err != nil {
		return plan, err
	}
	for _, entry := range entries {
		if entry == nil || entry.OriginDomain != accountKVOutboxDomain {
			continue
		}
		matches := strings.HasPrefix(entry.OriginKey, "/personal/"+accountID+"/account/")
		if !matches {
			p.mutex.RLock()
			local, err := p.accountLocalForKey(accountID, entry.OriginKey)
			p.mutex.RUnlock()
			if err != nil {
				return plan, err
			}
			if local != nil && local.Provider != "account" {
				expected, err := accountEntry(accountID, local.Provider, local.Scope)
				if err != nil {
					return plan, err
				}
				matches = expected.Key == entry.OriginKey
			}
		}
		if !matches {
			continue
		}
		if entry.State == DKVSOutboxConflict {
			return plan, dkvsOutboxEntryError(entry)
		}
		if entry.State == DKVSOutboxAcknowledged {
			covered, err := p.accountKVACKCovered(entry)
			if err != nil {
				return plan, err
			}
			if covered {
				continue
			}
		}
		if entry.State == DKVSOutboxTerminal {
			return plan, &dkvsTerminalOutboxError{
				RequestID: entry.RequestID, Code: entry.LastErrorCode, Message: entry.LastError,
			}
		}
		plan.Pending = true
	}
	return plan, nil
}

type accountManagedDataSnapshot struct {
	Catalog    AccountManagedDataCatalog
	Bundle     account.ManagedDataBundle
	Hash       string
	Envelope   []byte
	Compressed bool
}

func configureAccountTemporaryRetention(store *dkvsStore,
	profile *accountManagementProfile) error {

	if profile == nil {
		return fmt.Errorf("account management profile is unavailable")
	}
	if profile.StorageMode != AccountStorageTemporary {
		return nil
	}
	options := dkvsindexer.RecordOptions{}
	if _, err := store.ConfigureFreeLocalRetention(&options); err != nil {
		return err
	}
	profile.RecordTTL = options.TTL
	return nil
}

// A verified paid ACK can outlive a local activation whose scope changed.
// Refuse a stale temporary profile before creating an outbox submission; do
// not promote the local profile or overwrite the newer remote configuration.
func rejectAccountRecordStorageDowngrade(profile *accountManagementProfile, values ...*dkvsValue) error {
	if profile == nil || profile.StorageMode != AccountStorageTemporary {
		return nil
	}
	for _, value := range values {
		if value == nil || value.record == nil {
			continue
		}
		proof, err := dkvsindexer.ParseFeeProof(value.record.FeeProof)
		if err != nil {
			return err
		}
		if proof.Mode == dkvsindexer.FeeModeAutopay {
			return ErrAccountStorageModeDowngrade
		}
	}
	return nil
}

func accountRecordMatchesStorage(value *dkvsValue, profile *accountManagementProfile) bool {
	if value == nil || value.record == nil || profile == nil {
		return false
	}
	proof, err := dkvsindexer.ParseFeeProof(value.record.FeeProof)
	if err != nil {
		return false
	}
	switch profile.StorageMode {
	case AccountStorageTemporary:
		return proof.Mode == dkvsindexer.FeeModeFreeLocal &&
			value.record.TTL == profile.RecordTTL && profile.RecordTTL != 0
	case AccountStoragePaid:
		return proof.Mode == dkvsindexer.FeeModeAutopay && value.record.TTL == 0 &&
			strings.EqualFold(strings.TrimSpace(proof.PoolContract),
				strings.TrimSpace(profile.AutopayContract))
	default:
		return false
	}
}

func (p *Manager) WaitAccountManagedDataReady(ctx context.Context) error {
	return p.WaitAccountBackup(ctx, "", "")
}
