package account

import (
	"context"
	"errors"
)

const (
	Version               = 1
	accountSecretSize     = 32
	MaxRecoveryObjectSize = 10 * 1024
)

type RecoveryMode string

const (
	RecoveryMode2Of2 RecoveryMode = "2of2"
	RecoveryMode2Of3 RecoveryMode = "2of3"
)

type ShareRole string

const (
	ShareRoleUser     ShareRole = "user"
	ShareRoleDKVS     ShareRole = "dkvs"
	ShareRoleGuardian ShareRole = "guardian"
)

var (
	ErrInvalidAccountID       = errors.New("invalid account id")
	ErrInvalidPackageID       = errors.New("invalid recovery package id")
	ErrInvalidBackup          = errors.New("invalid account backup")
	ErrInvalidRecoveryPackage = errors.New("invalid recovery package")
	ErrInvalidShare           = errors.New("invalid recovery share")
	ErrInsufficientShares     = errors.New("insufficient recovery shares")
	ErrRecoveryFailed         = errors.New("account recovery failed")
)

type SubAccount struct {
	Index uint32 `json:"index"`
	DID   string `json:"did"`
}

type WalletBackup struct {
	Name          string       `json:"name"`
	Mnemonic      string       `json:"mnemonic"`
	AccountCount  uint32       `json:"account_count"`
	SubAccounts   []SubAccount `json:"sub_accounts"`
}

type Backup struct {
	Version uint32         `json:"version"`
	Wallets []WalletBackup `json:"wallets"`
}

type Locator struct {
	Version      uint32       `json:"version"`
	AccountID    string       `json:"account_id"`
	PackageID    string       `json:"package_id"`
	RecoveryMode RecoveryMode `json:"recovery_mode"`
}

type EncryptedBlob struct {
	Algorithm  string `json:"algorithm"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

type Envelope struct {
	Version         uint32        `json:"version"`
	Locator         Locator       `json:"locator"`
	EncryptedBackup EncryptedBlob `json:"encrypted_backup"`
}

type RecoveryShare struct {
	Version   uint32    `json:"version"`
	PackageID string    `json:"package_id"`
	Threshold uint8     `json:"threshold"`
	Total     uint8     `json:"total"`
	Index     uint8     `json:"index"`
	Role      ShareRole `json:"role"`
	Data      string    `json:"data"`
	Checksum  string    `json:"checksum"`
}

type KnowledgeQuestion struct {
	ID                string `json:"id"`
	Prompt            string `json:"prompt"`
	CaseSensitive     bool   `json:"case_sensitive"`
	IgnorePunctuation bool   `json:"ignore_punctuation"`
}

type QuestionAnswer struct {
	Question     KnowledgeQuestion `json:"question"`
	Answer       string            `json:"-"`
	Confirmation string            `json:"-"`
}

type EncryptedQuestionShare struct {
	Question   KnowledgeQuestion `json:"question"`
	Salt       string            `json:"salt"`
	Vault      []byte            `json:"vault"`
	Nonce      string            `json:"nonce"`
	Ciphertext string            `json:"ciphertext"`
}

type KnowledgeRecoveryBundle struct {
	Version        uint32                   `json:"version"`
	PackageID      string                   `json:"package_id"`
	Threshold      uint8                    `json:"threshold"`
	Total          uint8                    `json:"total"`
	QuestionShares []EncryptedQuestionShare `json:"question_shares"`
}

type DKVSShareCapsule struct {
	Version    uint32 `json:"version"`
	PackageID  string `json:"package_id"`
	Algorithm  string `json:"algorithm"`
	Nonce      string `json:"nonce"`
	Ciphertext string `json:"ciphertext"`
}

type GuardianShareCapsule struct {
	Version            uint32 `json:"version"`
	PackageID          string `json:"package_id"`
	ShareID            string `json:"share_id"`
	Algorithm          string `json:"algorithm"`
	EphemeralPublicKey string `json:"ephemeral_public_key"`
	Nonce              string `json:"nonce"`
	Ciphertext         string `json:"ciphertext"`
}

type GuardianReference struct {
	MailboxID   string `json:"mailbox_id"`
	ShareID     string `json:"share_id"`
	CapsuleHash string `json:"capsule_hash"`
}

type Manifest struct {
	Version      uint32             `json:"version"`
	Locator      Locator            `json:"locator"`
	Threshold    uint8              `json:"threshold"`
	Total        uint8              `json:"total"`
	EnvelopeHash string             `json:"envelope_hash"`
	CreatedAt    int64              `json:"created_at"`
	Guardian     *GuardianReference `json:"guardian,omitempty"`
}

type RecoveryPackage struct {
	Envelope          Envelope                `json:"envelope"`
	Manifest          Manifest                `json:"manifest"`
	UserShare         RecoveryShare           `json:"user_share"`
	DKVSShareCapsule  DKVSShareCapsule        `json:"dkvs_share_capsule"`
	KnowledgeBundle   KnowledgeRecoveryBundle `json:"knowledge_bundle"`
	GuardianCapsule   *GuardianShareCapsule   `json:"guardian_capsule,omitempty"`
}

type CreateOptions struct {
	AccountID          string
	Backup             Backup
	RecoveryMode       RecoveryMode
	Questions          []QuestionAnswer
	GuardianMailboxID  string
	GuardianPublicKey  []byte
}

type AnswerAttempt struct {
	QuestionID string
	Answer     string
}

type RecoverySummary struct {
	AccountID    string          `json:"account_id"`
	PackageID    string          `json:"package_id"`
	RecoveryMode RecoveryMode    `json:"recovery_mode"`
	Wallets      []WalletSummary `json:"wallets"`
}

type WalletSummary struct {
	Name         string   `json:"name"`
	AccountCount uint32   `json:"account_count"`
	DIDs         []string `json:"dids"`
}

type NewDeviceRecoveryOptions struct {
	Envelope Envelope
	Shares   []RecoveryShare
	Confirm  func(RecoverySummary) (bool, error)
	Persist  func(accountSecret []byte, backup Backup) error
}

type Repository interface {
	SaveEnvelope(context.Context, Envelope) error
	SaveDKVSShareCapsule(context.Context, Locator, DKVSShareCapsule) error
	SaveKnowledgeBundle(context.Context, Locator, KnowledgeRecoveryBundle) error
	SaveManifest(context.Context, Manifest) error
	LoadEnvelope(context.Context, Locator) (*Envelope, error)
	LoadDKVSShareCapsule(context.Context, Locator) (*DKVSShareCapsule, error)
	LoadKnowledgeBundle(context.Context, Locator) (*KnowledgeRecoveryBundle, error)
	LoadManifest(context.Context, Locator) (*Manifest, error)
}
