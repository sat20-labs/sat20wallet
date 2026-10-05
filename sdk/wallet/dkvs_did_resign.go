package wallet

import (
	"errors"
	"fmt"
	"sort"

	"github.com/sat20-labs/sat20wallet/sdk/common"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
)

// ResignDIDRecords is the explicit receiving-wallet operation after a DID
// changes owner. It reads the complete current service collection (not a
// possibly incomplete local cache) and the optional name record, preserves
// their values/storage modes, and signs them with the receiving wallet.
//
// The one existing batch-CAS commit remains subject to current DID ownership,
// bound-CoreNode admission and payment verification. No write occurs before
// every record is built and the entire batch fits the published limits. Large
// collections are rejected rather than silently split into partial takeovers.
func (p *SatsNetDKVSClient) ResignDIDRecords(signer common.Wallet, did string) ([]*wire.DKVSRecord, error) {
	if p == nil || signer == nil {
		return nil, dkvs.ErrInvalidRecord
	}
	writer := p.WithWriteSigner(signer)
	signer = writer.writeSigner
	nameKey, err := dkvs.NameKey(did)
	if err != nil {
		return nil, err
	}
	prefix, err := serviceSubscriptionTarget(did)
	if err != nil {
		return nil, err
	}
	ctx := p.requestContext()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	records, err := p.readCurrentDirectory(ctx, prefix)
	if err != nil {
		return nil, err
	}
	name, err := p.GetRecordDirectContext(ctx, nameKey)
	if err == nil {
		records = append(records, name)
	} else if !errors.Is(err, dkvs.ErrRecordNotFound) {
		return nil, err
	}
	if len(records) == 0 {
		return []*wire.DKVSRecord{}, nil
	}
	config, err := p.GetDKVSClientConfig()
	if err != nil {
		return nil, err
	}
	if len(records) > dkvs.MaxBatchCASMutations || (config.MaxBatchMutations > 0 && len(records) > int(config.MaxBatchMutations)) {
		return nil, fmt.Errorf("%w: complete DID takeover must fit one atomic batch", dkvs.ErrBatchTooLarge)
	}
	height, err := p.GetBestHeight()
	if err != nil {
		return nil, err
	}
	sort.Slice(records, func(a, b int) bool { return records[a].Key < records[b].Key })
	mutations := make([]dkvs.CASMutation, 0, len(records))
	total := 0
	for _, old := range records {
		if old == nil || (old.Key != nameKey && !dkvs.ActiveScopeMatches(dkvs.ActiveScope{Prefix: prefix}, old.Key)) {
			return nil, dkvs.ErrInvalidSnapshot
		}
		if err := dkvs.VerifyRecordForClient(old, dkvs.RecordVerificationOptions{ExpectedKey: old.Key, Height: height}); err != nil {
			return nil, err
		}
		if old.Seq == ^uint64(0) {
			return nil, dkvs.ErrInvalidSequence
		}
		ttl := uint64(0)
		if old.TTL != 0 {
			expiry := dkvs.RecordExpiryHeight(old)
			if expiry == 0 || expiry <= height {
				return nil, dkvs.ErrExpiredRecord
			}
			ttl = expiry - height // taking ownership does not silently extend a lease
		}
		record, err := NewDKVSSignedRecord(signer, old.Key, old.Value, dkvs.RecordOptions{
			Seq: old.Seq + 1, IssueHeight: height, TTL: ttl,
			Flags: old.Flags, FeeProof: append([]byte(nil), old.FeeProof...),
		})
		if err != nil {
			return nil, err
		}
		size := dkvs.RecordSize(record)
		if size > dkvs.MaxBatchCASTotalSize-total || (config.MaxBatchBytes > 0 && size > int(config.MaxBatchBytes)-total) {
			return nil, fmt.Errorf("%w: complete DID takeover exceeds atomic batch bytes", dkvs.ErrBatchTooLarge)
		}
		total += size
		hash := dkvs.RecordHash(old)
		mutations = append(mutations, dkvs.CASMutation{Record: record, Precondition: dkvs.WritePrecondition{ExpectedHash: &hash}})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result, err := writer.PutRecordBatchCAS(mutations)
	if err != nil {
		return nil, err
	}
	return result.Records, nil
}
