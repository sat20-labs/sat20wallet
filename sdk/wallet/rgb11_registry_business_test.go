package wallet

import (
	"github.com/btcsuite/btcd/chaincfg"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func cloneRegistryTestRecord(r *wire.DKVSRecord) *wire.DKVSRecord {
	copied := *r
	copied.Value = append([]byte(nil), r.Value...)
	return &copied
}
func TestRGB11RegistryKeyAndValueBoundaries(t *testing.T) {
	_, makeRecord := newRGB11RegistryE2ESource(t)
	core := NewInternalWalletWithMnemonic(rgb11RegistryTestMnemonic, "", &chaincfg.TestNet4Params)
	record := makeRecord("alice", "USD", "f", 1, 3)
	for _, path := range []string{"/rgb11/alice/usd/1", rgb11wallet.RGB11RegistryPath + "/" + strings.Repeat("0", 64), rgb11wallet.RGB11RegistryPath + "/short", "/contract/rgb11/" + strings.ToUpper(rgb11RegistryRecordID(record)), "/blob/evm/source/testcontract"} {
		_, err := rgb11wallet.RGB11RegistryKey(strings.TrimPrefix(path, rgb11wallet.RGB11RegistryPath+"/"))
		require.Error(t, err, path)
	}
	// The old 33-byte value and armored/transfer transports are never accepted.
	_, err := rgb11wallet.DecodeRGB11RegistryValue(append([]byte{'f'}, make([]byte, 32)...))
	require.Error(t, err)
	for _, malformed := range [][]byte{append(append([]byte(nil), record.Value...), 0), append([]byte{2}, record.Value[1:]...), append([]byte{1, 0xfd, 5, 0}, record.Value[2:]...)} {
		_, err := rgb11wallet.DecodeRGB11RegistryValue(malformed)
		require.Error(t, err)
	}
	value, err := rgb11wallet.DecodeRGB11RegistryValue(record.Value)
	require.NoError(t, err)
	original := *value
	for _, mutation := range []func(*rgb11wallet.RGB11ContractValue){func(v *rgb11wallet.RGB11ContractValue) { v.ProviderDID = "abcdefghijk" }, func(v *rgb11wallet.RGB11ContractValue) { v.Ordinal = 0 }, func(v *rgb11wallet.RGB11ContractValue) { v.Ticker = "different" }, func(v *rgb11wallet.RGB11ContractValue) { v.Ticker = "USD" }, func(v *rgb11wallet.RGB11ContractValue) { v.ContractContent = []byte("not a contract") }} {
		v := original
		mutation(&v)
		_, err := rgb11wallet.EncodeRGB11RegistryValue(v)
		require.Error(t, err)
	}
	encoded, err := rgb11wallet.EncodeRGB11RegistryValue(original)
	require.NoError(t, err)
	require.Equal(t, record.Value, encoded)
	wrongID := cloneRegistryTestRecord(record)
	wrongID.Key = rgb11wallet.RGB11RegistryPath + "/" + strings.Repeat("f", 64)
	require.NoError(t, SignDKVSRecord(core, wrongID))
	_, err = rgb11wallet.VerifyRGB11RegistrationForClient(wrongID, strings.Repeat("f", 64), dkvsindexer.StaticSystemVerifier{Keys: [][]byte{core.GetPubKey().SerializeCompressed()}})
	require.Error(t, err)
}
