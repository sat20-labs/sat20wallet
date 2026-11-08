package rgb11wallet

import (
	"errors"
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
// the engine/common-reservation relationship. Staging runs before this gate;
// the underlying flush and post-commit publication callbacks run while it is
// held. Every scope of one wallet database shares the gate.
type ReservationCommitLocker interface {
	LockReservationCommit() func()
}

// ReservationCommitReader coordinates a multi-read observation with the
// reservation/projection commit gate. Readers must hold the returned release
// function across every query that forms one logical view.
type ReservationCommitReader interface {
	LockReservationRead() func()
}

// ReservationCommitStage stages related same-database records after the
// reservation write gate is acquired. Its completion callback publishes
// in-memory state after a successful database flush and before reservation
// callbacks, while the gate is still held.
type ReservationCommitStage func(indexer.WriteBatch) (func(committed bool), error)

// ReservationCommitBatch is implemented by wallet reservation batches that
// can stage related state in the same gated database commit.
type ReservationCommitBatch interface {
	FlushWith(ReservationCommitStage) error
}

// ReservationCommitMetadata joins the account dirty generation to the same
// local transaction. The returned callback must release any local state gate
// on both success and failure; it must never perform transport work.
type ReservationCommitMetadata interface {
	StageReservationMetadata(indexer.WriteBatch, string) (func(bool), error)
}

var ErrAtomicPersistenceUnavailable = errors.New("RGB11 database does not support atomic write transactions")

type reservationBatch struct {
	indexer.WriteBatch
	scope       string
	persistence ReservationPersistence
	committed   []func()
	changed     bool
}

func NewReservationWriteBatch(db indexer.KVDB, scope string, persistence ReservationPersistence) (indexer.WriteBatch, error) {
	atomic, ok := db.(indexer.AtomicWriteBatchDB)
	if !ok {
		return nil, ErrAtomicPersistenceUnavailable
	}
	batch := atomic.NewAtomicWriteBatch()
	if batch == nil {
		return nil, ErrAtomicPersistenceUnavailable
	}
	if persistence == nil {
		return batch, nil
	}
	return &reservationBatch{WriteBatch: batch, scope: scope, persistence: persistence}, nil
}

func (b *reservationBatch) Put(key, value []byte) error {
	if err := b.stageReservation(key, value); err != nil {
		return err
	}
	if err := b.WriteBatch.Put(key, value); err != nil {
		return err
	}
	b.changed = true
	return nil
}

func (b *reservationBatch) Delete(key []byte) error {
	if err := b.WriteBatch.Delete(key); err != nil {
		return err
	}
	b.changed = true
	return nil
}

// Unchanged snapshot records still rebuild reservation rows and RAM callbacks
// when their scope is imported; their potentially large raw values need no PUT.
func (b *reservationBatch) stageReservation(key, value []byte) error {
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
	return nil
}

func (b *reservationBatch) Flush() error {
	return b.FlushWith(nil)
}

func (b *reservationBatch) FlushWith(stage ReservationCommitStage) (err error) {
	var release func()
	if locker, ok := b.persistence.(ReservationCommitLocker); ok {
		release = locker.LockReservationCommit()
		defer release()
	}
	var finishMetadata func(bool)
	defer func() {
		if finishMetadata != nil {
			finishMetadata(false)
		}
	}()
	if metadata, ok := b.persistence.(ReservationCommitMetadata); ok && b.changed {
		finishMetadata, err = metadata.StageReservationMetadata(b.WriteBatch, b.scope)
		if err != nil {
			return err
		}
	}
	var finish func(bool)
	finishCalled := false
	defer func() {
		if finish != nil && !finishCalled {
			finish(false)
		}
	}()
	if stage != nil {
		finish, err = stage(b.WriteBatch)
		if err != nil {
			return err
		}
	}
	if err = b.WriteBatch.Flush(); err != nil {
		return err
	}
	if finishMetadata != nil {
		complete := finishMetadata
		finishMetadata = nil
		complete(true)
	}
	if finish != nil {
		complete := finish
		finish = nil
		finishCalled = true
		complete(true)
	}
	for _, done := range b.committed {
		done()
	}
	b.committed = nil
	return nil
}

func (s *ProjectionStore) SetReservationPersistence(p ReservationPersistence) { s.reservations = p }
func (s *EngineStore) SetReservationPersistence(p ReservationPersistence)     { s.reservations = p }
func (s *ProjectionStore) newWriteBatch() (indexer.WriteBatch, error) {
	s.mu.RLock()
	scope := s.scope
	s.mu.RUnlock()
	return NewReservationWriteBatch(s.db, scope, s.reservations)
}
func (s *EngineStore) newWriteBatch() (indexer.WriteBatch, error) {
	s.mu.RLock()
	scope := s.scope
	s.mu.RUnlock()
	return NewReservationWriteBatch(s.db, scope, s.reservations)
}
func (s *ProjectionStore) writeReservationRecord(key, value []byte) error {
	batch, err := s.newWriteBatch()
	if err != nil {
		return err
	}
	defer batch.Close()
	if err := batch.Put(key, value); err != nil {
		return err
	}
	return batch.Flush()
}

func (s *ProjectionStore) deleteReservationRecord(key []byte) error {
	batch, err := s.newWriteBatch()
	if err != nil {
		return err
	}
	defer batch.Close()
	if err := batch.Delete(key); err != nil {
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
