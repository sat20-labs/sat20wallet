package wallet

import (
	"bytes"
	"encoding/binary"
	"errors"
)

// Revocation contains data to construct punishment tx.
type Revocation struct {
	Id         int // commitHeight
	CommitTxId string
	Rev        []byte
}

type RevocationInTx struct {
	ShortChanId      uint64
	Id               int    // commitHeight
	PrevPaymentTxId  string // 不使用
	LocalCommitTxId  string
	LocalRev         []byte
	RemoteCommitTxId string
	RemoteRev        []byte
}

func (r *RevocationInTx) Serialize() ([]byte, error) {
	buf := new(bytes.Buffer)

	// ShortChanId: uint64 (8 bytes)
	if err := binary.Write(buf, binary.BigEndian, r.ShortChanId); err != nil {
		return nil, err
	}

	// Id: varint encoding
	if err := binary.Write(buf, binary.BigEndian, uint64(r.Id)); err != nil {
		return nil, err
	}

	// paymentTxId
	strLen := uint16(len(r.PrevPaymentTxId))
	if err := binary.Write(buf, binary.BigEndian, strLen); err != nil {
		return nil, err
	}
	if _, err := buf.Write([]byte(r.PrevPaymentTxId)); err != nil {
		return nil, err
	}

	// LocalCommitTxId length + string
	strLen = uint16(len(r.LocalCommitTxId))
	if err := binary.Write(buf, binary.BigEndian, strLen); err != nil {
		return nil, err
	}
	if _, err := buf.Write([]byte(r.LocalCommitTxId)); err != nil {
		return nil, err
	}

	// LocalRev length + bytes
	revLen := uint16(len(r.LocalRev))
	if err := binary.Write(buf, binary.BigEndian, revLen); err != nil {
		return nil, err
	}
	if _, err := buf.Write(r.LocalRev); err != nil {
		return nil, err
	}

	// RemoteCommitTxId length + string
	strLen = uint16(len(r.RemoteCommitTxId))
	if err := binary.Write(buf, binary.BigEndian, strLen); err != nil {
		return nil, err
	}
	if _, err := buf.Write([]byte(r.RemoteCommitTxId)); err != nil {
		return nil, err
	}

	// RemoteRev length + bytes
	revLen = uint16(len(r.RemoteRev))
	if err := binary.Write(buf, binary.BigEndian, revLen); err != nil {
		return nil, err
	}
	if _, err := buf.Write(r.RemoteRev); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func DeserializeRevocationInTx(data []byte) (*RevocationInTx, error) {
	if len(data) < 10 {
		return nil, errors.New("insufficient data")
	}

	r := &RevocationInTx{}
	buf := bytes.NewReader(data)

	// ShortChanId
	if err := binary.Read(buf, binary.BigEndian, &r.ShortChanId); err != nil {
		return nil, err
	}

	// Id
	var id uint64
	if err := binary.Read(buf, binary.BigEndian, &id); err != nil {
		return nil, err
	}
	r.Id = int(id)

	// paymentTxId
	var strLen uint16
	if err := binary.Read(buf, binary.BigEndian, &strLen); err != nil {
		return nil, err
	}
	paymentTxIdBytes := make([]byte, strLen)
	if _, err := buf.Read(paymentTxIdBytes); err != nil {
		return nil, err
	}
	r.PrevPaymentTxId = string(paymentTxIdBytes)

	// LocalCommitTxId
	if err := binary.Read(buf, binary.BigEndian, &strLen); err != nil {
		return nil, err
	}
	localCommitBytes := make([]byte, strLen)
	if _, err := buf.Read(localCommitBytes); err != nil {
		return nil, err
	}
	r.LocalCommitTxId = string(localCommitBytes)

	// LocalRev
	if err := binary.Read(buf, binary.BigEndian, &strLen); err != nil {
		return nil, err
	}
	r.LocalRev = make([]byte, strLen)
	if _, err := buf.Read(r.LocalRev); err != nil {
		return nil, err
	}

	// RemoteCommitTxId
	if err := binary.Read(buf, binary.BigEndian, &strLen); err != nil {
		return nil, err
	}
	remoteCommitBytes := make([]byte, strLen)
	if _, err := buf.Read(remoteCommitBytes); err != nil {
		return nil, err
	}
	r.RemoteCommitTxId = string(remoteCommitBytes)

	// RemoteRev
	if err := binary.Read(buf, binary.BigEndian, &strLen); err != nil {
		return nil, err
	}
	r.RemoteRev = make([]byte, strLen)
	if _, err := buf.Read(r.RemoteRev); err != nil {
		return nil, err
	}

	return r, nil
}
