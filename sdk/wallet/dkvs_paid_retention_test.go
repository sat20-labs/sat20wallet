package wallet

import (
	"strings"
	"testing"

	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestAttachAutopayFeeProofClearsRecordLease(t *testing.T) {
	record := &swire.DKVSRecord{Key: "/svc/test/value", TTL: 60_000, ExpiryHeight: 12345}
	proof := &dkvsindexer.FeeProof{Mode: dkvsindexer.FeeModeAutopay, PoolContract: "autopay"}
	require.NoError(t, AttachDKVSFeeProof(record, proof))
	require.Zero(t, record.TTL)
	require.Zero(t, record.ExpiryHeight)
	decoded, err := dkvsindexer.ParseFeeProof(record.FeeProof)
	require.NoError(t, err)
	require.Equal(t, dkvsindexer.FeeModeAutopay, decoded.Mode)
}

func TestAttachAutopayFeeProofRewritesBlobManifestLease(t *testing.T) {
	_, value, err := dkvsindexer.BuildBlobManifest([][]byte{[]byte("chunk")}, []byte("meta"), 60_000, 12345)
	require.NoError(t, err)
	key, err := dkvsindexer.BlobManifestKey(strings.Repeat("a", 64), "object")
	require.NoError(t, err)
	record := &swire.DKVSRecord{Key: key, Value: value, TTL: 60_000, ExpiryHeight: 12345}
	proof := &dkvsindexer.FeeProof{Mode: dkvsindexer.FeeModeAutopay, PoolContract: "autopay"}
	require.NoError(t, AttachDKVSFeeProof(record, proof))
	manifest, err := dkvsindexer.ParseBlobManifestValue(record.Value, dkvsindexer.BlobPolicy{})
	require.NoError(t, err)
	require.Zero(t, manifest.TTL)
	require.Zero(t, manifest.ExpiryHeight)
	require.Zero(t, record.TTL)
	require.Zero(t, record.ExpiryHeight)
}

func TestAttachFreeLocalFeeProofPreservesTTL(t *testing.T) {
	record := &swire.DKVSRecord{Key: "/svc/test/value", TTL: 60_000, ExpiryHeight: 12345}
	proof := &dkvsindexer.FeeProof{Mode: dkvsindexer.FeeModeFreeLocal}
	require.NoError(t, AttachDKVSFeeProof(record, proof))
	require.Equal(t, uint64(60_000), record.TTL)
	require.Equal(t, uint64(12345), record.ExpiryHeight)
}
