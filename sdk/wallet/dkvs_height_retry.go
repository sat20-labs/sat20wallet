package wallet

import (
	"fmt"

	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

// An unknown delivery always sends the captured request first. Only the node's
// explicit height refusal permits changing its signed bytes. The original CAS
// and prefix generations remain authoritative throughout all attempts.
func (p *SatsNetDKVSClient) submitDKVSBatch(mutations []dkvs.CASMutation, endpointID, requestID string,
	authorization *dkvs.WalletWriteAuthorization, entry *DKVSBatchOutboxEntry) (*dkvs.WriteResult, error) {
	for attempt := 0; ; attempt++ {
		result, err := p.sendDKVSBatch(mutations, endpointID, requestID, authorization)
		if !IsDKVSErrorCode(err, dkvs.ErrorCodeHeightTooLow) {
			return result, err
		}
		if attempt == 2 {
			return nil, &DKVSRequestPendingError{RequestID: requestID, Cause: err}
		}
		next, nextID, proof, err := p.refreshBatchHeight(mutations, endpointID, authorization)
		if err != nil {
			return nil, &DKVSRequestPendingError{RequestID: requestID, Cause: fmt.Errorf("refresh DKVS height: %w", err)}
		}
		if entry != nil {
			store := newDKVSReplicaStore(p.manager.owner.db)
			if err := store.ReplaceOutbox(entry, next, nextID, proof); err != nil {
				return nil, &DKVSRequestPendingError{RequestID: requestID, Cause: fmt.Errorf("persist DKVS height refresh: %w", err)}
			}
		}
		mutations, requestID, authorization = next, nextID, proof
	}
}

func (p *SatsNetDKVSClient) refreshBatchHeight(mutations []dkvs.CASMutation, endpointID string,
	authorization *dkvs.WalletWriteAuthorization) ([]dkvs.CASMutation, string, *dkvs.WalletWriteAuthorization, error) {
	height, err := p.GetBestHeight()
	if err != nil {
		return nil, "", nil, err
	}
	signer, err := p.batchWriteSigner(mutations)
	if err != nil {
		return nil, "", nil, err
	}
	next := make([]dkvs.CASMutation, len(mutations))
	for i, mutation := range mutations {
		record := *mutation.Record
		record.IssueHeight = height
		if err := SignDKVSRecord(signer, &record); err != nil {
			return nil, "", nil, err
		}
		next[i] = dkvs.CASMutation{Record: &record, Precondition: mutation.Precondition}
	}
	requestID, err := newDKVSRequestID()
	if err != nil {
		return nil, "", nil, err
	}
	proof := dkvs.CloneWalletWriteAuthorization(authorization)
	if proof != nil {
		proof, err = signWriteAuthorization(signer, next, endpointID, requestID, proof.Context)
		if err != nil {
			return nil, "", nil, err
		}
	}
	return next, requestID, proof, nil
}
