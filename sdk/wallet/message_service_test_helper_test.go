package wallet

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/sat20-labs/satoshinet/btcec"
	"github.com/sat20-labs/satoshinet/btcec/schnorr"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

type rgb11MessageServiceState struct {
	mu       sync.Mutex
	next     map[string]uint64
	accepted map[string][]byte
}

var rgb11MessageServiceStates sync.Map

type rgb11MessageNodeClient struct {
	NodeRPCClient
	remote *rgb11MemoryDKVSHTTP
	state  *rgb11MessageServiceState
}

func newRGB11MessageNodeClient(remote *rgb11MemoryDKVSHTTP) *rgb11MessageNodeClient {
	state := &rgb11MessageServiceState{next: make(map[string]uint64), accepted: make(map[string][]byte)}
	if remote != nil {
		if existing, loaded := rgb11MessageServiceStates.LoadOrStore(remote, state); loaded {
			state = existing.(*rgb11MessageServiceState)
		}
	}
	return &rgb11MessageNodeClient{remote: remote, state: state}
}

func rgb11MessageCorePrivateKey() *secp256k1.PrivateKey {
	seed := make([]byte, 32)
	seed[31] = 7
	return secp256k1.PrivKeyFromBytes(seed)
}

func (c *rgb11MessageNodeClient) CoreNodePubKey() *secp256k1.PublicKey {
	return rgb11MessageCorePrivateKey().PubKey()
}

func (c *rgb11MessageNodeClient) CoreNodeID() string {
	return hex.EncodeToString(c.CoreNodePubKey().SerializeCompressed())
}

func (c *rgb11MessageNodeClient) accountBound(accountID string) bool {
	if c == nil || c.remote == nil {
		return false
	}
	c.remote.mu.Lock()
	records := make([]*swire.DKVSRecord, 0, len(c.remote.records))
	for _, record := range c.remote.records {
		records = append(records, record)
	}
	c.remote.mu.Unlock()
	for _, record := range records {
		_, _, descriptor, err := dkvsindexer.ValidateAccountMappingBindingRecord(record)
		if err == nil && descriptor.AccountID == accountID && descriptor.CoreNodeID == c.CoreNodeID() {
			return true
		}
	}
	return false
}

func verifyRGB11TestDirect(message *swire.DirectMessage) ([]byte, error) {
	if message == nil {
		return nil, fmt.Errorf("missing direct message")
	}
	encoded, err := swire.SerializeDirectMessage(message, true)
	if err != nil {
		return nil, err
	}
	hash, err := swire.DirectMessageSigningHash(message)
	if err != nil {
		return nil, err
	}
	pubBytes, err := dkvsindexer.AccountPubKey(message.SenderAccount)
	if err != nil {
		return nil, err
	}
	pub, err := btcec.ParsePubKey(pubBytes)
	if err != nil {
		return nil, err
	}
	sig, err := schnorr.ParseSignature(message.SenderSignature)
	if err != nil || !sig.Verify(hash[:], pub) {
		return nil, fmt.Errorf("invalid direct signature")
	}
	return encoded, nil
}

func verifyMessageServiceTestQuery(req *swire.MessageServiceRequest) error {
	if req == nil || req.Auth == nil || req.Auth.ExpiresAtMS <= uint64(time.Now().UnixMilli()) {
		return fmt.Errorf("missing message query authorization")
	}
	hash, err := swire.MessageServiceQuerySigningHash(req)
	if err != nil {
		return err
	}
	pubBytes, err := dkvsindexer.AccountPubKey(req.AccountID)
	if err != nil {
		return err
	}
	pub, err := btcec.ParsePubKey(pubBytes)
	if err != nil {
		return err
	}
	signature, err := schnorr.ParseSignature(req.Auth.Signature)
	if err != nil || !signature.Verify(hash[:], pub) {
		return fmt.Errorf("invalid message query authorization")
	}
	return nil
}

func (c *rgb11MessageNodeClient) SendMessageServiceReq(req *swire.MessageServiceRequest) (*swire.MessageServiceResponse, error) {
	if c == nil || c.remote == nil || c.state == nil || req == nil {
		return nil, fmt.Errorf("message service unavailable")
	}
	switch req.Action {
	case swire.MessageServiceActionBindAccount:
		_, _, descriptor, err := dkvsindexer.ValidateAccountMappingBindingRecord(req.Record)
		if err != nil || descriptor.CoreNodeID != c.CoreNodeID() {
			return nil, fmt.Errorf("invalid account binding")
		}
		key := req.Record.Key
		c.remote.mu.Lock()
		current := c.remote.records[key]
		if current != nil && req.Record.Seq < current.Seq {
			c.remote.mu.Unlock()
			return nil, fmt.Errorf("stale account binding")
		}
		c.remote.records[key] = cloneRGB11DKVSRecord(req.Record)
		c.remote.mu.Unlock()
		return &swire.MessageServiceResponse{}, nil

	case swire.MessageServiceActionNextMessage:
		if verifyMessageServiceTestQuery(req) != nil || !c.accountBound(req.AccountID) {
			return nil, fmt.Errorf("sender not bound")
		}
		c.state.mu.Lock()
		next := c.state.next[req.AccountID]
		c.state.mu.Unlock()
		return &swire.MessageServiceResponse{NextSenderMsgID: next}, nil

	case swire.MessageServiceActionSendDirect:
		encoded, err := verifyRGB11TestDirect(req.Direct)
		if err != nil {
			return nil, err
		}
		if !c.accountBound(req.Direct.SenderAccount) || !c.accountBound(req.Direct.RecipientAccount) {
			return nil, fmt.Errorf("message account not bound")
		}
		identity := req.Direct.SenderAccount + ":" + req.Direct.MessageID
		c.state.mu.Lock()
		if previous := c.state.accepted[identity]; previous != nil {
			if !bytes.Equal(previous, encoded) {
				c.state.mu.Unlock()
				return nil, fmt.Errorf("sender message id reused")
			}
			c.state.mu.Unlock()
			return &swire.MessageServiceResponse{}, nil
		}
		if req.Direct.SenderMsgID != c.state.next[req.Direct.SenderAccount] {
			c.state.mu.Unlock()
			return nil, fmt.Errorf("unexpected sender message id")
		}
		c.state.accepted[identity] = append([]byte(nil), encoded...)
		c.state.next[req.Direct.SenderAccount]++
		c.state.mu.Unlock()

		key, err := dkvsindexer.MailMsgKey(
			req.Direct.RecipientAccount, req.Direct.SenderAccount,
			req.Direct.MessageID,
		)
		if err != nil {
			return nil, err
		}
		record := &swire.DKVSRecord{
			Version: dkvsindexer.Version, Key: key, Value: encoded, Seq: 1,
			IssueHeight: 1, TTL: testRGB11FreeLocalTTL,
		}
		proof, err := dkvsindexer.NewFreeLocalFeeProof(
			record.Key, "mail", uint32(dkvsindexer.RecordSize(record)),
			record.IssueHeight+record.TTL,
		)
		if err != nil {
			return nil, err
		}
		record.FeeProof, err = dkvsindexer.EncodeFeeProof(proof)
		if err != nil {
			return nil, err
		}
		c.remote.mu.Lock()
		if existing := c.remote.records[key]; existing != nil &&
			dkvsindexer.RecordHash(existing) != dkvsindexer.RecordHash(record) {
			c.remote.mu.Unlock()
			return nil, fmt.Errorf("mailbox direct record conflict")
		}
		// Match the core's collection generation update so an already synced
		// receiver observes subsequent direct messages, including batch ACKs.
		if c.remote.records[key] == nil {
			if prefix, err := dkvsindexer.CollectionPathForKey(key); err == nil {
				c.remote.generations[prefix]++
			}
		}
		c.remote.records[key] = cloneRGB11DKVSRecord(record)
		c.remote.mu.Unlock()
		return &swire.MessageServiceResponse{}, nil

	case swire.MessageServiceActionDeleteMailbox:
		if req.Record == nil {
			return nil, fmt.Errorf("missing mailbox tombstone")
		}
		parsed, err := dkvsindexer.ParseKey(req.Record.Key)
		if err != nil || parsed.Namespace != "mail" || len(parsed.Segments) == 0 ||
			!c.accountBound(parsed.Segments[0]) {
			return nil, fmt.Errorf("mailbox is not bound")
		}
		if err := dkvsindexer.VerifyAccountRecordForClient(req.Record,
			dkvsindexer.RecordVerificationOptions{ExpectedKey: req.Record.Key}); err != nil {
			return nil, err
		}
		c.remote.mu.Lock()
		current := c.remote.records[req.Record.Key]
		if current == nil || current.Seq == ^uint64(0) || req.Record.Seq != current.Seq+1 {
			c.remote.mu.Unlock()
			return nil, fmt.Errorf("stale mailbox tombstone")
		}
		delete(c.remote.records, req.Record.Key)
		c.remote.mu.Unlock()
		return &swire.MessageServiceResponse{}, nil
	default:
		return nil, fmt.Errorf("unsupported message action %s", req.Action)
	}
}
