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
	if len(metadata) == 0 {
		if len(data) > swire.MaxDKVSBlobValueSize {
			return nil, dkvsindexer.ErrRecordTooLarge
		}
		return append([]byte(nil), data...), nil
	}
	if len(metadata) > int(^uint32(0)) {
		return nil, dkvsindexer.ErrRecordTooLarge
	}
	value := make([]byte, 8+len(metadata)+len(data))
	copy(value, blobEnvelopeMagic)
	binary.BigEndian.PutUint32(value[4:8], uint32(len(metadata)))
	copy(value[8:], metadata)
	copy(value[8+len(metadata):], data)
	if len(value) > swire.MaxDKVSBlobValueSize {
		return nil, dkvsindexer.ErrRecordTooLarge
	}
	return value, nil
}

func DecodeBlobValue(value []byte) (*Blob, error) {
	if len(value) == 0 {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	blob := &Blob{}
	if len(value) < 8 || !bytes.Equal(value[:4], blobEnvelopeMagic) {
		blob.Data = append([]byte(nil), value...)
		return blob, nil
	}
	metadataSize := int(binary.BigEndian.Uint32(value[4:8]))
	if metadataSize <= 0 || metadataSize > len(value)-8 {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	blob.Metadata = append([]byte(nil), value[8:8+metadataSize]...)
	blob.Data = append([]byte(nil), value[8+metadataSize:]...)
	if len(blob.Data) == 0 {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	return blob, nil
}
