package wallet

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	strict "github.com/sat20-labs/rgb11/strict_encoding"
	"github.com/sat20-labs/sat20wallet/sdk/account"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

const (
	accountRootWrapperPath          = "account/root"
	accountRootWrapperMagic         = "ARWR"
	accountRootWrapperPayloadMagic  = "ARWP"
	accountRootWrapperCodecVersion  = uint8(1)
	accountRootWrapperJobID         = "account-root-key-wrapper"
	accountRootWrapperMaxNetwork    = 64
	accountRootWrapperMaxAccountID  = 128
	accountRootWrapperMaxPackageID  = 128
	accountRootWrapperMaxMode       = 32
	accountRootWrapperMaxContract   = 256
	accountRootWrapperMaxCiphertext = account.MaxRecoveryObjectSize + 64

	RootAccountRecoveryCodeRecovered = "ACCOUNT_ROOT_RECOVERED"
	RootAccountRecoveryCodeNotFound  = "ACCOUNT_ROOT_NOT_FOUND"
	RootAccountRecoveryCodePending   = "ACCOUNT_ROOT_DISCOVERY_PENDING"
)

var (
	ErrRootAccountNotFound         = errors.New("managed account was not found for root wallet")
	ErrRootAccountDiscoveryPending = errors.New("managed account discovery is pending")
	ErrRootAccountWrapperInvalid   = errors.New("managed account root wrapper is invalid")
	ErrRootAccountNetworkMismatch  = errors.New("managed account root wrapper belongs to another network")
)

type accountRootWrapperEnvelope struct {
	Network    string
	AccountID  string
	Nonce      []byte
	Ciphertext []byte
}

type accountRootWrapperPayload struct {
	Secret           []byte
	PackageID        string
	StorageMode      string
	RecordTTL        uint64
	AutopayContract  string
	GuardianLocation *AccountIndexerLocation
}

type accountRootWrapperStore interface {
	WaitReady(keys ...string) error
	Get(key string) (*dkvsValue, error)
	Update(keys []string, builder dkvsUpdateBuilder) ([]*dkvsValue, error)
}

type accountRootWrapperAuthoritativeReader interface {
	GetAuthoritative(key string) (*dkvsValue, error)
}

func accountRootWrapperVerificationRead(store accountRootWrapperStore, key string) (*dkvsValue, error) {
	if _, managed := store.(*dkvsStore); managed {
		return store.Get(key)
	}
	if authoritative, ok := store.(accountRootWrapperAuthoritativeReader); ok {
		return authoritative.GetAuthoritative(key)
	}
	return store.Get(key)
}

func accountRootWrapperVerificationSnapshot(store accountRootWrapperStore, keys ...string) (map[string]*dkvsValue, error) {
	if managed, ok := store.(*dkvsStore); ok {
		return managed.readSnapshot(keys...)
	}
	values := make(map[string]*dkvsValue, len(keys))
	for _, key := range keys {
		value, err := accountRootWrapperVerificationRead(store, key)
		if err != nil && !errors.Is(err, ErrDKVSRecordNotFound) {
			return nil, err
		}
		values[key] = value
	}
	return values, nil
}

func accountRootWrapperKey(root common.Wallet) (string, error) {
	if root == nil || root.GetPubKey() == nil {
		return "", fmt.Errorf("account management root wallet is unavailable")
	}
	return dkvsindexer.PersonalKey(root.GetPubKey().SerializeCompressed(), accountRootWrapperPath)
}

func accountRootWrapperAAD(network, accountID string) []byte {
	return []byte(accountRootWrapperDomain + "\x00" + network + "\x00" + accountID)
}

func encodeAccountRootWrapperEnvelope(value accountRootWrapperEnvelope) ([]byte, error) {
	var buf bytes.Buffer
	encoder := strict.NewEncoder(&buf)
	for _, encode := range []func() error{
		func() error { return encoder.Raw([]byte(accountRootWrapperMagic)) },
		func() error { return encoder.U8(accountRootWrapperCodecVersion) },
		func() error { return encoder.String(value.Network, 1, accountRootWrapperMaxNetwork) },
		func() error { return encoder.String(value.AccountID, 1, accountRootWrapperMaxAccountID) },
		func() error { return encoder.Bytes(value.Nonce, 1, 64) },
		func() error { return encoder.Bytes(value.Ciphertext, 1, accountRootWrapperMaxCiphertext) },
	} {
		if err := encode(); err != nil {
			return nil, ErrRootAccountWrapperInvalid
		}
	}
	return buf.Bytes(), nil
}

func decodeAccountRootWrapperEnvelope(encoded []byte) (accountRootWrapperEnvelope, error) {
	if len(encoded) == 0 || len(encoded) > accountRootWrapperMaxCiphertext+512 {
		return accountRootWrapperEnvelope{}, ErrRootAccountWrapperInvalid
	}
	reader := bytes.NewReader(encoded)
	decoder := strict.NewDecoder(reader)
	magic, err := decoder.Raw(uint64(len(accountRootWrapperMagic)))
	if err != nil || string(magic) != accountRootWrapperMagic {
		return accountRootWrapperEnvelope{}, ErrRootAccountWrapperInvalid
	}
	version, err := decoder.U8()
	if err != nil || version != accountRootWrapperCodecVersion {
		return accountRootWrapperEnvelope{}, ErrRootAccountWrapperInvalid
	}
	var value accountRootWrapperEnvelope
	if value.Network, err = decoder.String(1, accountRootWrapperMaxNetwork); err != nil {
		return accountRootWrapperEnvelope{}, ErrRootAccountWrapperInvalid
	}
	if value.AccountID, err = decoder.String(1, accountRootWrapperMaxAccountID); err != nil {
		return accountRootWrapperEnvelope{}, ErrRootAccountWrapperInvalid
	}
	if value.Nonce, err = decoder.Bytes(1, 64); err != nil {
		return accountRootWrapperEnvelope{}, ErrRootAccountWrapperInvalid
	}
	if value.Ciphertext, err = decoder.Bytes(1, accountRootWrapperMaxCiphertext); err != nil || reader.Len() != 0 {
		return accountRootWrapperEnvelope{}, ErrRootAccountWrapperInvalid
	}
	return value, nil
}

func encodeAccountRootWrapperPayload(value accountRootWrapperPayload) ([]byte, error) {
	if len(value.Secret) != 32 {
		return nil, ErrRootAccountWrapperInvalid
	}
	var buf bytes.Buffer
	encoder := strict.NewEncoder(&buf)
	for _, encode := range []func() error{
		func() error { return encoder.Raw([]byte(accountRootWrapperPayloadMagic)) },
		func() error { return encoder.U8(accountRootWrapperCodecVersion) },
		func() error { return encoder.Raw(value.Secret) },
		func() error { return encoder.String(value.PackageID, 0, accountRootWrapperMaxPackageID) },
		func() error { return encoder.String(value.StorageMode, 0, accountRootWrapperMaxMode) },
		func() error { return encoder.U64(value.RecordTTL) },
		func() error { return encoder.String(value.AutopayContract, 0, accountRootWrapperMaxContract) },
	} {
		if err := encode(); err != nil {
			return nil, ErrRootAccountWrapperInvalid
		}
	}
	if value.GuardianLocation == nil {
		if err := encoder.U8(0); err != nil {
			return nil, ErrRootAccountWrapperInvalid
		}
	} else {
		for _, encode := range []func() error{
			func() error { return encoder.U8(1) },
			func() error { return encoder.String(value.GuardianLocation.Scheme, 0, 16) },
			func() error { return encoder.String(value.GuardianLocation.Host, 1, 1024) },
			func() error { return encoder.String(value.GuardianLocation.Proxy, 0, 4096) },
		} {
			if err := encode(); err != nil {
				return nil, ErrRootAccountWrapperInvalid
			}
		}
	}
	if buf.Len() > account.MaxRecoveryObjectSize {
		return nil, ErrRootAccountWrapperInvalid
	}
	return buf.Bytes(), nil
}

func decodeAccountRootWrapperPayload(encoded []byte) (accountRootWrapperPayload, error) {
	if len(encoded) == 0 || len(encoded) > account.MaxRecoveryObjectSize {
		return accountRootWrapperPayload{}, ErrRootAccountWrapperInvalid
	}
	reader := bytes.NewReader(encoded)
	decoder := strict.NewDecoder(reader)
	magic, err := decoder.Raw(uint64(len(accountRootWrapperPayloadMagic)))
	if err != nil || string(magic) != accountRootWrapperPayloadMagic {
		return accountRootWrapperPayload{}, ErrRootAccountWrapperInvalid
	}
	version, err := decoder.U8()
	if err != nil || version != accountRootWrapperCodecVersion {
		return accountRootWrapperPayload{}, ErrRootAccountWrapperInvalid
	}
	secret, err := decoder.Raw(32)
	if err != nil {
		return accountRootWrapperPayload{}, ErrRootAccountWrapperInvalid
	}
	defer zeroBytes(secret)
	var value accountRootWrapperPayload
	if value.PackageID, err = decoder.String(0, accountRootWrapperMaxPackageID); err != nil {
		return accountRootWrapperPayload{}, ErrRootAccountWrapperInvalid
	}
	if value.StorageMode, err = decoder.String(0, accountRootWrapperMaxMode); err != nil {
		return accountRootWrapperPayload{}, ErrRootAccountWrapperInvalid
	}
	if value.RecordTTL, err = decoder.U64(); err != nil {
		return accountRootWrapperPayload{}, ErrRootAccountWrapperInvalid
	}
	if value.AutopayContract, err = decoder.String(0, accountRootWrapperMaxContract); err != nil {
		return accountRootWrapperPayload{}, ErrRootAccountWrapperInvalid
	}
	guardian, err := decoder.U8()
	if err != nil || guardian > 1 {
		return accountRootWrapperPayload{}, ErrRootAccountWrapperInvalid
	}
	if guardian == 1 {
		location := &AccountIndexerLocation{}
		if location.Scheme, err = decoder.String(0, 16); err != nil {
			return accountRootWrapperPayload{}, ErrRootAccountWrapperInvalid
		}
		if location.Host, err = decoder.String(1, 1024); err != nil {
			return accountRootWrapperPayload{}, ErrRootAccountWrapperInvalid
		}
		if location.Proxy, err = decoder.String(0, 4096); err != nil {
			return accountRootWrapperPayload{}, ErrRootAccountWrapperInvalid
		}
		value.GuardianLocation = location
	}
	if reader.Len() != 0 {
		return accountRootWrapperPayload{}, ErrRootAccountWrapperInvalid
	}
	value.Secret = append([]byte(nil), secret...)
	return value, nil
}

func sealAccountRootWrapper(root common.Wallet, network, accountID string,
	payload accountRootWrapperPayload, random io.Reader) ([]byte, error) {

	if len(payload.Secret) != 32 || strings.TrimSpace(network) == "" || strings.TrimSpace(accountID) == "" {
		return nil, ErrRootAccountWrapperInvalid
	}
	key, err := deriveAccountRootWrapperKey(root, network, accountID)
	if err != nil {
		return nil, err
	}
	defer zeroBytes(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if random == nil {
		random = rand.Reader
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(random, nonce); err != nil {
		return nil, err
	}
	plain, err := encodeAccountRootWrapperPayload(payload)
	if err != nil {
		return nil, err
	}
	defer zeroBytes(plain)
	envelope := accountRootWrapperEnvelope{
		Network: network, AccountID: accountID,
		Nonce: nonce, Ciphertext: aead.Seal(nil, nonce, plain, accountRootWrapperAAD(network, accountID)),
	}
	return encodeAccountRootWrapperEnvelope(envelope)
}

func openAccountRootWrapper(root common.Wallet, network, accountID string,
	encoded []byte) (accountRootWrapperPayload, error) {

	envelope, err := decodeAccountRootWrapperEnvelope(encoded)
	if err != nil || envelope.AccountID != accountID {
		return accountRootWrapperPayload{}, ErrRootAccountWrapperInvalid
	}
	if envelope.Network != network {
		return accountRootWrapperPayload{}, ErrRootAccountNetworkMismatch
	}
	key, err := deriveAccountRootWrapperKey(root, network, accountID)
	if err != nil {
		return accountRootWrapperPayload{}, err
	}
	defer zeroBytes(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return accountRootWrapperPayload{}, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil || len(envelope.Nonce) != aead.NonceSize() {
		return accountRootWrapperPayload{}, ErrRootAccountWrapperInvalid
	}
	plain, err := aead.Open(nil, envelope.Nonce, envelope.Ciphertext,
		accountRootWrapperAAD(network, accountID))
	if err != nil {
		return accountRootWrapperPayload{}, ErrRootAccountWrapperInvalid
	}
	defer zeroBytes(plain)
	payload, err := decodeAccountRootWrapperPayload(plain)
	if err != nil {
		return accountRootWrapperPayload{}, ErrRootAccountWrapperInvalid
	}
	return payload, nil
}

func rootWrapperPayload(profile accountManagementProfile, secret []byte) accountRootWrapperPayload {
	return accountRootWrapperPayload{Secret: append([]byte(nil), secret...), PackageID: profile.PackageID,
		StorageMode: profile.StorageMode, RecordTTL: profile.RecordTTL, AutopayContract: profile.AutopayContract}
}

func accountRootWrapperMetadataMatchesProfile(value accountRootWrapperPayload, profile accountManagementProfile) bool {
	return value.PackageID == profile.PackageID && value.StorageMode == profile.StorageMode &&
		value.RecordTTL == profile.RecordTTL && value.AutopayContract == profile.AutopayContract
}

func accountAutopayPoolFromValue(value *dkvsValue) (string, bool, error) {
	if value == nil || value.record == nil {
		return "", false, nil
	}
	proof, err := dkvsindexer.ParseFeeProof(value.record.FeeProof)
	if err != nil {
		return "", false, err
	}
	if proof.Mode != dkvsindexer.FeeModeAutopay {
		return "", false, nil
	}
	pool := strings.TrimSpace(proof.PoolContract)
	if pool == "" {
		return "", false, dkvsindexer.ErrInvalidRecord
	}
	return pool, true, nil
}

// Ordinary sync adopts the authenticated current configuration. Explicit
// activation alone replaces it; storage may upgrade to paid but never downgrade.
func (p *Manager) adoptRemoteAccountConfiguration(store accountRootWrapperStore,
	root common.Wallet, snapshot *accountManagementSyncSnapshot,
	stateValue, dataValue, wrapperValue *dkvsValue, applicationLocked bool) (bool, error) {
	if p == nil || store == nil || root == nil || snapshot == nil {
		return false, nil
	}
	if wrapperValue == nil {
		// Initial/expired temporary wrapper can be republished from verified local
		// state. A paid state without its current wrapper is incomplete.
		_, paid, proofErr := accountAutopayPoolFromValue(stateValue)
		if proofErr != nil {
			return false, proofErr
		}
		if paid {
			return false, ErrAccountStorageModeDowngrade
		}
		return false, nil
	}
	payload, err := openAccountRootWrapper(root, snapshot.network, snapshot.profile.AccountID, wrapperValue.Value)
	if err != nil {
		return false, err
	}
	defer zeroBytes(payload.Secret)
	if payload.PackageID == "" {
		return false, ErrRootAccountWrapperInvalid
	}
	if !bytes.Equal(payload.Secret, snapshot.secret) {
		return false, ErrRootAccountWrapperInvalid
	}
	if snapshot.profile.StorageMode == AccountStoragePaid && payload.StorageMode != AccountStoragePaid {
		return false, ErrAccountStorageModeDowngrade
	}
	candidate := snapshot.profile
	candidate.PackageID = payload.PackageID
	candidate.StorageMode = payload.StorageMode
	candidate.RecordTTL = payload.RecordTTL
	candidate.AutopayContract = payload.AutopayContract
	candidate.RecoveryConfigured = true
	if !accountRecordMatchesStorage(wrapperValue, &candidate) ||
		!accountRecordMatchesStorage(stateValue, &candidate) || !accountRecordMatchesStorage(dataValue, &candidate) {
		return false, ErrAccountStorageModeDowngrade
	}
	if accountRootWrapperMetadataMatchesProfile(payload, snapshot.profile) {
		return false, nil
	}
	adopted := false
	err = p.withAccountLocalState(applicationLocked, func() error {
		p.mutex.Lock()
		defer p.mutex.Unlock()
		if !p.accountSyncSnapshotCurrentLocked(snapshot) {
			return errAccountSnapshotChanged
		}
		if accountRootWrapperMetadataMatchesProfile(payload, *p.accountProfile) {
			return nil
		}
		updated := *p.accountProfile
		updated.PackageID = candidate.PackageID
		updated.RecoveryMode = candidate.RecoveryMode
		updated.StorageMode = candidate.StorageMode
		updated.RecordTTL = candidate.RecordTTL
		updated.AutopayContract = candidate.AutopayContract
		updated.PublicLocator = candidate.PublicLocator
		updated.RecoveryConfigured = candidate.RecoveryConfigured
		encoded, err := EncodeToBytes(&updated)
		if err != nil {
			return err
		}
		if err := p.db.Write(accountManagementProfileKey(), encoded); err != nil {
			return err
		}
		p.accountProfile = &updated
		p.bumpAccountGenerationLocked()
		adopted = true
		return nil
	})
	return adopted, err
}

func accountRootWrapperMutation(profile *accountManagementProfile, root common.Wallet,
	key string, value []byte) (dkvsValueMutation, error) {

	return accountStateMutation(profile, root, key, value)
}

func validateAccountRootWrapperSecret(root common.Wallet, accountID string,
	secret []byte, current *dkvsValue) error {

	if current == nil || dkvsindexer.IsTombstone(current.Flags) {
		return nil
	}
	existing, err := openAccountRootWrapper(root, _chain, accountID, current.Value)
	if err != nil {
		return err
	}
	defer zeroBytes(existing.Secret)
	if !bytes.Equal(existing.Secret, secret) {
		return fmt.Errorf("%w: remote wrapper contains another account secret",
			ErrRootAccountWrapperInvalid)
	}
	return nil
}

func verifyAccountRootWrapperStorage(store accountRootWrapperStore,
	profile *accountManagementProfile, root common.Wallet, secret []byte, key string) error {

	if err := store.WaitReady(key); err != nil {
		return err
	}
	// Managed write success includes verified local installation. Lightweight
	// test stores retain their explicit verification adapter.
	current, err := accountRootWrapperVerificationRead(store, key)
	if err != nil {
		return err
	}
	if err := validateAccountRootWrapperSecret(root, profile.AccountID, secret, current); err != nil {
		return err
	}
	payload, err := openAccountRootWrapper(root, _chain, profile.AccountID, current.Value)
	if err != nil {
		return err
	}
	defer zeroBytes(payload.Secret)
	if !accountRootWrapperMetadataMatchesProfile(payload, *profile) ||
		!accountRecordMatchesStorage(current, profile) {
		return fmt.Errorf("account root wrapper remote verification failed")
	}
	return nil
}

func (p *Manager) accountManagementVerifiedSnapshot(store accountRootWrapperStore) (
	accountManagementProfile, []byte, common.Wallet, uint64, error) {
	p.mutex.Lock()
	snapshot, _, err := p.captureAccountManagementSyncSnapshotBaseLocked()
	p.mutex.Unlock()
	if err != nil {
		return accountManagementProfile{}, nil, nil, 0, err
	}
	if snapshot == nil || !snapshot.profile.RecoveryConfigured {
		return accountManagementProfile{}, nil, nil, 0, ErrDKVSPathNotSynced
	}
	key, err := accountRootWrapperKey(snapshot.root)
	if err == nil {
		err = store.WaitReady(key)
	}
	if err != nil {
		zeroBytes(snapshot.secret)
		return accountManagementProfile{}, nil, nil, 0, err
	}
	p.mutex.RLock()
	current := p.accountSyncSnapshotCurrentLocked(snapshot)
	p.mutex.RUnlock()
	if !current {
		zeroBytes(snapshot.secret)
		return accountManagementProfile{}, nil, nil, 0, errAccountSnapshotChanged
	}
	return snapshot.profile, snapshot.secret, snapshot.root, snapshot.generation, nil
}

func (p *Manager) syncAccountRootWrapper(store accountRootWrapperStore) error {
	if p == nil {
		return ErrDKVSPathNotSynced
	}
	return p.runAccountOperation(nil, func() error {
		return p.syncAccountRootWrapperAttempt(store, 0)
	})
}

// syncAccountRootWrapperAttempt shares the account operation gate with paid activation and
// managed-state synchronization. Existing current metadata is authoritative;
// only an absent wrapper can be published by this maintenance job.
func (p *Manager) syncAccountRootWrapperAttempt(store accountRootWrapperStore, attempt int) error {
	profile, secret, root, generation, err := p.accountManagementVerifiedSnapshot(store)
	if err != nil {
		if errors.Is(err, errAccountSnapshotChanged) && attempt < 2 {
			return p.syncAccountRootWrapperAttempt(store, attempt+1)
		}
		return err
	}
	defer zeroBytes(secret)
	snapshot := &accountManagementSyncSnapshot{profile: profile, secret: secret,
		root: root, generation: generation, network: _chain}
	key, err := accountRootWrapperKey(root)
	if err != nil {
		return err
	}
	if managed, ok := store.(*dkvsStore); ok {
		if err := managed.SyncCurrent(key); err != nil {
			return err
		}
	}
	current, err := accountRootWrapperVerificationRead(store, key)
	if err != nil && !errors.Is(err, ErrDKVSRecordNotFound) {
		return err
	}
	p.mutex.RLock()
	valid := p.accountSyncSnapshotCurrentLocked(snapshot)
	p.mutex.RUnlock()
	if !valid {
		if attempt < 2 {
			return p.syncAccountRootWrapperAttempt(store, attempt+1)
		}
		return errAccountSnapshotChanged
	}
	if current != nil {
		payload, err := openAccountRootWrapper(root, _chain, profile.AccountID, current.Value)
		if err != nil {
			return err
		}
		defer zeroBytes(payload.Secret)
		if !bytes.Equal(payload.Secret, secret) {
			return ErrRootAccountWrapperInvalid
		}
		if !accountRootWrapperMetadataMatchesProfile(payload, profile) {
			managed, ok := store.(*dkvsStore)
			if !ok {
				return dkvsindexer.ErrWriteConflict
			}
			return p.syncAccountRecovery(managed, snapshot, payload)
		}
		if err := p.saveAccountKV(profile.AccountID, key, current.Value); err != nil {
			return err
		}
		if current.record != nil {
			p.mutex.RLock()
			local, err := p.readAccountLocal(profile.AccountID, "account", key)
			p.mutex.RUnlock()
			if err != nil {
				return err
			}
			return p.recordAccountEffect(local, current)
		}
		return nil
	}
	p.mutex.RLock()
	local, err := p.readAccountLocal(profile.AccountID, "account", key)
	p.mutex.RUnlock()
	if err != nil {
		return err
	}
	if local == nil || len(local.Value) == 0 {
		return ErrRootAccountDiscoveryPending
	}
	payload, err := openAccountRootWrapper(root, _chain, profile.AccountID, local.Value)
	if err != nil {
		return err
	}
	defer zeroBytes(payload.Secret)
	if !bytes.Equal(payload.Secret, secret) || !accountRootWrapperMetadataMatchesProfile(payload, profile) {
		return ErrRootAccountWrapperInvalid
	}
	if managed, ok := store.(*dkvsStore); ok {
		local, err = p.setAccountBaseline(local, nil)
		if err != nil {
			return err
		}
		return p.publishAccountValue(managed, local, root, profile)
	}
	return fmt.Errorf("managed wrapper repair requires a verified account source")
}

// SyncAccountRootWrapper upgrades an unlocked profile by wrapping its
// existing AccountSecret. It never creates or replaces an AccountSecret and it
// refuses to write until the current state/blob are verified remotely.
func (p *Manager) SyncAccountRootWrapper(ctx context.Context) error {
	if ctx != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
	if !p.accountManagedRecoveryConfigured() {
		return ErrDKVSPathNotSynced
	}
	store, err := p.accountDKVSStore()
	if err != nil {
		return err
	}
	return p.syncAccountRootWrapper(store)
}

func (p *Manager) scheduleAccountRootWrapperSync() {
	if p == nil || p.dkvs == nil {
		return
	}
	p.dkvs.schedule(accountRootWrapperJobID, func(store *dkvsStore) error {
		p.mutex.RLock()
		configured := p.accountProfile != nil && p.accountProfile.RecoveryConfigured
		p.mutex.RUnlock()
		if !configured {
			return nil
		}
		err := p.syncAccountRootWrapper(store)
		if errors.Is(err, ErrDKVSPathNotSynced) {
			return fmt.Errorf("%w: %v", errDKVSJobDeferred, err)
		}
		return err
	})
}

func rootDiscoveryError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrDKVSPathNotSynced) || errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w: %v", ErrRootAccountDiscoveryPending, err)
	}
	if errors.Is(err, ErrDKVSRecordNotFound) {
		return ErrRootAccountNotFound
	}
	var transportErr net.Error
	if errors.As(err, &transportErr) {
		return fmt.Errorf("%w: %v", ErrRootAccountDiscoveryPending, err)
	}
	var responseErr *HTTPResponseError
	if errors.As(err, &responseErr) && (responseErr.StatusCode == http.StatusNotFound || responseErr.StatusCode >= 500) {
		// A transport-level 404 here means the endpoint does not expose the
		// metadata route used for root discovery.  It is not evidence that the
		// deterministic root record itself is absent.
		return fmt.Errorf("%w: %v", ErrRootAccountDiscoveryPending, err)
	}
	return err
}

// RecoverAccountManagementFromRootMnemonic discovers the deterministic root
// wrapper, unwraps the original AccountSecret and then reuses the normal
// state/blob restore path. A non-root mnemonic simply returns not-found.
func (p *Manager) RecoverAccountManagementFromRootMnemonic(ctx context.Context,
	mnemonic, password string) ([]RestoredWalletResult, error) {

	if p == nil {
		return nil, fmt.Errorf("wallet manager is unavailable")
	}
	var results []RestoredWalletResult
	err := p.runAccountApplicationSync(ctx, func() error {
		store, storeErr := p.accountDKVSStore()
		if storeErr != nil {
			return rootDiscoveryError(storeErr)
		}
		location, locationErr := p.AccountIndexerLocation()
		if locationErr != nil {
			return locationErr
		}
		var recoveryErr error
		results, recoveryErr = p.recoverAccountManagementFromRootMnemonic(
			ctx, mnemonic, password, store, location)
		return recoveryErr
	})
	if err != nil {
		return results, err
	}
	p.finishAccountManagementRestorePostSync()
	return results, nil
}

func (p *Manager) accountManagementRootMnemonic(password string) (string, error) {
	if p == nil {
		return "", fmt.Errorf("wallet manager is unavailable")
	}
	p.mutex.Lock()
	defer p.mutex.Unlock()

	var info *WalletInfo
	var err error
	if p.accountProfile != nil {
		info, err = p.accountManagementRootWalletLocked()
	} else {
		info, err = p.accountManagementCandidateRootLocked()
	}
	if err != nil || info == nil {
		if err == nil {
			err = ErrAccountManagementWalletUnavailable
		}
		return "", err
	}
	return p.loadWalletSecret(info, password)
}

// RecoverAccountManagementFromCurrentWallet retries root discovery after a
// network switch without exposing the decrypted mnemonic to the application.
// "CurrentWallet" is retained in the API name for compatibility; discovery is
// always anchored to the account-management root wallet/account 0 and never to
// the UI-selected wallet or subaccount.
func (p *Manager) RecoverAccountManagementFromCurrentWallet(ctx context.Context,
	password string) ([]RestoredWalletResult, error) {

	if p == nil {
		return nil, fmt.Errorf("wallet manager is unavailable")
	}
	var results []RestoredWalletResult
	err := p.runAccountApplicationSync(ctx, func() error {
		mnemonic, loadErr := p.accountManagementRootMnemonic(password)
		if loadErr != nil {
			return loadErr
		}
		store, storeErr := p.accountDKVSStore()
		if storeErr != nil {
			return rootDiscoveryError(storeErr)
		}
		location, locationErr := p.AccountIndexerLocation()
		if locationErr != nil {
			return locationErr
		}
		var recoveryErr error
		results, recoveryErr = p.recoverAccountManagementFromRootMnemonic(
			ctx, mnemonic, password, store, location)
		return recoveryErr
	})
	if err != nil {
		return results, err
	}
	p.finishAccountManagementRestorePostSync()
	return results, nil
}

func (p *Manager) recoverAccountManagementFromRootMnemonic(ctx context.Context,
	mnemonic, password string, reader accountRootWrapperStore,
	location AccountIndexerLocation) ([]RestoredWalletResult, error) {
	root := NewInternalWalletWithMnemonic(mnemonic, "", GetChainParam())
	if root == nil {
		return nil, fmt.Errorf("invalid account root wallet")
	}
	root.SetSubAccount(0)
	accountID, err := dkvsAccountID(root)
	if err != nil {
		return nil, err
	}
	p.mutex.RLock()
	mismatch := p.accountProfile != nil && p.accountProfile.RecoveryConfigured &&
		(p.accountProfile.AccountID != accountID || p.accountProfile.RootFingerprint != walletFingerprint(root))
	p.mutex.RUnlock()
	if mismatch {
		return nil, ErrRootAccountWrapperInvalid
	}
	key, err := accountRootWrapperKey(root)
	if err != nil {
		return nil, err
	}
	if p.dkvs != nil {
		p.dkvs.rememberPaths([]string{key})
		p.dkvs.wakeSync()
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, rootDiscoveryError(err)
		}
	}
	wrapper, err := reader.Get(key)
	if err != nil {
		return nil, rootDiscoveryError(err)
	}
	if wrapper == nil {
		return nil, ErrRootAccountNotFound
	}
	config, err := openAccountRootWrapper(root, _chain, accountID, wrapper.Value)
	if err != nil {
		return nil, err
	}
	defer zeroBytes(config.Secret)
	store, ok := reader.(*dkvsStore)
	if !ok {
		return nil, fmt.Errorf("managed root recovery requires a verified account source")
	}
	packageKey, err := dkvsindexer.AccountPersonalKey(accountID, accountPackagePath(config.PackageID))
	if err != nil {
		return nil, err
	}
	if err := store.WaitReady(packageKey); err != nil {
		return nil, rootDiscoveryError(err)
	}
	if err := store.SyncCurrent(packageKey); err != nil {
		return nil, rootDiscoveryError(err)
	}
	packageValue, err := store.Get(packageKey)
	if err != nil {
		return nil, rootDiscoveryError(err)
	}
	if packageValue == nil {
		return nil, ErrRootAccountDiscoveryPending
	}
	pkg, err := account.DecodeRecoveryPackageStorage(packageValue.Value)
	if err != nil || pkg.Envelope.Locator.AccountID != accountID {
		return nil, ErrRootAccountWrapperInvalid
	}
	existing, err := p.authenticateAccountRestore(accountID, walletFingerprint(root), config.Secret, password)
	if err != nil {
		if errors.Is(err, ErrAccountManagementSecretConflict) {
			return nil, ErrRootAccountWrapperInvalid
		}
		return nil, err
	}
	if existing {
		if err := p.syncAccountModules(ctx); err != nil && !accountModuleOnly(err) {
			return nil, err
		}
		return p.restoredCatalogResults(), nil
	}
	recovered, err := p.loadAccountCopies(store, root, pkg.Envelope.Locator, config.Secret)
	if err != nil {
		return nil, err
	}
	return p.restoreAccountCopies(*recovered, config.Secret, password, pkg.Envelope.Locator,
		AccountManagementRestoreOptions{Location: location, StorageMode: config.StorageMode,
			RecordTTL: config.RecordTTL, AutopayContract: config.AutopayContract}, walletFingerprint(root))
}
