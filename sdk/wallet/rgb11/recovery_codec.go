package rgb11wallet

import (
	"bytes"
	"compress/flate"
	"io"

	strict "github.com/sat20-labs/rgb11/strict_encoding"
)

const (
	rgb11RecoveryMagic        = "R11K"
	rgb11RecoveryCodecVersion = uint8(1)
	rgb11RecoveryMaxRawSize   = 4 * 1024 * 1024
	rgb11RecoveryMaxRecords   = 16 * 1024
	rgb11RecoveryMaxFieldSize = 1024 * 1024
)

func encodeRecoveryRecords(encoder *strict.Encoder, records []SnapshotRecord) error {
	if len(records) > rgb11RecoveryMaxRecords {
		return ErrRGB11Inconsistent
	}
	if err := encoder.Length(uint64(len(records)), rgb11RecoveryMaxRecords); err != nil {
		return err
	}
	for _, record := range records {
		if err := encoder.String(record.Key, 1, 64*1024); err != nil {
			return err
		}
		if err := encoder.Bytes(record.Value, 1, rgb11RecoveryMaxFieldSize); err != nil {
			return err
		}
	}
	return nil
}

func decodeRecoveryRecords(decoder *strict.Decoder) ([]SnapshotRecord, error) {
	count, err := decoder.Length(rgb11RecoveryMaxRecords)
	if err != nil {
		return nil, err
	}
	records := make([]SnapshotRecord, 0, count)
	for index := uint64(0); index < count; index++ {
		key, err := decoder.String(1, 64*1024)
		if err != nil {
			return nil, err
		}
		value, err := decoder.Bytes(1, rgb11RecoveryMaxFieldSize)
		if err != nil {
			return nil, err
		}
		records = append(records, SnapshotRecord{Key: key, Value: value})
	}
	return records, nil
}

func encodeRecoveryRaw(value *RecoveryPackage) ([]byte, error) {
	packageValue, err := normalizeRecoveryPackage(value)
	if err != nil {
		return nil, err
	}
	if err := ValidateRecoveryPackage(packageValue); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	encoder := strict.NewEncoder(&buf)
	for _, write := range []func() error{
		func() error { return encoder.Raw([]byte(rgb11RecoveryMagic)) },
		func() error { return encoder.U8(rgb11RecoveryCodecVersion) },
		func() error { return encoder.U32(packageValue.Version) },
		func() error { return encoder.String(packageValue.WalletID, 1, 128) },
		func() error { return encoder.U32(packageValue.AccountIndex) },
		func() error { return encoder.String(packageValue.EngineBuildID, 1, 1024) },
		func() error { return encodeRecoveryRecords(encoder, packageValue.ProjectionRecords) },
		func() error { return encodeRecoveryRecords(encoder, packageValue.EngineRecords) },
	} {
		if err := write(); err != nil {
			return nil, err
		}
	}
	if buf.Len() == 0 || buf.Len() > rgb11RecoveryMaxRawSize {
		return nil, ErrRGB11Inconsistent
	}
	return buf.Bytes(), nil
}

func decodeRecoveryRaw(raw []byte) (*RecoveryPackage, error) {
	if len(raw) == 0 || len(raw) > rgb11RecoveryMaxRawSize {
		return nil, ErrRGB11Inconsistent
	}
	reader := bytes.NewReader(raw)
	decoder := strict.NewDecoder(reader)
	magic, err := decoder.Raw(uint64(len(rgb11RecoveryMagic)))
	if err != nil || string(magic) != rgb11RecoveryMagic {
		return nil, ErrRGB11Inconsistent
	}
	codecVersion, err := decoder.U8()
	if err != nil || codecVersion != rgb11RecoveryCodecVersion {
		return nil, ErrRGB11Inconsistent
	}
	version, err := decoder.U32()
	if err != nil || version != RecoveryPackageVersion {
		return nil, ErrRGB11Inconsistent
	}
	walletID, err := decoder.String(1, 128)
	if err != nil {
		return nil, err
	}
	accountIndex, err := decoder.U32()
	if err != nil {
		return nil, err
	}
	buildID, err := decoder.String(1, 1024)
	if err != nil {
		return nil, err
	}
	projection, err := decodeRecoveryRecords(decoder)
	if err != nil {
		return nil, err
	}
	engine, err := decodeRecoveryRecords(decoder)
	if err != nil || reader.Len() != 0 {
		return nil, ErrRGB11Inconsistent
	}
	packageValue := &RecoveryPackage{
		Version: version, WalletID: walletID, AccountIndex: accountIndex,
		EngineBuildID: buildID, ProjectionRecords: projection, EngineRecords: engine,
	}
	if err := ValidateRecoveryPackage(packageValue); err != nil {
		return nil, err
	}
	return normalizeRecoveryPackage(packageValue)
}

func deflateRecovery(raw []byte) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString(rgb11RecoveryMagic)
	buf.WriteByte(rgb11RecoveryCodecVersion)
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

func inflateRecovery(value []byte) ([]byte, error) {
	if len(value) <= len(rgb11RecoveryMagic)+1 ||
		string(value[:len(rgb11RecoveryMagic)]) != rgb11RecoveryMagic ||
		value[len(rgb11RecoveryMagic)] != rgb11RecoveryCodecVersion {
		return nil, ErrRGB11Inconsistent
	}
	reader := flate.NewReader(bytes.NewReader(value[len(rgb11RecoveryMagic)+1:]))
	defer reader.Close()
	raw, err := io.ReadAll(io.LimitReader(reader, rgb11RecoveryMaxRawSize+1))
	if err != nil || len(raw) == 0 || len(raw) > rgb11RecoveryMaxRawSize {
		return nil, ErrRGB11Inconsistent
	}
	return raw, nil
}

func EncodeRecoveryPackage(value *RecoveryPackage) ([]byte, error) {
	raw, err := encodeRecoveryRaw(value)
	if err != nil {
		return nil, err
	}
	return deflateRecovery(raw)
}

func DecodeRecoveryPackage(value []byte) (*RecoveryPackage, error) {
	raw, err := inflateRecovery(value)
	if err != nil {
		return nil, err
	}
	packageValue, err := decodeRecoveryRaw(raw)
	if err != nil {
		return nil, err
	}
	canonical, err := EncodeRecoveryPackage(packageValue)
	if err != nil || !bytes.Equal(canonical, value) {
		return nil, ErrRGB11Inconsistent
	}
	return packageValue, nil
}
