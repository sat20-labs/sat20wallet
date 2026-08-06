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
}

var (
	ErrUtxoReserved         = errors.New("UTXO is already reserved")
	ErrUtxoReservationOwner = errors.New("UTXO reservation owner mismatch")
)

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
