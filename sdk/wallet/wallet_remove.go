package wallet

import (
	"bytes"
	"strings"

	indexer "github.com/sat20-labs/indexer/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

// Caller owns the RGB execution/lifecycle gates, reservation gate and Manager
// mutex. The catalog, business rows and owned locks share one atomic batch.
// Only receipt metadata needed to finish account deletion remains elsewhere.
func (p *Manager) stageWalletRemoval(batch indexer.WriteBatch, infos []*WalletInfo) (func(bool), error) {
	if len(infos) == 0 {
		return func(bool) {}, nil
	}
	ids := make(map[int64]bool, len(infos))
	fingerprints := make(map[string]bool, len(infos))
	scopes := make(map[string]bool)
	outpoints := make(map[string]bool)
	for _, info := range infos {
		ids[info.Id] = true
		for index := 0; index < max(info.Accounts, 1); index++ {
			scope := rgb11StorageScope(info.Id, uint32(index))
			scopes[scope] = true
			projection := rgb11wallet.NewProjectionStore(p.db, nil)
			engine := rgb11wallet.NewEngineStore(p.db)
			if err := projection.SetScope(scope); err != nil {
				return nil, err
			}
			if err := engine.SetScope(scope); err != nil {
				return nil, err
			}
			outputs, err := projection.ListOutputs()
			if err != nil {
				return nil, err
			}
			for _, output := range outputs {
				outpoints[output.OutPointStr] = true
			}
			if err := projection.StageSnapshotImport(batch, nil); err != nil {
				return nil, err
			}
			if err := engine.StageSnapshotImport(batch, nil); err != nil {
				return nil, err
			}
			if err := projection.StageDeleteLocalMetadata(batch); err != nil {
				return nil, err
			}
		}
		fingerprint := walletFingerprint(info.Wallet)
		if fingerprint != "" {
			fingerprints[fingerprint] = true
		}
	}
	active := GetDBKeyPrefix()
	partitions, err := p.scanWalletDeleteKeys(batch, fingerprints)
	if err != nil {
		return nil, err
	}
	removed := make(map[int64]bool)
	operationLogMu.Lock()
	failed := true
	defer func() {
		if failed {
			operationLogMu.Unlock()
		}
	}()
	for prefix, part := range partitions {
		resvPrefix := []byte(prefix + DB_KEY_RESV + RESV_TYPE_RGB11 + "-")
		if err := p.db.BatchRead(resvPrefix, false, func(key, raw []byte) error {
			var reservation RGB11TransferReservation
			if err := DecodeFromBytes(raw, &reservation); err != nil {
				return err
			}
			if !ids[reservation.WalletId.Id] {
				return nil
			}
			if prefix == active {
				removed[reservation.Id] = true
				scopes[reservation.Scope] = true
			}
			if reservation.LockOwner != "" {
				part.owners[reservation.LockOwner] = true
			}
			if reservation.LockOutpoint != "" {
				part.points[reservation.LockOutpoint] = true
			}
			return batch.Delete(bytes.Clone(key))
		}); err != nil {
			return nil, err
		}
		if err := p.stageDeletedWalletLogs(batch, prefix, ids); err != nil {
			return nil, err
		}
	}
	finishL1, err := p.utxoLockerL1.stageWalletUnlock(batch, ids, fingerprints, outpoints, partitions)
	if err != nil {
		return nil, err
	}
	finishL2, err := p.utxoLockerL2.stageWalletUnlock(batch, ids, fingerprints, outpoints, partitions)
	if err != nil {
		if finishL1 != nil {
			finishL1(false)
		}
		return nil, err
	}
	failed = false
	return func(committed bool) {
		defer operationLogMu.Unlock()
		if finishL2 != nil {
			finishL2(committed)
		}
		if finishL1 != nil {
			finishL1(committed)
		}
		if !committed {
			return
		}
		for id := range removed {
			delete(p.resvMap, id)
		}
		if p.rgbManager != nil && p.rgbManager.scopeStates != nil {
			states := p.rgbManager.scopeStates
			states.mu.Lock()
			defer states.mu.Unlock()
			for scope := range scopes {
				delete(states.states, scope)
				if worker := states.reconciliations[scope]; worker != nil {
					if worker.running && !worker.stopped && worker.stop != nil {
						worker.stopped = true
						close(worker.stop)
					}
					delete(states.reconciliations, scope)
				}
			}
		}
	}, nil
}

func (p *Manager) stageDeletedWalletLogs(batch indexer.WriteBatch, prefix string, ids map[int64]bool) error {
	logs := make(map[string]bool)
	if err := p.db.BatchRead([]byte(prefix+DB_KEY_OPERATION_LOG), false, func(key, raw []byte) error {
		var record OperationLogRecord
		if err := DecodeFromBytes(raw, &record); err != nil {
			return err
		}
		if ids[record.WalletID] {
			logs[record.ID] = true
			return batch.Delete(bytes.Clone(key))
		}
		return nil
	}); err != nil {
		return err
	}
	return p.db.BatchRead([]byte(prefix+DB_KEY_OPERATION_LOG_RELATION), false, func(key, value []byte) error {
		if logs[string(value)] {
			return batch.Delete(bytes.Clone(key))
		}
		return nil
	})
}

// Reservation owners and outpoints have meaning only inside their partition.
type walletDeletePartition struct {
	owners map[string]bool
	points map[string]bool
}

func (p *Manager) scanWalletDeleteKeys(batch indexer.WriteBatch, fingerprints map[string]bool) (map[string]*walletDeletePartition, error) {
	partitions := make(map[string]*walletDeletePartition)
	add := func(prefix string) {
		if partitions[prefix] == nil {
			partitions[prefix] = &walletDeletePartition{owners: make(map[string]bool), points: make(map[string]bool)}
		}
	}
	add(GetDBKeyPrefix())
	scan := indexer.ScanOptions{KeysOnly: true}
	if _mode != LIGHT_NODE {
		scan.Prefix = []byte("sp/")
	}
	err := p.db.Scan(scan, func(key, _ []byte) error {
		name := string(key)
		if !strings.Contains(name, "/") && _mode == LIGHT_NODE {
			for _, family := range []string{DB_KEY_RESV + RESV_TYPE_RGB11 + "-", DB_KEY_OPERATION_LOG,
				DB_KEY_LOCKEDUTXO + L1_NETWORK_BITCOIN + "-", DB_KEY_LOCKEDUTXO + L2_NETWORK_SATOSHI + "-"} {
				if prefix, _, found := strings.Cut(name, "-"+family); found {
					add(prefix + "-")
					break
				}
			}
			return nil
		}
		parts := strings.Split(name, "/")
		if parts[0] != "sp" && !strings.HasSuffix(parts[0], "-sp") {
			return nil
		}
		if len(parts) != 4 && !(len(parts) == 5 && parts[1] == "history") {
			return nil
		}
		if fingerprints[parts[len(parts)-2]] {
			return batch.Delete(bytes.Clone(key))
		}
		return nil
	})
	return partitions, err
}

func (p *UtxoLocker) stageWalletUnlock(batch indexer.WriteBatch, ids map[int64]bool, fingerprints, projected map[string]bool, partitions map[string]*walletDeletePartition) (func(bool), error) {
	if p == nil {
		return nil, nil
	}
	p.mutex.Lock()
	active := GetDBKeyPrefix()
	activePoints := make(map[string]bool)
	if p.network == L1_NETWORK_BITCOIN {
		for point := range p.lockmap {
			if projected[point] || partitions[active].points[point] {
				activePoints[point] = true
			}
		}
	}
	for prefix, part := range partitions {
		lockPrefix := []byte(prefix + DB_KEY_LOCKEDUTXO + p.network + "-")
		changed := false
		err := p.db.BatchRead(lockPrefix, false, func(key, raw []byte) error {
			point := string(key[len(lockPrefix):])
			if p.network != L1_NETWORK_BITCOIN || !(part.points[point] || projected[point]) {
				var lock LockedUtxo
				if err := DecodeFromBytes(raw, &lock); err != nil {
					return err
				}
				owned := ids[lock.ManualWalletID] || (lock.Owner != nil && fingerprints[lock.Owner.WalletFingerprint])
				if !owned && !(p.network == L1_NETWORK_BITCOIN && part.owners[lock.ReservationID]) {
					return nil
				}
			}
			if prefix == active {
				activePoints[point] = true
			}
			changed = true
			return batch.Delete(bytes.Clone(key))
		})
		if err == nil && changed {
			// A missing refresh marker makes every open locker reload from disk.
			err = batch.Delete([]byte(prefix + DB_KEY_LOCK_LASTTIME + p.network))
		}
		if err != nil {
			p.mutex.Unlock()
			return nil, err
		}
	}
	return func(committed bool) {
		defer p.mutex.Unlock()
		if committed {
			for point := range activePoints {
				delete(p.lockmap, point)
			}
		}
	}, nil
}
