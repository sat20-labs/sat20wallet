package dkvs

import (
	"bytes"
	"encoding/binary"

	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

var blobEnvelopeMagic = []byte{'D', 'K', 'B', '1'}

type Blob struct {
	Data     []byte
	Metadata []byte
}

// EncodeBlobValue treats Blob data as opaque. It deliberately does not apply
// implicit compression: callers may already provide compressed or encrypted
// bytes, and the generic Blob layer must preserve predictable size semantics.
func EncodeBlobValue(data, metadata []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	// One envelope for every value keeps arbitrary application bytes distinct
	// from framing, including payloads which start with the envelope magic.
	if len(data) > swire.MaxDKVSBlobValueSize-8 || len(metadata) > swire.MaxDKVSBlobValueSize-8-len(data) {
		return nil, dkvsindexer.ErrRecordTooLarge
	}
	value := make([]byte, 8+len(metadata)+len(data))
	copy(value, blobEnvelopeMagic)
	binary.BigEndian.PutUint32(value[4:8], uint32(len(metadata)))
	copy(value[8:], metadata)
	copy(value[8+len(metadata):], data)
	return value, nil
}

func DecodeBlobValue(value []byte) (*Blob, error) {
	if len(value) > swire.MaxDKVSBlobValueSize {
		return nil, dkvsindexer.ErrRecordTooLarge
	}
	if len(value) <= 8 || !bytes.Equal(value[:4], blobEnvelopeMagic) {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	blob := &Blob{}
	metadataSize := int(binary.BigEndian.Uint32(value[4:8]))
	if metadataSize < 0 || metadataSize >= len(value)-8 {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	blob.Metadata = append([]byte(nil), value[8:8+metadataSize]...)
	blob.Data = append([]byte(nil), value[8+metadataSize:]...)
	return blob, nil
}
