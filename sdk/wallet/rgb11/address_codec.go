package rgb11wallet

import (
	"encoding/binary"
	"errors"
)

const (
	ReceiveCapabilityAddress = uint8(1 << 0)
	ReceiveCapabilityAny     = uint8(1 << 1)

	AddressEnvelopeVersion = uint8(1)
	AddressEnvelopeInline  = uint8(1)
	AddressEnvelopeBlob    = uint8(2)

	AddressACKAccepted   = uint8(1)
	AddressACKNeedResend = uint8(2)
	AddressACKRejected   = uint8(3)
)

var (
	ErrTraditionalReceiveRequired = errors.New("receiver has no RGB11 DKVS address capability; use a traditional RGB invoice")
	ErrAddressMailbox             = errors.New("invalid RGB11 address mailbox message")
)

func EncodeAddressEnvelope(mode uint8, ciphertext []byte) ([]byte, error) {
	if mode != AddressEnvelopeInline && mode != AddressEnvelopeBlob {
		return nil, ErrAddressMailbox
	}
	if mode == AddressEnvelopeInline && len(ciphertext) == 0 {
		return nil, ErrAddressMailbox
	}
	value := []byte{AddressEnvelopeVersion, mode}
	if mode == AddressEnvelopeInline {
		value = append(value, ciphertext...)
	}
	return value, nil
}

func DecodeAddressEnvelope(value []byte) (uint8, []byte, error) {
	if len(value) < 2 || value[0] != AddressEnvelopeVersion {
		return 0, nil, ErrAddressMailbox
	}
	mode := value[1]
	switch mode {
	case AddressEnvelopeInline:
		if len(value) == 2 {
			return 0, nil, ErrAddressMailbox
		}
		return mode, append([]byte(nil), value[2:]...), nil
	case AddressEnvelopeBlob:
		if len(value) != 2 {
			return 0, nil, ErrAddressMailbox
		}
		return mode, nil, nil
	default:
		return 0, nil, ErrAddressMailbox
	}
}

func EncodeAddressACK(ack RGB11AddressACK) ([]byte, error) {
	if ack.Status != AddressACKAccepted && ack.Status != AddressACKNeedResend &&
		ack.Status != AddressACKRejected {
		return nil, ErrAddressMailbox
	}
	value := make([]byte, 4)
	value[0] = AddressEnvelopeVersion
	value[1] = ack.Status
	binary.BigEndian.PutUint16(value[2:], ack.Code)
	return value, nil
}

func DecodeAddressACK(value []byte) (RGB11AddressACK, error) {
	if len(value) != 4 || value[0] != AddressEnvelopeVersion {
		return RGB11AddressACK{}, ErrAddressMailbox
	}
	ack := RGB11AddressACK{Status: value[1], Code: binary.BigEndian.Uint16(value[2:])}
	if _, err := EncodeAddressACK(ack); err != nil {
		return RGB11AddressACK{}, err
	}
	return ack, nil
}
