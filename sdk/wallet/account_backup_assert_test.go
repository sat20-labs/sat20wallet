package wallet

import (
	"github.com/sat20-labs/sat20wallet/sdk/account"
	"testing"
)

// Read the authenticated per-module backup as a test oracle. Production
// operations use account management APIs and never read backup bytes.
func accountTestPayloads(p *Manager, providerID string) (map[string]AccountManagedDataPayload, error) {
	result := make(map[string]AccountManagedDataPayload)
	if p == nil {
		return result, nil
	}
	p.mutex.RLock()
	if p.accountProfile == nil || len(p.accountSecret) != 32 {
		p.mutex.RUnlock()
		return result, nil
	}
	accountID, network := p.accountProfile.AccountID, p.cfg.Chain
	secret := append([]byte(nil), p.accountSecret...)
	values, err := p.listAccountLocal(accountID)
	p.mutex.RUnlock()
	defer zeroBytes(secret)
	if err != nil {
		return nil, err
	}
	for _, value := range values {
		if value.Provider != providerID || value.Deleted || value.Restoring || value.Baseline == nil {
			continue
		}
		entry := account.ManagedEntry{Provider: value.Provider, Scope: value.Scope, Key: value.Key, Codec: value.Codec}
		payload, err := openAccountPayload(secret, accountID, network, entry, value.Baseline.Value)
		if err != nil {
			return nil, err
		}
		result[value.Scope] = AccountManagedDataPayload{Scope: value.Scope, Payload: payload}
	}
	return result, nil
}

func accountTestRow(t *testing.T, p *Manager, provider, scope string) *accountLocalKV {
	t.Helper()
	p.mutex.RLock()
	defer p.mutex.RUnlock()
	if p.accountProfile == nil {
		t.Fatal("missing account profile")
	}
	value, err := p.readAccountLocal(p.accountProfile.AccountID, provider, scope)
	if err != nil {
		t.Fatal(err)
	}
	if value == nil {
		return &accountLocalKV{}
	}
	return value
}

func walletTestRow(t *testing.T, p *Manager) *accountLocalKV {
	t.Helper()
	return accountTestRow(t, p, "wallets", AccountManagedDataGlobalScope)
}

// Fixture-only data writes notify the current account module interface.
func accountTestChange(p *Manager, provider, scope string) error {
	batch, err := NewAtomicWriteBatch(p.db)
	if err != nil {
		return err
	}
	defer batch.Close()
	finish, err := p.StageAccountChange(batch, provider, scope)
	if err != nil {
		return err
	}
	err = batch.Flush()
	finish(err == nil)
	return err
}
