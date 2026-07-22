package wallet

import (
	"testing"

	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func TestAttachAutopayFeeProofClearsRecordLease(t *testing.T) {
	record := &swire.DKVSRecord{TTL: 60_000, ExpiryHeight: 12345}
	proof := &dkvsindexer.FeeProof{Mode: dkvsindexer.FeeModeAutopay, PoolContract: "autopay"}
	require.NoError(t, AttachDKVSFeeProof(record, proof))
	require.Zero(t, record.TTL)
	require.Zero(t, record.ExpiryHeight)
	decoded, err := dkvsindexer.ParseFeeProof(record.FeeProof)
	require.NoError(t, err)
	require.Equal(t, dkvsindexer.FeeModeAutopay, decoded.Mode)
}

func TestAttachFreeLocalFeeProofPreservesTTL(t *testing.T) {
	record := &swire.DKVSRecord{TTL: 60_000, ExpiryHeight: 12345}
	proof := &dkvsindexer.FeeProof{Mode: dkvsindexer.FeeModeFreeLocal}
	require.NoError(t, AttachDKVSFeeProof(record, proof))
	require.Equal(t, uint64(60_000), record.TTL)
	require.Equal(t, uint64(12345), record.ExpiryHeight)
}
