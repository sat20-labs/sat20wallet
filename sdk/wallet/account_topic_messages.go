package wallet

import (
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
	"strconv"
)

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

// Account management owns mailbox subscription and record validation. Topic
// business code receives only domain messages, never transport records.
func (p *Manager) readAccountTopicKeys(accountID, topic string) ([]*swire.TopicKeyFanoutMessage, error) {
	store, err := p.ensureDKVSManager().primaryStore()
	if err != nil {
		return nil, err
	}
	if err := store.manager.ensureCurrentSubscription(store.client); err != nil {
		return nil, err
	}
	records, _, err := store.client.ListRecords("/mail/"+accountID+"/topic/"+topic+"/key", 0, 0)
	if err != nil {
		return nil, err
	}
	result := make([]*swire.TopicKeyFanoutMessage, 0, len(records))
	for _, record := range records {
		parsed, err := validateTopicMailboxOuter(accountID, record, topic, "key")
		if err != nil || len(parsed.Segments) != 5 {
			return result, dkvsindexer.ErrInvalidRecord
		}
		fanout, err := swire.DeserializeTopicKeyFanoutMessage(record.Value)
		if err != nil || fanout.TopicName != topic || len(fanout.Recipients) != 1 ||
			strconv.FormatUint(fanout.KeySeq, 10) != parsed.Segments[4] {
			return result, dkvsindexer.ErrInvalidRecord
		}
		result = append(result, fanout)
	}
	return result, nil
}

func (p *Manager) readAccountTopicMessages(accountID, topic string, start, limit int) ([]*swire.TopicPublishMessage, int, error) {
	store, err := p.ensureDKVSManager().primaryStore()
	if err != nil {
		return nil, 0, err
	}
	records, total, err := store.client.ListRecords("/mail/"+accountID+"/topic/"+topic+"/msg", start, limit)
	if err != nil {
		return nil, 0, err
	}
	result := make([]*swire.TopicPublishMessage, 0, len(records))
	for _, record := range records {
		parsed, err := validateTopicMailboxOuter(accountID, record, topic, "msg")
		if err != nil || len(parsed.Segments) != 6 {
			return nil, 0, dkvsindexer.ErrInvalidRecord
		}
		publish, err := swire.DeserializeTopicPublishMessage(record.Value)
		if err != nil || publish.TopicName != topic || publish.SenderAccount != parsed.Segments[4] ||
			publish.MessageID != parsed.Segments[5] {
			return nil, 0, dkvsindexer.ErrInvalidRecord
		}
		result = append(result, publish)
	}
	return result, total, nil
}
