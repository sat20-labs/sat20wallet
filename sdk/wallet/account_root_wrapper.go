package wallet

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/btcsuite/btcd/btcutil/hdkeychain"
	strict "github.com/sat20-labs/rgb11/strict_encoding"
	"github.com/sat20-labs/sat20wallet/sdk/account"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

const (
	accountRootWrapperPath          = "account/root-key-wrapper/current"
	accountRootWrapperMagic         = "ARWR"
	accountRootWrapperPayloadMagic  = "ARWP"
	accountRootWrapperCodecVersion  = uint8(1)
	accountRootWrapperPurpose       = uint32(1018)
	accountRootWrapperDomain        = "sat20/account/root-key-wrapper"
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
	Secret             []byte
	PackageID          string
	RecoveryMode       account.RecoveryMode
	StorageMode        string
	RecordTTL          uint64
	AutopayContract    string
	PublicLocator      string
	RecoveryConfigured bool
}

type accountRootWrapperStore interface {
	Refresh(keys ...string) error
	Get(key string) (*dkvsValue, error)
	Update(keys []string, builder dkvsUpdateBuilder) ([]*dkvsValue, error)
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
		func() error { return encoder.String(string(value.RecoveryMode), 0, accountRootWrapperMaxMode) },
		func() error { return encoder.String(value.StorageMode, 0, accountRootWrapperMaxMode) },
		func() error { return encoder.U64(value.RecordTTL) },
		func() error { return encoder.String(value.AutopayContract, 0, accountRootWrapperMaxContract) },
		func() error { return encoder.String(value.PublicLocator, 0, account.MaxRecoveryObjectSize) },
		func() error { return encoder.Bool(value.RecoveryConfigured) },
	} {
		if err := encode(); err != nil {
			return nil, ErrRootAccountWrapperInvalid
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
	recoveryMode, err := decoder.String(0, accountRootWrapperMaxMode)
	if err != nil {
		return accountRootWrapperPayload{}, ErrRootAccountWrapperInvalid
	}
	value.RecoveryMode = account.RecoveryMode(recoveryMode)
	if value.StorageMode, err = decoder.String(0, accountRootWrapperMaxMode); err != nil {
		return accountRootWrapperPayload{}, ErrRootAccountWrapperInvalid
	}
	if value.RecordTTL, err = decoder.U64(); err != nil {
		return accountRootWrapperPayload{}, ErrRootAccountWrapperInvalid
	}
	if value.AutopayContract, err = decoder.String(0, accountRootWrapperMaxContract); err != nil {
		return accountRootWrapperPayload{}, ErrRootAccountWrapperInvalid
	}
	if value.PublicLocator, err = decoder.String(0, account.MaxRecoveryObjectSize); err != nil {
		return accountRootWrapperPayload{}, ErrRootAccountWrapperInvalid
	}
	if value.RecoveryConfigured, err = decoder.Bool(); err != nil || reader.Len() != 0 {
		return accountRootWrapperPayload{}, ErrRootAccountWrapperInvalid
	}
	value.Secret = append([]byte(nil), secret...)
	return value, nil
}

func hkdfSHA256(ikm, salt, info []byte, size int) []byte {
	extract := hmac.New(sha256.New, salt)
	_, _ = extract.Write(ikm)
	prk := extract.Sum(nil)
	result := make([]byte, 0, size)
	previous := []byte(nil)
	for counter := byte(1); len(result) < size; counter++ {
		expand := hmac.New(sha256.New, prk)
		_, _ = expand.Write(previous)
		_, _ = expand.Write(info)
		_, _ = expand.Write([]byte{counter})
		previous = expand.Sum(nil)
		result = append(result, previous...)
	}
	zeroBytes(prk)
	zeroBytes(previous)
	return result[:size]
}

func deriveAccountRootWrapperKey(root common.Wallet, network, accountID string) ([]byte, error) {
	internal, ok := root.(*InternalWallet)
	if !ok || internal == nil {
		return nil, fmt.Errorf("account root wrapper requires a mnemonic wallet")
	}
	internal.mutex.RLock()
	master := internal.masterkey
	internal.mutex.RUnlock()
	if master == nil {
		return nil, fmt.Errorf("account root wrapper requires a mnemonic wallet")
	}
	key := master
	// m/1018'/0'/0'/0'/0' is exclusively reserved for wrapping the random
	// AccountSecret. It does not reuse the wallet's payment or DKVS signing key.
	for _, child := range []uint32{
		hdkeychain.HardenedKeyStart + accountRootWrapperPurpose,
		hdkeychain.HardenedKeyStart,
		hdkeychain.HardenedKeyStart,
		hdkeychain.HardenedKeyStart,
		hdkeychain.HardenedKeyStart,
	} {
		derived, err := key.Derive(child)
		if err != nil {
			return nil, err
		}
		key = derived
	}
	privateKey, err := key.ECPrivKey()
	if err != nil {
		return nil, err
	}
	ikm := privateKey.Serialize()
	defer zeroBytes(ikm)
	salt := sha256.Sum256(accountRootWrapperAAD(network, accountID))
	return hkdfSHA256(ikm, salt[:], []byte(accountRootWrapperDomain+"/aead"), 32), nil
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
	return accountRootWrapperPayload{
		Secret: append([]byte(nil), secret...), PackageID: profile.PackageID,
		RecoveryMode: profile.RecoveryMode, StorageMode: profile.StorageMode,
		RecordTTL: profile.RecordTTL, AutopayContract: profile.AutopayContract,
		PublicLocator: profile.PublicLocator, RecoveryConfigured: profile.RecoveryConfigured,
	}
}

func accountRootWrapperMetadataMatchesProfile(value accountRootWrapperPayload,
	profile accountManagementProfile) bool {

	return value.PackageID == profile.PackageID && value.RecoveryMode == profile.RecoveryMode &&
		value.StorageMode == profile.StorageMode && value.RecordTTL == profile.RecordTTL &&
		value.AutopayContract == profile.AutopayContract &&
		value.PublicLocator == profile.PublicLocator &&
		value.RecoveryConfigured == profile.RecoveryConfigured
}

func accountRootWrapperMutation(profile *accountManagementProfile, root common.Wallet,
	key string, value []byte) (dkvsValueMutation, error) {

	return accountStateMutation(profile, root, key, value)
}

func (p *Manager) accountManagementVerifiedSnapshot(store accountRootWrapperStore) (
	accountManagementProfile, []byte, common.Wallet, error) {

	p.mutex.RLock()
	if p.accountProfile == nil || len(p.accountSecret) != 32 {
		p.mutex.RUnlock()
		return accountManagementProfile{}, nil, nil, ErrDKVSPathNotSynced
	}
	profile := *p.accountProfile
	secret := append([]byte(nil), p.accountSecret...)
	p.mutex.RUnlock()
	root, err := p.accountManagementRootWallet()
	if err != nil {
		zeroBytes(secret)
		return accountManagementProfile{}, nil, nil, err
	}
	stateKey, err := p.accountManagedStateKey(root)
	if err != nil {
		zeroBytes(secret)
		return accountManagementProfile{}, nil, nil, err
	}
	dataKey, err := p.accountManagedDataBlobKey(root)
	if err != nil {
		zeroBytes(secret)
		return accountManagementProfile{}, nil, nil, err
	}
	if profile.ManagedDataDirty || profile.StateSeq == 0 || profile.ManagedDataRevision == 0 {
		zeroBytes(secret)
		return accountManagementProfile{}, nil, nil, ErrDKVSPathNotSynced
	}
	if err := store.Refresh(stateKey, dataKey); err != nil {
		zeroBytes(secret)
		return accountManagementProfile{}, nil, nil, err
	}
	state, err := store.Get(stateKey)
	if err != nil || state == nil || !bytes.Equal(state.Value, profile.StateEnvelope) {
		zeroBytes(secret)
		if err != nil {
			return accountManagementProfile{}, nil, nil, err
		}
		return accountManagementProfile{}, nil, nil, fmt.Errorf("account-managed state remote verification failed")
	}
	data, err := store.Get(dataKey)
	if err != nil || data == nil {
		zeroBytes(secret)
		if err != nil {
			return accountManagementProfile{}, nil, nil, err
		}
		return accountManagementProfile{}, nil, nil, fmt.Errorf("account-managed data remote verification failed")
	}
	blob, err := DecodeDKVSBlobValue(data.Value)
	if err != nil || !bytes.Equal(blob.Data, profile.ManagedDataEnvelope) {
		zeroBytes(secret)
		return accountManagementProfile{}, nil, nil, fmt.Errorf("account-managed data remote verification failed")
	}
	return profile, secret, root, nil
}

func (p *Manager) syncAccountRootWrapper(store accountRootWrapperStore) error {
	profile, secret, root, err := p.accountManagementVerifiedSnapshot(store)
	if err != nil {
		return err
	}
	defer zeroBytes(secret)
	key, err := accountRootWrapperKey(root)
	if err != nil {
		return err
	}
	encoded, err := sealAccountRootWrapper(root, _chain, profile.AccountID,
		rootWrapperPayload(profile, secret), nil)
	if err != nil {
		return err
	}
	if err := store.Refresh(key); err != nil {
		return err
	}
	current, getErr := store.Get(key)
	if getErr != nil && !errors.Is(getErr, ErrDKVSRecordNotFound) {
		return getErr
	}
	if current != nil {
		existing, openErr := openAccountRootWrapper(root, _chain, profile.AccountID, current.Value)
		if openErr != nil {
			return openErr
		}
		defer zeroBytes(existing.Secret)
		if !bytes.Equal(existing.Secret, secret) {
			return fmt.Errorf("%w: remote wrapper contains another account secret", ErrRootAccountWrapperInvalid)
		}
		if accountRootWrapperMetadataMatchesProfile(existing, profile) &&
			accountRecordMatchesStorage(current, &profile) {
			return nil
		}
	}
	captured := current
	_, err = store.Update([]string{key}, func(values map[string]*dkvsValue,
		_ map[string]uint64) ([]dkvsValueMutation, error) {
		actual := values[key]
		if captured == nil && actual != nil || captured != nil &&
			(actual == nil || actual.Seq != captured.Seq || actual.Hash != captured.Hash ||
				!bytes.Equal(actual.Value, captured.Value)) {
			return nil, dkvsindexer.ErrWriteConflict
		}
		mutation, mutationErr := accountRootWrapperMutation(&profile, root, key, encoded)
		if mutationErr != nil {
			return nil, mutationErr
		}
		return []dkvsValueMutation{mutation}, nil
	})
	return err
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
		return p.syncAccountRootWrapper(store)
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
	return err
}

// RecoverAccountManagementFromRootMnemonic discovers the deterministic root
// wrapper, unwraps the original random AccountSecret and then reuses the normal
// state/blob restore path. A non-root mnemonic simply returns not-found.
func (p *Manager) RecoverAccountManagementFromRootMnemonic(ctx context.Context,
	mnemonic, password string) ([]RestoredWalletResult, error) {

	if p == nil {
		return nil, fmt.Errorf("wallet manager is unavailable")
	}
	store, err := p.accountDKVSStore()
	if err != nil {
		return nil, rootDiscoveryError(err)
	}
	location, err := p.AccountIndexerLocation()
	if err != nil {
		return nil, err
	}
	return p.recoverAccountManagementFromRootMnemonic(ctx, mnemonic, password, store, location)
}

func (p *Manager) recoverAccountManagementFromRootMnemonic(ctx context.Context,
	mnemonic, password string, store accountRootWrapperStore,
	location AccountIndexerLocation) ([]RestoredWalletResult, error) {

	root := NewInternalWalletWithMnemonic(mnemonic, "", GetChainParam())
	if root == nil {
		return nil, fmt.Errorf("invalid account management root wallet")
	}
	root.SetSubAccount(0)
	accountID, err := dkvsAccountID(root)
	if err != nil {
		return nil, err
	}
	wrapperKey, err := accountRootWrapperKey(root)
	if err != nil {
		return nil, err
	}
	stateKey, err := p.accountManagedStateKey(root)
	if err != nil {
		return nil, err
	}
	dataKey, err := p.accountManagedDataBlobKey(root)
	if err != nil {
		return nil, err
	}
	if _, ok := store.(*dkvsStore); ok && p.dkvs != nil {
		// Discovery is local-first: register the deterministic keys and let the
		// existing background worker synchronize them. Get returns pending
		// immediately until that replica is ready, so ordinary imports are not
		// held behind an HTTP timeout.
		p.dkvs.rememberPaths([]string{wrapperKey, stateKey, dataKey})
		p.dkvs.wakeSync()
	}
	if ctx != nil {
		select {
		case <-ctx.Done():
			return nil, rootDiscoveryError(ctx.Err())
		default:
		}
	}
	wrapperValue, err := store.Get(wrapperKey)
	if err != nil {
		return nil, rootDiscoveryError(err)
	}
	if wrapperValue == nil {
		return nil, ErrRootAccountNotFound
	}
	payload, err := openAccountRootWrapper(root, _chain, accountID, wrapperValue.Value)
	if err != nil {
		return nil, err
	}
	defer zeroBytes(payload.Secret)
	stateValue, err := store.Get(stateKey)
	if err != nil {
		return nil, rootDiscoveryError(err)
	}
	if stateValue == nil {
		return nil, ErrRootAccountNotFound
	}
	state, err := account.OpenManagedState(payload.Secret, accountID, stateValue.Value)
	if err != nil || state.RootFingerprint != walletFingerprint(root) {
		return nil, fmt.Errorf("%w: managed state does not match root wrapper", ErrRootAccountWrapperInvalid)
	}
	var dataValue *dkvsValue
	if state.DataRevision != 0 {
		dataValue, err = store.Get(dataKey)
		if err != nil {
			return nil, rootDiscoveryError(err)
		}
	}
	managedData, err := openAccountManagedDataValue(payload.Secret, accountID, dataValue, state)
	if err != nil {
		return nil, err
	}
	recovered := RecoveredAccountManagementState{
		State: state, Seq: state.Revision, Hash: accountStateDigest(stateValue.Value),
		Envelope: append([]byte(nil), stateValue.Value...), ManagedData: managedData.Bundle,
		ManagedDataHash:     managedData.Hash,
		ManagedDataEnvelope: append([]byte(nil), managedData.Envelope...),
	}
	locator := account.Locator{AccountID: accountID, PackageID: payload.PackageID,
		RecoveryMode: payload.RecoveryMode}
	results, err := p.restoreAccountManagementState(recovered, payload.Secret, password,
		locator, AccountManagementRestoreOptions{
			Location: location, StorageMode: payload.StorageMode, RecordTTL: payload.RecordTTL,
			AutopayContract: payload.AutopayContract, PublicLocator: payload.PublicLocator,
		}, state.RootFingerprint)
	if err != nil {
		return nil, err
	}
	if !payload.RecoveryConfigured {
		p.mutex.Lock()
		if p.accountProfile != nil && p.accountProfile.AccountID == accountID {
			p.accountProfile.RecoveryConfigured = false
			err = p.saveAccountManagementProfileLocked()
		}
		p.mutex.Unlock()
		if err != nil {
			return nil, err
		}
	}
	return results, nil
}
