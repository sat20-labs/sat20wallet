package wallet

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

const (
	topicMessageOutboxVersion = uint32(2)
	topicMessageOutboxPrefix  = "topic-message-outbox/"
	topicControlOutboxVersion = uint32(1)
	topicControlOutboxPrefix  = "topic-control-outbox/"
)

type topicMessageOutboxRecord struct {
	Version         uint32
	ApplicationID   string
	TopicName       string
	ServiceCoreNode string
	SourceCoreNode  string
	Publish         swire.TopicPublishMessage
}

type topicControlOutboxRecord struct {
	Version         uint32
	TopicName       string
	ChangeType      string
	TargetAccount   string
	Commit          swire.TopicMembershipCommit
	WrappedTopicKey []byte
}

func (p *Manager) messageTopicWallet() (*InternalWallet, error) {
	if p == nil {
		return nil, ErrMessageServiceUnavailable
	}
	root, err := p.accountManagementRootWallet()
	if err != nil {
		return nil, err
	}
	wallet, ok := root.(*InternalWallet)
	if !ok {
		return nil, fmt.Errorf("topic service requires an internal root wallet")
	}
	return wallet, nil
}

func signTopicHash(wallet common.Wallet, hash [32]byte) ([]byte, error) {
	signer, ok := wallet.(dkvsAccountSchnorrSigner)
	if !ok {
		return nil, fmt.Errorf("topic wallet cannot sign")
	}
	return signer.SignSchnorrMessage(hash[:])
}

func (p *Manager) CreateMessageTopic(topicName, displayName string, maxMembers uint32) (*swire.TopicServiceSnapshot, error) {
	wallet, err := p.messageTopicWallet()
	if err != nil {
		return nil, err
	}
	if err := p.bindAccountToCurrentCoreNode(wallet); err != nil {
		return nil, err
	}
	client, coreID, err := p.messageServiceClient()
	if err != nil {
		return nil, err
	}
	accountID, err := dkvsAccountID(wallet)
	if err != nil {
		return nil, err
	}
	topicName, err = normalizeTopicID(topicName)
	if err != nil {
		return nil, err
	}
	meta := swire.TopicMeta{
		TopicName: topicName, DisplayName: strings.TrimSpace(displayName), OwnerAccount: accountID,
		ServiceCoreNode: coreID, MaxMembers: maxMembers,
	}
	request := &swire.TopicCreateRequest{Meta: meta}
	hash, err := swire.TopicCreateRequestSigningHash(request)
	if err != nil {
		return nil, err
	}
	request.OwnerSignature, err = signTopicHash(wallet, hash)
	if err != nil {
		return nil, err
	}
	payload, err := swire.EncodeTopicJSON(request)
	if err != nil {
		return nil, err
	}
	cryptoManager, err := NewTopicCryptoManager(p, wallet)
	if err != nil {
		return nil, err
	}
	if _, err := cryptoManager.LoadTopicKey(topicName, 1); err != nil {
		if !errors.Is(err, indexer.ErrKeyNotFound) {
			return nil, err
		}
		topicKey, keyErr := generateTopicKey()
		if keyErr != nil {
			return nil, keyErr
		}
		// Persist before the service call. If the HTTP response is lost after a
		// successful create, retry reuses the exact same local epoch-1 key. A
		// failed create may leave an unreachable local key, which is harmless.
		if keyErr := cryptoManager.StoreTopicKey(topicName, 1, topicKey); keyErr != nil {
			return nil, keyErr
		}
	}
	if _, err := client.SendMessageServiceReq(&swire.MessageServiceRequest{
		Action: swire.MessageServiceActionCreateTopic, Payload: payload,
	}); err != nil {
		return nil, err
	}
	return p.GetMessageTopicState(topicName)
}

func (p *Manager) GetMessageTopicState(topicName string) (*swire.TopicServiceSnapshot, error) {
	wallet, err := p.messageTopicWallet()
	if err != nil {
		return nil, err
	}
	if err := p.bindAccountToCurrentCoreNode(wallet); err != nil {
		return nil, err
	}
	client, _, err := p.messageServiceClient()
	if err != nil {
		return nil, err
	}
	accountID, err := dkvsAccountID(wallet)
	if err != nil {
		return nil, err
	}
	topicName, err = normalizeTopicID(topicName)
	if err != nil {
		return nil, err
	}
	stateRequest := &swire.MessageServiceRequest{
		Action: swire.MessageServiceActionTopicState, AccountID: accountID, TopicName: topicName,
	}
	if err := signMessageServiceQuery(wallet, stateRequest); err != nil {
		return nil, err
	}
	response, err := client.SendMessageServiceReq(stateRequest)
	if err != nil {
		return nil, err
	}
	var snapshot swire.TopicServiceSnapshot
	if response == nil || swire.DecodeTopicJSON(response.Payload, &snapshot) != nil || snapshot.Meta.TopicName != topicName {
		return nil, fmt.Errorf("invalid topic state response")
	}
	return &snapshot, nil
}

func (p *Manager) submitMessageTopicMembershipRequest(topicName, serviceCore, requestType string) error {
	wallet, err := p.messageTopicWallet()
	if err != nil {
		return err
	}
	if err := p.bindAccountToCurrentCoreNode(wallet); err != nil {
		return err
	}
	client, _, err := p.messageServiceClient()
	if err != nil {
		return err
	}
	accountID, err := dkvsAccountID(wallet)
	if err != nil {
		return err
	}
	topicName, err = normalizeTopicID(topicName)
	if err != nil {
		return err
	}
	request := &swire.TopicMembershipRequest{
		TopicName: topicName, AccountID: accountID,
		ServiceCoreNode: strings.TrimSpace(serviceCore), RequestType: requestType,
	}
	hash, err := swire.TopicMembershipRequestSigningHash(request)
	if err != nil {
		return err
	}
	request.Signature, err = signTopicHash(wallet, hash)
	if err != nil {
		return err
	}
	payload, err := swire.EncodeTopicJSON(request)
	if err != nil {
		return err
	}
	action := swire.MessageServiceActionTopicJoin
	if requestType == swire.TopicMembershipLeave {
		action = swire.MessageServiceActionTopicLeave
	}
	_, err = client.SendMessageServiceReq(&swire.MessageServiceRequest{Action: action, Payload: payload})
	return err
}

func (p *Manager) RequestMessageTopicJoin(topicName, serviceCore string) error {
	return p.submitMessageTopicMembershipRequest(topicName, serviceCore, swire.TopicMembershipJoin)
}

func (p *Manager) RequestMessageTopicLeave(topicName, serviceCore string) error {
	return p.submitMessageTopicMembershipRequest(topicName, serviceCore, swire.TopicMembershipLeave)
}

func (p *Manager) RejectMessageTopicJoin(topicName, targetAccount string) error {
	wallet, err := p.messageTopicWallet()
	if err != nil {
		return err
	}
	if err := p.bindAccountToCurrentCoreNode(wallet); err != nil {
		return err
	}
	snapshot, err := p.GetMessageTopicState(topicName)
	if err != nil {
		return err
	}
	owner, err := dkvsAccountID(wallet)
	if err != nil {
		return err
	}
	if snapshot.Meta.OwnerAccount != owner {
		return dkvsindexer.ErrPermissionDenied
	}
	rejection := &swire.TopicJoinRejection{
		TopicName: snapshot.Meta.TopicName, ServiceCoreNode: snapshot.Meta.ServiceCoreNode,
		OwnerAccount: owner, TargetAccount: strings.ToLower(strings.TrimSpace(targetAccount)),
	}
	hash, err := swire.TopicJoinRejectionSigningHash(rejection)
	if err != nil {
		return err
	}
	rejection.OwnerSignature, err = signTopicHash(wallet, hash)
	if err != nil {
		return err
	}
	payload, err := swire.EncodeTopicJSON(rejection)
	if err != nil {
		return err
	}
	client, _, err := p.messageServiceClient()
	if err != nil {
		return err
	}
	_, err = client.SendMessageServiceReq(&swire.MessageServiceRequest{
		Action: swire.MessageServiceActionTopicRejectJoin, Payload: payload,
	})
	return err
}

func topicSnapshotKeyHolders(snapshot *swire.TopicServiceSnapshot) []string {
	if snapshot == nil {
		return nil
	}
	result := make([]string, 0, snapshot.State.MemberCount)
	for _, member := range snapshot.Members {
		if member.Status == "ACTIVE" || member.Status == "PENDING_LEAVE" {
			result = append(result, member.AccountID)
		}
	}
	return result
}

func topicSnapshotMember(snapshot *swire.TopicServiceSnapshot, account string) (swire.TopicMember, bool) {
	if snapshot == nil {
		return swire.TopicMember{}, false
	}
	for _, member := range snapshot.Members {
		if member.AccountID == account {
			return member, true
		}
	}
	return swire.TopicMember{}, false
}

func topicControlOutboxKey(accountID, topicName string) []byte {
	return []byte(GetDBKeyPrefix() + topicControlOutboxPrefix + accountID + "/" + topicName)
}

func (p *Manager) loadTopicControlOutbox(accountID, topicName string) (*topicControlOutboxRecord, error) {
	if p == nil || p.db == nil {
		return nil, ErrMessageServiceUnavailable
	}
	raw, err := p.db.Read(topicControlOutboxKey(accountID, topicName))
	if errors.Is(err, indexer.ErrKeyNotFound) || err == nil && len(raw) == 0 {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var outbox topicControlOutboxRecord
	if DecodeFromBytes(raw, &outbox) != nil || outbox.Version != topicControlOutboxVersion ||
		outbox.TopicName != topicName || outbox.Commit.TopicName != topicName || len(outbox.WrappedTopicKey) == 0 {
		return nil, fmt.Errorf("invalid topic control outbox")
	}
	return &outbox, nil
}

func (p *Manager) saveTopicControlOutbox(accountID string, outbox *topicControlOutboxRecord) error {
	if p == nil || p.db == nil || outbox == nil {
		return ErrMessageServiceUnavailable
	}
	raw, err := EncodeToBytes(outbox)
	if err != nil {
		return err
	}
	return p.db.Write(topicControlOutboxKey(accountID, outbox.TopicName), raw)
}

func (p *Manager) deleteTopicControlOutbox(accountID, topicName string) error {
	if p == nil || p.db == nil {
		return nil
	}
	return p.db.Delete(topicControlOutboxKey(accountID, topicName))
}

func (p *Manager) sendPersistedTopicControl(wallet *InternalWallet, outbox *topicControlOutboxRecord) (*swire.TopicMembershipCommit, error) {
	if p == nil || wallet == nil || outbox == nil {
		return nil, ErrMessageServiceUnavailable
	}
	accountID, err := dkvsAccountID(wallet)
	if err != nil {
		return nil, err
	}
	client, coreID, err := p.messageServiceClient()
	if err != nil {
		return nil, err
	}
	// First-release SDK coordinates rotations only while connected to the Topic
	// Service CoreNode, so an HTTP success means CommitMembershipChange was
	// applied locally rather than merely queued for remote forwarding. The
	// server protocol itself still permits a remaining ACTIVE member elsewhere
	// to submit a REMOVE commit.
	if coreID != outbox.Commit.ServiceCoreNode {
		return nil, fmt.Errorf("topic rotation coordinator must be connected to Topic Service CoreNode")
	}
	payload, err := swire.EncodeTopicJSON(&outbox.Commit)
	if err != nil {
		return nil, err
	}
	if _, err := client.SendMessageServiceReq(&swire.MessageServiceRequest{
		Action: swire.MessageServiceActionTopicCommit, Payload: payload,
	}); err != nil {
		commit := outbox.Commit
		return &commit, err
	}
	newKey, err := wallet.DecryptFromAccount(accountID, outbox.WrappedTopicKey)
	if err != nil || len(newKey) != topicKeySize {
		return nil, fmt.Errorf("invalid pending topic key")
	}
	cryptoManager, err := NewTopicCryptoManager(p, wallet)
	if err != nil {
		return nil, err
	}
	if err := cryptoManager.StoreTopicKey(outbox.TopicName, outbox.Commit.NewKeySeq, newKey); err != nil {
		return nil, err
	}
	if err := p.deleteTopicControlOutbox(accountID, outbox.TopicName); err != nil {
		Log.Warnf("delete committed topic control outbox %s/%s failed: %v", accountID, outbox.TopicName, err)
	}
	commit := outbox.Commit
	return &commit, nil
}

func (p *Manager) commitMessageTopicChange(topicName, changeType, targetAccount string) (*swire.TopicMembershipCommit, error) {
	wallet, err := p.messageTopicWallet()
	if err != nil {
		return nil, err
	}
	if err := p.bindAccountToCurrentCoreNode(wallet); err != nil {
		return nil, err
	}
	issuer, err := dkvsAccountID(wallet)
	if err != nil {
		return nil, err
	}
	topicName, err = normalizeTopicID(topicName)
	if err != nil {
		return nil, err
	}
	targetAccount = strings.ToLower(strings.TrimSpace(targetAccount))
	if pending, err := p.loadTopicControlOutbox(issuer, topicName); err != nil {
		return nil, err
	} else if pending != nil {
		if pending.ChangeType != changeType || pending.TargetAccount != targetAccount {
			return nil, fmt.Errorf("another topic membership change is pending")
		}
		return p.sendPersistedTopicControl(wallet, pending)
	}

	snapshot, err := p.GetMessageTopicState(topicName)
	if err != nil {
		return nil, err
	}
	_, localCore, err := p.messageServiceClient()
	if err != nil {
		return nil, err
	}
	if localCore != snapshot.Meta.ServiceCoreNode {
		return nil, fmt.Errorf("topic rotation coordinator must be connected to Topic Service CoreNode")
	}
	issuerMember, issuerOK := topicSnapshotMember(snapshot, issuer)
	if !issuerOK || issuerMember.Status != "ACTIVE" {
		return nil, dkvsindexer.ErrPermissionDenied
	}
	holders := topicSnapshotKeyHolders(snapshot)
	switch changeType {
	case swire.TopicMembershipAdd:
		if issuer != snapshot.Meta.OwnerAccount {
			return nil, dkvsindexer.ErrPermissionDenied
		}
		target, ok := topicSnapshotMember(snapshot, targetAccount)
		if !ok || target.Status != "PENDING_JOIN" {
			return nil, fmt.Errorf("target is not pending join")
		}
		holders = append(holders, targetAccount)
	case swire.TopicMembershipRemove:
		target, ok := topicSnapshotMember(snapshot, targetAccount)
		if !ok || target.Status != "PENDING_LEAVE" || targetAccount == issuer {
			return nil, fmt.Errorf("target is not pending leave")
		}
		filtered := holders[:0]
		for _, holder := range holders {
			if holder != targetAccount {
				filtered = append(filtered, holder)
			}
		}
		holders = filtered
	case swire.TopicMembershipKick, swire.TopicMembershipBan:
		if issuer != snapshot.Meta.OwnerAccount {
			return nil, dkvsindexer.ErrPermissionDenied
		}
		target, ok := topicSnapshotMember(snapshot, targetAccount)
		if !ok || target.Role == "OWNER" || (target.Status != "ACTIVE" && target.Status != "PENDING_LEAVE") {
			return nil, fmt.Errorf("target is not removable")
		}
		filtered := holders[:0]
		for _, holder := range holders {
			if holder != targetAccount {
				filtered = append(filtered, holder)
			}
		}
		holders = filtered
	case swire.TopicMembershipRotate:
		if issuer != snapshot.Meta.OwnerAccount || targetAccount != "" {
			return nil, dkvsindexer.ErrPermissionDenied
		}
	default:
		return nil, fmt.Errorf("unsupported topic membership change")
	}
	if len(holders) == 0 {
		return nil, fmt.Errorf("topic would have no key holders")
	}
	newKey, err := generateTopicKey()
	if err != nil {
		return nil, err
	}
	cryptoManager, err := NewTopicCryptoManager(p, wallet)
	if err != nil {
		return nil, err
	}
	newKeySeq := snapshot.State.KeySeq + 1
	packages, err := cryptoManager.CreateKeyPackages(snapshot.Meta.TopicName, newKeySeq, newKey, holders)
	if err != nil {
		return nil, err
	}
	commit := swire.TopicMembershipCommit{
		TopicName: snapshot.Meta.TopicName, ServiceCoreNode: snapshot.Meta.ServiceCoreNode,
		IssuerAccount: issuer, BaseKeySeq: snapshot.State.KeySeq, NewKeySeq: newKeySeq,
		ChangeType: changeType, TargetAccount: targetAccount, KeyPackages: packages,
	}
	hash, err := swire.TopicMembershipCommitSigningHash(&commit)
	if err != nil {
		return nil, err
	}
	commit.IssuerSignature, err = signTopicHash(wallet, hash)
	if err != nil {
		return nil, err
	}
	wrapped, err := wallet.EncryptToAccount(issuer, newKey)
	if err != nil {
		return nil, err
	}
	outbox := &topicControlOutboxRecord{
		Version: topicControlOutboxVersion, TopicName: topicName,
		ChangeType: changeType, TargetAccount: targetAccount, Commit: commit, WrappedTopicKey: wrapped,
	}
	if err := p.saveTopicControlOutbox(issuer, outbox); err != nil {
		return nil, err
	}
	return p.sendPersistedTopicControl(wallet, outbox)
}

func (p *Manager) ApproveMessageTopicJoin(topicName, targetAccount string) (*swire.TopicMembershipCommit, error) {
	return p.commitMessageTopicChange(topicName, swire.TopicMembershipAdd, targetAccount)
}

func (p *Manager) FinalizeMessageTopicLeave(topicName, targetAccount string) (*swire.TopicMembershipCommit, error) {
	return p.commitMessageTopicChange(topicName, swire.TopicMembershipRemove, targetAccount)
}

func (p *Manager) KickMessageTopicMember(topicName, targetAccount string) (*swire.TopicMembershipCommit, error) {
	return p.commitMessageTopicChange(topicName, swire.TopicMembershipKick, targetAccount)
}

func (p *Manager) BanMessageTopicMember(topicName, targetAccount string) (*swire.TopicMembershipCommit, error) {
	return p.commitMessageTopicChange(topicName, swire.TopicMembershipBan, targetAccount)
}

func (p *Manager) RotateMessageTopicKey(topicName string) (*swire.TopicMembershipCommit, error) {
	return p.commitMessageTopicChange(topicName, swire.TopicMembershipRotate, "")
}

func topicMessageOutboxKey(senderAccount, applicationID string) []byte {
	return []byte(GetDBKeyPrefix() + topicMessageOutboxPrefix + senderAccount + "/" + applicationID)
}

func (p *Manager) loadTopicMessageOutbox(senderAccount, applicationID string) (*topicMessageOutboxRecord, error) {
	if p == nil || p.db == nil || !validMessageApplicationID(applicationID) {
		return nil, ErrMessageServiceUnavailable
	}
	raw, err := p.db.Read(topicMessageOutboxKey(senderAccount, applicationID))
	if errors.Is(err, indexer.ErrKeyNotFound) || err == nil && len(raw) == 0 {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var outbox topicMessageOutboxRecord
	if DecodeFromBytes(raw, &outbox) != nil || outbox.Version != topicMessageOutboxVersion ||
		outbox.ApplicationID != applicationID || outbox.Publish.SenderAccount != senderAccount || strings.TrimSpace(outbox.SourceCoreNode) == "" {
		return nil, fmt.Errorf("invalid topic message outbox")
	}
	return &outbox, nil
}

func (p *Manager) saveTopicMessageOutbox(senderAccount string, outbox *topicMessageOutboxRecord) error {
	if p == nil || p.db == nil || outbox == nil {
		return ErrMessageServiceUnavailable
	}
	raw, err := EncodeToBytes(outbox)
	if err != nil {
		return err
	}
	return p.db.Write(topicMessageOutboxKey(senderAccount, outbox.ApplicationID), raw)
}

func (p *Manager) deleteTopicMessageOutbox(senderAccount, applicationID string) error {
	if p == nil || p.db == nil {
		return nil
	}
	return p.db.Delete(topicMessageOutboxKey(senderAccount, applicationID))
}

func (p *Manager) sendPersistedTopicMessage(outbox *topicMessageOutboxRecord) (*swire.TopicPublishMessage, error) {
	client, coreID, err := p.messageServiceClient()
	if err != nil {
		return nil, err
	}
	if outbox == nil || outbox.SourceCoreNode != coreID {
		return nil, fmt.Errorf("topic outbox belongs to a different CoreNode")
	}
	payload, err := swire.SerializeTopicPublishMessage(&outbox.Publish, true)
	if err != nil {
		return nil, err
	}
	if _, err := client.SendMessageServiceReq(&swire.MessageServiceRequest{
		Action: swire.MessageServiceActionTopicPublish, TargetCoreNode: outbox.ServiceCoreNode, Payload: payload,
	}); err != nil {
		return &outbox.Publish, err
	}
	if err := p.deleteTopicMessageOutbox(outbox.Publish.SenderAccount, outbox.ApplicationID); err != nil {
		Log.Warnf("delete accepted topic outbox %s/%s failed: %v", outbox.Publish.SenderAccount, outbox.ApplicationID, err)
	}
	message := outbox.Publish
	return &message, nil
}

func (p *Manager) PublishMessageTopic(applicationID, topicName, serviceCore string, plaintext []byte) (*swire.TopicPublishMessage, error) {
	if !validMessageApplicationID(applicationID) || len(plaintext) == 0 || strings.TrimSpace(serviceCore) == "" {
		return nil, fmt.Errorf("invalid topic publish request")
	}
	wallet, err := p.messageTopicWallet()
	if err != nil {
		return nil, err
	}
	p.messageSendMu.Lock()
	defer p.messageSendMu.Unlock()
	if err := p.bindAccountToCurrentCoreNode(wallet); err != nil {
		return nil, err
	}
	sender, err := dkvsAccountID(wallet)
	if err != nil {
		return nil, err
	}
	topicName, err = normalizeTopicID(topicName)
	if err != nil {
		return nil, err
	}
	existing, err := p.loadTopicMessageOutbox(sender, applicationID)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		if existing.TopicName != topicName || existing.ServiceCoreNode != serviceCore {
			return nil, fmt.Errorf("topic application id already used")
		}
		return p.sendPersistedTopicMessage(existing)
	}
	cryptoManager, err := NewTopicCryptoManager(p, wallet)
	if err != nil {
		return nil, err
	}
	keySeq, err := cryptoManager.CurrentTopicKeySeq(topicName)
	if err != nil {
		return nil, err
	}
	client, sourceCore, err := p.messageServiceClient()
	if err != nil {
		return nil, err
	}
	nextRequest := &swire.MessageServiceRequest{
		Action: swire.MessageServiceActionNextMessage, AccountID: sender,
	}
	if err := signMessageServiceQuery(wallet, nextRequest); err != nil {
		return nil, err
	}
	next, err := client.SendMessageServiceReq(nextRequest)
	if err != nil {
		return nil, err
	}
	messageID, err := newAccountMessageID()
	if err != nil {
		return nil, err
	}
	publish, err := cryptoManager.EncryptPublish(topicName, keySeq, next.NextSenderMsgID, messageID, plaintext)
	if err != nil {
		return nil, err
	}
	outbox := &topicMessageOutboxRecord{
		Version: topicMessageOutboxVersion, ApplicationID: applicationID,
		TopicName: topicName, ServiceCoreNode: serviceCore, SourceCoreNode: sourceCore, Publish: *publish,
	}
	if err := p.saveTopicMessageOutbox(sender, outbox); err != nil {
		return nil, err
	}
	return p.sendPersistedTopicMessage(outbox)
}

func (p *Manager) RetryMessageTopicPublish(applicationID string) (*swire.TopicPublishMessage, error) {
	wallet, err := p.messageTopicWallet()
	if err != nil {
		return nil, err
	}
	sender, err := dkvsAccountID(wallet)
	if err != nil {
		return nil, err
	}
	p.messageSendMu.Lock()
	defer p.messageSendMu.Unlock()
	outbox, err := p.loadTopicMessageOutbox(sender, applicationID)
	if err != nil || outbox == nil {
		return nil, err
	}
	return p.sendPersistedTopicMessage(outbox)
}

func validateTopicMailboxOuter(accountID string, record *swire.DKVSRecord, topicName, kind string) (dkvsindexer.ParsedKey, error) {
	var empty dkvsindexer.ParsedKey
	if record == nil || record.Version != dkvsindexer.Version || record.Seq != 1 || len(record.Value) == 0 ||
		len(record.PubKey) != 0 || len(record.Signature) != 0 || len(record.FeeProof) != 0 || record.Flags != 0 {
		return empty, dkvsindexer.ErrInvalidRecord
	}
	parsed, err := dkvsindexer.ParseKey(record.Key)
	if err != nil || parsed.Namespace != "mail" || len(parsed.Segments) < 4 || parsed.Segments[0] != accountID ||
		parsed.Segments[1] != "topic" || parsed.Segments[2] != topicName || parsed.Segments[3] != kind {
		return empty, dkvsindexer.ErrInvalidRecord
	}
	return parsed, nil
}

func (p *Manager) SyncMessageTopicKeys(topicName string) (int, error) {
	wallet, err := p.messageTopicWallet()
	if err != nil {
		return 0, err
	}
	accountID, err := dkvsAccountID(wallet)
	if err != nil {
		return 0, err
	}
	topicName, err = normalizeTopicID(topicName)
	if err != nil {
		return 0, err
	}
	store, err := p.accountDKVSStore()
	if err != nil {
		return 0, err
	}
	// An explicit topic-key sync must first compare the managed mailbox
	// generation with the CoreNode. Reads normally use the local snapshot, but
	// this operation is the application-level request to refresh it.
	if err := store.manager.ensureCurrentSubscription(store.client); err != nil {
		return 0, err
	}
	prefix := "/mail/" + accountID + "/topic/" + topicName + "/key"
	records, _, err := store.client.ListRecords(prefix, 0, 0)
	if err != nil {
		return 0, err
	}
	cryptoManager, err := NewTopicCryptoManager(p, wallet)
	if err != nil {
		return 0, err
	}
	accepted := 0
	for _, record := range records {
		parsed, err := validateTopicMailboxOuter(accountID, record, topicName, "key")
		if err != nil || len(parsed.Segments) != 5 {
			return accepted, dkvsindexer.ErrInvalidRecord
		}
		fanout, err := swire.DeserializeTopicKeyFanoutMessage(record.Value)
		if err != nil || fanout.TopicName != topicName || len(fanout.Recipients) != 1 ||
			strconv.FormatUint(fanout.KeySeq, 10) != parsed.Segments[4] {
			return accepted, dkvsindexer.ErrInvalidRecord
		}
		if err := cryptoManager.AcceptKeyPackage(fanout); err != nil {
			return accepted, err
		}
		accepted++
	}
	return accepted, nil
}

func (p *Manager) ReadMessageTopicMessages(topicName string, start, limit int) ([]*TopicMessage, int, error) {
	wallet, err := p.messageTopicWallet()
	if err != nil {
		return nil, 0, err
	}
	accountID, err := dkvsAccountID(wallet)
	if err != nil {
		return nil, 0, err
	}
	topicName, err = normalizeTopicID(topicName)
	if err != nil {
		return nil, 0, err
	}
	if _, err := p.SyncMessageTopicKeys(topicName); err != nil {
		return nil, 0, err
	}
	store, err := p.accountDKVSStore()
	if err != nil {
		return nil, 0, err
	}
	prefix := "/mail/" + accountID + "/topic/" + topicName + "/msg"
	records, total, err := store.client.ListRecords(prefix, start, limit)
	if err != nil {
		return nil, 0, err
	}
	cryptoManager, err := NewTopicCryptoManager(p, wallet)
	if err != nil {
		return nil, 0, err
	}
	result := make([]*TopicMessage, 0, len(records))
	for _, record := range records {
		parsed, err := validateTopicMailboxOuter(accountID, record, topicName, "msg")
		if err != nil || len(parsed.Segments) != 6 {
			return nil, 0, dkvsindexer.ErrInvalidRecord
		}
		publish, err := swire.DeserializeTopicPublishMessage(record.Value)
		if err != nil || publish.TopicName != topicName || publish.SenderAccount != parsed.Segments[4] ||
			publish.MessageID != parsed.Segments[5] {
			return nil, 0, dkvsindexer.ErrInvalidRecord
		}
		plaintext, err := cryptoManager.DecryptPublish(publish)
		if err != nil {
			return nil, 0, err
		}
		result = append(result, &TopicMessage{Publish: publish, Plaintext: plaintext, Record: record})
	}
	return result, total, nil
}
