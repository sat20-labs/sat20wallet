package wallet

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/sat20-labs/sat20wallet/sdk/common"
)

const accountManagedActiveEnvelopeVersion = uint32(1)

type accountManagedActiveEnvelope struct {
	Version     uint32
	Provider    string
	Scope       string
	ContentHash string
	Payload     []byte
}

const accountManagedActiveMagic = "AMA1"

func accountManagedActiveApplicationID(provider, scope, contentHash string) string {
	scopeHash := sha256.Sum256([]byte(scope))
	return "ama-" + provider + "-" + hex.EncodeToString(scopeHash[:8]) + "-" + contentHash[:24]
}

func encodeAccountManagedActiveEnvelope(provider string, payload AccountManagedDataPayload) ([]byte, string, error) {
	provider = strings.TrimSpace(provider)
	scope := strings.TrimSpace(payload.Scope)
	if !validAccountManagedProviderID(provider) || scope == "" || len(payload.Payload) == 0 {
		return nil, "", fmt.Errorf("invalid account-managed active payload")
	}
	digest := sha256.Sum256(payload.Payload)
	contentHash := hex.EncodeToString(digest[:])
	if len(provider) > 255 || len(scope) > 65535 {
		return nil, "", fmt.Errorf("account-managed active envelope field is too large")
	}
	var encoded bytes.Buffer
	encoded.WriteString(accountManagedActiveMagic)
	encoded.WriteByte(byte(len(provider)))
	encoded.WriteString(provider)
	if err := binary.Write(&encoded, binary.BigEndian, uint16(len(scope))); err != nil {
		return nil, "", err
	}
	encoded.WriteString(scope)
	encoded.Write(digest[:])
	encoded.Write(payload.Payload)
	return encoded.Bytes(), contentHash, nil
}

func decodeAccountManagedActiveEnvelope(encoded []byte) (*accountManagedActiveEnvelope, error) {
	reader := bytes.NewReader(encoded)
	magic := make([]byte, len(accountManagedActiveMagic))
	if _, err := reader.Read(magic); err != nil || string(magic) != accountManagedActiveMagic {
		return nil, fmt.Errorf("invalid account-managed active envelope")
	}
	providerSize, err := reader.ReadByte()
	if err != nil || providerSize == 0 {
		return nil, fmt.Errorf("invalid account-managed active envelope")
	}
	provider := make([]byte, int(providerSize))
	if _, err := reader.Read(provider); err != nil {
		return nil, fmt.Errorf("invalid account-managed active envelope")
	}
	var scopeSize uint16
	if binary.Read(reader, binary.BigEndian, &scopeSize) != nil || scopeSize == 0 {
		return nil, fmt.Errorf("invalid account-managed active envelope")
	}
	scope := make([]byte, int(scopeSize))
	hash := make([]byte, sha256.Size)
	if _, err := reader.Read(scope); err != nil {
		return nil, fmt.Errorf("invalid account-managed active envelope")
	}
	if _, err := reader.Read(hash); err != nil || reader.Len() == 0 {
		return nil, fmt.Errorf("invalid account-managed active envelope")
	}
	payload := make([]byte, reader.Len())
	if _, err := reader.Read(payload); err != nil ||
		!validAccountManagedProviderID(string(provider)) || strings.TrimSpace(string(scope)) == "" {
		return nil, fmt.Errorf("invalid account-managed active envelope")
	}
	digest := sha256.Sum256(payload)
	if !bytes.Equal(hash, digest[:]) {
		return nil, fmt.Errorf("account-managed active payload hash mismatch")
	}
	return &accountManagedActiveEnvelope{
		Version: accountManagedActiveEnvelopeVersion, Provider: string(provider),
		Scope: string(scope), ContentHash: hex.EncodeToString(hash), Payload: payload,
	}, nil
}

func (p *Manager) accountManagedActiveRoot() (common.Wallet, AccountManagedDataCatalog, bool, error) {
	if p == nil {
		return nil, AccountManagedDataCatalog{}, false, nil
	}
	p.mutex.RLock()
	configured := p.accountProfile != nil && p.accountProfile.RecoveryConfigured
	p.mutex.RUnlock()
	if !configured {
		return nil, AccountManagedDataCatalog{}, false, nil
	}
	root, err := p.accountManagementRootWallet()
	if err != nil {
		return nil, AccountManagedDataCatalog{}, false, err
	}
	catalog, err := p.accountManagedDataCatalog()
	if err != nil {
		return nil, AccountManagedDataCatalog{}, false, err
	}
	return root, catalog, true, nil
}

// syncAccountManagedActiveData is the acceptance barrier used before an RGB
// broadcast or receiver ACK. Returning nil means either account recovery is not
// configured or every active payload has been accepted by the bound CoreNode.
func (p *Manager) syncAccountManagedActiveData(providerID string) error {
	return p.syncAccountManagedActiveDataMode(providerID, false)
}

func (p *Manager) scheduleAccountManagedActiveDataSync(providerID string) {
	if p == nil || p.accountManagedActiveDataProvider(providerID) == nil {
		return
	}
	p.managedActiveStateMu.Lock()
	if p.managedActiveGen == nil {
		p.managedActiveGen = make(map[string]uint64)
		p.managedActiveRunning = make(map[string]bool)
	}
	p.managedActiveGen[providerID]++
	if p.managedActiveRunning[providerID] {
		p.managedActiveStateMu.Unlock()
		return
	}
	p.managedActiveRunning[providerID] = true
	p.managedActiveStateMu.Unlock()
	go func() {
		for {
			p.managedActiveStateMu.Lock()
			generation := p.managedActiveGen[providerID]
			p.managedActiveStateMu.Unlock()
			if err := p.syncAccountManagedActiveData(providerID); err != nil {
				Log.Warningf("sync %s active account data failed: %v", providerID, err)
				p.managedActiveStateMu.Lock()
				p.managedActiveRunning[providerID] = false
				p.managedActiveStateMu.Unlock()
				return
			}
			p.managedActiveStateMu.Lock()
			if p.managedActiveGen[providerID] == generation {
				p.managedActiveRunning[providerID] = false
				p.managedActiveStateMu.Unlock()
				return
			}
			p.managedActiveStateMu.Unlock()
		}
	}()
}

func (p *Manager) syncAccountManagedActiveDataMode(providerID string, pruneAbsent bool) error {
	provider := p.accountManagedActiveDataProvider(providerID)
	if provider == nil {
		return nil
	}
	p.managedActiveSyncMu.Lock()
	defer p.managedActiveSyncMu.Unlock()
	root, catalog, configured, err := p.accountManagedActiveRoot()
	if err != nil || !configured {
		return err
	}
	payloads, err := provider.ExportActive(catalog)
	if err != nil {
		return fmt.Errorf("export %s active account data: %w", providerID, err)
	}
	if err := provider.ValidateActive(catalog, payloads); err != nil {
		return fmt.Errorf("validate %s active account data: %w", providerID, err)
	}
	allowed := accountManagedScopeSet(catalog)
	keep := make(map[string]struct{}, len(payloads))
	for _, payload := range payloads {
		if _, ok := allowed[strings.TrimSpace(payload.Scope)]; !ok {
			return fmt.Errorf("provider %s exported invalid active scope %q", providerID, payload.Scope)
		}
		body, hash, err := encodeAccountManagedActiveEnvelope(providerID, payload)
		if err != nil {
			return err
		}
		applicationID := accountManagedActiveApplicationID(providerID, payload.Scope, hash)
		keep[applicationID] = struct{}{}
		if _, err := p.sendWalletDirectMessage(root, applicationID,
			AccountMessageKindManagedActive, catalog.AccountID, body); err != nil {
			return fmt.Errorf("persist %s active account data: %w", providerID, err)
		}
		if err := p.deleteSupersededAccountManagedActive(root, providerID, payload.Scope, applicationID); err != nil {
			return err
		}
	}
	if pruneAbsent {
		messages, err := p.readWalletDirectMessages(root)
		if err != nil {
			return err
		}
		for _, item := range messages {
			if item == nil || item.Payload == nil || item.Record == nil ||
				item.Payload.Kind != AccountMessageKindManagedActive {
				continue
			}
			envelope, err := decodeAccountManagedActiveEnvelope(item.Payload.Body)
			if err != nil || envelope.Provider != providerID {
				continue
			}
			if _, current := keep[item.Payload.ApplicationID]; current {
				continue
			}
			if err := p.DeleteMailboxMessage(root, item.Record.Key); err != nil &&
				!errors.Is(err, ErrDKVSRecordNotFound) {
				return err
			}
		}
	}
	return nil
}

func (p *Manager) deleteSupersededAccountManagedActive(root common.Wallet,
	providerID, scope, keepApplicationID string) error {
	messages, err := p.readWalletDirectMessages(root)
	if err != nil {
		return err
	}
	for _, item := range messages {
		if item == nil || item.Payload == nil || item.Record == nil ||
			item.Payload.Kind != AccountMessageKindManagedActive ||
			item.Payload.ApplicationID == keepApplicationID {
			continue
		}
		envelope, err := decodeAccountManagedActiveEnvelope(item.Payload.Body)
		if err != nil || envelope.Provider != providerID || envelope.Scope != scope {
			continue
		}
		if err := p.DeleteMailboxMessage(root, item.Record.Key); err != nil {
			return err
		}
	}
	return nil
}

func (p *Manager) importAccountManagedActiveData() error {
	root, catalog, configured, err := p.accountManagedActiveRoot()
	if err != nil || !configured {
		return err
	}
	messages, err := p.readWalletDirectMessages(root)
	if err != nil {
		return err
	}
	type candidate struct {
		messageID string
		payload   AccountManagedDataPayload
	}
	allowed := accountManagedScopeSet(catalog)
	staleKeys := make([]string, 0)
	latest := make(map[string]candidate)
	for _, item := range messages {
		if item == nil || item.Payload == nil || item.Direct == nil ||
			item.Payload.Kind != AccountMessageKindManagedActive ||
			item.Direct.SenderAccount != catalog.AccountID || item.Direct.RecipientAccount != catalog.AccountID {
			continue
		}
		envelope, err := decodeAccountManagedActiveEnvelope(item.Payload.Body)
		if err != nil {
			return err
		}
		if _, currentScope := allowed[strings.TrimSpace(envelope.Scope)]; !currentScope {
			if item.Record != nil && strings.TrimSpace(item.Record.Key) != "" {
				staleKeys = append(staleKeys, item.Record.Key)
			}
			continue
		}
		key := envelope.Provider + "\x00" + envelope.Scope
		if current, ok := latest[key]; ok && current.messageID >= item.Direct.MessageID {
			continue
		}
		latest[key] = candidate{messageID: item.Direct.MessageID,
			payload: AccountManagedDataPayload{Scope: envelope.Scope, Payload: append([]byte(nil), envelope.Payload...)}}
	}
	byProvider := make(map[string][]AccountManagedDataPayload)
	for key, item := range latest {
		providerID := strings.SplitN(key, "\x00", 2)[0]
		byProvider[providerID] = append(byProvider[providerID], item.payload)
	}
	for providerID, payloads := range byProvider {
		provider := p.accountManagedActiveDataProvider(providerID)
		if provider == nil {
			return fmt.Errorf("account-managed active provider %q is unavailable", providerID)
		}
		if err := provider.ValidateActive(catalog, payloads); err != nil {
			return fmt.Errorf("validate imported %s active account data: %w", providerID, err)
		}
		if err := provider.ImportActive(catalog, payloads); err != nil {
			return fmt.Errorf("import %s active account data: %w", providerID, err)
		}
	}
	// A deleted wallet/account removes its scope from the authoritative
	// catalog. Historical FREE_LOCAL mailbox entries for that scope are cache
	// debris, not recovery input. Ignore them synchronously and prune them after
	// the recovery barrier so they can never make a valid restore fail.
	p.pruneStaleAccountManagedActive(root, staleKeys)
	return nil
}

func (p *Manager) pruneStaleAccountManagedActive(root common.Wallet, keys []string) {
	if p == nil || root == nil || len(keys) == 0 {
		return
	}
	mailboxRoot := cloneWalletAtAccountZero(root)
	staleKeys := append([]string(nil), keys...)
	go func() {
		for _, key := range staleKeys {
			if err := p.DeleteMailboxMessage(mailboxRoot, key); err != nil &&
				!errors.Is(err, ErrDKVSRecordNotFound) {
				Log.Warningf("prune stale account-managed active message failed: %v", err)
			}
		}
	}()
}

func (p *Manager) cleanupAccountManagedActiveDataAfterDurable(providerID string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), accountManagedDataReadyTimeout)
		defer cancel()
		if err := p.WaitAccountManagedDataReady(ctx); err != nil {
			return
		}
		_ = p.syncAccountManagedActiveDataMode(providerID, true)
	}()
}
