package wallet

import (
	"sync"

	"github.com/sat20-labs/sat20wallet/sdk/common"
)

const (
	RESV_TYPE_INSC         = "insc"
	RESV_TYPE_LOCALACTION  = "localaction"
	RESV_TYPE_REMOTEACTION = "remoteaction"
)

type Reservation interface {
	GetId() int64
	GetType() string
	GetStatus() ResvStatus
	SetStatus(ResvStatus)
	GetResult() []byte
	GetWalletId() common.WalletId
	InitLocalWallet(walletMgr *Manager) error
	GetBase() *ReservationBase
	GetStructInDB() any
}

type ReservationBase struct {
	Id          int64
	IsInitiator bool
	Status      ResvStatus
	WalletId    common.WalletId

	localWallet common.Wallet
	mutex       *sync.RWMutex
}

func (p *ReservationBase) GetId() int64 {
	return p.Id
}

func (p *ReservationBase) GetStatus() ResvStatus {
	return p.Status
}

func (p *ReservationBase) SetStatus(r ResvStatus) {
	p.Status = r
}

func (p *ReservationBase) GetWalletId() common.WalletId {
	return p.WalletId
}

func (p *ReservationBase) InitLocalWallet(walletMgr *Manager) error {
	if p.WalletId.Id == 0 {
		return nil
	}
	w := walletMgr.FindWalletById(p.WalletId.Id)
	if w == nil {
		return nil
	}
	p.localWallet = w.Clone()
	p.localWallet.SetSubAccount(p.WalletId.SubAccountId)
	return nil
}

func (p *ReservationBase) InitRuntime() {
	if p.mutex == nil {
		p.mutex = new(sync.RWMutex)
	}
}

func (p *ReservationBase) Mutex() *sync.RWMutex {
	p.InitRuntime()
	return p.mutex
}

func (p *ReservationBase) Lock() {
	p.InitRuntime()
	p.mutex.Lock()
}

func (p *ReservationBase) Unlock() {
	p.InitRuntime()
	p.mutex.Unlock()
}

func (p *ReservationBase) RLock() {
	p.InitRuntime()
	p.mutex.RLock()
}

func (p *ReservationBase) RUnlock() {
	p.InitRuntime()
	p.mutex.RUnlock()
}

func (p *ReservationBase) LocalWallet() common.Wallet {
	return p.localWallet
}

func (p *ReservationBase) SetLocalWallet(w common.Wallet) {
	p.localWallet = w
	if w != nil {
		p.WalletId = w.GetWalletId()
	}
}

func (p *ReservationBase) GetResult() []byte {
	return nil
}

func (p *ReservationBase) GetBase() *ReservationBase {
	return p
}

func newReservationBase(id int64, status ResvStatus, wallet common.Wallet) ReservationBase {
	return NewReservationBase(id, true, status, wallet)
}

func NewReservationBase(id int64, isInitiator bool, status ResvStatus, wallet common.Wallet) ReservationBase {
	base := ReservationBase{
		Id:          id,
		IsInitiator: isInitiator,
		Status:      status,
		mutex:       new(sync.RWMutex),
	}
	if wallet != nil {
		base.localWallet = wallet.Clone()
		base.WalletId = wallet.GetWalletId()
	}
	return base
}

func newResvFromType(typ string) Reservation {
	switch typ {
	case RESV_TYPE_LOCALACTION:
		return &LocalActionPerformData{
			ReservationBase: ReservationBase{mutex: new(sync.RWMutex)},
		}
	case RESV_TYPE_REMOTEACTION:
		return &RemoteActionPerformReservation{
			ReservationBase: ReservationBase{mutex: new(sync.RWMutex)},
		}
	case RESV_TYPE_INSC:
		return &InscribeResv{}
	default:
		return nil
	}
}
