package wallet

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

// Modules receive authenticated application messages. Account management owns
// the mailbox, transport records, retention, subscription and local cursor.
type accountMessageReceipt struct {
	Key         string
	Hash        string
	IssueHeight uint64
	TTL         uint64
}

type accountMessage struct {
	Sender    string
	Recipient string
	ID        string
	Kind      string
	Body      []byte
	Receipt   accountMessageReceipt
}

func accountMessageKey(to, from, id string) (string, error) {
	return dkvsindexer.MailMsgKey(to, from, id)
}

func accountWalletID(wallet common.Wallet) (string, error) {
	return dkvsAccountID(wallet)
}

func accountMessageView(item *AccountDirectMessage) *accountMessage {
	if item == nil || item.Direct == nil || item.Payload == nil || item.Record == nil {
		return nil
	}
	if _, err := accountMessageKey(item.Direct.RecipientAccount, item.Direct.SenderAccount, item.Payload.ApplicationID); err != nil {
		return nil
	}
	hash := dkvsindexer.RecordHash(item.Record)
	return &accountMessage{
		Sender: item.Direct.SenderAccount, Recipient: item.Direct.RecipientAccount,
		ID: item.Payload.ApplicationID, Kind: item.Payload.Kind, Body: bytes.Clone(item.Payload.Body),
		Receipt: accountMessageReceipt{Key: item.Record.Key, Hash: hex.EncodeToString(hash[:]),
			IssueHeight: item.Record.IssueHeight, TTL: item.Record.TTL},
	}
}

func (p *Manager) watchAccountMailbox(wallet common.Wallet) error {
	id, err := accountWalletID(wallet)
	if err != nil {
		return err
	}
	target, err := mailboxSubscriptionTarget(id)
	if err != nil {
		return err
	}
	return p.SubscribeDKVSPrefix(target)
}

func (p *Manager) readAccountMessages(wallet common.Wallet, verify dkvsindexer.RecordVerificationOptions) ([]*accountMessage, error) {
	id, err := accountWalletID(wallet)
	if err != nil {
		return nil, err
	}
	store, err := p.accountDKVSStore()
	if err != nil {
		return nil, err
	}
	if err = p.watchAccountMailbox(wallet); err != nil {
		return nil, err
	}
	if err = p.dkvs.ensureCurrentSubscription(store.client); err != nil {
		return nil, err
	}
	values, err := store.ListMailboxVerified(id, verify)
	if err != nil {
		return nil, err
	}
	if len(values) == 0 {
		return nil, nil
	}
	scope := sha256.Sum256([]byte(id + "\x00" + store.client.replicaNamespace))
	key := []byte(GetDBKeyPrefix() + "account-mailbox-cursor/" + hex.EncodeToString(scope[:]))
	cursor, err := p.db.Read(key)
	if err != nil && !errors.Is(err, indexer.ErrKeyNotFound) {
		return nil, err
	}
	start := sort.Search(len(values), func(i int) bool { return values[i].Key > string(cursor) })
	if start == len(values) {
		start = 0
	}
	count := min(len(values), 256)
	messages := make([]*accountMessage, 0, count)
	for i := 0; i < count; i++ {
		value := values[(start+i)%len(values)]
		item, err := decodeWalletDirectRecord(wallet, value.record)
		if err != nil || item == nil {
			messages = append(messages, nil)
			continue
		}
		messages = append(messages, accountMessageView(item))
	}
	// The cursor rotates through retained messages; failed application messages
	// remain in the mailbox and are retried on the next pass.
	if err := p.db.Write(key, []byte(values[(start+count-1)%len(values)].Key)); err != nil {
		return nil, err
	}
	return messages, nil
}

func (p *Manager) sendAccountMessage(wallet common.Wallet, id, kind, recipient string,
	body []byte, payout bool) (*accountMessageReceipt, error) {
	node := p.getAccountServiceNode()
	if payout {
		node = p.payoutMessageNode()
	}
	message, err := p.sendWalletMessageOnNode(wallet, id, kind, recipient, body, node, payout)
	if err != nil {
		return nil, err
	}
	raw, err := swire.SerializeDirectMessage(message, true)
	if err != nil {
		return nil, err
	}
	key, err := accountMessageKey(recipient, message.SenderAccount, message.MessageID)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(raw)
	return &accountMessageReceipt{Key: key, Hash: hex.EncodeToString(hash[:])}, nil
}

func (p *Manager) resolveRGBAddress(address string, verify dkvsindexer.RecordVerificationOptions) (*RGB11AddressEndpoint, error) {
	store, err := p.accountDKVSStore()
	if err != nil {
		return nil, err
	}
	key, err := dkvsindexer.AccountMappingKey(GetChainParam().Name, address)
	if err != nil {
		return nil, ErrRGB11TraditionalReceiveRequired
	}
	value, err := store.GetVerified(key, verify)
	if err != nil {
		return nil, ErrRGB11TraditionalReceiveRequired
	}
	service, err := dkvsindexer.DecodeAccountServiceDescriptor(value.Value)
	if err != nil || service.Capabilities&accountServiceCapabilityRGB11Direct == 0 {
		return nil, ErrRGB11TraditionalReceiveRequired
	}
	pubkey, err := dkvsindexer.AccountPubKey(service.AccountID)
	if err != nil {
		return nil, err
	}
	script, err := AddrToPkScript(address, GetChainParam())
	if err != nil {
		return nil, err
	}
	return &RGB11AddressEndpoint{AccountID: service.AccountID, Address: address, MailboxID: service.AccountID,
		CompressedPubKey: pubkey, PkScript: script, CapabilityFlags: RGB11ReceiveCapabilityAddress | RGB11ReceiveCapabilityAny,
		CapabilityRecordKey: key, CapabilityRecordHash: value.Hash, IssueHeight: value.IssueHeight, TTL: value.TTL}, nil
}

func (p *Manager) forgetAccountMessage(wallet common.Wallet, id string) error {
	sender, err := accountWalletID(wallet)
	if err != nil {
		return err
	}
	return p.deleteWalletMessageOutbox(sender, id)
}
func (p *Manager) hasAccountMessageEvidence(keys ...string) (bool, error) {
	store, err := p.accountDKVSStore()
	if err != nil {
		return false, err
	}
	return store.hasLocalRecordEvidence(keys...)
}

func (p *Manager) enableRGBAddress(wallet common.Wallet, flags uint8) (*RGB11AddressEndpoint, error) {
	if _, ok := wallet.(*InternalWallet); !ok {
		return nil, fmt.Errorf("RGB11 address receive requires an internal wallet")
	}
	if flags != 0 && flags&RGB11ReceiveCapabilityAddress == 0 {
		return nil, ErrRGB11TraditionalReceiveRequired
	}
	if err := p.bindAccountToCurrentCoreNode(wallet); err != nil {
		return nil, fmt.Errorf("bind RGB11 receive account to current CoreNode: %w", err)
	}
	return p.resolveRGBAddress(wallet.GetAddress(), dkvsindexer.RecordVerificationOptions{})
}

func (p *Manager) registerDKVSDomainObservers() {
	if p == nil || p.dkvs == nil {
		return
	}
	p.dkvs.addObserver(func(paths []string) {
		p.scheduleMailboxRefreshForChanges(paths)
		accountID := p.rootDKVSAccountID()
		relevant := false
		for _, path := range paths {
			parsed, err := dkvsindexer.ParsePrefix(path)
			if err != nil || len(parsed.Segments) < 2 || parsed.Segments[0] != accountID {
				continue
			}
			// Control records share the account collection; provider modules are
			// account-owned blobs. Mailbox and unrelated public subscriptions do
			// not require replaying the private account synchronization path.
			if parsed.Namespace == "blob" || parsed.Namespace == "personal" && parsed.Segments[1] == "account" {
				relevant = true
				break
			}
		}
		if !relevant {
			return
		}
		p.requestAccountSync()
	})
}

// Notify UI subscribers after a module has committed local state.
func (p *Manager) notifyAccountData() {
	if p != nil && p.dkvs != nil {
		p.dkvs.notifyCallback()
	}
}
