package wallet

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/sat20-labs/sat20wallet/sdk/account"
	templatecontract "github.com/sat20-labs/satoshinet/contract/template"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

func accountBlobLimit(state *dkvsindexer.AutopayContractState, payer string) uint32 {
	if state != nil {
		if limit := state.Delegates[payer].BlobKeyLimit; limit != 0 {
			return limit
		}
	}
	return templatecontract.AutopayDefaultBlobKeyLimit
}

func accountBlobTarget(state *dkvsindexer.AutopayContractState, payer string, required uint32) uint32 {
	limit := accountBlobLimit(state, payer)
	if required > limit {
		limit = required
	}
	return limit
}

func accountFundingBlobLimit(record *OperationLogRecord) (uint32, error) {
	if record == nil {
		return 0, fmt.Errorf("AUTOPAY blob capacity intent is unavailable")
	}
	limit, err := strconv.ParseUint(record.Parameters["blob_key_limit"], 10, 32)
	if err != nil || limit == 0 || limit > uint64(templatecontract.AutopayMaxBlobKeyLimit) {
		return 0, fmt.Errorf("AUTOPAY blob capacity intent is invalid")
	}
	return uint32(limit), nil
}

// Count distinct keys, not wallets or providers. A logical deletion retains
// its occupied slot until its delete ACK or a verified absence was saved.
// Discovery uses the same local-only Export/Validate path as initial backup.
func (p *Manager) accountBlobRequirements() (uint32, error) {
	catalog, err := p.accountManagedDataCatalog()
	if err != nil {
		return 0, err
	}
	index := account.ManagedIndex{Version: account.ManagedIndexVersion}
	p.mutex.RLock()
	if p.accountProfile != nil && p.accountProfile.AccountID == catalog.AccountID && len(p.accountSecret) == 32 {
		key, keyErr := account.ManagedIndexKey(catalog.AccountID)
		if keyErr != nil {
			err = keyErr
		} else if local, readErr := p.readAccountLocal(catalog.AccountID, "account", key); readErr != nil {
			err = readErr
		} else if local != nil && len(local.Value) != 0 {
			index, err = account.OpenManagedIndex(p.accountSecret, catalog.AccountID, catalog.Network, local.Value)
		}
	}
	p.mutex.RUnlock()
	if err != nil {
		return 0, err
	}
	entries, generation, err := p.accountModuleEntries(catalog, index)
	if err != nil {
		return 0, err
	}
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	current, err := p.accountManagedCatalogLocked()
	if err != nil {
		return 0, err
	}
	if generation != p.accountGeneration || !reflect.DeepEqual(current, catalog) {
		return 0, errAccountSnapshotChanged
	}
	values, err := p.listAccountLocal(catalog.AccountID)
	if err != nil {
		return 0, err
	}
	keys, reclaimed := make(map[string]bool), make(map[string]bool)
	add := func(key string) error {
		parsed, err := dkvsindexer.ParseKey(key)
		if err != nil {
			return err
		}
		if dkvsindexer.IsBlobKey(parsed) && parsed.Segments[0] == catalog.AccountID {
			keys[key] = true
		}
		return nil
	}
	for _, value := range values {
		if value.Provider == "account" {
			continue
		}
		entry, err := accountEntry(catalog.AccountID, value.Provider, value.Scope)
		if err != nil {
			return 0, err
		}
		if value.Deleted && value.Confirmed != 0 && value.Confirmed == value.Generation && len(value.Value) == 0 &&
			(value.Baseline == nil || dkvsindexer.IsTombstone(value.Baseline.Flags)) {
			reclaimed[entry.Key] = true
			continue
		}
		if value.Deleted && value.Baseline != nil && !dkvsindexer.IsTombstone(value.Baseline.Flags) {
			proof, err := dkvsindexer.ParseFeeProof(value.Baseline.FeeProof)
			if err != nil {
				return 0, err
			}
			if proof.Mode != dkvsindexer.FeeModeAutopay {
				continue
			}
		}
		if err := add(entry.Key); err != nil {
			return 0, err
		}
	}
	for _, entry := range append(entries, index.Entries...) {
		if entry.Deleted || reclaimed[entry.Key] {
			continue
		}
		if entry.Key == "" {
			entry, err = accountEntry(catalog.AccountID, entry.Provider, entry.Scope)
			if err != nil {
				return 0, err
			}
		}
		if err := add(entry.Key); err != nil {
			return 0, err
		}
	}
	// The existing replica registry also retains remotely received or pending
	// deletion records which may no longer appear in the live wallet catalog.
	replica := newDKVSReplicaStore(p.db)
	records, err := replica.ListSubscriptionRecords(p.dkvsReplicaNamespace())
	if err != nil {
		return 0, err
	}
	for _, record := range records {
		if record == nil || reclaimed[record.Key] || dkvsindexer.IsTombstone(record.Flags) {
			continue
		}
		parsed, err := dkvsindexer.ParseKey(record.Key)
		if err != nil || !dkvsindexer.IsBlobKey(parsed) || parsed.Segments[0] != catalog.AccountID {
			continue
		}
		proof, err := dkvsindexer.ParseFeeProof(record.FeeProof)
		if err != nil {
			return 0, err
		}
		if proof.Mode == dkvsindexer.FeeModeAutopay &&
			strings.EqualFold(proof.PoolContract, dkvsindexer.NetworkDefaultsForParams(GetChainParam_SatsNet()).AutopayContract) {
			keys[record.Key] = true
		}
	}
	if len(keys) > int(templatecontract.AutopayMaxBlobKeyLimit) {
		return 0, &DKVSError{Code: dkvsindexer.ErrorCodeQuotaExceeded,
			Message: "account modules exceed the AUTOPAY blob capacity limit"}
	}
	return uint32(len(keys)), nil
}
