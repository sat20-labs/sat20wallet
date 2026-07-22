package wallet

import (
	"bytes"
	"encoding/hex"
	"errors"
	"sort"

	"github.com/sat20-labs/sat20wallet/sdk/common"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

const (
	rgb11WalletSnapshotMetadataPrefix = "SAT20-RGB11-WALLET-SNAPSHOT-V1\x00"
	rgb11SnapshotGCPageSize           = 1000
)

func rgb11WalletSnapshotMetadata(walletID string) []byte {
	return []byte(rgb11WalletSnapshotMetadataPrefix + walletID)
}

// pruneRGB11WalletSnapshots removes superseded immutable snapshot blobs after
// the new wallet head has become active. Tombstones are fee-free and signed by
// the same wallet. A failed cleanup does not invalidate the already-published
// head; the next successful backup retries cleanup across all marked snapshots.
func (p *SatsNetDKVSClient) pruneRGB11WalletSnapshots(wallet common.Wallet, walletID string,
	keepOperationID [32]byte) error {

	if p == nil || wallet == nil || walletID == "" {
		return dkvsindexer.ErrInvalidRecord
	}
	pubKey, err := dkvsWalletPubKey(wallet)
	if err != nil {
		return err
	}
	accountID := dkvsindexer.AccountID(pubKey)
	if accountID == "" {
		return dkvsindexer.ErrInvalidSignature
	}
	prefix := "/blob/" + accountID
	records := make([]*swire.DKVSRecord, 0)
	for start := 0; ; {
		page, total, err := p.ListRecords(prefix, start, rgb11SnapshotGCPageSize)
		if err != nil {
			return err
		}
		records = append(records, page...)
		start += len(page)
		if len(page) == 0 || start >= total {
			break
		}
	}

	marker := rgb11WalletSnapshotMetadata(walletID)
	keepObjectID := hex.EncodeToString(keepOperationID[:])
	removeObjects := make(map[string]struct{})
	for _, record := range records {
		if record == nil {
			continue
		}
		parsed, err := dkvsindexer.ParseKey(record.Key)
		if err != nil || parsed.Namespace != "blob" || len(parsed.Segments) != 3 ||
			parsed.Segments[0] != accountID || parsed.Segments[2] != "manifest" {
			continue
		}
		manifest, err := dkvsindexer.ParseBlobManifestValue(record.Value, dkvsindexer.DefaultBlobPolicy())
		if err != nil || !bytes.Equal(manifest.Metadata, marker) || parsed.Segments[1] == keepObjectID {
			continue
		}
		removeObjects[parsed.Segments[1]] = struct{}{}
	}
	if len(removeObjects) == 0 {
		return nil
	}

	remove := make([]*swire.DKVSRecord, 0)
	for _, record := range records {
		if record == nil {
			continue
		}
		parsed, err := dkvsindexer.ParseKey(record.Key)
		if err != nil || parsed.Namespace != "blob" || len(parsed.Segments) < 3 || parsed.Segments[0] != accountID {
			continue
		}
		if _, ok := removeObjects[parsed.Segments[1]]; ok {
			remove = append(remove, record)
		}
	}
	// Delete chunks first and manifests last. This avoids leaving a live
	// manifest that advertises already-deleted chunks during partial cleanup.
	sort.Slice(remove, func(a, b int) bool {
		parsedA, _ := dkvsindexer.ParseKey(remove[a].Key)
		parsedB, _ := dkvsindexer.ParseKey(remove[b].Key)
		manifestA := len(parsedA.Segments) == 3 && parsedA.Segments[2] == "manifest"
		manifestB := len(parsedB.Segments) == 3 && parsedB.Segments[2] == "manifest"
		if manifestA != manifestB {
			return !manifestA
		}
		return remove[a].Key < remove[b].Key
	})
	for _, record := range remove {
		_, err := p.TombstoneSigned(wallet, record.Key, dkvsindexer.RecordOptions{Seq: record.Seq + 1})
		if err != nil && !errors.Is(err, ErrDKVSRecordNotFound) {
			return err
		}
	}
	return nil
}
