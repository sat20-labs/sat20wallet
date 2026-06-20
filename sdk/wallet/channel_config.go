package wallet

import (
	"bytes"
	"encoding/gob"

	"github.com/btcsuite/btcd/btcec/v2"
)

type ChannelConfigV2 struct {
	InitialBalance      int64
	WalletId            uint32
	PaymentKey          *btcec.PublicKey
	RevocationBasePoint *btcec.PublicKey
}

func GobEncodePubKey(pk *btcec.PublicKey) []byte {
	if pk == nil {
		return []byte{}
	}
	return pk.SerializeCompressed()
}

func GobDecodePubKey(data []byte) (*btcec.PublicKey, error) {
	if len(data) == 0 {
		return nil, nil
	}
	return btcec.ParsePubKey(data)
}

func (p *ChannelConfigV2) GobEncode() ([]byte, error) {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)

	if err := enc.Encode(p.InitialBalance); err != nil {
		return nil, err
	}
	if err := enc.Encode(p.WalletId); err != nil {
		return nil, err
	}
	if err := enc.Encode(GobEncodePubKey(p.PaymentKey)); err != nil {
		return nil, err
	}
	if err := enc.Encode(GobEncodePubKey(p.RevocationBasePoint)); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (p *ChannelConfigV2) GobDecode(data []byte) error {
	buf := bytes.NewBuffer(data)
	dec := gob.NewDecoder(buf)

	if err := dec.Decode(&p.InitialBalance); err != nil {
		return err
	}
	if err := dec.Decode(&p.WalletId); err != nil {
		return err
	}

	var pubKeyBytes []byte
	if err := dec.Decode(&pubKeyBytes); err != nil {
		return err
	}
	paymentKey, err := GobDecodePubKey(pubKeyBytes)
	if err != nil {
		return err
	}
	p.PaymentKey = paymentKey

	if err := dec.Decode(&pubKeyBytes); err != nil {
		return err
	}
	revocationBasePoint, err := GobDecodePubKey(pubKeyBytes)
	if err != nil {
		return err
	}
	p.RevocationBasePoint = revocationBasePoint

	return nil
}
