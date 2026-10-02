package wallet

import rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"

var (
	_ rgb11wallet.ReservationCommitLocker = (*rgb11Manager)(nil)
	_ rgb11wallet.ReservationCommitLocker = (*rgb11SnapshotReservations)(nil)
)

func (p *rgb11Manager) rgb11ReservationOwner() *Manager {
	if p.accountOwner != nil {
		return p.accountOwner
	}
	return p.Manager
}

// The scope managers are lightweight views, not independent databases. Use
// the real account owner so a background/root scope commit cannot bypass the
// selected scope's coherent reservation read.
func (p *rgb11Manager) LockReservationCommit() func() {
	owner := p.rgb11ReservationOwner()
	owner.rgbReservationMu.Lock()
	return owner.rgbReservationMu.Unlock
}

func (p *rgb11Manager) beginRGB11ReservationRead() func() {
	owner := p.rgb11ReservationOwner()
	owner.rgbReservationMu.RLock()
	return owner.rgbReservationMu.RUnlock
}

func (s *rgb11SnapshotReservations) LockReservationCommit() func() {
	return s.manager.LockReservationCommit()
}
