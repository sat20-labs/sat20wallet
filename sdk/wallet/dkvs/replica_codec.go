package dkvs

import (
	"bytes"
	"encoding/binary"
	"io"
	"strings"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

const (
	dkvsSubscriptionStateMagic = "DKSS"
	dkvsLocalKeyStateMagic     = "DKKS"
	dkvsOutboxEntryMagic       = "DKOB"
	dkvsStorageCodecVersion    = uint8(3)

	maxDKVSEndpointIDSize = uint64(512)
	maxDKVSNamespaceSize  = uint64(128)
	maxDKVSRequestIDSize  = uint64(128)
	maxDKVSErrorCodeSize  = uint64(128)
	maxDKVSErrorSize      = uint64(4096)
	maxDKVSOriginDomain   = uint64(128)
	maxDKVSStateEncoded   = uint64(16 * 1024)
	maxDKVSOutboxEncoded  = uint64(dkvsindexer.MaxBatchCASTotalSize + 64*1024)
)

type walletDKVSEncoder struct {
	buf bytes.Buffer
}

type walletDKVSDecoder struct {
	reader *bytes.Reader
}

func newWalletDKVSEncoder(magic string) *walletDKVSEncoder {
	encoder := &walletDKVSEncoder{}
	_, _ = encoder.buf.WriteString(magic)
	_ = encoder.buf.WriteByte(dkvsStorageCodecVersion)
	return encoder
}

func (e *walletDKVSEncoder) u8(value uint8) {
	_ = e.buf.WriteByte(value)
}

func (e *walletDKVSEncoder) uvarint(value uint64) {
	var scratch [binary.MaxVarintLen64]byte
	size := binary.PutUvarint(scratch[:], value)
	_, _ = e.buf.Write(scratch[:size])
}

func (e *walletDKVSEncoder) raw(value []byte) {
	_, _ = e.buf.Write(value)
}

func (e *walletDKVSEncoder) bytes(value []byte, maximum uint64) error {
	if uint64(len(value)) > maximum {
		return dkvsindexer.ErrInvalidRecord
	}
	e.uvarint(uint64(len(value)))
	e.raw(value)
	return nil
}

func (e *walletDKVSEncoder) string(value string, maximum uint64) error {
	return e.bytes([]byte(value), maximum)
}

func (e *walletDKVSEncoder) encoded() []byte {
	return e.buf.Bytes()
}

func newWalletDKVSDecoder(encoded []byte, magic string) (*walletDKVSDecoder, error) {
	decoder := &walletDKVSDecoder{reader: bytes.NewReader(encoded)}
	header, err := decoder.raw(uint64(len(magic)))
	if err != nil || string(header) != magic {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	version, err := decoder.u8()
	if err != nil || version != dkvsStorageCodecVersion {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	return decoder, nil
}

func (d *walletDKVSDecoder) raw(size uint64) ([]byte, error) {
	if d == nil || d.reader == nil || size > uint64(d.reader.Len()) {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	if size == 0 {
		return nil, nil
	}
	result := make([]byte, int(size))
	if _, err := io.ReadFull(d.reader, result); err != nil {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	return result, nil
}

func (d *walletDKVSDecoder) u8() (uint8, error) {
	if d == nil || d.reader == nil {
		return 0, dkvsindexer.ErrInvalidRecord
	}
	value, err := d.reader.ReadByte()
	if err != nil {
		return 0, dkvsindexer.ErrInvalidRecord
	}
	return value, nil
}

func (d *walletDKVSDecoder) uvarint() (uint64, error) {
	if d == nil || d.reader == nil {
		return 0, dkvsindexer.ErrInvalidRecord
	}
	value, err := binary.ReadUvarint(d.reader)
	if err != nil {
		return 0, dkvsindexer.ErrInvalidRecord
	}
	return value, nil
}

func (d *walletDKVSDecoder) bytes(maximum uint64) ([]byte, error) {
	size, err := d.uvarint()
	if err != nil || size > maximum {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	return d.raw(size)
}

func (d *walletDKVSDecoder) string(maximum uint64) (string, error) {
	value, err := d.bytes(maximum)
	if err != nil {
		return "", err
	}
	return string(value), nil
}

func (d *walletDKVSDecoder) done() bool {
	return d != nil && d.reader != nil && d.reader.Len() == 0
}

func subscriptionStatusCode(status string) (uint8, error) {
	switch status {
	case DKVSSubscriptionSyncing:
		return 1, nil
	case DKVSSubscriptionReady:
		return 2, nil
	case DKVSSubscriptionOfflineReady:
		return 3, nil
	case DKVSSubscriptionResetRequired:
		return 4, nil
	case DKVSSubscriptionError:
		return 5, nil
	default:
		return 0, dkvsindexer.ErrInvalidRecord
	}
}

func subscriptionStatusFromCode(code uint8) (string, error) {
	switch code {
	case 1:
		return DKVSSubscriptionSyncing, nil
	case 2:
		return DKVSSubscriptionReady, nil
	case 3:
		return DKVSSubscriptionOfflineReady, nil
	case 4:
		return DKVSSubscriptionResetRequired, nil
	case 5:
		return DKVSSubscriptionError, nil
	default:
		return "", dkvsindexer.ErrInvalidRecord
	}
}

func encodeDKVSSubscriptionState(state *SubscriptionState) ([]byte, error) {
	if state == nil {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	prefixes, err := NormalizeSubscriptionPrefixes(state.Prefixes)
	if err != nil {
		return nil, err
	}
	status, err := subscriptionStatusCode(state.Status)
	if err != nil {
		return nil, err
	}
	encoder := newWalletDKVSEncoder(dkvsSubscriptionStateMagic)
	if err := encoder.string(strings.TrimSpace(state.EndpointID), maxDKVSEndpointIDSize); err != nil {
		return nil, err
	}
	encoder.uvarint(uint64(len(prefixes)))
	for _, prefix := range prefixes {
		if err := encoder.string(prefix, dkvsindexer.MaxPrefixLength); err != nil {
			return nil, err
		}
	}
	for _, prefix := range prefixes {
		generation, ok := state.Generations[prefix]
		if !ok {
			encoder.u8(0)
			continue
		}
		encoder.u8(1)
		encoder.uvarint(generation)
	}
	encoder.uvarint(state.ViewHeight)
	encoder.uvarint(state.LastSyncAtMS)
	encoder.u8(status)
	if err := encoder.string(state.LastErrorCode, maxDKVSErrorCodeSize); err != nil {
		return nil, err
	}
	return encoder.encoded(), nil
}

func decodeDKVSSubscriptionState(encoded []byte) (*SubscriptionState, error) {
	if len(encoded) == 0 || uint64(len(encoded)) > maxDKVSStateEncoded {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	decoder, err := newWalletDKVSDecoder(encoded, dkvsSubscriptionStateMagic)
	if err != nil {
		return nil, err
	}
	endpointID, err := decoder.string(maxDKVSEndpointIDSize)
	if err != nil {
		return nil, err
	}
	count, err := decoder.uvarint()
	if err != nil || count > dkvsindexer.MaxPrefixesPerTerminal {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	prefixes := make([]string, 0, count)
	for index := uint64(0); index < count; index++ {
		prefix, err := decoder.string(dkvsindexer.MaxPrefixLength)
		if err != nil {
			return nil, err
		}
		prefixes = append(prefixes, prefix)
	}
	prefixes, err = NormalizeSubscriptionPrefixes(prefixes)
	if err != nil || uint64(len(prefixes)) != count {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	generations := make(map[string]uint64, len(prefixes))
	for _, prefix := range prefixes {
		present, presentErr := decoder.u8()
		if presentErr != nil || present > 1 {
			return nil, dkvsindexer.ErrInvalidRecord
		}
		if present == 0 {
			continue
		}
		generation, generationErr := decoder.uvarint()
		if generationErr != nil {
			return nil, generationErr
		}
		generations[prefix] = generation
	}
	viewHeight, err := decoder.uvarint()
	if err != nil {
		return nil, err
	}
	lastSyncAtMS, err := decoder.uvarint()
	if err != nil {
		return nil, err
	}
	statusCode, err := decoder.u8()
	if err != nil {
		return nil, err
	}
	status, err := subscriptionStatusFromCode(statusCode)
	if err != nil {
		return nil, err
	}
	lastErrorCode, err := decoder.string(maxDKVSErrorCodeSize)
	if err != nil || !decoder.done() {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	return &SubscriptionState{
		EndpointID: endpointID, Prefixes: prefixes, Generations: generations, ViewHeight: viewHeight,
		LastSyncAtMS: lastSyncAtMS, Status: status, LastErrorCode: lastErrorCode,
	}, nil
}

func storageModeCode(mode dkvsindexer.StorageMode) (uint8, error) {
	switch mode {
	case "":
		return 0, nil
	case dkvsindexer.StorageModeFreeLocal:
		return 1, nil
	case dkvsindexer.StorageModeAutopay:
		return 2, nil
	case dkvsindexer.StorageModePaid:
		return 3, nil
	default:
		return 0, dkvsindexer.ErrInvalidRecord
	}
}

func storageModeFromCode(code uint8) (dkvsindexer.StorageMode, error) {
	switch code {
	case 0:
		return "", nil
	case 1:
		return dkvsindexer.StorageModeFreeLocal, nil
	case 2:
		return dkvsindexer.StorageModeAutopay, nil
	case 3:
		return dkvsindexer.StorageModePaid, nil
	default:
		return "", dkvsindexer.ErrInvalidRecord
	}
}

func encodeDKVSLocalKeyState(state LocalKeyState) ([]byte, error) {
	if state.Key == "" || state.Seq == 0 {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	hash, err := chainhash.NewHashFromStr(state.ETag)
	if err != nil {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	mode, err := storageModeCode(state.StorageMode)
	if err != nil {
		return nil, err
	}
	encoder := newWalletDKVSEncoder(dkvsLocalKeyStateMagic)
	encoder.uvarint(state.Seq)
	encoder.raw(hash[:])
	if state.Deleted {
		encoder.u8(1)
	} else {
		encoder.u8(0)
	}
	encoder.uvarint(state.ExpiryHeight)
	encoder.u8(mode)
	return encoder.encoded(), nil
}

func decodeDKVSLocalKeyState(encoded []byte, key string) (*LocalKeyState, error) {
	if strings.TrimSpace(key) == "" || uint64(len(encoded)) > 128 {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	decoder, err := newWalletDKVSDecoder(encoded, dkvsLocalKeyStateMagic)
	if err != nil {
		return nil, err
	}
	seq, err := decoder.uvarint()
	if err != nil || seq == 0 {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	hashBytes, err := decoder.raw(chainhash.HashSize)
	if err != nil {
		return nil, err
	}
	var hash chainhash.Hash
	copy(hash[:], hashBytes)
	deleted, err := decoder.u8()
	if err != nil || deleted > 1 {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	expiryHeight, err := decoder.uvarint()
	if err != nil {
		return nil, err
	}
	modeCode, err := decoder.u8()
	if err != nil {
		return nil, err
	}
	mode, err := storageModeFromCode(modeCode)
	if err != nil || !decoder.done() {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	return &LocalKeyState{
		Key: key, Seq: seq, ETag: hash.String(), Deleted: deleted == 1,
		ExpiryHeight: expiryHeight, StorageMode: mode,
	}, nil
}

func outboxStateCode(state string) (uint8, error) {
	switch state {
	case DKVSOutboxPending:
		return 1, nil
	case DKVSOutboxInflight:
		return 2, nil
	case DKVSOutboxConflict:
		return 3, nil
	case DKVSOutboxTerminal:
		return 4, nil
	default:
		return 0, dkvsindexer.ErrInvalidRecord
	}
}

func outboxStateFromCode(code uint8) (string, error) {
	switch code {
	case 1:
		return DKVSOutboxPending, nil
	case 2:
		return DKVSOutboxInflight, nil
	case 3:
		return DKVSOutboxConflict, nil
	case 4:
		return DKVSOutboxTerminal, nil
	default:
		return "", dkvsindexer.ErrInvalidRecord
	}
}

func encodeDKVSOutboxEntry(entry *BatchOutboxEntry) ([]byte, error) {
	if entry == nil || strings.TrimSpace(entry.Namespace) == "" || strings.TrimSpace(entry.RequestID) == "" ||
		uint64(len(strings.TrimSpace(entry.Namespace))) > maxDKVSNamespaceSize ||
		uint64(len(strings.TrimSpace(entry.RequestID))) > maxDKVSRequestIDSize ||
		len(entry.Mutations) == 0 || len(entry.Mutations) > dkvsindexer.MaxBatchCASMutations {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	state, err := outboxStateCode(entry.State)
	if err != nil {
		return nil, err
	}
	encoder := newWalletDKVSEncoder(dkvsOutboxEntryMagic)
	if err := encoder.string(strings.TrimSpace(entry.EndpointID), maxDKVSEndpointIDSize); err != nil {
		return nil, err
	}
	encoder.uvarint(uint64(len(entry.Mutations)))
	var totalRecordBytes uint64
	for _, mutation := range entry.Mutations {
		if uint64(len(mutation.Record)) > ^uint64(0)-totalRecordBytes {
			return nil, dkvsindexer.ErrInvalidRecord
		}
		totalRecordBytes += uint64(len(mutation.Record))
		if totalRecordBytes > dkvsindexer.MaxBatchCASTotalSize {
			return nil, dkvsindexer.ErrBatchTooLarge
		}
		if err := encoder.bytes(mutation.Record, dkvsindexer.MaxBatchCASTotalSize); err != nil {
			return nil, err
		}
		switch {
		case mutation.ExpectAbsent && mutation.ExpectedETag == "":
			encoder.u8(1)
		case !mutation.ExpectAbsent && mutation.ExpectedETag != "":
			encoder.u8(2)
			hash, err := chainhash.NewHashFromStr(mutation.ExpectedETag)
			if err != nil {
				return nil, dkvsindexer.ErrInvalidRecord
			}
			encoder.raw(hash[:])
		default:
			return nil, dkvsindexer.ErrInvalidRecord
		}
	}
	encoder.u8(state)
	encoder.uvarint(uint64(entry.Attempts))
	if err := encoder.string(entry.LastErrorCode, maxDKVSErrorCodeSize); err != nil {
		return nil, err
	}
	if err := encoder.string(entry.LastError, maxDKVSErrorSize); err != nil {
		return nil, err
	}
	encoder.uvarint(entry.CreatedAtMS)
	encoder.uvarint(entry.UpdatedAtMS)
	if err := encoder.string(strings.TrimSpace(entry.OriginKey), dkvsindexer.MaxKeySize); err != nil {
		return nil, err
	}
	if err := encoder.string(strings.TrimSpace(entry.OriginDomain), maxDKVSOriginDomain); err != nil {
		return nil, err
	}
	encoder.uvarint(entry.OriginGeneration)
	// Keep the v3 encoding byte-for-byte compatible for the common false case.
	// Older v3 entries end at OriginGeneration; conflict-rebased entries append
	// one explicit flag byte.
	if entry.PreservePrefixGenerations {
		encoder.u8(1)
	}
	encoded := encoder.encoded()
	if uint64(len(encoded)) > maxDKVSOutboxEncoded {
		return nil, dkvsindexer.ErrBatchTooLarge
	}
	return encoded, nil
}

func decodeDKVSOutboxEntry(encoded []byte, namespace, requestID string) (*BatchOutboxEntry, error) {
	namespace, requestID = strings.TrimSpace(namespace), strings.TrimSpace(requestID)
	if namespace == "" || requestID == "" || uint64(len(namespace)) > maxDKVSNamespaceSize ||
		uint64(len(requestID)) > maxDKVSRequestIDSize || len(encoded) == 0 ||
		uint64(len(encoded)) > maxDKVSOutboxEncoded {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	decoder, err := newWalletDKVSDecoder(encoded, dkvsOutboxEntryMagic)
	if err != nil {
		return nil, err
	}
	endpointID, err := decoder.string(maxDKVSEndpointIDSize)
	if err != nil {
		return nil, err
	}
	count, err := decoder.uvarint()
	if err != nil || count == 0 || count > dkvsindexer.MaxBatchCASMutations {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	mutations := make([]PersistedMutation, 0, count)
	var totalRecordBytes uint64
	for index := uint64(0); index < count; index++ {
		record, err := decoder.bytes(dkvsindexer.MaxBatchCASTotalSize)
		if err != nil {
			return nil, err
		}
		if uint64(len(record)) > ^uint64(0)-totalRecordBytes {
			return nil, dkvsindexer.ErrInvalidRecord
		}
		totalRecordBytes += uint64(len(record))
		if totalRecordBytes > dkvsindexer.MaxBatchCASTotalSize {
			return nil, dkvsindexer.ErrBatchTooLarge
		}
		precondition, err := decoder.u8()
		if err != nil {
			return nil, err
		}
		mutation := PersistedMutation{Record: record}
		switch precondition {
		case 1:
			mutation.ExpectAbsent = true
		case 2:
			hashBytes, err := decoder.raw(chainhash.HashSize)
			if err != nil {
				return nil, err
			}
			var hash chainhash.Hash
			copy(hash[:], hashBytes)
			mutation.ExpectedETag = hash.String()
		default:
			return nil, dkvsindexer.ErrInvalidRecord
		}
		mutations = append(mutations, mutation)
	}
	stateCode, err := decoder.u8()
	if err != nil {
		return nil, err
	}
	state, err := outboxStateFromCode(stateCode)
	if err != nil {
		return nil, err
	}
	attempts, err := decoder.uvarint()
	if err != nil || attempts > uint64(^uint32(0)) {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	lastErrorCode, err := decoder.string(maxDKVSErrorCodeSize)
	if err != nil {
		return nil, err
	}
	lastError, err := decoder.string(maxDKVSErrorSize)
	if err != nil {
		return nil, err
	}
	createdAtMS, err := decoder.uvarint()
	if err != nil {
		return nil, err
	}
	updatedAtMS, err := decoder.uvarint()
	if err != nil {
		return nil, err
	}
	originKey, err := decoder.string(dkvsindexer.MaxKeySize)
	if err != nil {
		return nil, err
	}
	originDomain, err := decoder.string(maxDKVSOriginDomain)
	if err != nil {
		return nil, err
	}
	originGeneration, err := decoder.uvarint()
	if err != nil {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	preservePrefixGenerations := false
	if !decoder.done() {
		flag, flagErr := decoder.u8()
		if flagErr != nil || flag != 1 || !decoder.done() {
			return nil, dkvsindexer.ErrInvalidRecord
		}
		preservePrefixGenerations = true
	}
	return &BatchOutboxEntry{
		RequestID: requestID, Namespace: namespace, EndpointID: endpointID, Mutations: mutations,
		State: state, Attempts: uint32(attempts), LastErrorCode: lastErrorCode, LastError: lastError,
		CreatedAtMS: createdAtMS, UpdatedAtMS: updatedAtMS, OriginKey: originKey,
		OriginDomain: originDomain, OriginGeneration: originGeneration,
		PreservePrefixGenerations: preservePrefixGenerations,
	}, nil
}
