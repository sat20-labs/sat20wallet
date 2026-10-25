package wallet

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/btcsuite/btcd/wire"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
)

var errSTPRGBEvidenceMissing = errors.New("RGB reservation evidence is missing or unavailable")

// STPRGBReservationEvidence belongs only to one runtime reservation phase.
// Query preparation fills these private facts before the business channel lock;
// consumers never retain a network provider or persist these observations.
// A later protocol message must refresh mutable chain observations.
type STPRGBReservationEvidence struct {
	channelID string
	network   string
	height    uint64
	entropy   [32]byte
	rawTxs    map[string]stpRGBRawFact
	statuses  map[string]stpRGBStatusFact
	outspends map[string]stpRGBOutspendFact
	utxos     map[string]stpRGBUTXOFact
	tip       *rgb11wallet.BitcoinTip

	// One synchronous operation owns these local construction observations.
	// They are not shared with another reservation or persisted on restart.
	active         *stpRGBEvidenceView
	prepared       map[string][]byte
	missing        error
	oldRemoteTxID  string
	oldRemoteProof []byte

	// Only inputs that determine the prequeried network dependencies are bound.
	channel             *Channel
	manager             *Manager
	capturedStatus      ChannelStatus
	capturedHeight      int
	capturedResvID      int64
	capturedInventory   []byte
	capturedLocalProof  []byte
	capturedRemoteProof []byte
	walletID            int64
	account             uint32
	paymentKey          []byte
	requestHash         [32]byte
	selectedInputs      []string
	reservation         *SplicingReservation
	paymentReservation  *PaymentReservation
	paymentOldChannel   *Channel
	reservationID       int64
	reservationStatus   ResvStatus
	reservationFunding  []byte

	// Read-only capture of one confirmed old private carrier. These bytes are
	// runtime-only, never a second inventory or a persisted channel field.
	closedSourceFunding []byte
	closedSourceBackup  []byte
}

type stpRGBRawFact struct {
	raw []byte
	err error
}
type stpRGBStatusFact struct {
	value *rgb11wallet.BitcoinTxStatus
	err   error
}
type stpRGBOutspendFact struct {
	value *rgb11wallet.BitcoinOutspend
	err   error
}
type stpRGBUTXOFact struct {
	value *rgb11wallet.BitcoinUTXO
	err   error
}

func newSTPRGBReservationEvidence() *STPRGBReservationEvidence {
	return &STPRGBReservationEvidence{rawTxs: make(map[string]stpRGBRawFact), statuses: make(map[string]stpRGBStatusFact),
		outspends: make(map[string]stpRGBOutspendFact), utxos: make(map[string]stpRGBUTXOFact)}
}

// This private view is scratch state for one local calculation, not a mutable
// cache shared by reservations. In particular, a validator may accept an
// embedded prepared witness after a resolver error. Its caller MUST also check
// complete() before signing, persisting or releasing an authorization.
type stpRGBEvidenceView struct {
	facts    *STPRGBReservationEvidence
	prepared map[string][]byte
	missing  error
	context  rgb11wallet.ChannelContext
}

func (f *STPRGBReservationEvidence) newView() *stpRGBEvidenceView {
	v := &stpRGBEvidenceView{facts: f, prepared: make(map[string][]byte)}
	if f != nil {
		v.context = rgb11wallet.ChannelContext{ChannelID: f.channelID, BitcoinNetwork: f.network, Height: f.height, Entropy: f.entropy}
		for id, raw := range f.prepared {
			v.prepared[id] = append([]byte(nil), raw...)
		}
	}
	return v
}

func (v *stpRGBEvidenceView) unavailable(kind, key string) error {
	err := fmt.Errorf("%w: %s %s", errSTPRGBEvidenceMissing, kind, key)
	if v != nil && v.missing == nil {
		v.missing = err
	}
	if v != nil && v.facts != nil && v.facts.missing == nil {
		v.facts.missing = err
	}
	return err
}

func (v *stpRGBEvidenceView) complete() error {
	if v == nil || v.facts == nil {
		return errSTPRGBEvidenceMissing
	}
	if v.facts.missing != nil {
		return v.facts.missing
	}
	return v.missing
}

// Only a transaction actually constructed in this reservation can be treated
// as Prepared. Bind it to the exact canonical locally built proof and private
// channel context. An arbitrary absent network transaction is never Prepared.
func (v *stpRGBEvidenceView) addLocallyConstructed(tx *wire.MsgTx, proof []byte) error {
	if v == nil || v.facts == nil || tx == nil {
		return fmt.Errorf("missing locally constructed RGB transaction context")
	}
	pkg, err := rgb11wallet.DecodeChannelPackage(proof)
	if err != nil {
		return err
	}
	last := pkg.Steps[len(pkg.Steps)-1]
	if last.Context.ChannelID != v.context.ChannelID || last.Context.BitcoinNetwork != v.context.BitcoinNetwork ||
		last.Context.Height != v.context.Height || last.Context.Entropy != v.context.Entropy {
		return fmt.Errorf("locally constructed RGB transaction changed reservation context")
	}
	unsigned := tx.Copy()
	for _, input := range unsigned.TxIn {
		input.Witness = nil
	}
	var raw bytes.Buffer
	if err := unsigned.Serialize(&raw); err != nil {
		return err
	}
	if !bytes.Equal(raw.Bytes(), last.Tx) {
		return fmt.Errorf("locally constructed RGB transaction differs from its proof")
	}
	v.prepared[tx.TxID()] = append([]byte(nil), raw.Bytes()...)
	return nil
}

func (v *stpRGBEvidenceView) GetRawTx(id string) ([]byte, error) {
	if v != nil && v.facts != nil {
		if fact, exists := v.facts.rawTxs[id]; exists && fact.err == nil && len(fact.raw) != 0 {
			return append([]byte(nil), fact.raw...), nil
		}
		if raw, local := v.prepared[id]; local {
			return append([]byte(nil), raw...), nil
		}
	}
	return nil, v.unavailable("transaction", id)
}

func (v *stpRGBEvidenceView) GetTxStatus(id string) (*rgb11wallet.BitcoinTxStatus, error) {
	if v != nil && v.facts != nil {
		if fact, exists := v.facts.statuses[id]; exists && fact.err == nil && fact.value != nil {
			copy := *fact.value
			return &copy, nil
		}
		if _, local := v.prepared[id]; local {
			return &rgb11wallet.BitcoinTxStatus{TxID: id}, nil
		}
	}
	return nil, v.unavailable("transaction status", id)
}

func (v *stpRGBEvidenceView) GetOutspend(point string) (*rgb11wallet.BitcoinOutspend, error) {
	if v != nil && v.facts != nil {
		if fact, exists := v.facts.outspends[point]; exists && fact.err == nil && fact.value != nil {
			copy := *fact.value
			return &copy, nil
		}
	}
	return nil, v.unavailable("outspend", point)
}

func (v *stpRGBEvidenceView) GetUTXO(point string) (*rgb11wallet.BitcoinUTXO, error) {
	if v != nil && v.facts != nil {
		if fact, exists := v.facts.utxos[point]; exists && fact.err == nil && fact.value != nil {
			copy := *fact.value
			copy.PkScript = append([]byte(nil), fact.value.PkScript...)
			return &copy, nil
		}
	}
	return nil, v.unavailable("UTXO", point)
}

func (v *stpRGBEvidenceView) GetTip() (*rgb11wallet.BitcoinTip, error) {
	if v != nil && v.facts != nil && v.facts.tip != nil {
		copy := *v.facts.tip
		return &copy, nil
	}
	return nil, v.unavailable("chain tip", "")
}

func (*stpRGBEvidenceView) Broadcast([]byte) (string, error) {
	return "", fmt.Errorf("RGB reservation evidence cannot broadcast")
}

// useView is only called while the existing operation's channel/reservation
// exclusion is held. A missing observation remains sticky for that operation.
func (f *STPRGBReservationEvidence) useView() *stpRGBEvidenceView {
	if f == nil {
		return nil
	}
	if f.active == nil {
		f.active = f.newView()
		if f.prepared == nil {
			f.prepared = make(map[string][]byte)
		}
		f.active.prepared = f.prepared
	}
	return f.active
}

func (f *STPRGBReservationEvidence) bindConstructedChannel(channel *Channel) error {
	if f == nil || channel == nil || channel.ChannelId != f.channelID || uint64(channel.CommitHeight)+1 != f.height {
		return fmt.Errorf("RGB construction changed its captured channel context")
	}
	if f.entropy != ([32]byte{}) && f.entropy != channel.AssetEntropy {
		return fmt.Errorf("RGB construction changed its captured channel entropy")
	}
	f.entropy = channel.AssetEntropy
	if f.active != nil {
		f.active.context.Entropy = f.entropy
	}
	return nil
}

func (v *stpRGBEvidenceView) bindConstructedChannel(channel *Channel) error {
	if v == nil || v.facts == nil {
		return errSTPRGBEvidenceMissing
	}
	return v.facts.bindConstructedChannel(channel)
}

func (f *STPRGBReservationEvidence) oldRemoteView(channel *Channel) (*stpRGBEvidenceView, error) {
	if f == nil || channel == nil || channel.RemoteCommitment == nil {
		return nil, errSTPRGBEvidenceMissing
	}
	proof := channel.RemoteCommitment.AssetProofs["rgb11"]
	if len(proof) == 0 {
		return f.useView(), nil
	}
	if channel.RemoteCommitment.CommitTx == nil || channel.RemoteCommitment.CommitTx.TxID() != f.oldRemoteTxID ||
		!bytes.Equal(proof, f.oldRemoteProof) {
		return nil, fmt.Errorf("RGB old obligation changed its exact captured transaction or proof")
	}
	pkg, err := rgb11wallet.DecodeChannelPackage(proof)
	if err != nil {
		return nil, err
	}
	last := pkg.Steps[len(pkg.Steps)-1]
	v := f.newView()
	v.context = last.Context
	v.prepared = f.useView().prepared
	return v, nil
}

// Clone isolates mutable construction/missing state for an independently
// rebuilt detached reservation. No query result or witness byte is aliased.
func (f *STPRGBReservationEvidence) Clone() *STPRGBReservationEvidence {
	if f == nil {
		return nil
	}
	copy := *f
	copy.rawTxs = make(map[string]stpRGBRawFact, len(f.rawTxs))
	for key, value := range f.rawTxs {
		value.raw = append([]byte(nil), value.raw...)
		copy.rawTxs[key] = value
	}
	copy.statuses = make(map[string]stpRGBStatusFact, len(f.statuses))
	for key, value := range f.statuses {
		if value.value != nil {
			clone := *value.value
			value.value = &clone
		}
		copy.statuses[key] = value
	}
	copy.outspends = make(map[string]stpRGBOutspendFact, len(f.outspends))
	for key, value := range f.outspends {
		if value.value != nil {
			clone := *value.value
			value.value = &clone
		}
		copy.outspends[key] = value
	}
	copy.utxos = make(map[string]stpRGBUTXOFact, len(f.utxos))
	for key, value := range f.utxos {
		if value.value != nil {
			clone := *value.value
			clone.PkScript = append([]byte(nil), clone.PkScript...)
			value.value = &clone
		}
		copy.utxos[key] = value
	}
	if f.tip != nil {
		tip := *f.tip
		copy.tip = &tip
	}
	copy.prepared = make(map[string][]byte, len(f.prepared))
	for key, raw := range f.prepared {
		copy.prepared[key] = append([]byte(nil), raw...)
	}
	copy.oldRemoteProof = append([]byte(nil), f.oldRemoteProof...)
	copy.capturedInventory = append([]byte(nil), f.capturedInventory...)
	copy.capturedLocalProof = append([]byte(nil), f.capturedLocalProof...)
	copy.capturedRemoteProof = append([]byte(nil), f.capturedRemoteProof...)
	copy.paymentKey = append([]byte(nil), f.paymentKey...)
	copy.selectedInputs = append([]string(nil), f.selectedInputs...)
	copy.reservationFunding = append([]byte(nil), f.reservationFunding...)
	copy.closedSourceFunding = append([]byte(nil), f.closedSourceFunding...)
	copy.closedSourceBackup = append([]byte(nil), f.closedSourceBackup...)
	copy.active = nil
	if f.active != nil {
		copy.active = copy.useView()
		copy.active.context = f.active.context
		copy.active.missing = f.active.missing
	}
	return &copy
}

// ValidateForChannel is local-only and must run immediately after acquiring
// the original business lock, before modifying channel or reservation state.
func (f *STPRGBReservationEvidence) ValidateForChannel(channel *Channel, request []byte) error {
	if f == nil {
		return nil
	}
	if channel == nil || channel.LocalCommitment == nil || channel.RemoteCommitment == nil || channel != f.channel || channel.ChannelId != f.channelID || channel.Status != f.capturedStatus ||
		channel.CommitHeight != f.capturedHeight || channel.ResvId != f.capturedResvID || sha256.Sum256(request) != f.requestHash ||
		!bytes.Equal(channel.AssetFunding["rgb11"], f.capturedInventory) ||
		!bytes.Equal(channel.LocalCommitment.AssetProofs["rgb11"], f.capturedLocalProof) ||
		!bytes.Equal(channel.RemoteCommitment.AssetProofs["rgb11"], f.capturedRemoteProof) {
		return fmt.Errorf("RGB evidence no longer matches its exact request or channel inputs")
	}
	if f.reservation != nil {
		r := f.reservation
		if r.Channel != channel || r.Id != f.reservationID || r.Status != f.reservationStatus || !bytes.Equal(r.AssetFunding["rgb11"], f.reservationFunding) || len(r.SplicingInputs) != len(f.selectedInputs) {
			return fmt.Errorf("RGB evidence changed its captured reservation inputs or phase")
		}
		for i, input := range r.SplicingInputs {
			if input == nil || input.OutPointStr != f.selectedInputs[i] {
				return fmt.Errorf("RGB evidence changed its captured funding inputs")
			}
		}
		if len(f.oldRemoteProof) != 0 && (r.OldChannel == nil || r.OldChannel.RemoteCommitment == nil || r.OldChannel.RemoteCommitment.CommitTx == nil ||
			r.OldChannel.RemoteCommitment.CommitTx.TxID() != f.oldRemoteTxID || !bytes.Equal(r.OldChannel.RemoteCommitment.AssetProofs["rgb11"], f.oldRemoteProof)) {
			return fmt.Errorf("RGB evidence changed its captured old obligation")
		}
	}
	if r := f.paymentReservation; r != nil {
		if r.Channel != channel || r.Id != f.reservationID || r.Status != f.reservationStatus || r.OldChannel != f.paymentOldChannel {
			return fmt.Errorf("RGB evidence changed its captured payment or phase")
		}
		if len(f.oldRemoteProof) != 0 && (r.OldChannel == nil || r.OldChannel.RemoteCommitment == nil || r.OldChannel.RemoteCommitment.CommitTx == nil ||
			r.OldChannel.RemoteCommitment.CommitTx.TxID() != f.oldRemoteTxID || !bytes.Equal(r.OldChannel.RemoteCommitment.AssetProofs["rgb11"], f.oldRemoteProof)) {
			return fmt.Errorf("RGB evidence changed its captured old payment obligation")
		}
		if f.manager != nil {
			registered, ok := f.manager.GetPaymentReservation(f.reservationID)
			if !ok || registered != r {
				return fmt.Errorf("RGB evidence captured a replaced registered payment")
			}
		}
	}
	if len(f.closedSourceFunding) != 0 {
		if f.manager == nil || len(f.closedSourceBackup) == 0 {
			return fmt.Errorf("RGB closed source lost its captured backup")
		}
		raw, err := f.manager.GetDB().Read([]byte(GetChannelBackupKey(f.channelID)))
		if err != nil || !bytes.Equal(raw, f.closedSourceBackup) {
			return fmt.Errorf("RGB closed source backup changed after evidence capture")
		}
	}
	// Channel -> manager read lock is the existing peer-sync/install order.
	// A replaced registry object must not leave this old pointer usable.
	if f.manager != nil && f.manager.GetChannel(f.channelID) != channel {
		return fmt.Errorf("RGB evidence captured a replaced registered channel")
	}
	owner := channel.LocalWallet()
	if owner == nil || owner.GetId() != f.walletID || owner.GetSubAccount() != f.account ||
		!bytes.Equal(owner.GetPaymentPubKey().SerializeCompressed(), f.paymentKey) {
		return fmt.Errorf("RGB evidence changed its captured wallet account")
	}
	return f.useView().complete()
}
