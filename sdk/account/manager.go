package account

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
)

type Manager struct {
	repository Repository
	random     io.Reader
}

func NewManager(repository Repository) *Manager {
	return &Manager{repository: repository, random: rand.Reader}
}

func NewManagerWithRandom(repository Repository, random io.Reader) *Manager {
	manager := NewManager(repository)
	if random != nil {
		manager.random = random
	}
	return manager
}

func randomHex(random io.Reader, size int) (string, error) {
	value := make([]byte, size)
	if _, err := io.ReadFull(random, value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

// CreateRecoveryPackage derives a recovery package from the account's existing
// AccountSecret. AccountSecret creation belongs to wallet account-management
// initialization; recovery package setup must never create or replace it.
func (m *Manager) CreateRecoveryPackage(options CreateOptions, accountSecret []byte) (*RecoveryPackage, error) {
	if m == nil {
		return nil, ErrInvalidRecoveryPackage
	}
	if !validHex(options.AccountID, 64) {
		return nil, ErrInvalidAccountID
	}
	if len(accountSecret) != accountSecretSize {
		return nil, fmt.Errorf("account secret must be %d bytes", accountSecretSize)
	}
	backup, err := NormalizeBackup(options.Backup)
	if err != nil {
		return nil, err
	}
	if options.RecoveryMode != RecoveryMode2Of2 && options.RecoveryMode != RecoveryMode2Of3 {
		return nil, ErrInvalidRecoveryPackage
	}
	if len(options.Questions) != knowledgeQuestionCount {
		return nil, fmt.Errorf("three knowledge questions are required")
	}
	if options.RecoveryMode == RecoveryMode2Of3 && (len(options.GuardianPublicKey) != 32 || !validHex(options.GuardianMailboxID, 64)) {
		return nil, fmt.Errorf("guardian mailbox and X25519 public key are required")
	}
	packageID, err := randomHex(m.random, 16)
	if err != nil {
		return nil, err
	}
	locator := Locator{Version: Version, AccountID: options.AccountID, PackageID: packageID, RecoveryMode: options.RecoveryMode}
	secret := append([]byte(nil), accountSecret...)
	defer zero(secret)
	shares, err := splitSecret(secret, packageID, options.RecoveryMode, m.random)
	if err != nil {
		return nil, err
	}
	encrypted, err := encryptBackup(secret, locator, backup, m.random)
	if err != nil {
		return nil, err
	}
	envelope := Envelope{Version: Version, Locator: locator, EncryptedBackup: encrypted}
	dkvsCapsule, knowledge, err := createKnowledgeRecovery(packageID, shares[1], options.Questions, m.random)
	if err != nil {
		return nil, err
	}
	sealed, err := sealRecoveryKnowledge(locator, shares[0], knowledge, m.random)
	if err != nil {
		return nil, err
	}
	envelopeHash, err := HashEnvelope(envelope)
	if err != nil {
		return nil, err
	}
	manifest := Manifest{Version: Version, Locator: locator, Threshold: 2, Total: uint8(len(shares)), EnvelopeHash: envelopeHash}
	result := &RecoveryPackage{Envelope: envelope, Manifest: manifest, UserShare: shares[0],
		DKVSShareCapsule: dkvsCapsule, KnowledgeBundle: knowledge, SealedKnowledge: sealed}
	if options.RecoveryMode == RecoveryMode2Of3 {
		capsule, err := EncryptGuardianShare(shares[2], options.GuardianPublicKey, m.random)
		if err != nil {
			return nil, err
		}
		hash, err := HashGuardianCapsule(capsule)
		if err != nil {
			return nil, err
		}
		manifest.Guardian = &GuardianReference{MailboxID: options.GuardianMailboxID, ShareID: capsule.ShareID, CapsuleHash: hash}
		result.Manifest = manifest
		result.GuardianCapsule = &capsule
	}
	if err := ValidateRecoveryPackage(*result); err != nil {
		return nil, err
	}
	return result, nil
}

func ValidateRecoveryPackage(value RecoveryPackage) error {
	if err := validateStoredRecoveryPackage(value); err != nil {
		return err
	}
	_, err := OpenRecoveryKnowledge(value, value.UserShare)
	return err
}

func (m *Manager) Publish(ctx context.Context, value RecoveryPackage) error {
	if m == nil || m.repository == nil {
		return fmt.Errorf("account repository is required")
	}
	if err := ValidateRecoveryPackage(value); err != nil {
		return err
	}
	value.UserShare = RecoveryShare{}
	value.KnowledgeBundle = KnowledgeRecoveryBundle{}
	return m.repository.SaveRecoveryPackage(ctx, value)
}

func (m *Manager) Load(ctx context.Context, locator Locator) (*RecoveryPackage, error) {
	if m == nil || m.repository == nil {
		return nil, fmt.Errorf("account repository is required")
	}
	if err := ValidateLocator(locator); err != nil {
		return nil, err
	}
	return m.repository.LoadRecoveryPackage(ctx, locator)
}

func RecoverAccount(envelope Envelope, shares ...RecoveryShare) (Backup, []byte, error) {
	if err := validateEnvelope(envelope); err != nil {
		return Backup{}, nil, err
	}
	secret, err := CombineShares(shares...)
	if err != nil {
		return Backup{}, nil, err
	}
	backup, err := decryptBackup(secret, envelope.Locator, envelope.EncryptedBackup)
	if err != nil {
		zero(secret)
		return Backup{}, nil, err
	}
	return backup, secret, nil
}

func RecoverDKVSShare(capsule DKVSShareCapsule, bundle KnowledgeRecoveryBundle, answers []AnswerAttempt) (RecoveryShare, error) {
	return recoverDKVSShare(capsule, bundle, answers)
}

func RecoverWithKnowledge(envelope Envelope, capsule DKVSShareCapsule, bundle KnowledgeRecoveryBundle, answers []AnswerAttempt, companion RecoveryShare) (Backup, []byte, error) {
	dkvsShare, err := recoverDKVSShare(capsule, bundle, answers)
	if err != nil {
		return Backup{}, nil, err
	}
	return RecoverAccount(envelope, dkvsShare, companion)
}

func RestoreOnNewDevice(options NewDeviceRecoveryOptions) (RecoverySummary, error) {
	backup, secret, err := RecoverAccount(options.Envelope, options.Shares...)
	if err != nil {
		return RecoverySummary{}, err
	}
	defer zero(secret)
	summary := SummarizeBackup(options.Envelope.Locator, backup)
	if options.Confirm == nil || options.Persist == nil {
		return RecoverySummary{}, fmt.Errorf("confirm and persist callbacks are required")
	}
	ok, err := options.Confirm(summary)
	if err != nil {
		return RecoverySummary{}, err
	}
	if !ok {
		return RecoverySummary{}, fmt.Errorf("new-device recovery was not confirmed")
	}
	secretCopy := append([]byte(nil), secret...)
	defer zero(secretCopy)
	if err := options.Persist(secretCopy, backup); err != nil {
		return RecoverySummary{}, err
	}
	return summary, nil
}
