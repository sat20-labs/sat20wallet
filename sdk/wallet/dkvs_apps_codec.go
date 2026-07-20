package wallet

import (
	"bytes"
	"sort"

	strict "github.com/sat20-labs/rgb11/strict_encoding"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

const (
	dkvsAppPayloadMagic     = "DKWA"
	dkvsAppPayloadVersion   = uint8(1)
	dkvsRecoveryPayload     = uint8(1)
	dkvsGuardianPayload     = uint8(2)
	dkvsOfflinePayload      = uint8(3)
	dkvsAuthenticityPayload = uint8(4)
	dkvsAppMaxText          = 4 * 1024
	dkvsAppMaxCiphertext    = 64 * 1024
	dkvsAppMaxMetadata      = 64
)

func newDKVSAppEncoder(kind uint8) (*bytes.Buffer, *strict.Encoder, error) {
	var buf bytes.Buffer
	encoder := strict.NewEncoder(&buf)
	if err := encoder.Raw([]byte(dkvsAppPayloadMagic)); err != nil {
		return nil, nil, err
	}
	if err := encoder.U8(dkvsAppPayloadVersion); err != nil {
		return nil, nil, err
	}
	if err := encoder.U8(kind); err != nil {
		return nil, nil, err
	}
	return &buf, encoder, nil
}

func newDKVSAppDecoder(value []byte, expectedKind uint8) (*bytes.Reader, *strict.Decoder, error) {
	reader := bytes.NewReader(value)
	decoder := strict.NewDecoder(reader)
	magic, err := decoder.Raw(uint64(len(dkvsAppPayloadMagic)))
	if err != nil || string(magic) != dkvsAppPayloadMagic {
		return nil, nil, dkvsindexer.ErrInvalidRecord
	}
	version, err := decoder.U8()
	if err != nil || version != dkvsAppPayloadVersion {
		return nil, nil, dkvsindexer.ErrInvalidRecord
	}
	kind, err := decoder.U8()
	if err != nil || kind != expectedKind {
		return nil, nil, dkvsindexer.ErrInvalidRecord
	}
	return reader, decoder, nil
}

func encodeDKVSStringMap(encoder *strict.Encoder, metadata map[string]string) error {
	if len(metadata) > dkvsAppMaxMetadata {
		return dkvsindexer.ErrInvalidRecord
	}
	keys := make([]string, 0, len(metadata))
	for key := range metadata {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if err := encoder.Length(uint64(len(keys)), dkvsAppMaxMetadata); err != nil {
		return err
	}
	for _, key := range keys {
		if err := encoder.String(key, 1, dkvsAppMaxText); err != nil {
			return err
		}
		if err := encoder.String(metadata[key], 0, dkvsAppMaxText); err != nil {
			return err
		}
	}
	return nil
}

func decodeDKVSStringMap(decoder *strict.Decoder) (map[string]string, error) {
	count, err := decoder.Length(dkvsAppMaxMetadata)
	if err != nil {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	if count == 0 {
		return nil, nil
	}
	metadata := make(map[string]string, count)
	lastKey := ""
	for index := uint64(0); index < count; index++ {
		key, err := decoder.String(1, dkvsAppMaxText)
		if err != nil || (index > 0 && key <= lastKey) {
			return nil, dkvsindexer.ErrInvalidRecord
		}
		value, err := decoder.String(0, dkvsAppMaxText)
		if err != nil {
			return nil, dkvsindexer.ErrInvalidRecord
		}
		metadata[key] = value
		lastKey = key
	}
	return metadata, nil
}

func requireDKVSAppEOF(reader *bytes.Reader) error {
	if reader.Len() != 0 {
		return dkvsindexer.ErrInvalidRecord
	}
	return nil
}

func encodeDKVSRecoveryBackup(value DKVSWalletRecoveryBackup) ([]byte, error) {
	buf, encoder, err := newDKVSAppEncoder(dkvsRecoveryPayload)
	if err != nil {
		return nil, err
	}
	for _, encode := range []func() error{
		func() error { return encoder.U32(value.Version) },
		func() error { return encoder.String(value.WalletID, 1, dkvsAppMaxText) },
		func() error { return encoder.Bytes(value.EncryptedBackup, 1, dkvsAppMaxCiphertext) },
		func() error { return encodeDKVSStringMap(encoder, value.Metadata) },
	} {
		if err := encode(); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

func decodeDKVSRecoveryBackup(data []byte) (*DKVSWalletRecoveryBackup, error) {
	reader, decoder, err := newDKVSAppDecoder(data, dkvsRecoveryPayload)
	if err != nil {
		return nil, err
	}
	value := &DKVSWalletRecoveryBackup{}
	if value.Version, err = decoder.U32(); err != nil {
		return nil, err
	}
	if value.WalletID, err = decoder.String(1, dkvsAppMaxText); err != nil {
		return nil, err
	}
	if value.EncryptedBackup, err = decoder.Bytes(1, dkvsAppMaxCiphertext); err != nil {
		return nil, err
	}
	if value.Metadata, err = decodeDKVSStringMap(decoder); err != nil {
		return nil, err
	}
	if err := requireDKVSAppEOF(reader); err != nil {
		return nil, err
	}
	return value, nil
}

func encodeDKVSGuardianShare(value DKVSGuardianShare) ([]byte, error) {
	buf, encoder, err := newDKVSAppEncoder(dkvsGuardianPayload)
	if err != nil {
		return nil, err
	}
	for _, encode := range []func() error{
		func() error { return encoder.U32(value.Version) },
		func() error { return encoder.String(value.PackageID, 1, dkvsAppMaxText) },
		func() error { return encoder.String(value.ShareID, 1, dkvsAppMaxText) },
		func() error { return encoder.Bytes(value.Ciphertext, 1, dkvsAppMaxCiphertext) },
		func() error { return encodeDKVSStringMap(encoder, value.Metadata) },
	} {
		if err := encode(); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

func decodeDKVSGuardianShare(data []byte) (*DKVSGuardianShare, error) {
	reader, decoder, err := newDKVSAppDecoder(data, dkvsGuardianPayload)
	if err != nil {
		return nil, err
	}
	value := &DKVSGuardianShare{}
	if value.Version, err = decoder.U32(); err != nil {
		return nil, err
	}
	if value.PackageID, err = decoder.String(1, dkvsAppMaxText); err != nil {
		return nil, err
	}
	if value.ShareID, err = decoder.String(1, dkvsAppMaxText); err != nil {
		return nil, err
	}
	if value.Ciphertext, err = decoder.Bytes(1, dkvsAppMaxCiphertext); err != nil {
		return nil, err
	}
	if value.Metadata, err = decodeDKVSStringMap(decoder); err != nil {
		return nil, err
	}
	if err := requireDKVSAppEOF(reader); err != nil {
		return nil, err
	}
	return value, nil
}

func encodeDKVSOfflineMessage(value DKVSOfflineMessage) ([]byte, error) {
	buf, encoder, err := newDKVSAppEncoder(dkvsOfflinePayload)
	if err != nil {
		return nil, err
	}
	for _, encode := range []func() error{
		func() error { return encoder.U32(value.Version) },
		func() error { return encoder.Bytes(value.FromPubKey, 1, 128) },
		func() error { return encoder.String(value.ToMailboxID, 1, dkvsAppMaxText) },
		func() error { return encoder.String(value.MessageID, 1, dkvsAppMaxText) },
		func() error { return encoder.Bytes(value.EncryptedMessage, 1, dkvsAppMaxCiphertext) },
		func() error { return encodeDKVSStringMap(encoder, value.Metadata) },
	} {
		if err := encode(); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

func decodeDKVSOfflineMessage(data []byte) (*DKVSOfflineMessage, error) {
	reader, decoder, err := newDKVSAppDecoder(data, dkvsOfflinePayload)
	if err != nil {
		return nil, err
	}
	value := &DKVSOfflineMessage{}
	if value.Version, err = decoder.U32(); err != nil {
		return nil, err
	}
	if value.FromPubKey, err = decoder.Bytes(1, 128); err != nil {
		return nil, err
	}
	if value.ToMailboxID, err = decoder.String(1, dkvsAppMaxText); err != nil {
		return nil, err
	}
	if value.MessageID, err = decoder.String(1, dkvsAppMaxText); err != nil {
		return nil, err
	}
	if value.EncryptedMessage, err = decoder.Bytes(1, dkvsAppMaxCiphertext); err != nil {
		return nil, err
	}
	if value.Metadata, err = decodeDKVSStringMap(decoder); err != nil {
		return nil, err
	}
	if err := requireDKVSAppEOF(reader); err != nil {
		return nil, err
	}
	return value, nil
}

func encodeDKVSServiceAuthenticity(value DKVSServiceAuthenticity) ([]byte, error) {
	buf, encoder, err := newDKVSAppEncoder(dkvsAuthenticityPayload)
	if err != nil {
		return nil, err
	}
	for _, encode := range []func() error{
		func() error { return encoder.U32(value.Version) },
		func() error { return encoder.String(value.ServiceName, 1, dkvsAppMaxText) },
		func() error { return encoder.String(value.AppID, 1, dkvsAppMaxText) },
		func() error { return encoder.String(value.Release, 0, dkvsAppMaxText) },
		func() error { return encoder.String(value.ArtifactHash, 1, dkvsAppMaxText) },
		func() error { return encoder.String(value.DownloadURL, 0, dkvsAppMaxText) },
		func() error { return encodeDKVSStringMap(encoder, value.Metadata) },
	} {
		if err := encode(); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

func decodeDKVSServiceAuthenticity(data []byte) (*DKVSServiceAuthenticity, error) {
	reader, decoder, err := newDKVSAppDecoder(data, dkvsAuthenticityPayload)
	if err != nil {
		return nil, err
	}
	value := &DKVSServiceAuthenticity{}
	if value.Version, err = decoder.U32(); err != nil {
		return nil, err
	}
	if value.ServiceName, err = decoder.String(1, dkvsAppMaxText); err != nil {
		return nil, err
	}
	if value.AppID, err = decoder.String(1, dkvsAppMaxText); err != nil {
		return nil, err
	}
	if value.Release, err = decoder.String(0, dkvsAppMaxText); err != nil {
		return nil, err
	}
	if value.ArtifactHash, err = decoder.String(1, dkvsAppMaxText); err != nil {
		return nil, err
	}
	if value.DownloadURL, err = decoder.String(0, dkvsAppMaxText); err != nil {
		return nil, err
	}
	if value.Metadata, err = decodeDKVSStringMap(decoder); err != nil {
		return nil, err
	}
	if err := requireDKVSAppEOF(reader); err != nil {
		return nil, err
	}
	return value, nil
}
