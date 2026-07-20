package wallet

import "time"

// SetLockReason atomically creates or updates a persistent UTXO lock. It is
// used by RGB11 receivers to move a carrier from pending-rgb (mempool) to rgb
// (confirmed) without an unlock window in which ordinary coin selection could
// consume the output.
func (p *UtxoLocker) SetLockReason(utxo, reason string) error {
	if p == nil || utxo == "" || reason == "" {
		return nil
	}
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.reload()
	locked := p.lockmap[utxo]
	if locked == nil {
		locked = &LockedUtxo{LockedTime: time.Now().Unix()}
		p.lockmap[utxo] = locked
	}
	if locked.Reason == reason {
		return nil
	}
	locked.Reason = reason
	if err := saveLockedUtxo(p.db, p.network, utxo, locked); err != nil {
		return err
	}
	p.refreshTime = time.Now().UnixMilli()
	saveLastLockTime(p.db, p.network, p.refreshTime)
	return nil
}
