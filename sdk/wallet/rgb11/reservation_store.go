package rgb11wallet

import (
	indexer "github.com/sat20-labs/indexer/common"
	corewallet "github.com/sat20-labs/rgb11/wallet"
	"strings"
)

// ReservationPersistence joins RGB lifecycle writes to the wallet's existing
// reservation transaction. The callback is invoked only after the batch commits.
// Both stores and the implementation must use the same KVDB.
type ReservationPersistence interface {
	StageReservation(indexer.WriteBatch, string, any) (func(), error)
}

// ReservationCommitLocker coordinates a local atomic commit with readers of
// the engine/common-reservation relationship. Staging, callbacks and transport
// run outside this gate. Every scope of one wallet database shares the gate.
type ReservationCommitLocker interface {
	LockReservationCommit() func()
}

type reservationBatch struct {
	indexer.WriteBatch
	scope       string
	persistence ReservationPersistence
	committed   []func()
}

func NewReservationWriteBatch(db indexer.KVDB, scope string, persistence ReservationPersistence) indexer.WriteBatch {
	batch := db.NewWriteBatch()
	if batch == nil || persistence == nil {
		return batch
	}
	return &reservationBatch{WriteBatch: batch, scope: scope, persistence: persistence}
}

func (b *reservationBatch) Put(key, value []byte) error {
	var record any
	projection := "rgb11-" + b.scope + "-"
	engine := "rgb11-engine-" + b.scope + "-wallet/receive/"
	switch {
	case strings.HasPrefix(string(key), engine):
		request, err := corewallet.DecodeReceiveRequest(value)
		if err != nil {
			return err
		}
		record = request
	case strings.HasPrefix(string(key), projection+"pending-"):
		pending := new(PendingTransfer)
		if err := decode(value, pending); err != nil {
			return err
		}
		record = pending
	case strings.HasPrefix(string(key), projection+"transfer-"):
		state := new(TransferState)
		if err := decode(value, state); err != nil {
			return err
		}
		record = state
	case strings.HasPrefix(string(key), projection+"receive-reservation-"):
		lock := new(ReceiveReservation)
		if err := decode(value, lock); err != nil {
			return err
		}
		record = lock
	}
	if record != nil {
		done, err := b.persistence.StageReservation(b.WriteBatch, b.scope, record)
		if err != nil {
			return err
		}
		if done != nil {
			b.committed = append(b.committed, done)
		}
	}
	return b.WriteBatch.Put(key, value)
}

func (b *reservationBatch) Flush() error {
	err := func() error {
		if locker, ok := b.persistence.(ReservationCommitLocker); ok {
			release := locker.LockReservationCommit()
			defer release()
		}
		return b.WriteBatch.Flush()
	}()
	if err != nil {
		return err
	}
	for _, done := range b.committed {
		done()
	}
	b.committed = nil
	return nil
}

func (s *ProjectionStore) SetReservationPersistence(p ReservationPersistence) { s.reservations = p }
func (s *EngineStore) SetReservationPersistence(p ReservationPersistence)     { s.reservations = p }
func (s *ProjectionStore) newWriteBatch() indexer.WriteBatch {
	s.mu.RLock()
	scope := s.scope
	s.mu.RUnlock()
	return NewReservationWriteBatch(s.db, scope, s.reservations)
}
func (s *EngineStore) newWriteBatch() indexer.WriteBatch {
	s.mu.RLock()
	scope := s.scope
	s.mu.RUnlock()
	return NewReservationWriteBatch(s.db, scope, s.reservations)
}
func (s *ProjectionStore) writeReservationRecord(key, value []byte) error {
	batch := s.newWriteBatch()
	if batch == nil {
		return ErrWalletScope
	}
	defer batch.Close()
	if err := batch.Put(key, value); err != nil {
		return err
	}
	return batch.Flush()
}

// ReservationStateReader makes the common reservation authoritative. Every
// lifecycle write is transactionally paired with its reservation; a missing
// reservation is therefore corruption, not a legacy compatibility case.
type ReservationStateReader interface {
	LoadReservationStates(scope string) (map[string]*TransferState, error)
	LoadReservationState(scope, direction, transferID string) (*TransferState, error)
}

func (s *ProjectionStore) reservationState(state *TransferState) (*TransferState, error) {
	reader, ok := s.reservations.(ReservationStateReader)
	if !ok {
		return state, nil
	}
	s.mu.RLock()
	scope := s.scope
	s.mu.RUnlock()
	current, err := reader.LoadReservationState(scope, state.Direction, state.TransferID)
	if err != nil {
		return nil, err
	}
	if current == nil {
		return nil, ErrRGB11Inconsistent
	}
	return current, nil
}
