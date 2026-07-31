package wallet

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/sat20-labs/satoshinet/chaincfg/chainhash"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

type DKVSCASMutationRequest struct {
	Record       *swire.DKVSRecord `json:"record"`
	ExpectedHash string            `json:"expected_hash,omitempty"`
	ExpectAbsent bool              `json:"expect_absent,omitempty"`
}

type DKVSBatchCASRequest struct {
	Mutations         []DKVSCASMutationRequest      `json:"mutations"`
	PathPreconditions []DKVSPathPreconditionRequest `json:"path_preconditions,omitempty"`
	EndpointID        string                        `json:"endpoint_id,omitempty"`
}

type DKVSPathPreconditionRequest struct {
	Path               string `json:"path"`
	ExpectedRoot       string `json:"expected_root"`
	ExpectedGeneration uint64 `json:"expected_generation"`
}

type DKVSBatchCASResult struct {
	Applied int                 `json:"applied"`
	Records []*swire.DKVSRecord `json:"records"`
	Hashes  []string            `json:"hashes"`
}

type dkvsBatchCASResp struct {
	dkvsBaseResp
	Data *DKVSBatchCASResult `json:"data,omitempty"`
}

type DKVSDirectorySyncRequest struct {
	Prefix string `json:"prefix"`
	Cursor []byte `json:"cursor,omitempty"`
	Limit  uint32 `json:"limit,omitempty"`
}

type DKVSDirectoryWatchRequest struct {
	Prefix         string `json:"prefix"`
	Root           string `json:"root"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
}

func normalizeDKVSDirectoryPrefix(prefix string) (string, error) {
	prefix = strings.TrimSuffix(strings.TrimSpace(prefix), "/")
	if prefix == "" {
		return "", dkvsindexer.ErrInvalidKey
	}
	if _, err := dkvsindexer.ParsePrefix(prefix); err != nil {
		return "", err
	}
	return prefix, nil
}

func (p *SatsNetDKVSClient) dkvsWritePrecondition(key string) (dkvsindexer.WritePrecondition, *swire.DKVSRecord, error) {
	record, err := p.GetRecord(key)
	if err != nil {
		if errors.Is(err, ErrDKVSRecordNotFound) {
			return dkvsindexer.WritePrecondition{ExpectAbsent: true}, nil, nil
		}
		return dkvsindexer.WritePrecondition{}, nil, err
	}
	if record == nil {
		return dkvsindexer.WritePrecondition{}, nil, dkvsindexer.ErrInvalidRecord
	}
	hash := dkvsindexer.RecordHash(record)
	return dkvsindexer.WritePrecondition{ExpectedHash: &hash}, record, nil
}

func casMutationRequest(mutation dkvsindexer.CASMutation) (DKVSCASMutationRequest, error) {
	if mutation.Record == nil || !mutation.Precondition.Valid() {
		return DKVSCASMutationRequest{}, dkvsindexer.ErrInvalidRecord
	}
	req := DKVSCASMutationRequest{Record: mutation.Record, ExpectAbsent: mutation.Precondition.ExpectAbsent}
	if mutation.Precondition.ExpectedHash != nil {
		req.ExpectedHash = mutation.Precondition.ExpectedHash.String()
	}
	return req, nil
}

func (p *SatsNetDKVSClient) PutRecordCAS(record *swire.DKVSRecord,
	precondition dkvsindexer.WritePrecondition) (*swire.DKVSRecord, error) {

	if p.manager != nil && record != nil && p.manager.managesKey(record.Key) {
		result, err := p.manager.putBatchCAS(p, []dkvsindexer.CASMutation{{
			Record: record, Precondition: precondition,
		}})
		if err != nil {
			return nil, err
		}
		return result.Records[0], nil
	}
	req, err := casMutationRequest(dkvsindexer.CASMutation{Record: record, Precondition: precondition})
	if err != nil {
		return nil, err
	}
	var resp dkvsRecordResp
	if err := p.postJSON("/v3/dkvs/records/cas", req, &resp); err != nil {
		return nil, err
	}
	return verifyDKVSWriteEcho(record, resp.Data, resp.Hash)
}

func (p *SatsNetDKVSClient) PutRecordBatchCAS(mutations []dkvsindexer.CASMutation) (*DKVSBatchCASResult, error) {
	if p.manager != nil && p.manager.managesMutations(mutations) {
		return p.manager.putBatchCAS(p, mutations)
	}
	return p.PutRecordBatchCASWithPaths(mutations, nil)
}

func (p *SatsNetDKVSClient) PutRecordBatchCASWithPaths(mutations []dkvsindexer.CASMutation,
	pathConditions []dkvsindexer.PathWritePrecondition) (*DKVSBatchCASResult, error) {

	result, err := p.putRecordBatchCASV1Raw(mutations, pathConditions, "")
	if err != nil {
		return nil, err
	}
	return &DKVSBatchCASResult{
		Applied: result.Applied,
		Records: result.Records,
		Hashes:  result.Hashes,
	}, nil
}

func (p *SatsNetDKVSClient) SyncDirectory(req DKVSDirectorySyncRequest) (*DKVSSyncPage, error) {
	prefix, err := normalizeDKVSDirectoryPrefix(req.Prefix)
	if err != nil {
		return nil, err
	}
	req.Prefix = prefix
	var resp dkvsSyncResp
	if err := p.postJSON("/v3/dkvs/sync/directory", req, &resp); err != nil {
		return nil, err
	}
	if resp.Data == nil || strings.TrimSpace(resp.Data.Root) == "" {
		return nil, dkvsindexer.ErrInvalidCheckpoint
	}
	return resp.Data, nil
}

func (p *SatsNetDKVSClient) WatchDirectory(req DKVSDirectoryWatchRequest) (*DKVSWatchResult, error) {
	prefix, err := normalizeDKVSDirectoryPrefix(req.Prefix)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Root) == "" {
		return nil, dkvsindexer.ErrInvalidCheckpoint
	}
	req.Prefix = prefix
	var resp dkvsWatchResp
	if err := p.postJSON("/v3/dkvs/watch/directory", req, &resp); err != nil {
		return nil, err
	}
	if resp.Data == nil || strings.TrimSpace(resp.Data.Root) == "" {
		return nil, dkvsindexer.ErrInvalidCheckpoint
	}
	return resp.Data, nil
}

func (p *SatsNetDKVSClient) SyncDirectoryAll(prefix string,
	opts dkvsindexer.RecordVerificationOptions) ([]*swire.DKVSRecord, string, error) {

	prefix, err := normalizeDKVSDirectoryPrefix(prefix)
	if err != nil {
		return nil, "", err
	}
	if opts.Now == 0 {
		opts.Now = uint64(time.Now().UnixMilli())
	}
	var cursor []byte
	rootText := ""
	byKey := make(map[string]*swire.DKVSRecord)
	for pageNumber := 0; pageNumber < 10000; pageNumber++ {
		page, err := p.SyncDirectory(DKVSDirectorySyncRequest{
			Prefix: prefix, Cursor: cursor, Limit: swire.MaxDKVSRecordsPerMsg,
		})
		if err != nil {
			return nil, "", err
		}
		if rootText == "" {
			rootText = page.Root
		} else if rootText != page.Root {
			return nil, "", fmt.Errorf("DKVS directory root changed during pagination")
		}
		for _, record := range page.Records {
			if record == nil || (record.Key != prefix && !strings.HasPrefix(record.Key, prefix+"/")) {
				return nil, "", dkvsindexer.ErrInvalidKey
			}
			if err := dkvsindexer.VerifyRecordForClient(record, opts); err != nil {
				return nil, "", err
			}
			if previous := byKey[record.Key]; previous != nil &&
				dkvsindexer.RecordHash(previous) != dkvsindexer.RecordHash(record) {
				return nil, "", fmt.Errorf("conflicting DKVS directory record %s", record.Key)
			}
			byKey[record.Key] = record
		}
		if page.Done {
			keys := make([]string, 0, len(byKey))
			for key := range byKey {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			records := make([]*swire.DKVSRecord, 0, len(keys))
			for _, key := range keys {
				records = append(records, byKey[key])
			}
			want, err := chainhash.NewHashFromStr(rootText)
			if err != nil {
				return nil, "", dkvsindexer.ErrInvalidCheckpoint
			}
			computed, err := dkvsindexer.DirectoryRootFromRecords(records, opts.Height)
			if err != nil || computed != *want {
				return nil, "", dkvsindexer.ErrInvalidCheckpoint
			}
			return records, rootText, nil
		}
		if len(page.NextCursor) == 0 || bytes.Equal(page.NextCursor, cursor) {
			return nil, "", fmt.Errorf("invalid DKVS directory cursor")
		}
		cursor = append(cursor[:0], page.NextCursor...)
	}
	return nil, "", fmt.Errorf("DKVS directory sync exceeded page limit")
}
