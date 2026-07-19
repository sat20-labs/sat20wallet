package rgb11wallet

import (
	"errors"
	"strings"
	"sync"

	indexer "github.com/sat20-labs/indexer/common"
	corestorage "github.com/sat20-labs/rgb11/storage"
)

// EngineStore adapts the existing Wallet SDK KVDB to the standalone RGB11
// engine storage contract and isolates every wallet/account scope.
type EngineStore struct {
	db    indexer.KVDB
	mu    sync.RWMutex
	scope string
}

func NewEngineStore(db indexer.KVDB) *EngineStore { return &EngineStore{db: db} }

func (s *EngineStore) SetScope(scope string) error {
	if scope == "" || strings.ContainsAny(scope, "/:") {
		return ErrWalletScope
	}
	s.mu.Lock()
	s.scope = scope
	s.mu.Unlock()
	return nil
}

func (s *EngineStore) key(key []byte) ([]byte, error) {
	s.mu.RLock()
	scope := s.scope
	s.mu.RUnlock()
	if scope == "" || len(key) == 0 {
		return nil, ErrWalletScope
	}
	return append([]byte("rgb11-engine-"+scope+"-"), key...), nil
}

func (s *EngineStore) Get(key []byte) ([]byte, error) {
	key, err := s.key(key)
	if err != nil {
		return nil, err
	}
	value, err := s.db.Read(key)
	if err != nil {
		if errors.Is(err, indexer.ErrKeyNotFound) {
			return nil, corestorage.ErrNotFound
		}
		return nil, err
	}
	return value, nil
}

func (s *EngineStore) Begin() (corestorage.Tx, error) {
	if s == nil || s.db == nil {
		return nil, ErrWalletScope
	}
	batch := s.db.NewWriteBatch()
	if batch == nil {
		return nil, errors.New("RGB11 KVDB returned nil write batch")
	}
	return &engineTx{store: s, batch: batch}, nil
}

type engineTx struct {
	store *EngineStore
	batch indexer.WriteBatch
	done  bool
}

func (tx *engineTx) Put(key, value []byte) error {
	if tx.done {
		return errors.New("RGB11 transaction is closed")
	}
	key, err := tx.store.key(key)
	if err != nil {
		return err
	}
	return tx.batch.Put(key, value)
}

func (tx *engineTx) Delete(key []byte) error {
	if tx.done {
		return errors.New("RGB11 transaction is closed")
	}
	key, err := tx.store.key(key)
	if err != nil {
		return err
	}
	return tx.batch.Delete(key)
}

func (tx *engineTx) Commit() error {
	if tx.done {
		return errors.New("RGB11 transaction is closed")
	}
	tx.done = true
	err := tx.batch.Flush()
	tx.batch.Close()
	return err
}

func (tx *engineTx) Rollback() error {
	if tx.done {
		return nil
	}
	tx.done = true
	tx.batch.Close()
	return nil
}
