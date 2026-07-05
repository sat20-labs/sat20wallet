package wallet

import (
	"encoding/json"

	"github.com/sat20-labs/sat20wallet/sdk/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

const dkvsAppValueVersion = 1

type DKVSWalletRecoveryBackup struct {
	Version         uint32            `json:"version"`
	WalletID        string            `json:"wallet_id"`
	EncryptedBackup []byte            `json:"encrypted_backup"`
	Metadata        map[string]string `json:"metadata,omitempty"`
}

type DKVSGuardianShare struct {
	Version    uint32            `json:"version"`
	PackageID  string            `json:"package_id"`
	ShareID    string            `json:"share_id"`
	Ciphertext []byte            `json:"ciphertext"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

type DKVSOfflineMessage struct {
	Version          uint32            `json:"version"`
	FromPubKey       []byte            `json:"from_pubkey"`
	ToMailboxID      string            `json:"to_mailbox_id"`
	MessageID        string            `json:"message_id"`
	EncryptedMessage []byte            `json:"encrypted_message"`
	Metadata         map[string]string `json:"metadata,omitempty"`
}

type DKVSServiceAuthenticity struct {
	Version      uint32            `json:"version"`
	ServiceName  string            `json:"service_name"`
	AppID        string            `json:"app_id"`
	Release      string            `json:"release,omitempty"`
	ArtifactHash string            `json:"artifact_hash"`
	DownloadURL  string            `json:"download_url,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

func WalletRecoveryPath(walletID string) string {
	return "wallet-recovery/" + dkvsindexer.NormalizeNameID(walletID)
}

func (p *SatsNetDKVSClient) PutWalletRecoveryBackup(wallet common.Wallet, walletID string, encryptedBackup []byte, metadata map[string]string, opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
	if _, err := dkvsWalletPubKey(wallet); err != nil {
		return nil, dkvsindexer.ErrInvalidSignature
	}
	if walletID == "" || len(encryptedBackup) == 0 {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	value, err := json.Marshal(DKVSWalletRecoveryBackup{
		Version:         dkvsAppValueVersion,
		WalletID:        walletID,
		EncryptedBackup: append([]byte{}, encryptedBackup...),
		Metadata:        cloneStringMap(metadata),
	})
	if err != nil {
		return nil, err
	}
	return p.PutPersonalRecord(wallet, WalletRecoveryPath(walletID), value, opts)
}

func (p *SatsNetDKVSClient) GetWalletRecoveryBackup(pubKey []byte, walletID string) (*DKVSWalletRecoveryBackup, *swire.DKVSRecord, error) {
	record, err := p.GetPersonalRecord(pubKey, WalletRecoveryPath(walletID))
	if err != nil {
		return nil, nil, err
	}
	var backup DKVSWalletRecoveryBackup
	if err := json.Unmarshal(record.Value, &backup); err != nil {
		return nil, nil, err
	}
	if backup.Version != dkvsAppValueVersion || backup.WalletID != walletID {
		return nil, nil, dkvsindexer.ErrInvalidRecord
	}
	return &backup, record, nil
}

func (p *SatsNetDKVSClient) RenewWalletRecoveryBackup(wallet common.Wallet, walletID string, opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
	if _, err := dkvsWalletPubKey(wallet); err != nil {
		return nil, dkvsindexer.ErrInvalidSignature
	}
	return p.RenewPersonalRecord(wallet, WalletRecoveryPath(walletID), opts)
}

func (p *SatsNetDKVSClient) PutGuardianShare(ownerWallet common.Wallet, packageID, shareID string, encryptedShare []byte, metadata map[string]string, opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
	ownerPubKey, err := dkvsWalletPubKey(ownerWallet)
	if err != nil {
		return nil, dkvsindexer.ErrInvalidSignature
	}
	if packageID == "" || shareID == "" || len(encryptedShare) == 0 {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	mailboxID := dkvsindexer.AccountID(ownerPubKey)
	key, err := dkvsindexer.MailShareKey(mailboxID, dkvsindexer.NormalizeNameID(packageID), dkvsindexer.NormalizeNameID(shareID))
	if err != nil {
		return nil, err
	}
	value, err := json.Marshal(DKVSGuardianShare{
		Version:    dkvsAppValueVersion,
		PackageID:  packageID,
		ShareID:    shareID,
		Ciphertext: append([]byte{}, encryptedShare...),
		Metadata:   cloneStringMap(metadata),
	})
	if err != nil {
		return nil, err
	}
	record, err := NewDKVSSignedRecord(ownerWallet, key, value, opts)
	if err != nil {
		return nil, err
	}
	return p.PutMailboxShare(record)
}

func (p *SatsNetDKVSClient) ReadGuardianShares(ownerPubKey []byte, packageID string, start, limit int) ([]*DKVSGuardianShare, []*swire.DKVSRecord, int, error) {
	mailboxID := dkvsindexer.AccountID(ownerPubKey)
	prefix := "/mail/" + mailboxID + "/share/" + dkvsindexer.NormalizeNameID(packageID)
	if _, err := dkvsindexer.ParsePrefix(prefix); err != nil {
		return nil, nil, 0, err
	}
	records, total, err := p.ListRecords(prefix, start, limit)
	if err != nil {
		return nil, nil, 0, err
	}
	shares := make([]*DKVSGuardianShare, 0, len(records))
	for _, record := range records {
		var share DKVSGuardianShare
		if err := json.Unmarshal(record.Value, &share); err != nil {
			return nil, nil, 0, err
		}
		if share.Version != dkvsAppValueVersion || share.PackageID != packageID {
			return nil, nil, 0, dkvsindexer.ErrInvalidRecord
		}
		shares = append(shares, &share)
	}
	return shares, records, total, nil
}

func (p *SatsNetDKVSClient) SendOfflineMessage(senderWallet common.Wallet, recipientPubKey []byte, msgID string, encryptedMessage []byte, metadata map[string]string, opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
	senderPubKey, err := dkvsWalletPubKey(senderWallet)
	if err != nil {
		return nil, dkvsindexer.ErrInvalidSignature
	}
	if msgID == "" || len(encryptedMessage) == 0 {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	mailboxID := dkvsindexer.AccountID(recipientPubKey)
	safeMsgID := dkvsindexer.NormalizeNameID(msgID)
	value, err := json.Marshal(DKVSOfflineMessage{
		Version:          dkvsAppValueVersion,
		FromPubKey:       senderPubKey,
		ToMailboxID:      mailboxID,
		MessageID:        msgID,
		EncryptedMessage: append([]byte{}, encryptedMessage...),
		Metadata:         cloneStringMap(metadata),
	})
	if err != nil {
		return nil, err
	}
	return p.SendSignedMailboxMessage(senderWallet, mailboxID, safeMsgID, value, opts)
}

func (p *SatsNetDKVSClient) ReadOfflineMessages(recipientPubKey []byte, start, limit int) ([]*DKVSOfflineMessage, []*swire.DKVSRecord, int, error) {
	mailboxID := dkvsindexer.AccountID(recipientPubKey)
	records, total, err := p.ReadMailboxMessages(mailboxID, start, limit)
	if err != nil {
		return nil, nil, 0, err
	}
	messages := make([]*DKVSOfflineMessage, 0, len(records))
	for _, record := range records {
		var msg DKVSOfflineMessage
		if err := json.Unmarshal(record.Value, &msg); err != nil {
			return nil, nil, 0, err
		}
		if msg.Version != dkvsAppValueVersion || msg.ToMailboxID != mailboxID {
			return nil, nil, 0, dkvsindexer.ErrInvalidRecord
		}
		messages = append(messages, &msg)
	}
	return messages, records, total, nil
}

func ServiceAuthenticityPath(appID, release string) string {
	path := "authenticity/" + dkvsindexer.NormalizeNameID(appID)
	if release != "" {
		path += "/" + dkvsindexer.NormalizeNameID(release)
	}
	return path
}

func (p *SatsNetDKVSClient) PublishServiceAuthenticity(wallet common.Wallet, serviceName, appID, release, artifactHash, downloadURL string, metadata map[string]string, opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
	if artifactHash == "" {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	value, err := json.Marshal(DKVSServiceAuthenticity{
		Version:      dkvsAppValueVersion,
		ServiceName:  serviceName,
		AppID:        appID,
		Release:      release,
		ArtifactHash: artifactHash,
		DownloadURL:  downloadURL,
		Metadata:     cloneStringMap(metadata),
	})
	if err != nil {
		return nil, err
	}
	return p.PutSignedServiceRecord(wallet, serviceName, ServiceAuthenticityPath(appID, release), value, opts)
}

func (p *SatsNetDKVSClient) GetServiceAuthenticity(serviceName, appID, release string) (*DKVSServiceAuthenticity, *swire.DKVSRecord, error) {
	record, err := p.GetServiceRecord(serviceName, ServiceAuthenticityPath(appID, release))
	if err != nil {
		return nil, nil, err
	}
	var authenticity DKVSServiceAuthenticity
	if err := json.Unmarshal(record.Value, &authenticity); err != nil {
		return nil, nil, err
	}
	if authenticity.Version != dkvsAppValueVersion ||
		authenticity.ServiceName != serviceName ||
		authenticity.AppID != appID ||
		authenticity.Release != release {
		return nil, nil, dkvsindexer.ErrInvalidRecord
	}
	return &authenticity, record, nil
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
