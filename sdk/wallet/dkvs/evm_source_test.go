package dkvs

import (
	"crypto/sha256"
	"testing"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	"github.com/sat20-labs/satoshinet/btcec/ecdsa"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/stretchr/testify/require"
)

type sourceSigner struct {
	common.Wallet
	key *secp256k1.PrivateKey
}

func (s sourceSigner) GetPubKey() *secp256k1.PublicKey { return s.key.PubKey() }
func (s sourceSigner) SignMessage(message []byte) ([]byte, error) {
	hash := sha256.Sum256(message)
	return ecdsa.Sign(s.key, hash[:]).Serialize(), nil
}

func TestEVMSourceRecordUsesExistingPublicKeySigner(t *testing.T) {
	signer := sourceSigner{key: secp256k1.PrivKeyFromBytes([]byte{1})}
	record, err := NewSignedRecord(signer, "/contract/evm/source/testcontract", []byte(`{"source":"example"}`), dkvsindexer.RecordOptions{Seq: 1})
	require.NoError(t, err)
	require.Equal(t, signer.GetPubKey().SerializeCompressed(), record.PubKey)
	require.NoError(t, dkvsindexer.VerifyRecordForClient(record, dkvsindexer.RecordVerificationOptions{ExpectedKey: record.Key}))
}
