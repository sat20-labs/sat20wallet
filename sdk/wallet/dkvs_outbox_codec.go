package wallet

import (
	"bytes"

	strict "github.com/sat20-labs/rgb11/strict_encoding"
	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

const (
	dkvsOutboxMagic             = "DKOB"
	dkvsOutboxMaxNamespace      = 4 * 1024
	dkvsOutboxMaxEndpointID     = 4 * 1024
	dkvsOutboxMaxOriginDomain   = 256
	dkvsOutboxMaxErrorCode      = 256
	dkvsOutboxMaxErrorMessage   = 64 * 1024
	dkvsOutboxMaxPathConditions = dkvsindexer.MaxBatchCASMutations
)

func dkvsOutboxStateCode(state string) (uint8, error) {
	switch state {
	case dkvsSessionPrepared:
		return 1, nil
	case dkvsSessionInflight:
		return 2, nil
	case dkvsSessionConflict:
		return 3, nil
	case dkvsSessionError:
		return 4, nil
	case dkvsSessionTerminal:
		return 5, nil
	case dkvsSessionConfirmed:
		return 6, nil
	default:
		return 0, dkvsindexer.ErrInvalidRecord
	}
}

func dkvsOutboxStateFromCode(code uint8) (string, error) {
	switch code {
	case 1:
		return dkvsSessionPrepared, nil
	case 2:
		return dkvsSessionInflight, nil
	case 3:
		return dkvsSessionConflict, nil
	case 4:
		return dkvsSessionError, nil
	case 5:
		return dkvsSessionTerminal, nil
	case 6:
		return dkvsSessionConfirmed, nil
	default:
		return "", dkvsindexer.ErrInvalidRecord
	}
}

func encodeDKVSBatchOutboxEntry(entry *dkvsBatchOutboxEntry) ([]byte, error) {
	if entry == nil || len(entry.Mutations) == 0 ||
		len(entry.Mutations) > dkvsindexer.MaxBatchCASMutations ||
		len(entry.PathPreconditions) > dkvsOutboxMaxPathConditions {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	mutations, _, err := entry.decode()
	if err != nil {
		return nil, err
	}
	if _, err := dkvsindexer.ParseKey(entry.Key); err != nil {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	if err := verifyOutboxMutationKeys(mutations); err != nil {
		return nil, err
	}
	stateCode, err := dkvsOutboxStateCode(entry.State)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	encoder := strict.NewEncoder(&buf)
	if err := encoder.Raw([]byte(dkvsOutboxMagic)); err != nil {
		return nil, err
	}
	for _, field := range []struct {
		value string
		min   uint64
		max   uint64
	}{
		{entry.Key, 1, dkvsindexer.MaxKeySize},
		{entry.Namespace, 1, dkvsOutboxMaxNamespace},
	} {
		if err := encoder.String(field.value, field.min, field.max); err != nil {
			return nil, dkvsindexer.ErrInvalidRecord
		}
	}
	if err := encoder.Length(uint64(len(entry.Mutations)), dkvsindexer.MaxBatchCASMutations); err != nil {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	totalRecordBytes := 0
	for _, mutation := range entry.Mutations {
		totalRecordBytes += len(mutation.Record)
		if totalRecordBytes > dkvsindexer.MaxBatchCASTotalSize ||
			(len(mutation.ExpectedHash) != 0 && len(mutation.ExpectedHash) != chainhash.HashSize) {
			return nil, dkvsindexer.ErrInvalidRecord
		}
		if err := encoder.Bytes(mutation.Record, 1, dkvsindexer.MaxBatchCASTotalSize); err != nil {
			return nil, dkvsindexer.ErrInvalidRecord
		}
		if err := encoder.Option(len(mutation.ExpectedHash) != 0, func(value *strict.Encoder) error {
			return value.Raw(mutation.ExpectedHash)
		}); err != nil {
			return nil, dkvsindexer.ErrInvalidRecord
		}
		if err := encoder.Bool(mutation.ExpectAbsent); err != nil {
			return nil, err
		}
	}
	if err := encoder.Length(uint64(len(entry.PathPreconditions)), dkvsOutboxMaxPathConditions); err != nil {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	for _, condition := range entry.PathPreconditions {
		if len(condition.ExpectedRoot) != chainhash.HashSize {
			return nil, dkvsindexer.ErrInvalidRecord
		}
		if err := encoder.String(condition.Path, 1, dkvsindexer.MaxKeySize); err != nil {
			return nil, dkvsindexer.ErrInvalidRecord
		}
		if err := encoder.Raw(condition.ExpectedRoot); err != nil {
			return nil, err
		}
		if err := encoder.U64(condition.ExpectedGeneration); err != nil {
			return nil, err
		}
	}
	if err := encoder.String(entry.EndpointID, 0, dkvsOutboxMaxEndpointID); err != nil {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	if err := encoder.U8(stateCode); err != nil {
		return nil, err
	}
	if err := encoder.U32(entry.Attempts); err != nil {
		return nil, err
	}
	if err := encoder.String(entry.LastErrorCode, 0, dkvsOutboxMaxErrorCode); err != nil {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	if err := encoder.String(entry.LastError, 0, dkvsOutboxMaxErrorMessage); err != nil {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	for _, value := range []uint64{entry.CreatedAtMS, entry.UpdatedAtMS} {
		if err := encoder.U64(value); err != nil {
			return nil, err
		}
	}
	if err := encoder.String(entry.OriginDomain, 0, dkvsOutboxMaxOriginDomain); err != nil {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	if err := encoder.U64(entry.OriginGeneration); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func decodeDKVSBatchOutboxEntry(data []byte) (*dkvsBatchOutboxEntry, error) {
	reader := bytes.NewReader(data)
	decoder := strict.NewDecoder(reader)
	magic, err := decoder.Raw(uint64(len(dkvsOutboxMagic)))
	if err != nil || string(magic) != dkvsOutboxMagic {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	entry := &dkvsBatchOutboxEntry{}
	if entry.Key, err = decoder.String(1, dkvsindexer.MaxKeySize); err != nil {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	if entry.Namespace, err = decoder.String(1, dkvsOutboxMaxNamespace); err != nil {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	mutationCount, err := decoder.Length(dkvsindexer.MaxBatchCASMutations)
	if err != nil || mutationCount == 0 {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	entry.Mutations = make([]dkvsPersistedMutation, 0, mutationCount)
	totalRecordBytes := 0
	for index := uint64(0); index < mutationCount; index++ {
		mutation := dkvsPersistedMutation{}
		if mutation.Record, err = decoder.Bytes(1, dkvsindexer.MaxBatchCASTotalSize); err != nil {
			return nil, dkvsindexer.ErrInvalidRecord
		}
		totalRecordBytes += len(mutation.Record)
		if totalRecordBytes > dkvsindexer.MaxBatchCASTotalSize {
			return nil, dkvsindexer.ErrInvalidRecord
		}
		if _, err = decoder.Option(func(value *strict.Decoder) error {
			mutation.ExpectedHash, err = value.Raw(chainhash.HashSize)
			return err
		}); err != nil {
			return nil, dkvsindexer.ErrInvalidRecord
		}
		if mutation.ExpectAbsent, err = decoder.Bool(); err != nil {
			return nil, dkvsindexer.ErrInvalidRecord
		}
		entry.Mutations = append(entry.Mutations, mutation)
	}
	conditionCount, err := decoder.Length(dkvsOutboxMaxPathConditions)
	if err != nil {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	entry.PathPreconditions = make([]dkvsPersistedPathPrecondition, 0, conditionCount)
	for index := uint64(0); index < conditionCount; index++ {
		condition := dkvsPersistedPathPrecondition{}
		if condition.Path, err = decoder.String(1, dkvsindexer.MaxKeySize); err != nil {
			return nil, dkvsindexer.ErrInvalidRecord
		}
		if condition.ExpectedRoot, err = decoder.Raw(chainhash.HashSize); err != nil {
			return nil, dkvsindexer.ErrInvalidRecord
		}
		if condition.ExpectedGeneration, err = decoder.U64(); err != nil {
			return nil, dkvsindexer.ErrInvalidRecord
		}
		entry.PathPreconditions = append(entry.PathPreconditions, condition)
	}
	if entry.EndpointID, err = decoder.String(0, dkvsOutboxMaxEndpointID); err != nil {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	stateCode, err := decoder.U8()
	if err != nil {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	if entry.State, err = dkvsOutboxStateFromCode(stateCode); err != nil {
		return nil, err
	}
	if entry.Attempts, err = decoder.U32(); err != nil {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	if entry.LastErrorCode, err = decoder.String(0, dkvsOutboxMaxErrorCode); err != nil {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	if entry.LastError, err = decoder.String(0, dkvsOutboxMaxErrorMessage); err != nil {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	if entry.CreatedAtMS, err = decoder.U64(); err != nil {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	if entry.UpdatedAtMS, err = decoder.U64(); err != nil {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	if entry.OriginDomain, err = decoder.String(0, dkvsOutboxMaxOriginDomain); err != nil {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	if entry.OriginGeneration, err = decoder.U64(); err != nil || reader.Len() != 0 {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	mutations, _, err := entry.decode()
	if err != nil {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	if _, err := dkvsindexer.ParseKey(entry.Key); err != nil {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	if err := verifyOutboxMutationKeys(mutations); err != nil {
		return nil, err
	}
	return entry, nil
}
