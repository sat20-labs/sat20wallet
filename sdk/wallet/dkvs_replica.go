package wallet

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

const dkvsReplicaVersion = byte(1)

var (
	dkvsReplicaConfirmedPrefix = []byte("dkvs-replica-confirmed-v1:")
	dkvsReplicaOutboxPrefix    = []byte("dkvs-replica-outbox-v1:")
	dkvsReplicaRootPrefix      = []byte("dkvs-replica-root-v1:")
)

type dkvsReplicaStore struct {
	db indexer.KVDB
}

func newDKVSReplicaStore(db indexer.KVDB) *dkvsReplicaStore {
	return &dkvsReplicaStore{db: db}
}

func dkvsReplicaScope(namespace string, filters []dkvsindexer.Subscription) string {
	normalized := append([]dkvsindexer.Subscription(nil), filters...)
	sort.Slice(normalized, func(i, j int) bool {
		if normalized[i].Type == normalized[j].Type {
			return normalized[i].Target < normalized[j].Target
		}
		return normalized[i].Type < normalized[j].Type
	})
	var builder strings.Builder
	builder.WriteString(strings.TrimSpace(namespace))
	for _, filter := range normalized {
		builder.WriteByte(0)
		builder.WriteString(string(filter.Type))
		builder.WriteByte(':')
		builder.WriteString(strings.TrimSpace(filter.Target))
	}
	hash := sha256.Sum256([]byte(builder.String()))
	return hex.EncodeToString(hash[:])
}

func dkvsReplicaPrefix(base []byte, scope string) []byte {
	key := make([]byte, 0, len(base)+len(scope)+1)
	key = append(key, base...)
	key = append(key, scope...)
	key = append(key, ':')
	return key
}

func dkvsReplicaRecordKey(base []byte, scope, recordKey string) []byte {
	prefix := dkvsReplicaPrefix(base, scope)
	hash := sha256.Sum256([]byte(recordKey))
	return append(prefix, hash[:]...)
}

func (s *dkvsReplicaStore) loadRecords(base []byte, scope string) ([]*swire.DKVSRecord, error) {
	if s == nil || s.db == nil || scope == "" {
		return nil, fmt.Errorf("DKVS replica store is unavailable")
	}
	prefix := dkvsReplicaPrefix(base, scope)
	records := make([]*swire.DKVSRecord, 0)
	err := s.db.BatchRead(prefix, false, func(_, value []byte) error {
		record, err := dkvsindexer.UnmarshalRecord(value)
		if err != nil {
			return err
		}
		records = append(records, record)
		return nil
	})
	sort.Slice(records, func(i, j int) bool { return records[i].Key < records[j].Key })
	return records, err
}

func (s *dkvsReplicaStore) loadConfirmed(scope string) ([]*swire.DKVSRecord, error) {
	return s.loadRecords(dkvsReplicaConfirmedPrefix, scope)
}

func (s *dkvsReplicaStore) loadOutbox(scope string) ([]*swire.DKVSRecord, error) {
	return s.loadRecords(dkvsReplicaOutboxPrefix, scope)
}

func (s *dkvsReplicaStore) loadRoot(scope string) (string, error) {
	if s == nil || s.db == nil || scope == "" {
		return "", fmt.Errorf("DKVS replica store is unavailable")
	}
	value, err := s.db.Read(append(append([]byte(nil), dkvsReplicaRootPrefix...), scope...))
	if err != nil {
		return "", err
	}
	if len(value) != 1+chainhash.HashSize || value[0] != dkvsReplicaVersion {
		return "", dkvsindexer.ErrInvalidRecord
	}
	var root chainhash.Hash
	copy(root[:], value[1:])
	return root.String(), nil
}

func recordMatchesAnyFilter(record *swire.DKVSRecord, filters []dkvsindexer.Subscription) bool {
	if record == nil {
		return false
	}
	for _, filter := range filters {
		if dkvsindexer.SubscriptionMatchesKey(filter, record.Key) {
			return true
		}
	}
	return false
}

func (s *dkvsReplicaStore) applyConfirmed(scope string, filters []dkvsindexer.Subscription,
	records []*swire.DKVSRecord, rootText string) error {

	if s == nil || s.db == nil || scope == "" || len(filters) == 0 {
		return fmt.Errorf("invalid DKVS replica mirror")
	}
	root, err := chainhash.NewHashFromStr(strings.TrimSpace(rootText))
	if err != nil {
		return dkvsindexer.ErrInvalidCheckpoint
	}
	incoming := make(map[string]*swire.DKVSRecord, len(records))
	now := uint64(time.Now().UnixMilli())
	for _, record := range records {
		if !recordMatchesAnyFilter(record, filters) {
			return dkvsindexer.ErrInvalidKey
		}
		if err := dkvsindexer.VerifyRecordForClient(record,
			dkvsindexer.RecordVerificationOptions{Now: now}); err != nil {
			return err
		}
		if previous := incoming[record.Key]; previous != nil &&
			dkvsindexer.RecordHash(previous) != dkvsindexer.RecordHash(record) {
			return fmt.Errorf("conflicting DKVS mirror record %s", record.Key)
		}
		incoming[record.Key] = record
	}
	existing, err := s.loadConfirmed(scope)
	if err != nil {
		return err
	}
	batch := s.db.NewWriteBatch()
	defer batch.Close()
	for _, record := range existing {
		if err := batch.Delete(dkvsReplicaRecordKey(dkvsReplicaConfirmedPrefix, scope, record.Key)); err != nil {
			return err
		}
	}
	for _, record := range incoming {
		encoded, err := dkvsindexer.MarshalRecord(record)
		if err != nil {
			return err
		}
		if err := batch.Put(dkvsReplicaRecordKey(dkvsReplicaConfirmedPrefix, scope, record.Key), encoded); err != nil {
			return err
		}
	}
	rootValue := make([]byte, 1+chainhash.HashSize)
	rootValue[0] = dkvsReplicaVersion
	copy(rootValue[1:], root[:])
	if err := batch.Put(append(append([]byte(nil), dkvsReplicaRootPrefix...), scope...), rootValue); err != nil {
		return err
	}
	return batch.Flush()
}

func (s *dkvsReplicaStore) queueOutbox(scope string, record *swire.DKVSRecord) error {
	if s == nil || s.db == nil || scope == "" {
		return fmt.Errorf("DKVS replica store is unavailable")
	}
	if err := dkvsindexer.VerifyRecordForClient(record,
		dkvsindexer.RecordVerificationOptions{Now: uint64(time.Now().UnixMilli())}); err != nil {
		return err
	}
	key := dkvsReplicaRecordKey(dkvsReplicaOutboxPrefix, scope, record.Key)
	if encoded, err := s.db.Read(key); err == nil {
		existing, decodeErr := dkvsindexer.UnmarshalRecord(encoded)
		if decodeErr != nil {
			return decodeErr
		}
		if dkvsindexer.CompareRecords(record, existing) <= 0 {
			return nil
		}
	} else if !errors.Is(err, indexer.ErrKeyNotFound) {
		return err
	}
	encoded, err := dkvsindexer.MarshalRecord(record)
	if err != nil {
		return err
	}
	return s.db.Write(key, encoded)
}

func (s *dkvsReplicaStore) acknowledgeOutbox(scope string, record *swire.DKVSRecord) error {
	if s == nil || s.db == nil || scope == "" || record == nil {
		return fmt.Errorf("invalid DKVS outbox acknowledgement")
	}
	return s.db.Delete(dkvsReplicaRecordKey(dkvsReplicaOutboxPrefix, scope, record.Key))
}
