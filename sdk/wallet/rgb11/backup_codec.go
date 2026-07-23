package rgb11wallet

import (
	"bytes"
	"compress/flate"
	"fmt"
	"io"

	indexer "github.com/sat20-labs/indexer/common"
	strict "github.com/sat20-labs/rgb11/strict_encoding"
	coresync "github.com/sat20-labs/rgb11/sync"
)

const (
	rgb11SnapshotPayloadMagic  = "R11S"
	rgb11SnapshotEnvelopeMagic = "R11E"
	rgb11AutoBackupPolicyMagic = "R11P"
	rgb11SnapshotCodecVersion  = uint8(2)
	rgb11SnapshotMaxRawSize    = 4 * 1024 * 1024
	rgb11SnapshotMaxRecords    = 16 * 1024
	rgb11SnapshotMaxFieldSize  = 1024 * 1024
)

// SnapshotPayloadMagic and SnapshotEnvelopeMagic identify the stable RGB11 wallet recovery codecs.
const (
	SnapshotPayloadMagic  = rgb11SnapshotPayloadMagic
	SnapshotEnvelopeMagic = rgb11SnapshotEnvelopeMagic
)

func validAutoBackupPolicy(policy *RGB11AutoBackupPolicy) bool {
	if policy == nil || policy.Version != 1 || !policy.Enabled {
		return false
	}
	// TTL=0 and ExpiryHeight=0 denotes continuous AUTOPAY retention.
	// A non-zero TTL denotes bounded temporary retention.
	return policy.TTL != 0 || policy.ExpiryHeight == 0
}

func EncodeAutoBackupPolicy(policy *RGB11AutoBackupPolicy) ([]byte, error) {
	if !validAutoBackupPolicy(policy) {
		return nil, ErrRGB11Inconsistent
	}
	var buf bytes.Buffer
	encoder := strict.NewEncoder(&buf)
	for _, encode := range []func() error{
		func() error { return encoder.Raw([]byte(rgb11AutoBackupPolicyMagic)) },
		func() error { return encoder.U8(rgb11SnapshotCodecVersion) },
		func() error { return encoder.U32(policy.Version) },
		func() error { return encoder.Bool(policy.Enabled) },
		func() error { return encoder.U64(policy.TTL) },
		func() error { return encoder.U64(policy.ExpiryHeight) },
	} {
		if err := encode(); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

func DecodeAutoBackupPolicy(value []byte) (*RGB11AutoBackupPolicy, error) {
	reader := bytes.NewReader(value)
	decoder := strict.NewDecoder(reader)
	magic, err := decoder.Raw(uint64(len(rgb11AutoBackupPolicyMagic)))
	if err != nil || string(magic) != rgb11AutoBackupPolicyMagic {
		return nil, ErrRGB11Inconsistent
	}
	codecVersion, err := decoder.U8()
	if err != nil || codecVersion != rgb11SnapshotCodecVersion {
		return nil, ErrRGB11Inconsistent
	}
	policy := &RGB11AutoBackupPolicy{}
	if policy.Version, err = decoder.U32(); err != nil {
		return nil, err
	}
	if policy.Enabled, err = decoder.Bool(); err != nil {
		return nil, err
	}
	if policy.TTL, err = decoder.U64(); err != nil {
		return nil, err
	}
	if policy.ExpiryHeight, err = decoder.U64(); err != nil || reader.Len() != 0 || !validAutoBackupPolicy(policy) {
		return nil, ErrRGB11Inconsistent
	}
	return policy, nil
}

// EncodeWalletSnapshotPayload uses a deterministic binary layout and
// compresses it before encryption.
func EncodeWalletSnapshotPayload(snapshot *RGB11WalletSnapshot) ([]byte, error) {
	encoded, err := encodeCompactRGB11WalletSnapshot(snapshot)
	if err != nil {
		return nil, err
	}
	return deflateRGB11Snapshot(encoded)
}

func DecodeWalletSnapshotPayload(payload []byte) (*RGB11WalletSnapshot, error) {
	decoded, err := inflateRGB11Snapshot(payload)
	if err != nil {
		return nil, err
	}
	return decodeCompactRGB11WalletSnapshot(decoded)
}

func encodeCompactRGB11WalletSnapshot(snapshot *RGB11WalletSnapshot) ([]byte, error) {
	if snapshot == nil || snapshot.WalletID == "" || len(snapshot.WalletID) > 128 {
		return nil, ErrRGB11Inconsistent
	}
	var buf bytes.Buffer
	encoder := strict.NewEncoder(&buf)
	if err := encoder.Raw([]byte(rgb11SnapshotPayloadMagic)); err != nil {
		return nil, err
	}
	if err := encoder.U8(rgb11SnapshotCodecVersion); err != nil {
		return nil, err
	}
	if err := encoder.U32(snapshot.Version); err != nil {
		return nil, err
	}
	if err := encoder.String(snapshot.WalletID, 1, 128); err != nil {
		return nil, err
	}
	if err := encoder.U32(snapshot.AccountIndex); err != nil {
		return nil, err
	}
	if err := encoder.String(snapshot.EngineBuildID, 0, 1024); err != nil {
		return nil, err
	}
	if err := encodeRGB11SnapshotRecords(encoder, snapshot.ProjectionRecords); err != nil {
		return nil, err
	}
	if err := encodeRGB11SnapshotRecords(encoder, snapshot.EngineRecords); err != nil {
		return nil, err
	}
	if len(snapshot.TickerInfos) > rgb11SnapshotMaxRecords {
		return nil, ErrRGB11Inconsistent
	}
	if err := encoder.Length(uint64(len(snapshot.TickerInfos)), rgb11SnapshotMaxRecords); err != nil {
		return nil, err
	}
	for _, info := range snapshot.TickerInfos {
		if info == nil {
			return nil, ErrRGB11Inconsistent
		}
		if err := encodeRGB11TickerInfo(encoder, info); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

func decodeCompactRGB11WalletSnapshot(value []byte) (*RGB11WalletSnapshot, error) {
	reader := bytes.NewReader(value)
	decoder := strict.NewDecoder(reader)
	magic, err := decoder.Raw(uint64(len(rgb11SnapshotPayloadMagic)))
	if err != nil || string(magic) != rgb11SnapshotPayloadMagic {
		return nil, ErrRGB11Inconsistent
	}
	codecVersion, err := decoder.U8()
	if err != nil || codecVersion != rgb11SnapshotCodecVersion {
		return nil, ErrRGB11Inconsistent
	}
	version, err := decoder.U32()
	if err != nil {
		return nil, err
	}
	walletID, err := decoder.String(1, 128)
	if err != nil {
		return nil, err
	}
	accountIndex, err := decoder.U32()
	if err != nil {
		return nil, err
	}
	buildID, err := decoder.String(0, 1024)
	if err != nil {
		return nil, err
	}
	projection, err := decodeRGB11SnapshotRecords(decoder)
	if err != nil {
		return nil, err
	}
	engine, err := decodeRGB11SnapshotRecords(decoder)
	if err != nil {
		return nil, err
	}
	tickerCount, err := decoder.Length(rgb11SnapshotMaxRecords)
	if err != nil {
		return nil, err
	}
	tickers := make([]*indexer.TickerInfo, 0, tickerCount)
	for index := uint64(0); index < tickerCount; index++ {
		info, err := decodeRGB11TickerInfo(decoder)
		if err != nil {
			return nil, err
		}
		tickers = append(tickers, info)
	}
	if reader.Len() != 0 {
		return nil, ErrRGB11Inconsistent
	}
	return &RGB11WalletSnapshot{
		Version: version, WalletID: walletID, AccountIndex: accountIndex, EngineBuildID: buildID,
		ProjectionRecords: projection, EngineRecords: engine, TickerInfos: tickers,
	}, nil
}

func encodeRGB11TickerInfo(encoder *strict.Encoder, info *indexer.TickerInfo) error {
	if info == nil {
		return ErrRGB11Inconsistent
	}
	for _, encode := range []func() error{
		func() error { return encoder.String(info.Protocol, 0, 128) },
		func() error { return encoder.String(info.Type, 0, 128) },
		func() error { return encoder.String(info.Ticker, 0, rgb11SnapshotMaxFieldSize) },
		func() error { return encoder.String(info.DisplayName, 0, rgb11SnapshotMaxFieldSize) },
		func() error { return encoder.U64(uint64(info.Id)) },
		func() error { return encoder.U64(uint64(int64(info.Divisibility))) },
		func() error { return encoder.U64(uint64(int64(info.StartBlock))) },
		func() error { return encoder.U64(uint64(int64(info.EndBlock))) },
		func() error { return encoder.U64(uint64(int64(info.SelfMint))) },
		func() error { return encoder.U64(uint64(int64(info.DeployHeight))) },
		func() error { return encoder.U64(uint64(info.DeployBlocktime)) },
		func() error { return encoder.String(info.DeployTx, 0, 128) },
		func() error { return encoder.String(info.Limit, 0, 1024) },
		func() error { return encoder.U64(uint64(int64(info.N))) },
		func() error { return encoder.String(info.TotalMinted, 0, 1024) },
		func() error { return encoder.U64(uint64(info.MintTimes)) },
		func() error { return encoder.String(info.MaxSupply, 0, 1024) },
		func() error { return encoder.U64(uint64(int64(info.HoldersCount))) },
		func() error { return encoder.String(info.InscriptionId, 0, 128) },
		func() error { return encoder.U64(uint64(info.InscriptionNum)) },
		func() error { return encoder.String(info.Description, 0, rgb11SnapshotMaxFieldSize) },
		func() error { return encoder.String(info.Rarity, 0, 1024) },
		func() error { return encoder.String(info.DeployAddress, 0, 256) },
		func() error { return encoder.Bytes(info.Content, 0, rgb11SnapshotMaxFieldSize) },
		func() error { return encoder.String(info.ContentType, 0, 1024) },
		func() error { return encoder.String(info.Delegate, 0, 256) },
		func() error { return encoder.U64(uint64(int64(info.Status))) },
	} {
		if err := encode(); err != nil {
			return err
		}
	}
	return nil
}

func decodeRGB11TickerInfo(decoder *strict.Decoder) (*indexer.TickerInfo, error) {
	info := &indexer.TickerInfo{}
	var err error
	if info.Protocol, err = decoder.String(0, 128); err != nil {
		return nil, err
	}
	if info.Type, err = decoder.String(0, 128); err != nil {
		return nil, err
	}
	if info.Ticker, err = decoder.String(0, rgb11SnapshotMaxFieldSize); err != nil {
		return nil, err
	}
	if info.DisplayName, err = decoder.String(0, rgb11SnapshotMaxFieldSize); err != nil {
		return nil, err
	}
	if info.Id, err = decodeRGB11Int64(decoder); err != nil {
		return nil, err
	}
	if info.Divisibility, err = decodeRGB11Int(decoder); err != nil {
		return nil, err
	}
	if info.StartBlock, err = decodeRGB11Int(decoder); err != nil {
		return nil, err
	}
	if info.EndBlock, err = decodeRGB11Int(decoder); err != nil {
		return nil, err
	}
	if info.SelfMint, err = decodeRGB11Int(decoder); err != nil {
		return nil, err
	}
	if info.DeployHeight, err = decodeRGB11Int(decoder); err != nil {
		return nil, err
	}
	if info.DeployBlocktime, err = decodeRGB11Int64(decoder); err != nil {
		return nil, err
	}
	if info.DeployTx, err = decoder.String(0, 128); err != nil {
		return nil, err
	}
	if info.Limit, err = decoder.String(0, 1024); err != nil {
		return nil, err
	}
	if info.N, err = decodeRGB11Int(decoder); err != nil {
		return nil, err
	}
	if info.TotalMinted, err = decoder.String(0, 1024); err != nil {
		return nil, err
	}
	if info.MintTimes, err = decodeRGB11Int64(decoder); err != nil {
		return nil, err
	}
	if info.MaxSupply, err = decoder.String(0, 1024); err != nil {
		return nil, err
	}
	if info.HoldersCount, err = decodeRGB11Int(decoder); err != nil {
		return nil, err
	}
	if info.InscriptionId, err = decoder.String(0, 128); err != nil {
		return nil, err
	}
	if info.InscriptionNum, err = decodeRGB11Int64(decoder); err != nil {
		return nil, err
	}
	if info.Description, err = decoder.String(0, rgb11SnapshotMaxFieldSize); err != nil {
		return nil, err
	}
	if info.Rarity, err = decoder.String(0, 1024); err != nil {
		return nil, err
	}
	if info.DeployAddress, err = decoder.String(0, 256); err != nil {
		return nil, err
	}
	if info.Content, err = decoder.Bytes(0, rgb11SnapshotMaxFieldSize); err != nil {
		return nil, err
	}
	if info.ContentType, err = decoder.String(0, 1024); err != nil {
		return nil, err
	}
	if info.Delegate, err = decoder.String(0, 256); err != nil {
		return nil, err
	}
	if info.Status, err = decodeRGB11Int(decoder); err != nil {
		return nil, err
	}
	return info, nil
}

func decodeRGB11Int64(decoder *strict.Decoder) (int64, error) {
	value, err := decoder.U64()
	return int64(value), err
}

func decodeRGB11Int(decoder *strict.Decoder) (int, error) {
	value, err := decodeRGB11Int64(decoder)
	return int(value), err
}

func encodeRGB11SnapshotRecords(encoder *strict.Encoder, records []SnapshotRecord) error {
	if len(records) > rgb11SnapshotMaxRecords {
		return ErrRGB11Inconsistent
	}
	if err := encoder.Length(uint64(len(records)), rgb11SnapshotMaxRecords); err != nil {
		return err
	}
	for _, record := range records {
		if err := encoder.String(record.Key, 1, 64*1024); err != nil {
			return err
		}
		if err := encoder.Bytes(record.Value, 1, rgb11SnapshotMaxFieldSize); err != nil {
			return err
		}
	}
	return nil
}

func decodeRGB11SnapshotRecords(decoder *strict.Decoder) ([]SnapshotRecord, error) {
	count, err := decoder.Length(rgb11SnapshotMaxRecords)
	if err != nil {
		return nil, err
	}
	records := make([]SnapshotRecord, 0, count)
	for index := uint64(0); index < count; index++ {
		key, err := decoder.String(1, 64*1024)
		if err != nil {
			return nil, err
		}
		value, err := decoder.Bytes(1, rgb11SnapshotMaxFieldSize)
		if err != nil {
			return nil, err
		}
		records = append(records, SnapshotRecord{Key: key, Value: value})
	}
	return records, nil
}

func deflateRGB11Snapshot(raw []byte) ([]byte, error) {
	if len(raw) == 0 || len(raw) > rgb11SnapshotMaxRawSize {
		return nil, ErrRGB11Inconsistent
	}
	var buf bytes.Buffer
	buf.WriteString(rgb11SnapshotPayloadMagic)
	buf.WriteByte(rgb11SnapshotCodecVersion)
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

func inflateRGB11Snapshot(payload []byte) ([]byte, error) {
	if len(payload) <= len(rgb11SnapshotPayloadMagic)+1 || string(payload[:len(rgb11SnapshotPayloadMagic)]) != rgb11SnapshotPayloadMagic || payload[len(rgb11SnapshotPayloadMagic)] != rgb11SnapshotCodecVersion {
		return nil, ErrRGB11Inconsistent
	}
	reader := flate.NewReader(bytes.NewReader(payload[len(rgb11SnapshotPayloadMagic)+1:]))
	defer reader.Close()
	raw, err := io.ReadAll(io.LimitReader(reader, rgb11SnapshotMaxRawSize+1))
	if err != nil || len(raw) == 0 || len(raw) > rgb11SnapshotMaxRawSize {
		return nil, ErrRGB11Inconsistent
	}
	return raw, nil
}

func EncodeEncryptedSnapshot(walletID string, operationID [32]byte, ciphertext []byte) ([]byte, error) {
	if walletID == "" || len(walletID) > 128 || len(ciphertext) == 0 || len(ciphertext) > rgb11SnapshotMaxRawSize {
		return nil, ErrRGB11Inconsistent
	}
	var buf bytes.Buffer
	encoder := strict.NewEncoder(&buf)
	if err := encoder.Raw([]byte(rgb11SnapshotEnvelopeMagic)); err != nil {
		return nil, err
	}
	if err := encoder.U8(rgb11SnapshotCodecVersion); err != nil {
		return nil, err
	}
	if err := encoder.String(walletID, 1, 128); err != nil {
		return nil, err
	}
	if err := encoder.Raw(operationID[:]); err != nil {
		return nil, err
	}
	if err := encoder.Bytes(ciphertext, 1, rgb11SnapshotMaxRawSize); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func DecodeEncryptedSnapshot(value []byte) (walletID string, operationID [32]byte, ciphertext []byte, err error) {
	reader := bytes.NewReader(value)
	decoder := strict.NewDecoder(reader)
	magic, decodeErr := decoder.Raw(uint64(len(rgb11SnapshotEnvelopeMagic)))
	if decodeErr != nil || string(magic) != rgb11SnapshotEnvelopeMagic {
		return "", operationID, nil, ErrRGB11Inconsistent
	}
	codecVersion, decodeErr := decoder.U8()
	if decodeErr != nil || codecVersion != rgb11SnapshotCodecVersion {
		return "", operationID, nil, ErrRGB11Inconsistent
	}
	walletID, err = decoder.String(1, 128)
	if err != nil {
		return "", operationID, nil, err
	}
	operation, decodeErr := decoder.Raw(32)
	if decodeErr != nil {
		return "", operationID, nil, decodeErr
	}
	copy(operationID[:], operation)
	ciphertext, err = decoder.Bytes(1, rgb11SnapshotMaxRawSize)
	if err != nil || reader.Len() != 0 {
		return "", operationID, nil, ErrRGB11Inconsistent
	}
	return walletID, operationID, ciphertext, nil
}

func DecodeWalletHead(value []byte) (*coresync.WalletHead, error) {
	reader := bytes.NewReader(value)
	decoder := strict.NewDecoder(reader)
	version, err := decoder.U32()
	if err != nil {
		return nil, err
	}
	walletID, err := decoder.String(1, 128)
	if err != nil {
		return nil, err
	}
	seq, err := decoder.U64()
	if err != nil {
		return nil, err
	}
	stateHash, err := decoder.Raw(32)
	if err != nil {
		return nil, err
	}
	operationID, err := decoder.Raw(32)
	if err != nil || reader.Len() != 0 {
		return nil, fmt.Errorf("%w: invalid RGB11 head payload", ErrRGB11Inconsistent)
	}
	head := &coresync.WalletHead{Version: version, WalletID: walletID, Seq: seq}
	copy(head.StateHash[:], stateHash)
	copy(head.OperationID[:], operationID)
	return head, nil
}
