package wallet

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	indexer "github.com/sat20-labs/indexer/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

const dkvsLocalOverlayPageSize = 256

// loadEndpointLocalOverlay reads only FREE_LOCAL records visible from the
// connected endpoint. They are deliberately excluded from the network
// PathSnapshot and are therefore maintained as an endpoint-scoped overlay.
func (p *SatsNetDKVSClient) loadEndpointLocalOverlay(path string,
	viewHeight uint64) ([]*swire.DKVSRecord, string, error) {

	if p == nil {
		return nil, "", ErrDKVSPathNotSynced
	}
	path = strings.TrimSuffix(strings.TrimSpace(path), "/")
	if _, err := dkvsindexer.ParsePrefix(path); err != nil {
		return nil, "", err
	}
	config, err := p.GetClientConfigV1()
	if err != nil {
		return nil, "", err
	}
	endpointID := strings.TrimSpace(config.EndpointID)
	if endpointID == "" {
		return nil, "", dkvsindexer.ErrStaleEndpoint
	}

	local := make([]*swire.DKVSRecord, 0)
	start := 0
	for page := 0; page < 10000; page++ {
		records, total, err := p.ListRecords(path, start, dkvsLocalOverlayPageSize)
		if err != nil {
			return nil, "", err
		}
		for _, record := range records {
			if record == nil || dkvsindexer.RecordRequiresPathPrecondition(record) {
				continue
			}
			recordPath, err := dkvsindexer.CollectionPathForKey(record.Key)
			if err != nil || recordPath != path {
				return nil, "", dkvsindexer.ErrInvalidRecord
			}
			if dkvsindexer.IsTombstone(record.Flags) ||
				dkvsindexer.IsExpired(record, viewHeight) {
				continue
			}
			if err := dkvsindexer.VerifyRecordForClient(record,
				dkvsindexer.RecordVerificationOptions{
					ExpectedKey: record.Key, Height: viewHeight,
				}); err != nil {
				return nil, "", fmt.Errorf("verify endpoint-local DKVS record path=%s key=%s: %w", path, record.Key, err)
			}
			local = append(local, record)
		}
		start += len(records)
		if len(records) == 0 || start >= total {
			break
		}
	}
	sort.Slice(local, func(i, j int) bool { return local[i].Key < local[j].Key })
	return local, endpointID, nil
}

// applyEndpointLocalOverlay atomically replaces the FREE_LOCAL portion of one
// endpoint-scoped path while preserving its validated network replica and
// network PathMeta baseline.
func (s *dkvsReplicaStore) applyEndpointLocalOverlay(scope, path, endpointID string,
	records []*swire.DKVSRecord, serverTimeMS uint64) error {

	if s == nil || s.db == nil || scope == "" || path == "" || endpointID == "" {
		return dkvsindexer.ErrInvalidRecord
	}
	state, err := s.loadPathState(scope)
	if err != nil {
		if errors.Is(err, indexer.ErrKeyNotFound) {
			return ErrDKVSPathNotSynced
		}
		return err
	}
	if state.Path != path {
		return dkvsindexer.ErrInvalidRecord
	}
	if state.HasLocalOnly && state.EndpointID != "" && state.EndpointID != endpointID {
		return dkvsindexer.ErrLocalOnlyEndpointMismatch
	}

	byKey, err := s.loadConfirmedByKey(scope)
	if err != nil {
		return err
	}
	for key, record := range byKey {
		if record == nil {
			continue
		}
		recordPath, pathErr := dkvsindexer.CollectionPathForKey(record.Key)
		if pathErr == nil && recordPath == path && !dkvsindexer.RecordRequiresPathPrecondition(record) {
			delete(byKey, key)
		}
	}
	for _, record := range records {
		if record == nil || dkvsindexer.RecordRequiresPathPrecondition(record) {
			return dkvsindexer.ErrInvalidRecord
		}
		recordPath, pathErr := dkvsindexer.CollectionPathForKey(record.Key)
		if pathErr != nil || recordPath != path {
			return dkvsindexer.ErrInvalidRecord
		}
		byKey[record.Key] = record
	}

	merged := make([]*swire.DKVSRecord, 0, len(byKey))
	for _, record := range byKey {
		merged = append(merged, record)
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].Key < merged[j].Key })
	verify := dkvsindexer.RecordVerificationOptions{}
	if state.PathMeta != nil {
		verify.Height = state.PathMeta.ViewHeight
	}
	batch := s.db.NewWriteBatch()
	defer batch.Close()
	if err := s.stageConfirmedReplacement(batch, scope, path, merged, verify); err != nil {
		return err
	}
	state.ServerTimeMS = serverTimeMS
	state.EndpointID = endpointID
	state.HasLocalOnly = len(records) != 0
	state.SessionState = dkvsSessionIdle
	state.LastErrorCode = ""
	if err := putPathStateBatch(batch, scope, state); err != nil {
		return err
	}
	return batch.Flush()
}

func (p *Manager) syncDKVSEndpointLocalOverlay(client *SatsNetDKVSClient,
	store *dkvsReplicaStore, path, scope string, viewHeight, serverTimeMS uint64) error {

	records, endpointID, err := client.loadEndpointLocalOverlay(path, viewHeight)
	if err != nil {
		return err
	}
	return store.applyEndpointLocalOverlay(scope, path, endpointID, records, serverTimeMS)
}
