package wallet

import (
	"context"
	corewallet "github.com/sat20-labs/rgb11/wallet"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

var (
	_ rgb11wallet.ReservationCommitLocker = (*rgb11Manager)(nil)
	_ rgb11wallet.ReservationCommitReader = (*rgb11Manager)(nil)
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

func (p *rgb11Manager) LockReservationRead() func() {
	owner := p.rgb11ReservationOwner()
	owner.rgbReservationMu.RLock()
	return owner.rgbReservationMu.RUnlock
}

func (p *rgb11Manager) beginRGB11ReservationRead() func() {
	return p.LockReservationRead()
}

func (s *rgb11SnapshotReservations) LockReservationCommit() func() {
	return s.manager.LockReservationCommit()
}

// The original engine APIs do local read/modify/write work. Share the short
// snapshot publication gate, without changing the RGB library or holding a
// mutex across evidence, proxy, DKVS, or signing requests.
func (p *rgb11Manager) createRGB11ReceiveLocal(params corewallet.ReceiveParams) (*corewallet.ReceiveRequest, error) {
	unlock, err := p.lockRGB11ChainRefresh(context.Background())
	if err != nil {
		return nil, err
	}
	defer unlock()
	return p.engine.CreateReceive(params)
}

func (p *rgb11Manager) rejectRGB11ReceiveLocal(requestID, transferID, objectHash, reason string) error {
	unlock, err := p.lockRGB11ChainRefresh(context.Background())
	if err != nil {
		return err
	}
	defer unlock()
	return p.engine.MarkRelayRejected(requestID, transferID, objectHash, reason)
}
