package dkvs

import (
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

const (
	activeMetaMagic           = "DKAM"
	activeAckMagic            = "DKAK"
	activeReplicaCodecVersion = uint8(1)
)

func encodeActiveMeta(meta dkvsindexer.ActiveMeta) ([]byte, error) {
	scope, err := dkvsindexer.NormalizeActiveScope(meta.Scope)
	if err != nil || !dkvsindexer.SameActiveScope(scope, meta.Scope) || meta.EndpointID == "" {
		return nil, dkvsindexer.ErrInvalidSnapshot
	}
	e := newWalletDKVSEncoder(activeMetaMagic, activeReplicaCodecVersion)
	if err := e.string(meta.EndpointID, MaxActiveReplicaBytes); err != nil {
		return nil, err
	}
	if err := e.string(meta.Scope.Prefix, dkvsindexer.MaxPrefixLength); err != nil {
		return nil, err
	}
	e.uvarint(uint64(len(meta.Scope.Keys)))
	for _, key := range meta.Scope.Keys {
		if err := e.string(key, dkvsindexer.MaxKeySize); err != nil {
			return nil, err
		}
	}
	if meta.Scope.Network {
		e.u8(1)
	} else {
		e.u8(0)
	}
	e.uvarint(meta.Generation)
	e.raw(meta.Root[:])
	e.uvarint(meta.ViewHeight)
	if len(e.encoded()) > MaxActiveReplicaBytes {
		return nil, dkvsindexer.ErrInvalidSnapshot
	}
	return e.encoded(), nil
}

func decodeActiveMeta(encoded []byte) (*dkvsindexer.ActiveMeta, error) {
	if len(encoded) > MaxActiveReplicaBytes {
		return nil, dkvsindexer.ErrInvalidSnapshot
	}
	d, err := newWalletDKVSDecoder(encoded, activeMetaMagic, activeReplicaCodecVersion)
	if err != nil {
		return nil, err
	}
	meta := &dkvsindexer.ActiveMeta{}
	meta.EndpointID, err = d.string(MaxActiveReplicaBytes)
	if err != nil || meta.EndpointID == "" {
		return nil, dkvsindexer.ErrInvalidSnapshot
	}
	meta.Scope.Prefix, err = d.string(dkvsindexer.MaxPrefixLength)
	if err != nil {
		return nil, err
	}
	count, err := d.uvarint()
	if err != nil || count > dkvsindexer.MaxPrefixReadRecords {
		return nil, dkvsindexer.ErrInvalidSnapshot
	}
	if count != 0 {
		meta.Scope.Keys = make([]string, int(count))
	}
	for n := range meta.Scope.Keys {
		meta.Scope.Keys[n], err = d.string(dkvsindexer.MaxKeySize)
		if err != nil {
			return nil, err
		}
	}
	network, err := d.u8()
	if err != nil || network > 1 {
		return nil, dkvsindexer.ErrInvalidSnapshot
	}
	meta.Scope.Network = network == 1
	meta.Generation, err = d.uvarint()
	if err != nil {
		return nil, err
	}
	root, err := d.raw(chainhash.HashSize)
	if err != nil {
		return nil, err
	}
	copy(meta.Root[:], root)
	meta.ViewHeight, err = d.uvarint()
	if err != nil || !d.done() {
		return nil, dkvsindexer.ErrInvalidSnapshot
	}
	scope, err := dkvsindexer.NormalizeActiveScope(meta.Scope)
	if err != nil || !dkvsindexer.SameActiveScope(scope, meta.Scope) {
		return nil, dkvsindexer.ErrInvalidSnapshot
	}
	return meta, nil
}

func encodeActiveAckMark(mark activeAckMark) ([]byte, error) {
	e := newWalletDKVSEncoder(activeAckMagic, activeReplicaCodecVersion)
	if err := e.string(mark.EndpointID, MaxActiveReplicaBytes); err != nil {
		return nil, err
	}
	e.uvarint(mark.Generation)
	return e.encoded(), nil
}

func decodeActiveAckMark(encoded []byte) (activeAckMark, error) {
	var mark activeAckMark
	d, err := newWalletDKVSDecoder(encoded, activeAckMagic, activeReplicaCodecVersion)
	if err != nil {
		return mark, err
	}
	mark.EndpointID, err = d.string(MaxActiveReplicaBytes)
	if err != nil {
		return mark, err
	}
	mark.Generation, err = d.uvarint()
	if err != nil || !d.done() {
		return mark, dkvsindexer.ErrInvalidRecord
	}
	return mark, nil
}
