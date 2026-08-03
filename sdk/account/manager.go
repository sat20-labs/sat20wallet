package account

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"time"
)

type Manager struct {
	repository Repository
	random     io.Reader
	now        func() time.Time
}

func NewManager(repository Repository) *Manager {
	return &Manager{repository: repository, random: rand.Reader, now: time.Now}
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

func (m *Manager) CreateRecoveryPackage(options CreateOptions) (*RecoveryPackage, error) {
	result, secret, err := m.CreateRecoveryPackageWithSecret(options)
	zero(secret)
	return result, err
}

// CreateRecoveryPackageWithSecret is used by the wallet SDK activation flow.
// The caller must keep the returned secret in protected memory and clear it.
func (m *Manager) CreateRecoveryPackageWithSecret(options CreateOptions) (*RecoveryPackage, []byte, error) {
	if m == nil {
		return nil, nil, ErrInvalidRecoveryPackage
	}
	if !validHex(options.AccountID, 64) {
		return nil, nil, ErrInvalidAccountID
	}
	backup, err := NormalizeBackup(options.Backup)
	if err != nil {
		return nil, nil, err
	}
	if options.RecoveryMode != RecoveryMode2Of2 && options.RecoveryMode != RecoveryMode2Of3 {
		return nil, nil, ErrInvalidRecoveryPackage
	}
	if len(options.Questions) != knowledgeQuestionCount {
		return nil, nil, fmt.Errorf("three knowledge questions are required")
	}
	if options.RecoveryMode == RecoveryMode2Of3 && (len(options.GuardianPublicKey) != 32 || !validHex(options.GuardianMailboxID, 64)) {
		return nil, nil, fmt.Errorf("guardian mailbox and X25519 public key are required")
	}
	packageID, err := randomHex(m.random, 16)
	if err != nil {
		return nil, nil, err
	}
	locator := Locator{Version: Version, AccountID: options.AccountID, PackageID: packageID, RecoveryMode: options.RecoveryMode}
	secret := make([]byte, accountSecretSize)
	if _, err := io.ReadFull(m.random, secret); err != nil {
		return nil, nil, err
	}
	shares, err := splitSecret(secret, packageID, options.RecoveryMode, m.random)
	if err != nil {
		zero(secret)
		return nil, nil, err
	}
	encrypted, err := encryptBackup(secret, locator, backup, m.random)
	if err != nil {
		zero(secret)
		return nil, nil, err
	}
	envelope := Envelope{Version: Version, Locator: locator, EncryptedBackup: encrypted}
	dkvsCapsule, knowledge, err := createKnowledgeRecovery(packageID, shares[1], options.Questions, m.random)
	if err != nil {
		zero(secret)
		return nil, nil, err
	}
	envelopeHash, err := HashEnvelope(envelope)
	if err != nil {
		zero(secret)
		return nil, nil, err
	}
	manifest := Manifest{Version: Version, Locator: locator, Threshold: 2, Total: uint8(len(shares)), EnvelopeHash: envelopeHash, CreatedAt: m.now().UnixMilli()}
	result := &RecoveryPackage{Envelope: envelope, Manifest: manifest, UserShare: shares[0], DKVSShareCapsule: dkvsCapsule, KnowledgeBundle: knowledge}
	if options.RecoveryMode == RecoveryMode2Of3 {
		capsule, err := EncryptGuardianShare(shares[2], options.GuardianPublicKey, m.random)
		if err != nil {
			zero(secret)
			return nil, nil, err
		}
		hash, err := HashGuardianCapsule(capsule)
		if err != nil {
			zero(secret)
			return nil, nil, err
		}
		manifest.Guardian = &GuardianReference{MailboxID: options.GuardianMailboxID, ShareID: capsule.ShareID, CapsuleHash: hash}
		result.Manifest = manifest
		result.GuardianCapsule = &capsule
	}
	if err := ValidateRecoveryPackage(*result); err != nil {
		zero(secret)
		return nil, nil, err
	}
	return result, secret, nil
}

func ValidateRecoveryPackage(value RecoveryPackage) error {
	if err := validateEnvelope(value.Envelope); err != nil {
		return err
	}
	if err := validateManifest(value.Manifest, value.Envelope.Locator); err != nil {
		return err
	}
	hash, err := HashEnvelope(value.Envelope)
	if err != nil || hash != value.Manifest.EnvelopeHash {
		return ErrInvalidRecoveryPackage
	}
	if _, err := validateShare(value.UserShare); err != nil || value.UserShare.Role != ShareRoleUser || value.UserShare.PackageID != value.Envelope.Locator.PackageID {
		return ErrInvalidRecoveryPackage
	}
	if value.DKVSShareCapsule.Version != Version || value.DKVSShareCapsule.PackageID != value.Envelope.Locator.PackageID || value.KnowledgeBundle.PackageID != value.Envelope.Locator.PackageID {
		return ErrInvalidRecoveryPackage
	}
	if value.Envelope.Locator.RecoveryMode == RecoveryMode2Of3 {
		if value.GuardianCapsule == nil || value.Manifest.Guardian == nil {
			return ErrInvalidRecoveryPackage
		}
		hash, err := HashGuardianCapsule(*value.GuardianCapsule)
		if err != nil || hash != value.Manifest.Guardian.CapsuleHash || value.GuardianCapsule.ShareID != value.Manifest.Guardian.ShareID {
			return ErrInvalidRecoveryPackage
		}
	}
	return nil
}

func (m *Manager) Publish(ctx context.Context, value RecoveryPackage) error {
	if m == nil || m.repository == nil {
		return fmt.Errorf("account repository is required")
	}
	if err := ValidateRecoveryPackage(value); err != nil {
		return err
	}
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
