package wallet

import (
	"bytes"
	"os"
	"testing"

	"github.com/sat20-labs/rgb11/consignment"
	"github.com/sat20-labs/rgb11/issuance"
	"github.com/sat20-labs/rgb11/operations"
	"github.com/sat20-labs/rgb11/schemas"
	"github.com/sat20-labs/rgb11/seals"
	"github.com/sat20-labs/rgb11/strict_types"
	rgb11wallet "github.com/sat20-labs/sat20wallet/sdk/wallet/rgb11"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

func publicPrivacyRevealedCount(value strict_types.Value) int {
	n := 0
	if value.Kind == strict_types.ValueUnion && value.Name == "revealed" {
		n++
	}
	if value.Inner != nil {
		n += publicPrivacyRevealedCount(*value.Inner)
	}
	for _, field := range value.Fields {
		n += publicPrivacyRevealedCount(field.Value)
	}
	for _, item := range value.Items {
		n += publicPrivacyRevealedCount(item)
	}
	for _, entry := range value.Entries {
		n += publicPrivacyRevealedCount(entry.Key)
		n += publicPrivacyRevealedCount(entry.Value)
	}
	return n
}

// A real issuer contract contains private genesis disclosures. Publishing the
// contract must preserve its identity and rights without publishing those seals.
func TestRGB11RegistrarPublicContractHasNoGenesisDisclosures(t *testing.T) {
	for _, kind := range []schemas.Kind{schemas.NIA, schemas.IFA, schemas.UDA} {
		t.Run(string(kind), func(t *testing.T) {
			source, _ := newRGB11RegistryE2ESource(t)
			backend := &registrarTestBackend{indexer: source.indexer}
			registrar := newRegistrarTest(t, backend)
			seal, err := seals.NewGraphBlindSeal(bytes.Repeat([]byte{41}, 32), 0, 424242)
			require.NoError(t, err)
			spec := issuance.Spec{Kind: kind, Network: issuance.BitcoinTestnet4, Ticker: "PRIV", Name: "Public separation fixture", Timestamp: 1700000000, Allocations: []issuance.Allocation{{Seal: seal, Amount: 1}}}
			if kind == schemas.IFA {
				control, err := seals.NewGraphBlindSeal(bytes.Repeat([]byte{42}, 32), 1, 434343)
				require.NoError(t, err)
				spec.Allocations[0].Amount = 1000
				spec.InflationRights = []issuance.Allocation{{Seal: control, Amount: 9000}}
			}
			issued, err := issuance.Issue(spec)
			require.NoError(t, err)
			raw, err := consignment.EncodeFile(issued.Container)
			require.NoError(t, err)
			before := append([]byte(nil), raw...)
			genesis, ok := issued.Container.Value.Field("genesis")
			require.True(t, ok)
			require.Positive(t, publicPrivacyRevealedCount(genesis), "fixture must include real private seals")
			registration, err := registrar.Register(raw)
			require.NoError(t, err)
			key, err := rgb11wallet.RGB11RegistryKey(registration.ContractID)
			require.NoError(t, err)
			stored, err := source.indexer.Get(key)
			require.NoError(t, err)
			value, err := rgb11wallet.DecodeRGB11RegistryValue(stored.Value)
			require.NoError(t, err)
			public, err := consignment.DecodeFile(value.ContractContent)
			require.NoError(t, err)
			require.Equal(t, issued.ContractID, public.ContractID)
			publicGenesis, ok := public.Value.Field("genesis")
			require.True(t, ok)
			require.Zero(t, publicPrivacyRevealedCount(publicGenesis), "public contract exposes private genesis seals")
			require.Equal(t, issued.Container.GenesisReport.FungibleAmounts, public.GenesisReport.FungibleAmounts, "ordinary and control rights must be preserved")
			require.Equal(t, before, raw, "publishing must not mutate the issuer recovery material")
			again, err := registrar.Register(raw)
			require.NoError(t, err)
			require.Equal(t, registration, again)
			writes := backend.puts.Load()
			again, err = registrar.Register(value.ContractContent)
			require.NoError(t, err)
			require.Equal(t, registration, again)
			require.Equal(t, writes, backend.puts.Load(), "public and private representations must not create a second record")
			canonical, err := rgb11wallet.PublicRGB11Contract(value.ContractContent)
			require.NoError(t, err)
			require.Equal(t, value.ContractContent, canonical)
			for _, field := range []string{"terminals", "bundles"} {
				v, ok := public.Value.Field(field)
				require.True(t, ok)
				require.Empty(t, v.Unwrap().Items)
				require.Empty(t, v.Unwrap().Entries)
			}
			direct, err := rgb11wallet.EncodeRGB11RegistryValue(rgb11wallet.RGB11ContractValue{ContractContent: raw})
			require.NoError(t, err)
			require.Equal(t, stored.Value, direct, "direct registry encoding must also enforce public content")
			tampered := cloneRegistryTestRecord(stored)
			tampered.Value[len(tampered.Value)-1] ^= 1
			_, err = rgb11wallet.VerifyRGB11RegistrationForClient(tampered, registration.ContractID, registrar.authority)
			require.Error(t, err, "tampered public content must not be accepted")
		})
	}
}

// A contract envelope may carry transfer history. Even valid history is not
// appropriate for the shared genesis catalog and must remain private.
func TestRGB11PublicContractDropsTransferHistory(t *testing.T) {
	raw, err := os.ReadFile("../../../rgb11/testvectors/rc11/nia-transfer.rgba")
	require.NoError(t, err)
	transfer, err := consignment.Decode(raw)
	require.NoError(t, err)
	transferFile, err := consignment.EncodeFile(transfer)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(transferFile), 7)
	require.Equal(t, []byte{'R', 'G', 'B', 0, 'T', 'F', 'R'}, transferFile[:7])
	binaryTransfer, err := consignment.DecodeFile(transferFile)
	require.NoError(t, err, "the negative fixture must pass binary decoding")
	require.Equal(t, "transfer", binaryTransfer.Armor.Type)
	require.True(t, binaryTransfer.GenesisValid)
	_, err = rgb11wallet.PublicRGB11Contract(transferFile)
	require.ErrorIs(t, err, dkvsindexer.ErrInvalidRecord, "a valid transfer is rejected by the contract type check")
	// The official history has no terminals. Add one with a real bundle ID
	// and a strictly encoded test seal commitment to exercise its removal.
	history, ok := transfer.Value.Field("bundles")
	require.True(t, ok)
	require.NotEmpty(t, history.Unwrap().Items)
	bundleValue, ok := history.Unwrap().Items[0].Unwrap().Field("bundle")
	require.True(t, ok)
	bundle, err := operations.CommitBundle(bundleValue)
	require.NoError(t, err)
	registry, err := strict_types.RC11Registry()
	require.NoError(t, err)
	bundleID, err := registry.Decode("RGBCommit", "BundleId", bundle.BundleID[:])
	require.NoError(t, err)
	secret, err := seals.NewWitnessBlindSeal(1, 454545).Conceal()
	require.NoError(t, err)
	secretSet, err := registry.Decode("RGBStd", "SecretSeals", append([]byte{1, 0}, secret[:]...))
	require.NoError(t, err)
	value := transfer.Value.Clone()
	for i := range value.Fields {
		if value.Fields[i].Name == "terminals" {
			value.Fields[i].Value = strict_types.Value{Kind: strict_types.ValueMap, Entries: []strict_types.Entry{{Key: bundleID, Value: secretSet}}}
		}
		if value.Fields[i].Name == "transfer" {
			value.Fields[i].Value = strict_types.Value{Kind: strict_types.ValueEnum, Name: "false", Tag: 0}
		}
	}
	armored, err := consignment.EncodeArmor(value)
	require.NoError(t, err)
	private, err := consignment.DecodeArmor(armored)
	require.NoError(t, err)
	bundles, ok := private.Value.Field("bundles")
	require.True(t, ok)
	require.NotEmpty(t, bundles.Unwrap().Items, "fixture must actually carry private transfer history")
	privateTerminals, ok := private.Value.Field("terminals")
	require.True(t, ok)
	require.NotEmpty(t, privateTerminals.Unwrap().Entries, "fixture must actually carry private terminals")
	file, err := consignment.EncodeFile(private)
	require.NoError(t, err)
	before := append([]byte(nil), file...)
	publicBytes, err := rgb11wallet.PublicRGB11Contract(file)
	require.NoError(t, err)
	public, err := consignment.DecodeFile(publicBytes)
	require.NoError(t, err)
	require.Equal(t, private.ContractID, public.ContractID)
	require.Equal(t, private.SchemaID, public.SchemaID)
	publicBundles, ok := public.Value.Field("bundles")
	require.True(t, ok)
	require.Empty(t, publicBundles.Unwrap().Items)
	terminals, ok := public.Value.Field("terminals")
	require.True(t, ok)
	require.Empty(t, terminals.Unwrap().Entries)
	genesis, ok := public.Value.Field("genesis")
	require.True(t, ok)
	require.Zero(t, publicPrivacyRevealedCount(genesis))
	require.Equal(t, before, file)
}

func TestRGB11RegistryRejectsUnconcealedStoredContract(t *testing.T) {
	seal, err := seals.NewGraphBlindSeal(bytes.Repeat([]byte{43}, 32), 0, 444444)
	require.NoError(t, err)
	issued, err := issuance.Issue(issuance.Spec{Kind: schemas.NIA, Network: issuance.BitcoinTestnet4, Ticker: "PRIVATE", Name: "Unconcealed record fixture", Timestamp: 1700000000, Allocations: []issuance.Allocation{{Seal: seal, Amount: 1}}})
	require.NoError(t, err)
	private, err := consignment.EncodeFile(issued.Container)
	require.NoError(t, err)
	var encoded bytes.Buffer
	encoded.WriteByte(rgb11wallet.RGB11ContractValueVersion)
	require.NoError(t, wire.WriteVarBytes(&encoded, wire.ProtocolVersion, private))
	_, err = rgb11wallet.DecodeRGB11RegistryValue(encoded.Bytes())
	require.ErrorIs(t, err, dkvsindexer.ErrInvalidRecord, "stored public registry records must not disclose genesis seals")
	public, err := rgb11wallet.PublicRGB11Contract(private)
	require.NoError(t, err)
	require.NotEqual(t, private, public)
	value, _, err := rgb11wallet.NewRGB11ContractValue(public)
	require.NoError(t, err)
	canonical, err := rgb11wallet.EncodeRGB11RegistryValue(value)
	require.NoError(t, err)
	_, err = rgb11wallet.DecodeRGB11RegistryValue(canonical)
	require.NoError(t, err)
}
