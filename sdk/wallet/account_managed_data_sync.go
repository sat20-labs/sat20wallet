package wallet

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/account"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

const accountManagedDataReadyTimeout = 30 * time.Second

const accountManagedOutboxDomain = "account-managed"

var ErrAccountManagedDataImportIncomplete = errors.New("account-managed data import is incomplete; restore the wallet before uploading")

func accountManagedDataImportKey() []byte {
	return []byte(GetDBKeyPrefix() + "account-managed-data-import-pending")
}

// This persistent marker is only a crash boundary for a real remote recovery
// import. Runtime concurrency is excluded by the application sync gate; normal
// PUT/ACK processing must never create it. Only a fully successful import
// removes the marker. Its presence after restart blocks uploads until the SDK
// executes an explicit full recovery again.
func (p *Manager) checkAccountManagedDataImport() error {
	if p == nil || p.db == nil {
		return fmt.Errorf("wallet database is unavailable")
	}
	_, err := p.db.Read(accountManagedDataImportKey())
	if errors.Is(err, indexer.ErrKeyNotFound) {
		return nil
	}
	if err != nil {
		return err
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
	seenState := false
	for _, mutation := range mutations {
		if mutation.Record == nil ||
			(mutation.Record.Key != stateKey && mutation.Record.Key != dataKey) {
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
	profile := *p.accountProfile
	p.mutex.RUnlock()
	root, err := p.accountManagementRootWallet()
	if err != nil {
		return plan, err
	}
	stateKey, err := p.accountManagedStateKey(root)
	if err != nil {
		return plan, err
	}
	dataKey, err := p.accountManagedDataBlobKey(root)
	if err != nil {
		return plan, err
	}
	store, err := p.accountDKVSStore()
	if err != nil {
		return plan, err
	}
	return p.accountManagedOutboxPlanFor(store, profile, stateKey, dataKey)
}

func (p *Manager) accountManagedOutboxPlanFor(store *dkvsStore,
	profile accountManagementProfile, stateKey, dataKey string) (accountManagedOutboxPlan, error) {
	plan := accountManagedOutboxPlan{}
	if store == nil || store.client == nil || p == nil || p.db == nil ||
		strings.TrimSpace(store.client.replicaNamespace) == "" {
		return plan, nil
	}
	entries, err := newDKVSReplicaStore(p.db).LoadOutbox(store.client.replicaNamespace)
	if err != nil {
		return plan, err
	}
	for _, entry := range entries {
		_, matches := accountManagedEntryRecords(entry, stateKey, dataKey)
		if !matches {
			continue
		}
		currentGeneration := entry.OriginDomain == accountManagedOutboxDomain &&
			entry.OriginGeneration == profile.ManagedDataGeneration
		if currentGeneration && entry.State != DKVSOutboxTerminal &&
			entry.State != DKVSOutboxConflict {
			plan.Pending = true
			continue
		}
		if entry.State != DKVSOutboxTerminal {
			continue
		}
		panic(&dkvsTerminalOutboxError{
			RequestID: entry.RequestID, Code: entry.LastErrorCode, Message: entry.LastError,
		})
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

func shouldReuseRemoteManagedDataEnvelope(remoteState account.ManagedState, mergedHash string,
	remote *accountManagedDataSnapshot, compressionBeneficial bool) bool {

	return remote != nil && remoteState.DataRevision != 0 &&
		remoteState.DataHash == mergedHash && len(remote.Envelope) != 0 &&
		!(compressionBeneficial && !remote.Compressed)
}

func (p *Manager) buildAccountManagedDataSnapshot(secret []byte, accountID string,
	revision uint64) (*accountManagedDataSnapshot, error) {

	catalog, err := p.accountManagedDataCatalog()
	if err != nil {
		return nil, err
	}
	bundle, hash, err := p.exportAccountManagedData(catalog, revision)
	if err != nil {
		return nil, err
	}
	envelope, info, err := account.SealManagedDataBundleWithInfo(secret, accountID, bundle, nil)
	if err != nil {
		return nil, err
	}
	return &accountManagedDataSnapshot{
		Catalog: catalog, Bundle: bundle, Hash: hash, Envelope: envelope,
		Compressed: info.Compressed,
	}, nil
}

func verifyAccountManagedDataReference(state account.ManagedState,
	bundle account.ManagedDataBundle, expectedHash string) error {

	if state.DataRevision == 0 {
		if state.DataHash != "" || len(bundle.Items) != 0 {
			return fmt.Errorf("managed data reference is inconsistent")
		}
		return nil
	}
	if bundle.Revision != state.DataRevision || expectedHash == "" ||
		state.DataHash != expectedHash {
		return fmt.Errorf("managed data reference does not match account state")
	}
	return nil
}

func openAccountManagedDataValue(secret []byte, accountID string, value *dkvsValue,
	state account.ManagedState) (*accountManagedDataSnapshot, error) {

	if state.DataRevision == 0 {
		return &accountManagedDataSnapshot{
			Bundle: account.ManagedDataBundle{
				Version: account.ManagedDataBundleVersion, Revision: 1,
			},
		}, nil
	}
	if value == nil {
		return nil, fmt.Errorf("account-managed data blob is unavailable")
	}
	blob, err := DecodeDKVSBlobValue(value.Value)
	if err != nil {
		return nil, err
	}
	bundle, info, err := account.OpenManagedDataBundleWithInfo(secret, accountID, blob.Data)
	if err != nil {
		return nil, err
	}
	hash, err := accountManagedDataContentHash(bundle.Items)
	if err != nil {
		return nil, err
	}
	if err := verifyAccountManagedDataReference(state, bundle, hash); err != nil {
		return nil, err
	}
	return &accountManagedDataSnapshot{Bundle: bundle, Hash: hash,
		Envelope: append([]byte(nil), blob.Data...), Compressed: info.Compressed}, nil
}

func (p *Manager) importAccountManagedDataSnapshot(value *accountManagedDataSnapshot) error {
	if value == nil {
		return nil
	}
	catalog, err := p.accountManagedDataCatalog()
	if err != nil {
		return err
	}
	value.Catalog = catalog
	return p.importAccountManagedData(catalog, value.Bundle)
}

func accountManagementMutations(profile *accountManagementProfile, root common.Wallet,
	stateKey string, stateEnvelope []byte, dataKey string, dataEnvelope []byte,
	writeData bool) ([]dkvsValueMutation, error) {

	stateMutation, err := accountStateMutation(profile, root, stateKey, stateEnvelope)
	if err != nil {
		return nil, err
	}
	mutations := []dkvsValueMutation{stateMutation}
	if writeData {
		dataMutation, err := accountManagedDataMutation(profile, root, dataKey, dataEnvelope)
		if err != nil {
			return nil, err
		}
		mutations = append(mutations, dataMutation)
	}
	return mutations, nil
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

func (p *Manager) verifyAccountManagedStorage(store *dkvsStore,
	profile *accountManagementProfile, stateKey, dataKey string,
	stateEnvelope, dataEnvelope []byte) error {

	if store == nil || profile == nil {
		return fmt.Errorf("account-managed storage is unavailable")
	}
	if err := store.WaitReady(stateKey, dataKey); err != nil {
		return err
	}
	stateValue, err := store.Get(stateKey)
	if err != nil {
		return err
	}
	dataValue, err := store.Get(dataKey)
	if err != nil {
		return err
	}
	if !bytes.Equal(stateValue.Value, stateEnvelope) ||
		!accountRecordMatchesStorage(stateValue, profile) ||
		!accountRecordMatchesStorage(dataValue, profile) {
		return fmt.Errorf("account-managed storage readback does not match the committed policy")
	}
	blob, err := DecodeDKVSBlobValue(dataValue.Value)
	if err != nil || !bytes.Equal(blob.Data, dataEnvelope) {
		return fmt.Errorf("account-managed data blob readback mismatch")
	}
	return nil
}

func (p *Manager) requireCurrentAccountManagedData() error {
	if p == nil {
		return ErrDKVSPathNotSynced
	}
	p.mutex.RLock()
	if p.accountProfile == nil {
		p.mutex.RUnlock()
		return nil
	}
	profile := *p.accountProfile
	profile.StateEnvelope = append([]byte(nil), p.accountProfile.StateEnvelope...)
	profile.ManagedDataEnvelope = append([]byte(nil), p.accountProfile.ManagedDataEnvelope...)
	p.mutex.RUnlock()
	// An account transport/import operation still running is a normal wait, not
	// a failed recovery. Inspect the coordinator state without holding its mutex
	// while reading the durable marker.
	if p.accountOperationActive() {
		return ErrDKVSPathNotSynced
	}
	importErr := p.checkAccountManagedDataImport()
	if importErr != nil {
		return importErr
	}
	if profile.ManagedDataDirty || profile.StateSeq == 0 ||
		profile.ManagedDataRevision == 0 {
		return ErrDKVSPathNotSynced
	}
	root, err := p.accountManagementRootWallet()
	if err != nil {
		return err
	}
	stateKey, err := p.accountManagedStateKey(root)
	if err != nil {
		return err
	}
	dataKey, err := p.accountManagedDataBlobKey(root)
	if err != nil {
		return err
	}
	store, err := p.accountDKVSStore()
	if err != nil {
		return err
	}
	if err := store.WaitReady(stateKey, dataKey); err != nil {
		return err
	}
	stateValue, err := store.Get(stateKey)
	if err != nil {
		return err
	}
	dataValue, err := store.Get(dataKey)
	if err != nil {
		return err
	}
	if accountStateDigest(stateValue.Value) != profile.StateHash ||
		!bytes.Equal(stateValue.Value, profile.StateEnvelope) {
		return dkvsindexer.ErrWriteConflict
	}
	blob, err := DecodeDKVSBlobValue(dataValue.Value)
	if err != nil || !bytes.Equal(blob.Data, profile.ManagedDataEnvelope) {
		return dkvsindexer.ErrWriteConflict
	}
	state, err := account.OpenManagedState(p.accountSecret, profile.AccountID,
		stateValue.Value)
	if err != nil || state.DataRevision != profile.ManagedDataRevision ||
		state.DataHash != profile.ManagedDataHash {
		return dkvsindexer.ErrWriteConflict
	}
	return nil
}

// WaitAccountManagedDataReady waits until the account-managed state and blob
// covering current wallet data are confirmed in DKVS. It never holds an RGB
// operation lock and always has a bounded, cancellable lifetime.
func (p *Manager) WaitAccountManagedDataReady(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		err := p.waitAccountManagedDataReadyAttempt(ctx)
		if err == nil || !errors.Is(err, ErrAccountManagementWalletUnavailable) {
			return err
		}

		// Wallet activation/switching is a transient readiness condition.
		// Preserve the Wait contract and let the caller's context decide how
		// long to wait instead of returning a terminal-looking error.
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (p *Manager) waitAccountManagedDataReadyAttempt(ctx context.Context) error {
	if p == nil {
		return ErrDKVSPathNotSynced
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, accountManagedDataReadyTimeout)
		defer cancel()
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		err := p.requireCurrentAccountManagedData()
		if err == nil {
			return nil
		}
		if !errors.Is(err, ErrDKVSPathNotSynced) &&
			!errors.Is(err, dkvsindexer.ErrWriteConflict) &&
			!errors.Is(err, dkvsindexer.ErrResetRequired) &&
			!errors.Is(err, dkvsindexer.ErrEndpointMismatch) {
			return err
		}
		_, planErr := p.currentAccountManagedOutboxPlan()
		if planErr != nil && !errors.Is(planErr, ErrAccountManagementWalletUnavailable) {
			return planErr
		}
		p.markDKVSStateDirty()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
