package wallet

import (
	"bytes"
	"fmt"

	"github.com/sat20-labs/sat20wallet/sdk/account"
)

// AccountDataMerger is optional. Equality and one-sided changes need no hook;
// account management never unions opaque business payloads on its own.
type AccountDataMerger interface {
	Merge(AccountManagedDataCatalog, string, []byte, []byte, []byte) ([]byte, error)
}

type accountModuleError struct {
	provider string
	scope    string
	cause    error
}

func (e *accountModuleError) Error() string { return "account module backup: " + e.cause.Error() }
func (e *accountModuleError) Unwrap() error { return e.cause }

func accountModuleOnly(err error) bool {
	if err == nil {
		return false
	}
	if _, ok := err.(*accountModuleError); ok {
		return true
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, cause := range joined.Unwrap() {
			if !accountModuleOnly(cause) && !accountACKCleanupOnly(cause) {
				return false
			}
		}
		return len(joined.Unwrap()) != 0
	}
	return false
}

func accountScopeCatalog(catalog AccountManagedDataCatalog, scope string) (AccountManagedDataCatalog, error) {
	if scope == AccountManagedDataGlobalScope {
		return catalog, nil
	}
	for _, item := range catalog.Scopes {
		if item.ID() == scope {
			catalog.Scopes = []AccountManagedDataScope{item}
			return catalog, nil
		}
	}
	return AccountManagedDataCatalog{}, fmt.Errorf("account module scope is unavailable")
}

func (p *Manager) accountProvider(id string) AccountManagedDataProvider {
	p.managedDataMu.RLock()
	defer p.managedDataMu.RUnlock()
	return p.managedDataProviders[id]
}

func (p *Manager) exportAccountPayload(entry account.ManagedEntry, catalog AccountManagedDataCatalog) ([]byte, error) {
	if entry.Provider == "wallets" && entry.Scope == AccountManagedDataGlobalScope {
		p.mutex.RLock()
		snapshot, infos, err := p.captureAccountManagementSyncSnapshotBaseLocked()
		p.mutex.RUnlock()
		if err != nil || snapshot == nil {
			return nil, err
		}
		defer zeroBytes(snapshot.secret)
		state, err := p.buildInitialManagedStateFromInfosLocked(snapshot.password, snapshot.profile.RootFingerprint, infos)
		if err != nil {
			return nil, err
		}
		for _, mutation := range snapshot.pending {
			if mutation.Type == accountMutationDeleteWallet && findManagedWallet(&state, mutation.Fingerprint) == nil {
				state.Wallets = append(state.Wallets, account.ManagedWallet{Fingerprint: mutation.Fingerprint, Deleted: true})
			}
		}
		p.mutex.RLock()
		previous, err := p.readAccountLocal(snapshot.profile.AccountID, "wallets", AccountManagedDataGlobalScope)
		p.mutex.RUnlock()
		if err != nil {
			return nil, err
		}
		if previous != nil && len(previous.Value) != 0 {
			retained, err := account.OpenManagedState(snapshot.secret, snapshot.profile.AccountID, previous.Value)
			if err != nil {
				return nil, err
			}
			for _, item := range retained.Wallets {
				if item.Deleted && findManagedWallet(&state, item.Fingerprint) == nil {
					state.Wallets = append(state.Wallets, item)
				}
			}
		}
		return account.EncodeManagedState(state)
	}
	provider := p.accountProvider(entry.Provider)
	if provider == nil {
		return nil, fmt.Errorf("account provider is unavailable")
	}
	selected, err := accountScopeCatalog(catalog, entry.Scope)
	if err != nil {
		return nil, err
	}
	payloads, err := provider.Export(selected)
	if err != nil {
		return nil, err
	}
	if err := provider.Validate(selected, payloads); err != nil {
		return nil, err
	}
	for _, payload := range payloads {
		if payload.Scope == entry.Scope {
			return bytes.Clone(payload.Payload), nil
		}
	}
	return nil, fmt.Errorf("account provider omitted its registered scope")
}

func openAccountPayload(secret []byte, accountID, network string, entry account.ManagedEntry, encoded []byte) ([]byte, error) {
	if entry.Provider == "wallets" && entry.Scope == AccountManagedDataGlobalScope {
		state, err := account.OpenManagedState(secret, accountID, encoded)
		if err != nil {
			return nil, err
		}
		return account.EncodeManagedState(state)
	}
	blob, err := DecodeDKVSBlobValue(encoded)
	if err != nil {
		return nil, err
	}
	if len(blob.Metadata) != 0 {
		return nil, fmt.Errorf("account module metadata must be empty")
	}
	module, err := account.OpenManagedModule(secret, accountID, network, entry.Key, blob.Data)
	if err != nil {
		return nil, err
	}
	if module.Provider != entry.Provider || module.Scope != entry.Scope || module.Codec != entry.Codec {
		return nil, fmt.Errorf("account module does not match its index entry")
	}
	return module.Payload, nil
}

func sealAccountPayload(secret []byte, accountID, network string, entry account.ManagedEntry, payload []byte) ([]byte, error) {
	if entry.Provider == "wallets" && entry.Scope == AccountManagedDataGlobalScope {
		state, err := account.DecodeManagedState(payload)
		if err != nil {
			return nil, err
		}
		return account.SealManagedState(secret, accountID, state, nil)
	}
	encoded, err := account.SealManagedModule(secret, accountID, network, account.ManagedModule{
		Provider: entry.Provider, Scope: entry.Scope, Codec: entry.Codec, Payload: payload,
	}, nil)
	if err != nil {
		return nil, err
	}
	return EncodeDKVSBlobValue(encoded, nil)
}

func (p *Manager) validateAccountPayload(entry account.ManagedEntry, catalog AccountManagedDataCatalog, payload []byte) error {
	if entry.Provider == "wallets" {
		state, err := account.DecodeManagedState(payload)
		if err != nil {
			return err
		}
		return validateAccountWalletState(state)
	}
	provider := p.accountProvider(entry.Provider)
	if provider == nil {
		return fmt.Errorf("account provider is unavailable")
	}
	selected, err := accountScopeCatalog(catalog, entry.Scope)
	if err != nil {
		return err
	}
	return provider.Validate(selected, []AccountManagedDataPayload{{Scope: entry.Scope, Payload: payload}})
}

func validateAccountWalletState(state account.ManagedState) error {
	if err := account.ValidateManagedState(state); err != nil {
		return err
	}
	for _, item := range state.Wallets {
		if item.Deleted {
			continue
		}
		wallet, _, local, err := accountWalletMaterial(item.Type, item.Secret)
		zeroBytes(local)
		if err != nil || wallet == nil || walletFingerprint(wallet) != item.Fingerprint {
			return account.ErrInvalidBackup
		}
	}
	return nil
}

func (p *Manager) importAccountPayload(entry account.ManagedEntry, catalog AccountManagedDataCatalog, payload []byte) error {
	if err := p.validateAccountPayload(entry, catalog, payload); err != nil {
		return err
	}
	if entry.Provider == "wallets" {
		state, err := account.DecodeManagedState(payload)
		if err != nil {
			return err
		}
		return p.importAccountWallets(state)
	}
	if entry.Provider == rgb11AccountManagedProviderID {
		if err := p.verifyRGB11PublicRecoveryReferences(account.ManagedDataBundle{Items: []account.ManagedDataItem{{Provider: entry.Provider, Scope: entry.Scope, Payload: payload}}}); err != nil {
			return err
		}
	}
	selected, err := accountScopeCatalog(catalog, entry.Scope)
	if err != nil {
		return err
	}
	return p.accountProvider(entry.Provider).Import(selected, []AccountManagedDataPayload{{Scope: entry.Scope, Payload: payload}})
}
