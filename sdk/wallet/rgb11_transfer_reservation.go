package wallet

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/rgb11/invoicing"
	corewallet "github.com/sat20-labs/rgb11/wallet"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

const (
	RS_RGB11_PREPARED ResvStatus = 0x7000 + iota
	RS_RGB11_DELIVERED
	RS_RGB11_ACKNOWLEDGED
	RS_RGB11_BROADCAST
)

// RGB11TransferReservation uses the common wallet reservation namespace and
// recovery machinery. Private request/pending payloads remain in their scoped
// stores; this record owns the lifecycle and references those payloads.
type RGB11TransferReservation struct {
	ReservationBase
	Scope         string
	Identity      string
	RequestID     string
	Invoice       string
	ReceiveMode   string
	TransportMode string
	ContractID    string
	AmountRaw     string
	CreatedAt     int64
	Expiry        int64
	LockOwner     string
	LockOutpoint  string
	State         *rgb11wallet.TransferState
}

var _ Reservation = (*RGB11TransferReservation)(nil)

func (r *RGB11TransferReservation) GetType() string    { return RESV_TYPE_RGB11 }
func (r *RGB11TransferReservation) GetStructInDB() any { return r }

func rgb11TransferResvID(scope, identity string) int64 {
	hash := sha256.Sum256([]byte("rgb11-resv:" + scope + ":" + identity))
	return int64(binary.BigEndian.Uint64(hash[:8])&((1<<53)-1)) + 1
}

func rgb11ResvStatus(state *rgb11wallet.TransferState) ResvStatus {
	if state == nil {
		return RS_INIT
	}
	switch state.Status {
	case "settled":
		return RS_CONFIRMED
	case "rejected", "cancelled", "expired":
		return RS_CLOSED
	case "pending", "broadcast", "broadcasted", rgb11StatusBroadcastAttempted:
		return RS_RGB11_BROADCAST
	case "awaiting_broadcast":
		return RS_RGB11_ACKNOWLEDGED
	case "prepared":
		if state.AckStatus == "accepted" {
			return RS_RGB11_ACKNOWLEDGED
		}
		return RS_RGB11_PREPARED
	}
	if state.AckStatus == "accepted" {
		return RS_RGB11_ACKNOWLEDGED
	}
	return RS_RGB11_DELIVERED
}

// StageReservation is called by RGB stores with their own underlying DB batch.
// No successful lifecycle write can become visible without its common resv.
func (p *rgb11Manager) StageReservation(batch indexer.WriteBatch, scope string, value any) (func(), error) {
	return p.stageRGB11Reservation(batch, scope, value, nil)
}

func (p *rgb11Manager) stageRGB11Reservation(batch indexer.WriteBatch, scope string, value any, snapshot *rgb11SnapshotReservations) (func(), error) {
	var request *corewallet.ReceiveRequest
	var state *rgb11wallet.TransferState
	var lock *rgb11wallet.ReceiveReservation
	var owner string
	var createdAt int64
	switch v := value.(type) {
	case *corewallet.ReceiveRequest:
		request = v
	case *rgb11wallet.PendingTransfer:
		state, owner, createdAt = &v.State, v.ReservationID, v.CreatedAt
	case *rgb11wallet.TransferState:
		state = v
	case *rgb11wallet.ReceiveReservation:
		lock = v
	default:
		return nil, ErrRGB11Inconsistent
	}
	if state != nil && state.Direction == "receive" && state.Invoice != "" {
		match := func(candidate *corewallet.ReceiveRequest) error {
			if candidate.Invoice == state.Invoice {
				if request != nil && request.RequestID != candidate.RequestID {
					return ErrRGB11Inconsistent
				}
				request = candidate
			}
			return nil
		}
		if snapshot != nil {
			for _, candidate := range snapshot.requests {
				if err := match(candidate); err != nil {
					return nil, err
				}
			}
		} else {
			prefix := []byte("rgb11-engine-" + scope + "-wallet/receive/")
			if err := p.db.BatchRead(prefix, false, func(_, raw []byte) error {
				candidate, err := corewallet.DecodeReceiveRequest(raw)
				if err != nil {
					return err
				}
				return match(candidate)
			}); err != nil {
				return nil, err
			}
		}
	}

	identity := ""
	// Reuse the established binding even after address-mode invoice compaction.
	// The transfer ID is proof-derived; clearing display invoice text must never
	// create another reservation for the same incoming transition.
	if state != nil && state.Direction == "receive" {
		match := func(r *RGB11TransferReservation) error {
			if r.Scope == scope && r.State != nil && r.State.Direction == "receive" && r.State.TransferID == state.TransferID {
				if identity != "" && identity != r.Identity {
					return ErrRGB11Inconsistent
				}
				identity = r.Identity
			}
			return nil
		}
		if snapshot != nil {
			for _, r := range snapshot.records {
				if err := match(r); err != nil {
					return nil, err
				}
			}
		} else if err := p.db.BatchRead([]byte(GetDBKeyPrefix()+DB_KEY_RESV+RESV_TYPE_RGB11+"-"), false, func(_, raw []byte) error {
			var r RGB11TransferReservation
			if err := DecodeFromBytes(raw, &r); err != nil {
				return err
			}
			return match(&r)
		}); err != nil {
			return nil, err
		}
	}
	if identity == "" {
		if request != nil {
			identity = "receive:" + request.RequestID
		} else if lock != nil {
			identity = "receive:" + lock.RequestID
		} else if state != nil {
			if state.Direction == "receive" && state.Invoice != "" && !state.AddressMode && snapshot == nil {
				return nil, fmt.Errorf("%w: receive transfer has no invoice request", ErrRGB11Inconsistent)
			}
			identity = state.Direction + ":" + state.TransferID
		}
	}
	if identity == "" {
		return nil, ErrRGB11Inconsistent
	}

	id := rgb11TransferResvID(scope, identity)
	if current := p.GetResv(id); current != nil && current.GetType() != RESV_TYPE_RGB11 {
		return nil, ErrRGB11Inconsistent
	}
	var resv *RGB11TransferReservation
	var existing Reservation
	var err error
	if snapshot != nil {
		if current := snapshot.records[id]; current != nil {
			existing = current
		} else {
			err = indexer.ErrKeyNotFound
		}
	} else {
		existing, err = LoadReservation(p.db, nil, RESV_TYPE_RGB11, id)
	}
	if err == nil {
		resv = existing.(*RGB11TransferReservation)
		if resv.Scope != scope || resv.Identity != identity {
			return nil, ErrRGB11Inconsistent
		}
	} else if errors.Is(err, indexer.ErrKeyNotFound) {
		var walletID int64
		var account uint32
		if n, err := fmt.Sscanf(scope, "wallet-%d-account-%d-rgb11v2", &walletID, &account); err != nil || n != 2 || scope != rgb11StorageScope(walletID, account) {
			return nil, ErrRGB11Inconsistent
		}
		resv = &RGB11TransferReservation{ReservationBase: NewReservationBase(id, state != nil && state.Direction == "send", RS_INIT, nil), Scope: scope, Identity: identity, CreatedAt: time.Now().Unix()}
		resv.WalletId = common.WalletId{Id: walletID, SubAccountId: account}
	} else {
		return nil, err
	}
	if request != nil {
		resv.RequestID, resv.Invoice, resv.ReceiveMode = request.RequestID, request.Invoice, string(request.Mode)
		resv.CreatedAt, resv.Expiry = request.CreatedAt, request.Expiry
		invoice, err := invoicing.Parse(request.Invoice)
		if err != nil {
			return nil, err
		}
		if invoice.Contract != nil {
			resv.ContractID = invoice.Contract.String()
		}
		if invoice.Assignment != nil {
			resv.AmountRaw = strconv.FormatUint(uint64(invoice.Assignment.Amount), 10)
		}
		resv.TransportMode, err = rgb11InvoiceTransportMode(invoice)
		if err != nil {
			return nil, err
		}
	}
	if createdAt > 0 {
		resv.CreatedAt = createdAt
	}
	if state != nil {
		if resv.State != nil && (resv.State.Direction != state.Direction || resv.State.TransferID != state.TransferID || (resv.State.WitnessTxID != "" && state.WitnessTxID != "" && resv.State.WitnessTxID != state.WitnessTxID)) {
			return nil, ErrRGB11InvoiceMismatch
		}
		copy := *state
		resv.State = &copy
		resv.Invoice, resv.Expiry, resv.TransportMode = state.Invoice, state.Expiry, state.TransportMode
		resv.Status = rgb11ResvStatus(state)
	}
	if lock != nil {
		resv.LockOwner, resv.LockOutpoint = lock.ReservationID, lock.OutPoint
	}
	if owner != "" {
		resv.LockOwner = owner
	}
	encoded, err := EncodeToBytes(resv)
	if err != nil {
		return nil, err
	}
	if err := batch.Put([]byte(GetResvKey(RESV_TYPE_RGB11, id)), encoded); err != nil {
		return nil, err
	}
	if snapshot != nil {
		snapshot.records[id] = resv
	}
	return func() {
		p.mutex.Lock()
		defer p.mutex.Unlock()
		if p.resvMap == nil {
			p.resvMap = make(map[int64]Reservation)
		}
		p.addResvLocked(resv)
	}, nil
}

func (p *rgb11Manager) loadRGB11Reservations() ([]*RGB11TransferReservation, error) {
	scope := p.rgb11ScopeKey()
	result := make([]*RGB11TransferReservation, 0)
	err := p.db.BatchRead([]byte(GetDBKeyPrefix()+DB_KEY_RESV+RESV_TYPE_RGB11+"-"), false, func(key, raw []byte) error {
		var resv RGB11TransferReservation
		if err := DecodeFromBytes(raw, &resv); err != nil {
			return err
		}
		if resv.Scope != scope {
			return nil
		}
		if string(key) != GetResvKey(RESV_TYPE_RGB11, resv.Id) || resv.Id != rgb11TransferResvID(scope, resv.Identity) || rgb11StorageScope(resv.WalletId.Id, resv.WalletId.SubAccountId) != scope {
			return ErrRGB11Inconsistent
		}
		result = append(result, &resv)
		return nil
	})
	sort.Slice(result, func(i, j int) bool {
		if result[i].CreatedAt != result[j].CreatedAt {
			return result[i].CreatedAt > result[j].CreatedAt
		}
		return result[i].Id > result[j].Id
	})
	return result, err
}

func (p *rgb11Manager) rgb11ReservationRecoverable(r *RGB11TransferReservation) (bool, error) {
	if p == nil || r == nil || r.RequestID == "" || r.Invoice == "" || p.engine == nil || p.projectionStore == nil {
		return false, nil
	}
	request, err := p.engine.LoadReceive(r.RequestID)
	if err != nil {
		return false, err
	}
	if request.Invoice != r.Invoice {
		return false, ErrRGB11Inconsistent
	}
	if request.Mode != corewallet.ReceiveWitness || len(request.WitnessScript) == 0 {
		return true, nil
	}
	if p.wallet == nil || p.wallet.GetAddress() == "" {
		return false, ErrRGB11WalletLocked
	}
	walletScript, err := AddrToPkScript(p.wallet.GetAddress(), GetChainParam())
	if err != nil {
		return false, err
	}
	// Fixed-address witness invoices intentionally use the account key directly.
	if bytes.Equal(request.WitnessScript, walletScript) {
		return true, nil
	}
	key, err := p.projectionStore.LoadReceiveKey(request.WitnessScript)
	if errors.Is(err, indexer.ErrKeyNotFound) {
		// Crash boundary: Engine/CreateReceive and the common reservation were
		// committed, but the independently derived signing locator was not.
		// Such an invoice must never be advertised as recoverable.
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if key.RequestID != request.RequestID || key.ScopeIndex != p.wallet.GetSubAccount() ||
		key.LogicalAddress != p.wallet.GetAddress() || len(key.InternalPubKey) != 32 {
		return false, ErrRGB11Inconsistent
	}
	return true, nil
}

func (p *rgb11Manager) rgb11ReservationViews() ([]*rgb11wallet.RGB11Reservation, error) {
	// Individual KVDB reads are atomic, but their combination is not a snapshot.
	// Keep the engine and common-reservation generations together while local
	// creation, rollback and snapshot-import commits take the write side.
	release := p.beginRGB11ReservationRead()
	defer release()
	reservations, err := p.loadRGB11Reservations()
	if err != nil {
		return nil, err
	}
	byRequest := make(map[string]*RGB11TransferReservation, len(reservations))
	for _, reservation := range reservations {
		if reservation.RequestID == "" {
			continue
		}
		if byRequest[reservation.RequestID] != nil {
			return nil, ErrRGB11Inconsistent
		}
		byRequest[reservation.RequestID] = reservation
	}
	engineRecords, err := p.engineStore.ExportSnapshot()
	if err != nil {
		return nil, err
	}
	for _, record := range engineRecords {
		request, err := corewallet.DecodeReceiveRequest(record.Value)
		if err != nil {
			return nil, err
		}
		if byRequest[request.RequestID] == nil {
			return nil, ErrRGB11Inconsistent
		}
	}
	views := make([]*rgb11wallet.RGB11Reservation, 0, len(reservations))
	for _, r := range reservations {
		// Existing transfers already supply history. The additional view contains
		// only invoices the PWA can still resume, avoiding a second history payload.
		if r.RequestID == "" || r.Invoice == "" || r.Status == RS_CLOSED ||
			(r.State == nil && r.Expiry > 0 && r.Expiry <= time.Now().Unix()) ||
			(r.State != nil && (r.State.Direction != "receive" || r.State.AddressMode || r.State.Status == "settled" || r.State.Status == "rejected")) {
			continue
		}
		recoverable, err := p.rgb11ReservationRecoverable(r)
		if err != nil {
			return nil, err
		}
		if !recoverable {
			continue
		}

		status := "created"
		if r.Status == RS_CLOSED {
			status = "expired"
		}
		direction := "receive"
		if strings.HasPrefix(r.Identity, "send:") {
			direction = "send"
		}
		if r.State != nil {
			status = r.State.Status
		}
		views = append(views, &rgb11wallet.RGB11Reservation{ID: strconv.FormatInt(r.Id, 10), RequestID: r.RequestID, Direction: direction, Status: status, Invoice: r.Invoice, Mode: r.ReceiveMode, TransportMode: r.TransportMode, ContractID: r.ContractID, AmountRaw: r.AmountRaw, CreatedAt: r.CreatedAt, Expiry: r.Expiry, Transfer: publicRGB11ReservationTransfer(r.State)})
	}
	return views, nil
}

func (p *rgb11Manager) LoadReservationState(scope, direction, transferID string) (*rgb11wallet.TransferState, error) {
	var state *rgb11wallet.TransferState
	err := p.db.BatchRead([]byte(GetDBKeyPrefix()+DB_KEY_RESV+RESV_TYPE_RGB11+"-"), false, func(_, raw []byte) error {
		var r RGB11TransferReservation
		if err := DecodeFromBytes(raw, &r); err != nil {
			return err
		}
		if r.Scope == scope && r.State != nil && r.State.Direction == direction && r.State.TransferID == transferID {
			if state != nil {
				return ErrRGB11Inconsistent
			}
			state = r.State
		}
		return nil
	})
	return state, err
}

func (p *rgb11Manager) discardUnpublishedRGB11Receive(requestID string) error {
	scope := p.rgb11ScopeKey()
	batch := rgb11wallet.NewReservationWriteBatch(p.db, scope, p)
	if batch == nil {
		return ErrRGB11Inconsistent
	}
	defer batch.Close()
	for _, key := range []string{
		"rgb11-engine-" + scope + "-wallet/receive/" + requestID,
		"rgb11-" + scope + "-receive-reservation-" + requestID,
		GetResvKey(RESV_TYPE_RGB11, rgb11TransferResvID(scope, "receive:"+requestID)),
	} {
		if err := batch.Delete([]byte(key)); err != nil {
			return err
		}
	}
	if err := batch.Flush(); err != nil {
		return err
	}
	p.DelResvWithId(rgb11TransferResvID(scope, "receive:"+requestID))
	return nil
}

func (p *rgb11Manager) rgb11ReceiveReservationUsed(requestID string) (bool, error) {
	request, err := p.engine.LoadReceive(requestID)
	if err != nil {
		return false, err
	}
	if request.Status != corewallet.ReceivePrepared || request.TransferID != "" || request.WitnessTxID != "" {
		return true, nil
	}
	// A crash may occur after the validated transfer/resv commit but before
	// the engine acknowledgement. Never infer unused from engine status alone.
	states, err := p.projectionStore.ListTransfers()
	if err != nil {
		return false, err
	}
	for _, state := range states {
		if state.Direction == "receive" && state.Invoice == request.Invoice {
			return true, nil
		}
	}
	return false, nil
}

// Snapshot state is built only from the validated incoming records. This
// prevents stale pre-restore resv state from leaking into the new generation.
type rgb11SnapshotReservations struct {
	manager  *rgb11Manager
	requests []*corewallet.ReceiveRequest
	records  map[int64]*RGB11TransferReservation
}

func (s *rgb11SnapshotReservations) StageReservation(batch indexer.WriteBatch, scope string, value any) (func(), error) {
	return s.manager.stageRGB11Reservation(batch, scope, value, s)
}
func (p *rgb11Manager) importRGB11ReservationSnapshot(snapshot *RGB11WalletSnapshot) error {
	stage := &rgb11SnapshotReservations{manager: p, records: make(map[int64]*RGB11TransferReservation)}
	for _, record := range snapshot.EngineRecords {
		request, err := corewallet.DecodeReceiveRequest(record.Value)
		if err != nil {
			return err
		}
		stage.requests = append(stage.requests, request)
	}
	old, err := p.loadRGB11Reservations()
	if err != nil {
		return err
	}
	batch := rgb11wallet.NewReservationWriteBatch(p.db, p.rgb11ScopeKey(), stage)
	if batch == nil {
		return ErrRGB11Inconsistent
	}
	defer batch.Close()
	for _, r := range old {
		if err := batch.Delete([]byte(GetResvKey(RESV_TYPE_RGB11, r.Id))); err != nil {
			return err
		}
	}
	if err := p.engineStore.StageSnapshotImport(batch, snapshot.EngineRecords); err != nil {
		return err
	}
	if err := p.projectionStore.StageSnapshotImport(batch, snapshot.ProjectionRecords); err != nil {
		return err
	}
	if err := batch.Flush(); err != nil {
		return err
	}
	for _, r := range old {
		if stage.records[r.Id] == nil {
			p.DelResvWithId(r.Id)
		}
	}
	return nil
}

func (p *rgb11Manager) LoadReservationStates(scope string) (map[string]*rgb11wallet.TransferState, error) {
	result := make(map[string]*rgb11wallet.TransferState)
	err := p.db.BatchRead([]byte(GetDBKeyPrefix()+DB_KEY_RESV+RESV_TYPE_RGB11+"-"), false, func(key, raw []byte) error {
		var r RGB11TransferReservation
		if err := DecodeFromBytes(raw, &r); err != nil {
			return err
		}
		if r.Scope != scope {
			return nil
		}
		if string(key) != GetResvKey(RESV_TYPE_RGB11, r.Id) || r.Id != rgb11TransferResvID(scope, r.Identity) || rgb11StorageScope(r.WalletId.Id, r.WalletId.SubAccountId) != scope {
			return ErrRGB11Inconsistent
		}
		if r.State == nil {
			return nil
		}
		keyID := r.State.Direction + ":" + r.State.TransferID
		if result[keyID] != nil {
			return ErrRGB11Inconsistent
		}
		result[keyID] = r.State
		return nil
	})
	return result, err
}

func publicRGB11ReservationTransfer(state *rgb11wallet.TransferState) *rgb11wallet.RGB11ReservationTransfer {
	if state == nil {
		return nil
	}
	return &rgb11wallet.RGB11ReservationTransfer{
		TransferID: state.TransferID, BatchID: state.BatchID, BatchTransferIDs: append([]string(nil), state.BatchTransferIDs...),
		Status: state.Status, AckStatus: state.AckStatus, WitnessTxID: state.WitnessTxID, ConsignmentHash: state.ConsignmentHash,
		Asset: state.Asset, OutputOutPoints: append([]string(nil), state.OutputOutPoints...),
	}
}
