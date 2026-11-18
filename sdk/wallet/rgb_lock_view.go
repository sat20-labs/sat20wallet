package wallet

import (
	"sort"

	"github.com/sat20-labs/sat20wallet/sdk/common"
)

// UtxoLockView is a serialization-only wrapper. LockedUtxo's persisted shape
// and the locker's write/release behavior remain unchanged.
type UtxoLockView struct {
	*LockedUtxo
	Category string `json:"category"` // manual, rgb, or other; not an unlock permission
}

func DescribeUtxoLock(lock *LockedUtxo) UtxoLockView {
	view := UtxoLockView{LockedUtxo: cloneLockedUtxo(lock), Category: "other"}
	if lock == nil {
		return view
	}
	switch lock.Reason {
	case common.UtxoLockRGB, common.UtxoLockRGBPending, common.UtxoLockRGBWithdrawal:
		view.Category = "rgb"
	case common.UtxoLockManual:
		view.Category = "manual"
	}
	if lock.ReservationPreviousReason == common.UtxoLockRGB {
		// Another operation can temporarily hold an RGB carrier. Retain its
		// actual reason, but never offer it as a generic unlockable row.
		view.Category = "rgb"
	}
	return view
}

func (p *Manager) captureRGBLockState() (*RGB11State, RGBLockScope, []RGBLockView, error) {
	var scope RGBLockScope
	releaseObservation := p.beginRGB11ReadObservation()
	defer releaseObservation()
	runtime, err := p.synchronizedRGB11Manager()
	if err != nil {
		return nil, scope, nil, err
	}
	p.mutex.RLock()
	account, err := runtime.fixedRGB11ScopeAccount()
	if err == nil {
		scope = RGBLockScope{Network: p.GetChain(), WalletID: account.WalletID, AccountIndex: account.AccountIndex}
	}
	p.mutex.RUnlock()
	if err != nil {
		return nil, scope, nil, err
	}
	if runtime.projectionStore == nil || runtime.utxoLockerL1 == nil {
		return nil, scope, nil, ErrRGB11Inconsistent
	}
	releaseReservations := runtime.beginRGB11ReservationRead()
	defer releaseReservations()
	state, err := runtime.rgb11StateSnapshot()
	if err != nil {
		return nil, scope, nil, err
	}
	reservations, err := runtime.loadRGB11Reservations()
	if err != nil {
		return nil, scope, nil, err
	}

	points := make(map[string]bool)
	pointIDs := make(map[string]map[string]bool)
	ownerIDs := make(map[string]map[string]bool)
	knownOwners := make(map[string]bool)
	assets := make(map[string][]AssetAmount)
	addID := func(index map[string]map[string]bool, key, id string) {
		if key == "" || id == "" {
			return
		}
		if index[key] == nil {
			index[key] = make(map[string]bool)
		}
		index[key][id] = true
	}
	for _, output := range state.Outputs {
		points[output.OutPointStr] = true
		for _, asset := range output.Assets {
			assets[output.OutPointStr] = append(assets[output.OutPointStr], rgbDisplayAmount(asset))
		}
	}
	for _, proof := range state.Proofs {
		points[proof.OutPoint] = true
	}
	transferIDs := make(map[string]bool)
	for _, transfer := range state.Transfers {
		if transfer.Direction == "send" {
			transferIDs[transfer.TransferID] = true
		}
		for _, point := range append(append([]string(nil), transfer.InputOutPoints...), transfer.OutputOutPoints...) {
			points[point] = true
			if transfer.Direction == "send" {
				addID(pointIDs, point, transfer.TransferID)
			}
		}
	}
	for _, reservation := range reservations {
		if reservation.LockOutpoint != "" {
			points[reservation.LockOutpoint] = true
		}
		if reservation.LockOwner != "" {
			knownOwners[reservation.LockOwner] = true
			if reservation.State != nil && transferIDs[reservation.State.TransferID] {
				addID(ownerIDs, reservation.LockOwner, reservation.State.TransferID)
			}
		}
	}

	rows := make([]RGBLockView, 0)
	appendLocks := func(chain string, locker *UtxoLocker) {
		if locker == nil {
			return
		}
		for outpoint, lock := range locker.GetLockedUtxoList() {
			if lock == nil || DescribeUtxoLock(lock).Category != "rgb" {
				continue
			}
			row := RGBLockView{Outpoint: outpoint, Chain: chain, Scope: "unknown", Reason: lock.Reason, LockedTime: lock.LockedTime, TransferIDs: []string{}}
			if lock.Value > 0 {
				value := lock.Value
				row.Value = &value
			}
			for _, asset := range lock.Assets {
				row.Assets = append(row.Assets, rgbDisplayAmount(asset))
			}
			// The L1 records above establish associations, not a claim of
			// ownership. L2 withdrawal locks lack that evidence and stay unknown.
			if chain == "btc" && (points[outpoint] || knownOwners[lock.ReservationID]) {
				row.Scope = "current"
				if len(assets[outpoint]) > 0 {
					row.Assets = append([]AssetAmount(nil), assets[outpoint]...)
				}
				ids := make(map[string]bool)
				for id := range pointIDs[outpoint] {
					ids[id] = true
				}
				for id := range ownerIDs[lock.ReservationID] {
					ids[id] = true
				}
				for id := range ids {
					row.TransferIDs = append(row.TransferIDs, id)
				}
				sort.Strings(row.TransferIDs)
			}
			rows = append(rows, row)
		}
	}
	appendLocks("btc", runtime.utxoLockerL1)
	appendLocks("satsnet", runtime.utxoLockerL2)
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Chain != rows[j].Chain {
			return rows[i].Chain < rows[j].Chain
		}
		return rows[i].Outpoint < rows[j].Outpoint
	})
	return state, scope, rows, nil
}
