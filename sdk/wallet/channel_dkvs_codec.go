package wallet

import (
	"bytes"
	"compress/flate"
	"encoding/hex"
	"fmt"
	"io"
)

const (
	channelDKVSMagic      = "DKCH"
	punishTxDKVSMagic     = "DKPT"
	channelDKVSVersion    = byte(1)
	channelDKVSMaxRawSize = 8 * 1024 * 1024
)

// EncodeChannelDKVSValue compresses the prepared channel snapshot before it
// is written to the legacy L1 key-value backup store.
func EncodeChannelDKVSValue(raw []byte) ([]byte, error) {
	if len(raw) == 0 || len(raw) > channelDKVSMaxRawSize {
		return nil, fmt.Errorf("invalid compact channel backup")
	}
	var buf bytes.Buffer
	buf.WriteString(channelDKVSMagic)
	buf.WriteByte(channelDKVSVersion)
	writer, err := flate.NewWriter(&buf, flate.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := writer.Write(raw); err != nil {
		_ = writer.Close()
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// DecodeChannelDKVSValue accepts only the current compact channel backup
// format; legacy uncompressed values are intentionally not accepted.
func DecodeChannelDKVSValue(value []byte) ([]byte, error) {
	if len(value) <= len(channelDKVSMagic)+1 || string(value[:len(channelDKVSMagic)]) != channelDKVSMagic || value[len(channelDKVSMagic)] != channelDKVSVersion {
		return nil, fmt.Errorf("invalid compact channel backup")
	}
	reader := flate.NewReader(bytes.NewReader(value[len(channelDKVSMagic)+1:]))
	defer reader.Close()
	raw, err := io.ReadAll(io.LimitReader(reader, channelDKVSMaxRawSize+1))
	if err != nil || len(raw) == 0 || len(raw) > channelDKVSMaxRawSize {
		return nil, fmt.Errorf("invalid compact channel backup")
	}
	return raw, nil
}

// EncodePunishTxDKVSValue stores raw transaction bytes instead of their
// two-times-larger hexadecimal representation.
func EncodePunishTxDKVSValue(txsHex string) ([]byte, error) {
	raw, err := hex.DecodeString(txsHex)
	if err != nil || len(raw) == 0 || len(raw) > channelDKVSMaxRawSize {
		return nil, fmt.Errorf("invalid compact punish transaction backup")
	}
	value := make([]byte, len(punishTxDKVSMagic)+1+len(raw))
	copy(value, punishTxDKVSMagic)
	value[len(punishTxDKVSMagic)] = channelDKVSVersion
	copy(value[len(punishTxDKVSMagic)+1:], raw)
	return value, nil
}
