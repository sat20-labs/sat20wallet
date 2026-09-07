package wallet

import (
	"fmt"

	"github.com/sat20-labs/sat20wallet/sdk/common"
	dkvscore "github.com/sat20-labs/sat20wallet/sdk/wallet/dkvs"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

const dkvsAppValueVersion = 1

type DKVSOfflineMessage = dkvscore.DKVSOfflineMessage
type DKVSServiceAuthenticity = dkvscore.DKVSServiceAuthenticity

func encodeDKVSOfflineMessage(value DKVSOfflineMessage) ([]byte, error) {
	return dkvscore.EncodeOfflineMessage(value)
}

func decodeDKVSOfflineMessage(value []byte) (*DKVSOfflineMessage, error) {
	return dkvscore.DecodeOfflineMessage(value)
}

func encodeDKVSServiceAuthenticity(value DKVSServiceAuthenticity) ([]byte, error) {
	return dkvscore.EncodeServiceAuthenticity(value)
}

func decodeDKVSServiceAuthenticity(value []byte) (*DKVSServiceAuthenticity, error) {
	return dkvscore.DecodeServiceAuthenticity(value)
}

// offlineMessageKeyID renders a positive int64 timestamp as a fixed-width
// decimal DKVS segment. Lexicographic key order is therefore identical to
// chronological order. Callers should use Unix milliseconds.
func offlineMessageKeyID(messageID int64) (string, error) {
	if messageID <= 0 {
		return "", dkvsindexer.ErrInvalidRecord
	}
	return fmt.Sprintf("%019d", messageID), nil
}

func (p *SatsNetDKVSClient) SendOfflineMessage(senderWallet common.Wallet, recipientPubKey []byte, msgID int64,
	encryptedMessage []byte, metadata map[string]string, opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
	return nil, ErrDKVSMessageManagerRequired
}

func (p *SatsNetDKVSClient) SendOfflineMessageWithAutopay(senderWallet common.Wallet, recipientPubKey []byte,
	msgID int64, encryptedMessage []byte, metadata map[string]string, opts dkvsindexer.RecordOptions,
	autopay DKVSAutopayOptions) (*swire.DKVSRecord, error) {
	return nil, ErrDKVSMessageManagerRequired
}

func buildOfflineMessage(senderWallet common.Wallet, recipientPubKey []byte, msgID int64, encryptedMessage []byte,
	metadata map[string]string) (string, string, []byte, error) {

	senderPubKey, err := dkvsWalletPubKey(senderWallet)
	if err != nil {
		return "", "", nil, dkvsindexer.ErrInvalidSignature
	}
	if len(encryptedMessage) == 0 {
		return "", "", nil, dkvsindexer.ErrInvalidRecord
	}
	stableMsgID, err := offlineMessageKeyID(msgID)
	if err != nil {
		return "", "", nil, err
	}
	mailboxID := dkvsindexer.AccountID(recipientPubKey)
	if _, err := dkvsindexer.MailMsgKey(mailboxID, dkvsindexer.AccountID(senderPubKey), stableMsgID); err != nil {
		return "", "", nil, err
	}
	value, err := encodeDKVSOfflineMessage(DKVSOfflineMessage{
		Version:          dkvsAppValueVersion,
		FromPubKey:       senderPubKey,
		ToMailboxID:      mailboxID,
		MessageID:        msgID,
		EncryptedMessage: append([]byte{}, encryptedMessage...),
		Metadata:         cloneStringMap(metadata),
	})
	if err != nil {
		return "", "", nil, err
	}
	return mailboxID, stableMsgID, value, nil
}

func (p *SatsNetDKVSClient) ReadOfflineMessages(recipientPubKey []byte, start, limit int) ([]*DKVSOfflineMessage, []*swire.DKVSRecord, int, error) {
	return nil, nil, 0, ErrDKVSMessageManagerRequired
}

// ServiceAuthenticityPath identifies one stable application/component object.
// appID is a stable component identifier under a service, for example desktop,
// pwa, extension, android or ios. Release/version belongs to the value and
// advances through DKVS Seq; it never becomes part of the logical key.
func ServiceAuthenticityPath(appID, _ string) string {
	return "authenticity/" + dkvsindexer.NormalizeNameID(appID)
}

func (p *SatsNetDKVSClient) PublishServiceAuthenticity(wallet common.Wallet, serviceName, appID, release, artifactHash, downloadURL string, metadata map[string]string, opts dkvsindexer.RecordOptions) (*swire.DKVSRecord, error) {
	if artifactHash == "" {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	value, err := encodeDKVSServiceAuthenticity(DKVSServiceAuthenticity{
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
	authenticity, err := decodeDKVSServiceAuthenticity(record.Value)
	if err != nil {
		return nil, nil, err
	}
	if authenticity.Version != dkvsAppValueVersion ||
		authenticity.ServiceName != serviceName ||
		authenticity.AppID != appID ||
		authenticity.Release != release {
		return nil, nil, dkvsindexer.ErrInvalidRecord
	}
	return authenticity, record, nil
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
