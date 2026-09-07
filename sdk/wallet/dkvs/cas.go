package dkvs

import (
	"errors"
	"fmt"
	"strings"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

type CASMutationRequest struct {
	Record       *swire.DKVSRecord `json:"record"`
	ExpectedETag string            `json:"expected_etag,omitempty"`
	ExpectAbsent bool              `json:"expect_absent,omitempty"`
}

type BatchCASRequest struct {
	RequestID  string               `json:"request_id,omitempty"`
	Mutations  []CASMutationRequest `json:"mutations"`
	EndpointID string               `json:"endpoint_id,omitempty"`
}

type BatchCASResult struct {
	Applied      int                            `json:"applied"`
	Records      []*swire.DKVSRecord            `json:"records"`
	Hashes       []string                       `json:"hashes"`
	PrefixStates []dkvsindexer.PrefixGeneration `json:"prefix_states,omitempty"`
	ViewHeight   uint64                         `json:"view_height,omitempty"`
	EndpointID   string                         `json:"endpoint_id,omitempty"`
	RequestID    string                         `json:"request_id,omitempty"`
}

func MutationRequest(mutation dkvsindexer.CASMutation) (CASMutationRequest, error) {
	if mutation.Record == nil || !mutation.Precondition.Valid() {
		return CASMutationRequest{}, dkvsindexer.ErrInvalidRecord
	}
	request := CASMutationRequest{
		Record: mutation.Record, ExpectAbsent: mutation.Precondition.ExpectAbsent,
	}
	if mutation.Precondition.ExpectedHash != nil {
		request.ExpectedETag = mutation.Precondition.ExpectedHash.String()
	}
	return request, nil
}

func BuildBatchCASRequest(mutations []dkvsindexer.CASMutation,
	endpointID, requestID string) (BatchCASRequest, error) {

	if len(mutations) == 0 || len(mutations) > dkvsindexer.MaxBatchCASMutations {
		return BatchCASRequest{}, dkvsindexer.ErrInvalidRecord
	}
	request := BatchCASRequest{
		RequestID: strings.TrimSpace(requestID), EndpointID: strings.TrimSpace(endpointID),
		Mutations: make([]CASMutationRequest, 0, len(mutations)),
	}
	for _, mutation := range mutations {
		item, err := MutationRequest(mutation)
		if err != nil {
			return BatchCASRequest{}, err
		}
		request.Mutations = append(request.Mutations, item)
	}
	return request, nil
}

func VerifyWriteEcho(request, echoed *swire.DKVSRecord, hashText string) (*swire.DKVSRecord, error) {
	if request == nil || echoed == nil {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	want := dkvsindexer.RecordHash(request)
	if dkvsindexer.RecordHash(echoed) != want {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	if strings.TrimSpace(hashText) != "" {
		got, err := chainhash.NewHashFromStr(strings.TrimSpace(hashText))
		if err != nil || *got != want {
			return nil, dkvsindexer.ErrInvalidRecord
		}
	}
	return echoed, nil
}

func VerifyWriteResult(mutations []dkvsindexer.CASMutation, requestID string,
	result *dkvsindexer.WriteResult) error {

	if result == nil {
		return fmt.Errorf("DKVS batch response is nil: %w", dkvsindexer.ErrInvalidRecord)
	}
	if result.RequestID != "" && requestID != "" && result.RequestID != requestID {
		return dkvsindexer.ErrInvalidRecord
	}
	if result.Applied != 0 && result.Applied != len(mutations) {
		return fmt.Errorf("DKVS batch applied=%d mutations=%d: %w",
			result.Applied, len(mutations), dkvsindexer.ErrInvalidRecord)
	}
	if len(result.Records) != len(mutations) || len(result.Hashes) != len(mutations) {
		return fmt.Errorf("DKVS batch echo records=%d hashes=%d mutations=%d: %w",
			len(result.Records), len(result.Hashes), len(mutations), dkvsindexer.ErrInvalidRecord)
	}
	for index, mutation := range mutations {
		if _, err := VerifyWriteEcho(mutation.Record, result.Records[index], result.Hashes[index]); err != nil {
			return fmt.Errorf("verify DKVS batch echo index=%d key=%s: %w",
				index, mutation.Record.Key, err)
		}
	}
	return nil
}

func BatchResult(result *dkvsindexer.WriteResult) *BatchCASResult {
	if result == nil {
		return nil
	}
	return &BatchCASResult{
		Applied: result.Applied, Records: result.Records, Hashes: result.Hashes,
		PrefixStates: result.PrefixStates,
		ViewHeight:   result.ViewHeight, EndpointID: result.EndpointID,
		RequestID: result.RequestID,
	}
}

func IsErrorCode(err error, code dkvsindexer.ErrorCode) bool {
	var remote *DKVSError
	return errors.As(err, &remote) && remote.Code == code
}
