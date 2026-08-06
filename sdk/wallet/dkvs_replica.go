package wallet

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

const dkvsReplicaVersion = byte(2)

var (
	dkvsReplicaConfirmedPrefix = []byte("dkvs-replica-confirmed-v1:")
	dkvsReplicaRootPrefix      = []byte("dkvs-replica-root-v1:")
)

type dkvsReplicaStore struct {
	db indexer.KVDB
}

type dkvsReplicaBaseline struct {
	DirectoryRoot chainhash.Hash
	ActiveRoot    chainhash.Hash
	Generation    uint64
}

func newDKVSReplicaStore(db indexer.KVDB) *dkvsReplicaStore {
	return &dkvsReplicaStore{db: db}
}

func dkvsReplicaScope(namespace string, filters []dkvsindexer.Subscription) string {
	normalized := append([]dkvsindexer.Subscription(nil), filters...)
	for index := range normalized {
		normalized[index].Target = strings.TrimSpace(normalized[index].Target)
		// A PathLocalOnly canonical path is also its exact key. Normalize a
		// prefix registration to the exact-key scope so readiness, confirmed
		// reads and watches all address the same endpoint-local replica.
		if normalized[index].Type == dkvsindexer.SubscriptionPrefix {
			if mode, err := dkvsindexer.PathModeForKey(normalized[index].Target); err == nil &&
				mode == dkvsindexer.PathLocalOnly {
				normalized[index].Type = dkvsindexer.SubscriptionKey
			}
		}
	}
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
		builder.WriteString(filter.Target)
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

func (s *dkvsReplicaStore) loadBaseline(scope string) (*dkvsReplicaBaseline, error) {
	if s == nil || s.db == nil || scope == "" {
		return nil, fmt.Errorf("DKVS replica store is unavailable")
	}
	value, err := s.db.Read(append(append([]byte(nil), dkvsReplicaRootPrefix...), scope...))
	if err != nil {
		return nil, err
	}
	if len(value) != 1+2*chainhash.HashSize+8 || value[0] != dkvsReplicaVersion {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	baseline := &dkvsReplicaBaseline{}
	copy(baseline.DirectoryRoot[:], value[1:1+chainhash.HashSize])
	copy(baseline.ActiveRoot[:], value[1+chainhash.HashSize:1+2*chainhash.HashSize])
	baseline.Generation = binary.LittleEndian.Uint64(value[1+2*chainhash.HashSize:])
	return baseline, nil
}

func (s *dkvsReplicaStore) loadRoot(scope string) (string, error) {
	baseline, err := s.loadBaseline(scope)
	if err != nil {
		return "", err
	}
	return baseline.DirectoryRoot.String(), nil
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
	records []*swire.DKVSRecord, directoryRootText string, activeRoot chainhash.Hash,
	generation uint64) error {

	if s == nil || s.db == nil || scope == "" || len(filters) == 0 {
		return fmt.Errorf("invalid DKVS replica mirror")
	}
	directoryRoot, err := chainhash.NewHashFromStr(strings.TrimSpace(directoryRootText))
	if err != nil {
		return dkvsindexer.ErrInvalidCheckpoint
	}
	incoming := make(map[string]*swire.DKVSRecord, len(records))
	for _, record := range records {
		if !recordMatchesAnyFilter(record, filters) {
			return dkvsindexer.ErrInvalidKey
		}
		if err := dkvsindexer.VerifyRecordForClient(record,
			dkvsindexer.RecordVerificationOptions{}); err != nil {
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
	rootValue := make([]byte, 1+2*chainhash.HashSize+8)
	rootValue[0] = dkvsReplicaVersion
	copy(rootValue[1:1+chainhash.HashSize], directoryRoot[:])
	copy(rootValue[1+chainhash.HashSize:1+2*chainhash.HashSize], activeRoot[:])
	binary.LittleEndian.PutUint64(rootValue[1+2*chainhash.HashSize:], generation)
	if err := batch.Put(append(append([]byte(nil), dkvsReplicaRootPrefix...), scope...), rootValue); err != nil {
		return err
	}
	return batch.Flush()
}
