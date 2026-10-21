package wallet

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/wire"

	db "github.com/sat20-labs/indexer/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	sbtcutil "github.com/sat20-labs/satoshinet/btcutil"
	swire "github.com/sat20-labs/satoshinet/wire"
)

// 只需要在发起方对utxo做锁定，因为remote端不需要检查，所有的utxo由发起方选好

type LockedUtxo struct {
	LockedTime                int64          `json:"lockedTime"`
	Reason                    string         `json:"reason"`
	ReservationID             string         `json:"reservation_id,omitempty"`
	ReservationPreviousReason string         `json:"reservation_previous_reason,omitempty"`
	Value                     int64          `json:"value"`
	Assets                    swire.TxAssets `json:"assets"`
	Owner                     *UtxoLockOwner `json:"-"`
}

// UtxoLockOwner scopes a DApp-created lock to the exact wallet identity that
// approved it. A nil owner denotes an existing wallet/system lock and remains
// readable by older databases.
type UtxoLockOwner struct {
	Origin            string `json:"origin"`
	Network           string `json:"network"`
	WalletFingerprint string `json:"wallet_fingerprint"`
	AccountIndex      uint32 `json:"account_index"`
}

type rgb11ReservationLockTarget struct {
	ReservationID  string
	PreviousReason string
}

// rgb11LockPlan is derived from a complete RGB11 snapshot. Owners listed in
// ReplacedOwners are the only existing operation reservations that this
// candidate may move or retire; all other lock owners remain protected.
type rgb11LockPlan struct {
	ReplacedOwners map[string]struct{}
	DesiredOwners  map[string]rgb11ReservationLockTarget
	Consumed       map[string]struct{}
	Reasons        map[string]string
}

var (
	ErrUtxoReserved         = errors.New("UTXO is already reserved")
	ErrUtxoReservationOwner = errors.New("UTXO reservation owner mismatch")
	ErrUtxoLockOwner        = errors.New("UTXO lock owner mismatch")
)

func (p UtxoLockOwner) valid() bool {
	return strings.TrimSpace(p.Origin) != "" && strings.TrimSpace(p.Network) != "" &&
		strings.TrimSpace(p.WalletFingerprint) != ""
}

func sameUtxoLockOwner(left, right *UtxoLockOwner) bool {
	return left != nil && right != nil && *left == *right
}

// 前端钱包在background和worker两个不同线程存在两个不同的stp模块，需要考虑这种情况下的数据同步
type UtxoLocker struct {
	mutex       sync.RWMutex
	network     string
	address     string                 // 暂时不区分地址，降低复杂性
	lockmap     map[string]*LockedUtxo // utxo -> lock time
	refreshTime int64

	db        db.KVDB
	rpcClient IndexerRPCClient
}

func NewUtxoLocker(db db.KVDB, rpc IndexerRPCClient, network string) *UtxoLocker {
	locker := &UtxoLocker{
		lockmap:   make(map[string]*LockedUtxo),
		db:        db,
		rpcClient: rpc,
		network:   network,
	}
	return locker
}

func (p *UtxoLocker) Init() {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.reload()
}

func (p *UtxoLocker) Reload(address string) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.reload()
}

func (p *UtxoLocker) reload() {
	lastTime, err := loadLastLockTime(p.db, p.network)
	if err == nil {
		if lastTime == p.refreshTime {
			return
		}
	}

	p.lockmap = loadAllLockedUtxoFromDB(p.db, p.network)
	//Log.Infof("reload %s %d ", p.network, len(p.lockmap))
	p.refreshTime = lastTime
}

func (p *UtxoLocker) LockUtxosWithTx(tx *wire.MsgTx) error {
	if tx == nil {
		return nil
	}
	p.mutex.Lock()
	defer p.mutex.Unlock()
	for _, in := range tx.TxIn {
		err := p.lockUtxo(in.PreviousOutPoint.String(), "broadcasted")
		if err != nil {
			return err
		}
	}
	p.refreshTime = time.Now().UnixMilli()
	saveLastLockTime(p.db, p.network, p.refreshTime)
	return nil
}

func (p *UtxoLocker) UnlockUtxosWithTx(tx *wire.MsgTx) error {
	if tx == nil {
		return nil
	}
	p.mutex.Lock()
	defer p.mutex.Unlock()
	for _, in := range tx.TxIn {
		err := p.unlockUtxo(in.PreviousOutPoint.String())
		if err != nil {
			return err
		}
	}
	p.refreshTime = time.Now().UnixMilli()
	saveLastLockTime(p.db, p.network, p.refreshTime)
	return nil
}

func (p *UtxoLocker) LockUtxosWithTx_SatsNet(tx *swire.MsgTx) error {
	if tx == nil {
		return nil
	}
	p.mutex.Lock()
	defer p.mutex.Unlock()
	for _, in := range tx.TxIn {
		err := p.lockUtxo(in.PreviousOutPoint.String(), "broadcasted")
		if err != nil {
			return err
		}
	}
	p.refreshTime = time.Now().UnixMilli()
	saveLastLockTime(p.db, p.network, p.refreshTime)
	return nil
}

func (p *UtxoLocker) UnlockUtxosWithTx_SatsNet(tx *swire.MsgTx) error {
	if tx == nil {
		return nil
	}
	p.mutex.Lock()
	defer p.mutex.Unlock()
	for _, in := range tx.TxIn {
		err := p.unlockUtxo(in.PreviousOutPoint.String())
		if err != nil {
			return err
		}
	}
	p.refreshTime = time.Now().UnixMilli()
	saveLastLockTime(p.db, p.network, p.refreshTime)
	return nil
}

func (p *UtxoLocker) LockUtxos(utxos []string, reason string) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	for _, utxo := range utxos {
		err := p.lockUtxo(utxo, reason)
		if err != nil {
			return err
		}
	}
	p.refreshTime = time.Now().UnixMilli()
	saveLastLockTime(p.db, p.network, p.refreshTime)
	return nil
}

func (p *UtxoLocker) LockUtxo(utxo, reason string) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	err := p.lockUtxo(utxo, reason)
	if err != nil {
		return err
	}
	p.refreshTime = time.Now().UnixMilli()
	saveLastLockTime(p.db, p.network, p.refreshTime)
	return nil
}

// LockUtxoForOwner creates a persistent DApp-owned lock. It cannot claim an
// existing wallet/system lock or a lock belonging to another DApp identity.
func (p *UtxoLocker) LockUtxoForOwner(utxo, reason string, owner UtxoLockOwner) error {
	if strings.TrimSpace(utxo) == "" || strings.TrimSpace(reason) == "" || !owner.valid() {
		return fmt.Errorf("invalid owned UTXO lock")
	}
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.reload()
	if current := p.lockmap[utxo]; current != nil {
		if sameUtxoLockOwner(current.Owner, &owner) {
			return nil
		}
		return fmt.Errorf("%w: %s", ErrUtxoLockOwner, utxo)
	}
	lock := &LockedUtxo{LockedTime: time.Now().Unix(), Reason: reason, Owner: &owner}
	return p.persistReservationChangesLocked(map[string]*LockedUtxo{utxo: lock}, nil)
}

// UnlockUtxoForOwner refuses ownerless legacy/system locks and locks owned by
// another DApp. The unrestricted UnlockUtxo remains the explicit local-wallet
// management path.
func (p *UtxoLocker) UnlockUtxoForOwner(utxo string, owner UtxoLockOwner) error {
	if strings.TrimSpace(utxo) == "" || !owner.valid() {
		return fmt.Errorf("invalid owned UTXO unlock")
	}
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.reload()
	current := p.lockmap[utxo]
	if current == nil {
		return nil
	}
	if !sameUtxoLockOwner(current.Owner, &owner) {
		return fmt.Errorf("%w: %s", ErrUtxoLockOwner, utxo)
	}
	return p.persistReservationChangesLocked(nil, []string{utxo})
}

func normalizedReservationUtxos(utxos []string) []string {
	unique := make(map[string]struct{}, len(utxos))
	for _, utxo := range utxos {
		utxo = strings.TrimSpace(utxo)
		if utxo != "" {
			unique[utxo] = struct{}{}
		}
	}
	result := make([]string, 0, len(unique))
	for utxo := range unique {
		result = append(result, utxo)
	}
	sort.Strings(result)
	return result
}

func cloneLockedUtxo(value *LockedUtxo) *LockedUtxo {
	if value == nil {
		return nil
	}
	clone := *value
	clone.Assets = append(swire.TxAssets(nil), value.Assets...)
	if value.Owner != nil {
		owner := *value.Owner
		clone.Owner = &owner
	}
	return &clone
}

// persistReservationChangesLocked commits lock records and the cross-context
// refresh marker in one database batch, then updates memory only after Flush.
// The caller must hold p.mutex for writing.
func (p *UtxoLocker) persistReservationChangesLocked(puts map[string]*LockedUtxo, deletes []string) error {
	batch := p.db.NewWriteBatch()
	if batch == nil {
		return fmt.Errorf("create UTXO lock batch")
	}
	defer batch.Close()
	for utxo, lock := range puts {
		encoded, err := EncodeToBytes(lock)
		if err != nil {
			return err
		}
		if err := batch.Put([]byte(GetLockedUtxoKey(p.network, utxo)), encoded); err != nil {
			return err
		}
	}
	for _, utxo := range deletes {
		if err := batch.Delete([]byte(GetLockedUtxoKey(p.network, utxo))); err != nil {
			return err
		}
	}
	refreshTime := time.Now().UnixMilli()
	encodedTime, err := EncodeToBytes(refreshTime)
	if err != nil {
		return err
	}
	if err := batch.Put([]byte(GeLastLockTimeKey(p.network)), encodedTime); err != nil {
		return err
	}
	if err := batch.Flush(); err != nil {
		return err
	}
	for utxo, lock := range puts {
		p.lockmap[utxo] = cloneLockedUtxo(lock)
	}
	for _, utxo := range deletes {
		delete(p.lockmap, utxo)
	}
	p.refreshTime = refreshTime
	return nil
}

// stageRGB11LockReasons adds carrier-lock updates to an existing wallet DB
// batch. The caller runs this from the RGB reservation commit gate; the locker
// mutex remains held until the batch completion callback publishes memory.
func (p *UtxoLocker) stageRGB11LockReasons(batch db.WriteBatch, reasons map[string]string) (func(bool), error) {
	if p == nil || batch == nil || p.db == nil {
		return nil, fmt.Errorf("stage RGB11 carrier locks")
	}
	p.mutex.Lock()
	unlockOnError := true
	defer func() {
		if unlockOnError {
			p.mutex.Unlock()
		}
	}()
	puts := make(map[string]*LockedUtxo, len(reasons))
	// Read only the target lock records, propagating failures. The legacy
	// reload path replaces the whole cache from a prefix scan whose error is
	// ignored; it must not be used while preparing an atomic RGB11 commit.
	for _, utxo := range normalizedReservationUtxos(mapKeys(reasons)) {
		raw, err := p.db.Read([]byte(GetLockedUtxoKey(p.network, utxo)))
		var lock *LockedUtxo
		if err != nil {
			if errors.Is(err, db.ErrKeyNotFound) {
				if memory := p.lockmap[utxo]; memory != nil && memory.ReservationID != "" {
					return nil, fmt.Errorf("read active RGB11 carrier lock %s: %w", utxo, err)
				}
			} else {
				return nil, err
			}
		} else {
			lock = new(LockedUtxo)
			if err := DecodeFromBytes(raw, lock); err != nil {
				return nil, err
			}
			if memory := p.lockmap[utxo]; memory != nil {
				lock.Owner = memory.Owner
			}
		}
		reason := reasons[utxo]
		if reason == "" {
			return nil, fmt.Errorf("empty RGB11 carrier lock reason")
		}
		lock = cloneLockedUtxo(lock)
		if lock != nil && lock.ReservationID != "" {
			// An active spend reservation owns its complete lock record and reason.
			continue
		}
		if lock == nil {
			lock = &LockedUtxo{LockedTime: time.Now().Unix()}
		}
		lock.Reason = reason
		encoded, err := EncodeToBytes(lock)
		if err != nil {
			return nil, err
		}
		if err := batch.Put([]byte(GetLockedUtxoKey(p.network, utxo)), encoded); err != nil {
			return nil, err
		}
		puts[utxo] = lock
	}
	if len(puts) == 0 {
		unlockOnError = false
		p.mutex.Unlock()
		return nil, nil
	}
	// Keep this context's refreshTime behind the marker written by the shared
	// batch. The next reload will then refresh unrelated lock entries changed
	// by another context without letting a failed stage alter the live cache.
	refreshTime := time.Now().UnixMilli()
	if refreshTime == p.refreshTime {
		refreshTime++
	}
	encodedTime, err := EncodeToBytes(refreshTime)
	if err != nil {
		return nil, err
	}
	if err := batch.Put([]byte(GeLastLockTimeKey(p.network)), encodedTime); err != nil {
		return nil, err
	}
	unlockOnError = false
	return func(committed bool) {
		if committed {
			for utxo, lock := range puts {
				p.lockmap[utxo] = cloneLockedUtxo(lock)
			}
		}
		p.mutex.Unlock()
	}, nil
}

// stageRGB11LockPlan applies carrier reasons and the complete lifecycle of
// candidate-owned RGB11 reservations in one surrounding wallet DB batch.
// It never releases an owner absent from the old RGB11 snapshot/common
// reservation records, and it only publishes lockmap changes after Flush.
func (p *UtxoLocker) stageRGB11LockPlan(batch db.WriteBatch, plan rgb11LockPlan) (func(bool), error) {
	if p == nil || batch == nil || p.db == nil {
		return nil, fmt.Errorf("stage RGB11 lock plan")
	}
	p.mutex.Lock()
	unlockOnError := true
	defer func() {
		if unlockOnError {
			p.mutex.Unlock()
		}
	}()

	prefix := []byte(GetDBKeyPrefix() + DB_KEY_LOCKEDUTXO + p.network + "-")
	current := make(map[string]*LockedUtxo)
	err := p.db.BatchRead(prefix, false, func(key, raw []byte) error {
		if !strings.HasPrefix(string(key), string(prefix)) {
			return fmt.Errorf("invalid UTXO lock key")
		}
		outpoint := strings.TrimPrefix(string(key), string(prefix))
		if outpoint == "" || GetLockedUtxoKey(p.network, outpoint) != string(key) {
			return fmt.Errorf("invalid UTXO lock outpoint")
		}
		lock := new(LockedUtxo)
		if err := DecodeFromBytes(raw, lock); err != nil {
			return err
		}
		if memory := p.lockmap[outpoint]; memory != nil {
			lock.Owner = memory.Owner
		}
		current[outpoint] = lock
		return nil
	})
	if err != nil {
		return nil, err
	}
	// A cached RGB owner with no backing DB record is a broken lock invariant;
	// do not silently publish a candidate that would make its input selectable.
	for outpoint, memory := range p.lockmap {
		if memory == nil || memory.ReservationID == "" {
			continue
		}
		if _, replaced := plan.ReplacedOwners[memory.ReservationID]; !replaced {
			if _, desired := plan.DesiredOwners[outpoint]; !desired {
				continue
			}
		}
		if current[outpoint] == nil {
			return nil, fmt.Errorf("read active RGB11 lock %s: %w", outpoint, db.ErrKeyNotFound)
		}
	}

	allowedOwners := make(map[string]struct{}, len(plan.ReplacedOwners)+len(plan.DesiredOwners))
	for owner := range plan.ReplacedOwners {
		if owner != "" {
			allowedOwners[owner] = struct{}{}
		}
	}
	for _, target := range plan.DesiredOwners {
		if target.ReservationID == "" {
			return nil, fmt.Errorf("RGB11 lock plan has an empty reservation owner")
		}
		allowedOwners[target.ReservationID] = struct{}{}
	}

	touched := make(map[string]struct{})
	for outpoint := range plan.DesiredOwners {
		touched[outpoint] = struct{}{}
	}
	for outpoint := range plan.Consumed {
		touched[outpoint] = struct{}{}
	}
	for outpoint := range plan.Reasons {
		touched[outpoint] = struct{}{}
	}
	for outpoint, lock := range current {
		if lock != nil {
			if _, replaced := plan.ReplacedOwners[lock.ReservationID]; replaced && lock.ReservationID != "" {
				touched[outpoint] = struct{}{}
			}
		}
	}

	puts := make(map[string]*LockedUtxo)
	deletes := make([]string, 0)
	setPut := func(outpoint string, lock *LockedUtxo) {
		puts[outpoint] = lock
		delete(current, outpoint)
	}
	setDelete := func(outpoint string) {
		deletes = append(deletes, outpoint)
		delete(current, outpoint)
	}

	for outpoint := range touched {
		lock := cloneLockedUtxo(current[outpoint])
		target, hasOwner := plan.DesiredOwners[outpoint]
		if hasOwner {
			if lock != nil && lock.ReservationID != "" && lock.ReservationID != target.ReservationID {
				if _, replaceable := plan.ReplacedOwners[lock.ReservationID]; !replaceable {
					return nil, fmt.Errorf("%w: %s", ErrUtxoReservationOwner, outpoint)
				}
			}
			if lock != nil && lock.ReservationID == "" && lock.Reason != rgb11wallet.LockReasonPending &&
				(target.PreviousReason == "" || lock.Reason != target.PreviousReason) {
				return nil, fmt.Errorf("%w: %s", ErrUtxoReserved, outpoint)
			}
			if lock == nil {
				lock = &LockedUtxo{LockedTime: time.Now().Unix()}
			}
			lock.Reason = rgb11wallet.LockReasonPending
			lock.ReservationID = target.ReservationID
			lock.ReservationPreviousReason = target.PreviousReason
			setPut(outpoint, lock)
			continue
		}

		if _, consumed := plan.Consumed[outpoint]; consumed {
			if lock != nil {
				if lock.ReservationID != "" {
					if _, replaceable := allowedOwners[lock.ReservationID]; !replaceable {
						return nil, fmt.Errorf("%w: %s", ErrUtxoReservationOwner, outpoint)
					}
				} else if lock.Reason != rgb11wallet.LockReasonRGB && lock.Reason != rgb11wallet.LockReasonPending {
					return nil, fmt.Errorf("%w: %s", ErrUtxoReserved, outpoint)
				}
				setDelete(outpoint)
			}
			continue
		}

		if lock != nil && lock.ReservationID != "" {
			if _, replaceable := plan.ReplacedOwners[lock.ReservationID]; !replaceable {
				// A non-RGB reservation owns the whole lock, including its reason.
				// If this is also a candidate RGB carrier, the wallet cannot publish
				// it as available while leaving an unrelated owner in place. Fail the
				// candidate closed; do not silently keep the lock and claim readiness.
				if plan.Reasons[outpoint] != "" {
					return nil, fmt.Errorf("%w: RGB11 carrier %s", ErrUtxoReservationOwner, outpoint)
				}
				continue
			}
			if reason := plan.Reasons[outpoint]; reason != "" {
				lock.Reason = reason
				lock.ReservationID = ""
				lock.ReservationPreviousReason = ""
				setPut(outpoint, lock)
			} else if lock.ReservationPreviousReason != "" {
				lock.Reason = lock.ReservationPreviousReason
				lock.ReservationID = ""
				lock.ReservationPreviousReason = ""
				setPut(outpoint, lock)
			} else {
				setDelete(outpoint)
			}
			continue
		}

		if reason := plan.Reasons[outpoint]; reason != "" {
			if lock != nil && lock.ReservationID == "" &&
				lock.Reason != rgb11wallet.LockReasonRGB && lock.Reason != rgb11wallet.LockReasonPending {
				// Preserve manual/system locks. Replacing their reason with the RGB
				// carrier marker would make them claimable by TryReserve and could
				// turn an explicitly protected carrier into a spend input.
				return nil, fmt.Errorf("%w: RGB11 carrier %s", ErrUtxoReserved, outpoint)
			}
			if lock == nil {
				lock = &LockedUtxo{LockedTime: time.Now().Unix()}
			}
			lock.Reason = reason
			setPut(outpoint, lock)
		}
	}

	if len(puts) == 0 && len(deletes) == 0 {
		unlockOnError = false
		p.mutex.Unlock()
		return nil, nil
	}
	for outpoint, lock := range puts {
		encoded, err := EncodeToBytes(lock)
		if err != nil {
			return nil, err
		}
		if err := batch.Put([]byte(GetLockedUtxoKey(p.network, outpoint)), encoded); err != nil {
			return nil, err
		}
	}
	for _, outpoint := range deletes {
		if err := batch.Delete([]byte(GetLockedUtxoKey(p.network, outpoint))); err != nil {
			return nil, err
		}
	}
	refreshTime := time.Now().UnixMilli()
	if refreshTime == p.refreshTime {
		refreshTime++
	}
	encodedTime, err := EncodeToBytes(refreshTime)
	if err != nil {
		return nil, err
	}
	if err := batch.Put([]byte(GeLastLockTimeKey(p.network)), encodedTime); err != nil {
		return nil, err
	}
	unlockOnError = false
	return func(committed bool) {
		if committed {
			for outpoint, lock := range puts {
				p.lockmap[outpoint] = cloneLockedUtxo(lock)
			}
			for _, outpoint := range deletes {
				delete(p.lockmap, outpoint)
			}
		}
		p.mutex.Unlock()
	}, nil
}

func mapKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}

// TryReserve atomically reserves every requested UTXO for one operation. It is
// all-or-nothing and may claim an existing lock only when its reason is listed
// as claimable (RGB carriers are persistently locked even while spendable).
func (p *UtxoLocker) TryReserve(utxos []string, reason, reservationID string, claimableReasons ...string) error {
	if p == nil || strings.TrimSpace(reason) == "" || strings.TrimSpace(reservationID) == "" {
		return fmt.Errorf("invalid UTXO reservation")
	}
	utxos = normalizedReservationUtxos(utxos)
	if len(utxos) == 0 {
		return fmt.Errorf("UTXO reservation is empty")
	}
	claimable := make(map[string]struct{}, len(claimableReasons))
	for _, value := range claimableReasons {
		claimable[value] = struct{}{}
	}
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.reload()
	puts := make(map[string]*LockedUtxo, len(utxos))
	for _, utxo := range utxos {
		current := p.lockmap[utxo]
		if current != nil && current.ReservationID == reservationID {
			updated := cloneLockedUtxo(current)
			updated.Reason = reason
			puts[utxo] = updated
			continue
		}
		if current != nil {
			if current.ReservationID != "" {
				return fmt.Errorf("%w: %s", ErrUtxoReserved, utxo)
			}
			if _, ok := claimable[current.Reason]; !ok {
				return fmt.Errorf("%w: %s", ErrUtxoReserved, utxo)
			}
		}
		updated := &LockedUtxo{LockedTime: time.Now().Unix(), Reason: reason, ReservationID: reservationID}
		if current != nil {
			updated = cloneLockedUtxo(current)
			updated.ReservationPreviousReason = current.Reason
			updated.Reason = reason
			updated.ReservationID = reservationID
		}
		puts[utxo] = updated
	}
	return p.persistReservationChangesLocked(puts, nil)
}

// EnsureReservation restores or adopts an active persisted reservation after a
// restart. previousReasons describes the lock that must be restored if the
// operation is later cancelled; it must be supplied by the operation journal
// or RGB proof index rather than inferred from a transient pending reason.
func (p *UtxoLocker) EnsureReservation(utxos []string, reason, reservationID string,
	previousReasons map[string]string) error {
	if p == nil || strings.TrimSpace(reason) == "" || strings.TrimSpace(reservationID) == "" {
		return fmt.Errorf("invalid UTXO reservation")
	}
	utxos = normalizedReservationUtxos(utxos)
	if len(utxos) == 0 {
		return nil
	}
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.reload()
	puts := make(map[string]*LockedUtxo, len(utxos))
	for _, utxo := range utxos {
		current := p.lockmap[utxo]
		previous := previousReasons[utxo]
		if current != nil && current.ReservationID != "" && current.ReservationID != reservationID {
			return fmt.Errorf("%w: %s", ErrUtxoReserved, utxo)
		}
		if current != nil && current.ReservationID == "" && current.Reason != reason &&
			(previous == "" || current.Reason != previous) {
			return fmt.Errorf("%w: %s", ErrUtxoReserved, utxo)
		}
		updated := cloneLockedUtxo(current)
		if updated == nil {
			updated = &LockedUtxo{LockedTime: time.Now().Unix()}
		}
		updated.Reason = reason
		updated.ReservationID = reservationID
		updated.ReservationPreviousReason = previous
		puts[utxo] = updated
	}
	return p.persistReservationChangesLocked(puts, nil)
}

// FinalizeReservation turns operation-owned outputs into ordinary persistent
// locks after they become independently spendable. It is idempotent for an
// already-finalized lock but rejects another live owner.
func (p *UtxoLocker) FinalizeReservation(utxos []string, reservationID, reason string) error {
	if p == nil || strings.TrimSpace(reservationID) == "" || strings.TrimSpace(reason) == "" {
		return fmt.Errorf("invalid UTXO reservation finalization")
	}
	utxos = normalizedReservationUtxos(utxos)
	if len(utxos) == 0 {
		return nil
	}
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.reload()
	puts := make(map[string]*LockedUtxo, len(utxos))
	for _, utxo := range utxos {
		current := p.lockmap[utxo]
		if current != nil && current.ReservationID != "" && current.ReservationID != reservationID {
			return fmt.Errorf("%w: %s", ErrUtxoReservationOwner, utxo)
		}
		updated := cloneLockedUtxo(current)
		if updated == nil {
			updated = &LockedUtxo{LockedTime: time.Now().Unix()}
		}
		updated.Reason = reason
		updated.ReservationID = ""
		updated.ReservationPreviousReason = ""
		puts[utxo] = updated
	}
	return p.persistReservationChangesLocked(puts, nil)
}

// ConsumeReservation removes inputs that were spent by the reservation. It is
// idempotent for missing locks and rejects the whole batch before writing when
// any existing lock belongs to another owner.
func (p *UtxoLocker) ConsumeReservation(utxos []string, reservationID string) error {
	if p == nil || strings.TrimSpace(reservationID) == "" {
		return fmt.Errorf("invalid UTXO reservation owner")
	}
	utxos = normalizedReservationUtxos(utxos)
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.reload()
	deletes := make([]string, 0, len(utxos))
	for _, utxo := range utxos {
		current := p.lockmap[utxo]
		if current == nil {
			continue
		}
		if current.ReservationID != reservationID {
			return fmt.Errorf("%w: %s", ErrUtxoReservationOwner, utxo)
		}
		deletes = append(deletes, utxo)
	}
	if len(deletes) == 0 {
		return nil
	}
	return p.persistReservationChangesLocked(nil, deletes)
}

// ReleaseReservation releases only locks owned by reservationID. Claimed RGB
// locks are restored to their previous reason instead of being deleted.
func (p *UtxoLocker) ReleaseReservation(utxos []string, reservationID string) error {
	if p == nil || strings.TrimSpace(reservationID) == "" {
		return fmt.Errorf("invalid UTXO reservation owner")
	}
	utxos = normalizedReservationUtxos(utxos)
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.reload()
	puts := make(map[string]*LockedUtxo)
	deletes := make([]string, 0, len(utxos))
	for _, utxo := range utxos {
		current := p.lockmap[utxo]
		if current == nil {
			continue
		}
		if current.ReservationID != reservationID {
			return fmt.Errorf("%w: %s", ErrUtxoReservationOwner, utxo)
		}
		if current.ReservationPreviousReason == "" {
			deletes = append(deletes, utxo)
			continue
		}
		restored := cloneLockedUtxo(current)
		restored.Reason = current.ReservationPreviousReason
		restored.ReservationID = ""
		restored.ReservationPreviousReason = ""
		puts[utxo] = restored
	}
	if len(puts) == 0 && len(deletes) == 0 {
		return nil
	}
	return p.persistReservationChangesLocked(puts, deletes)
}

func (p *UtxoLocker) lockUtxo(utxo, reason string) error {
	_, ok := p.lockmap[utxo]
	if ok {
		return nil
	}

	lockedUtxo := LockedUtxo{
		LockedTime: time.Now().Unix(),
		Reason:     reason,
	}
	err := saveLockedUtxo(p.db, p.network, utxo, &lockedUtxo)
	if err != nil {
		Log.Errorf("saveLockedUtxo %s failed, %v", utxo, err)
		return err
	}
	//go p.FillAsset(utxo, &lockedUtxo)

	p.lockmap[utxo] = &lockedUtxo
	return nil
}

func (p *UtxoLocker) FillAsset(utxo string, lock *LockedUtxo) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return p.fillAsset(utxo, lock)
}

func (p *UtxoLocker) fillAsset(utxo string, lock *LockedUtxo) error {
	txOutput, err := p.rpcClient.GetTxOutput(utxo)
	if err != nil {
		Log.Errorf("fillAsset->GetTxOutput %s failed, %v", utxo, err)
		return err
	}
	lock.Value = txOutput.Value()
	lock.Assets = txOutput.Assets
	err = saveLockedUtxo(p.db, p.network, utxo, lock)
	if err != nil {
		Log.Errorf("fillAsset->saveLockedUtxo %s failed, %v", utxo, err)
		return err
	}
	return nil
}

func (p *UtxoLocker) UnlockUtxo(utxo string) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.unlockUtxo(utxo)

	p.refreshTime = time.Now().UnixMilli()
	saveLastLockTime(p.db, p.network, p.refreshTime)

	return nil
}

func (p *UtxoLocker) unlockUtxo(utxo string) error {
	_, ok := p.lockmap[utxo]
	if !ok {
		return nil
	}
	DeleteLockedUtxo(p.db, p.network, utxo)
	delete(p.lockmap, utxo)

	return nil
}

// 调用之前，先reload确保数据最新
func (p *UtxoLocker) IsLocked(utxo string) bool {
	p.mutex.RLock()
	defer p.mutex.RUnlock()

	_, ok := p.lockmap[utxo]
	return ok
}

func (p *UtxoLocker) GetLockedUtxoList() map[string]*LockedUtxo {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.reload()
	result := make(map[string]*LockedUtxo)
	for k, v := range p.lockmap {
		result[k] = cloneLockedUtxo(v)
	}

	return result
}

// func (p *UtxoLocker) GetLockedAssetAmount(asset *swire.AssetName) *Decimal {
// 	p.mutex.RLock()
// 	defer p.mutex.RUnlock()

// 	p.reload()

// 	var assetAmt *Decimal
// 	var totalValue int64
// 	for k, v := range p.lockmap {

// 		if v.Value == 0 && len(v.Assets) == 0 {
// 			err := p.fillAsset(k, v)
// 			if err != nil {
// 				continue
// 			}
// 		}

// 		totalValue += v.Value
// 		if len(v.Assets) > 0 {
// 			info, err := v.Assets.Find(asset)
// 			if err == nil {
// 				if assetAmt == nil {
// 					assetAmt = &info.Amount
// 				} else {
// 					assetAmt = assetAmt.Add(&info.Amount)
// 				}
// 			}
// 		}
// 	}

// 	if indexer.IsPlainAsset(asset) {
// 		return indexer.NewDefaultDecimal(totalValue)
// 	}

// 	return assetAmt
// }

func (p *UtxoLocker) GetLockedUtxoListV2() map[string]bool {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.reload()
	result := make(map[string]bool)
	for k := range p.lockmap {
		result[k] = true
	}

	return result
}

func (p *UtxoLocker) CheckBlock(transactions []*btcutil.Tx) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.reload()
	bUpdated := false
	for _, tx := range transactions {
		msgTx := tx.MsgTx()
		for _, txIn := range msgTx.TxIn {
			utxo := txIn.PreviousOutPoint.String()
			_, ok := p.lockmap[utxo]
			if ok {
				DeleteLockedUtxo(p.db, p.network, utxo)
				delete(p.lockmap, utxo)
				bUpdated = true
			}
		}
	}

	if bUpdated {
		p.refreshTime = time.Now().UnixMilli()
		saveLastLockTime(p.db, p.network, p.refreshTime)
	}
}

func (p *UtxoLocker) CheckBlock_SatsNet(transactions []*sbtcutil.Tx) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.reload()
	bUpdated := false
	for _, tx := range transactions {
		msgTx := tx.MsgTx()
		for _, txIn := range msgTx.TxIn {
			utxo := txIn.PreviousOutPoint.String()
			_, ok := p.lockmap[utxo]
			if ok {
				DeleteLockedUtxo(p.db, p.network, utxo)
				delete(p.lockmap, utxo)
				bUpdated = true
			}
		}
	}

	if bUpdated {
		p.refreshTime = time.Now().UnixMilli()
		saveLastLockTime(p.db, p.network, p.refreshTime)
	}
}

func (p *UtxoLocker) CheckExisting() {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.reload()

	utxos := make([]string, 0)
	for k := range p.lockmap {
		utxos = append(utxos, k)
	}

	existingUtxos, err := p.rpcClient.GetExistingUtxos(utxos)
	if err != nil {
		Log.Errorf("GetExistingUtxos failed, %v", err)
		return
	}
	if len(existingUtxos) != len(utxos) {
		//Log.Infof("some utxo spent! should check channel status...")
		existingUtxoMap := make(map[string]bool)
		for _, u := range existingUtxos {
			existingUtxoMap[u] = true
		}
		deletedUtxoVect := make([]string, 0)
		for k := range p.lockmap {
			_, ok := existingUtxoMap[k]
			if ok {
				continue
			}
			deletedUtxoVect = append(deletedUtxoVect, k)
		}
		for _, k := range deletedUtxoVect {
			DeleteLockedUtxo(p.db, p.network, k)
			delete(p.lockmap, k)
			//Log.Infof("deleted locked utxo: %s", k)
		}
		p.refreshTime = time.Now().UnixMilli()
		saveLastLockTime(p.db, p.network, p.refreshTime)
	}
}

func (p *UtxoLocker) CheckUtxos(utxos []string) string {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	p.reload()
	for _, utxo := range utxos {
		_, ok := p.lockmap[utxo]
		if ok {
			return utxo
		}

	}
	return ""
}
