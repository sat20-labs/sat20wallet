package dkvs

import (
	"bytes"
	"testing"

	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDKVSReviewOpaqueBlobMagicMustRoundTrip(t *testing.T) {
	for _, test := range []struct {
		name string
		data []byte
	}{
		{"valid-envelope-prefix", []byte{'D', 'K', 'B', '1', 0, 0, 0, 1, 'm', 'd'}},
		{"invalid-envelope-prefix", []byte{'D', 'K', 'B', '1', 0xff, 0xff, 0xff, 0xff, 'd'}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Blob data is opaque, so a caller may supply bytes which happen to
			// start with the codec's own envelope marker without supplying metadata.
			encoded, err := EncodeBlobValue(test.data, nil)
			require.NoError(t, err)
			decoded, err := DecodeBlobValue(encoded)
			if !assert.NoError(t, err, "every accepted opaque blob must remain readable") {
				return
			}
			assert.Equal(t, test.data, decoded.Data, "decoding must preserve every data byte")
			assert.Empty(t, decoded.Metadata, "data bytes must not be reinterpreted as metadata")
		})
	}
}

func TestDKVSReviewBlobEnvelopeCapacityAndValidation(t *testing.T) {
	data := bytes.Repeat([]byte{'d'}, wire.MaxDKVSBlobValueSize-8)
	encoded, err := EncodeBlobValue(data, nil)
	require.NoError(t, err)
	assert.Len(t, encoded, wire.MaxDKVSBlobValueSize)
	decoded, err := DecodeBlobValue(encoded)
	require.NoError(t, err)
	assert.Equal(t, data, decoded.Data)
	_, err = EncodeBlobValue(append(data, 'd'), nil)
	assert.ErrorIs(t, err, dkvsindexer.ErrRecordTooLarge)
	_, err = EncodeBlobValue(data, []byte{'m'})
	assert.ErrorIs(t, err, dkvsindexer.ErrRecordTooLarge)
	_, err = EncodeBlobValue(nil, nil)
	assert.ErrorIs(t, err, dkvsindexer.ErrInvalidRecord)
	for _, value := range [][]byte{
		[]byte("unframed old data"),
		{'D', 'K', 'B', '1', 0, 0, 0, 0},
		{'D', 'K', 'B', '1', 0, 0, 0, 1, 'm'},
		{'D', 'K', 'B', '1', 0xff, 0xff, 0xff, 0xff, 'd'},
	} {
		_, err := DecodeBlobValue(value)
		assert.ErrorIs(t, err, dkvsindexer.ErrInvalidRecord)
	}
	_, err = DecodeBlobValue(bytes.Repeat([]byte{'d'}, wire.MaxDKVSBlobValueSize+1))
	assert.ErrorIs(t, err, dkvsindexer.ErrRecordTooLarge)
}
