package rgb11wallet

import (
	"bytes"
	"encoding/hex"
	"errors"
	"strings"

	indexercommon "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/rgb11/consensus"
	"github.com/sat20-labs/rgb11/consignment"
	"github.com/sat20-labs/rgb11/schemas"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	"github.com/sat20-labs/satoshinet/wire"
)

const (
	RGB11RegistryNamespace     = "contract"
	RGB11RegistryPath          = "/contract/rgb11"
	RGB11RegistryContractBytes = 32
	RGB11ContractValueVersion  = uint8(1)
)

type RGB11Registration struct {
	ContractID string `json:"contract_id"`
	SchemaID   string `json:"schema_id"`
	AssetType  string `json:"asset_type"`
	BaseTicker string `json:"base_ticker"`
}

// Technical registration contains public contract data only. DID ownership,
// ticker ordinals and display aliases have a separate naming lifecycle.
type RGB11ContractValue struct {
	ContractContent []byte
}

func NormalizeRGB11Ticker(raw string) (string, error) {
	ticker, err := namingBaseTicker(raw)
	if err != nil {
		return "", dkvsindexer.ErrInvalidRecord
	}
	return ticker, nil
}

func BuildRGB11AssetName(providerDID, ticker, assetType string, ordinal uint64) (string, error) {
	if !validRGB11AssetType(assetType) {
		return "", dkvsindexer.ErrInvalidRecord
	}
	name, err := BuildRegisteredAssetName(ticker, assetType, providerDID, ordinal)
	if err != nil {
		return "", dkvsindexer.ErrInvalidRecord
	}
	return name.String(), nil
}

func RGB11RegistryKey(contractID string) (string, error) {
	raw, err := hex.DecodeString(contractID)
	if err != nil || len(raw) != RGB11RegistryContractBytes || contractID != strings.ToLower(contractID) || strings.Trim(contractID, "0") == "" {
		return "", dkvsindexer.ErrInvalidKey
	}
	return RGB11RegistryPath + "/" + contractID, nil
}

func IsRGB11RegistryKey(parsed dkvsindexer.ParsedKey) bool {
	return parsed.Namespace == RGB11RegistryNamespace && len(parsed.Segments) == 2 && parsed.Segments[0] == "rgb11"
}

func validRGB11AssetType(assetType string) bool {
	return assetType == indexercommon.ASSET_TYPE_FT || assetType == indexercommon.ASSET_TYPE_NFT
}

func (value *RGB11ContractValue) registration() (*RGB11Registration, error) {
	if value == nil {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	container, err := consignment.DecodeFile(value.ContractContent)
	if err != nil || container.Armor == nil || container.Armor.Type != "contract" || !container.GenesisValid {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	canonical, err := consignment.EncodeFile(container)
	if err != nil || !bytes.Equal(canonical, value.ContractContent) {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	public, err := PublicRGB11Contract(canonical)
	if err != nil || !bytes.Equal(public, canonical) {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	schema, ok := container.Value.Field("schema")
	if !ok {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	types, ok := container.Value.Field("types")
	if !ok {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	genesis, ok := container.Value.Field("genesis")
	if !ok {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	metadata, err := schemas.ExtractGenesisAssetMetadata(schema, types, genesis)
	if err != nil {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	actualTicker, err := NormalizeRGB11Ticker(metadata.Ticker)
	if err != nil {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	descriptor, err := schemas.ByKind(container.GenesisReport.Kind)
	if err != nil {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	assetType := indexercommon.ASSET_TYPE_FT
	if !descriptor.Fungible {
		assetType = indexercommon.ASSET_TYPE_NFT
	}
	id, err := consensus.ParseContractID(container.ContractID)
	if err != nil {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	return &RGB11Registration{ContractID: hex.EncodeToString(id[:]), SchemaID: container.SchemaID,
		AssetType: assetType, BaseTicker: actualTicker}, nil
}

// The SDK business codec uses canonical CompactSize primitives. DKVS stores
// the resulting bytes without interpreting fields or the RGB contract.
func EncodeRGB11RegistryValue(value RGB11ContractValue) ([]byte, error) {
	public, err := PublicRGB11Contract(value.ContractContent)
	if err != nil {
		return nil, err
	}
	value.ContractContent = public
	if _, err := value.registration(); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	buf.WriteByte(RGB11ContractValueVersion)
	if err := wire.WriteVarBytes(&buf, wire.ProtocolVersion, value.ContractContent); err != nil {
		return nil, err
	}
	if buf.Len() > wire.MaxDKVSBlobValueSize {
		return nil, dkvsindexer.ErrRecordTooLarge
	}
	return buf.Bytes(), nil
}

func DecodeRGB11RegistryValue(raw []byte) (*RGB11ContractValue, error) {
	value, err := decodeRGB11ContractFields(raw)
	if err != nil {
		return nil, err
	}
	if _, err := value.registration(); err != nil {
		return nil, err
	}
	return value, nil
}

func decodeRGB11ContractFields(raw []byte) (*RGB11ContractValue, error) {
	if len(raw) == 0 || len(raw) > wire.MaxDKVSBlobValueSize || raw[0] != RGB11ContractValueVersion {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	buf := bytes.NewReader(raw[1:])
	content, err := wire.ReadVarBytes(buf, wire.ProtocolVersion, wire.MaxDKVSBlobValueSize, "RGB11 contract")
	if err != nil || buf.Len() != 0 {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	return &RGB11ContractValue{ContractContent: content}, nil
}

func RGB11RegistrationFromRecord(record *wire.DKVSRecord) (*RGB11Registration, error) {
	if record == nil {
		return nil, dkvsindexer.ErrRecordNotFound
	}
	parsed, err := dkvsindexer.ParseKey(record.Key)
	if err != nil || !IsRGB11RegistryKey(parsed) {
		return nil, dkvsindexer.ErrInvalidKey
	}
	value, err := decodeRGB11ContractFields(record.Value)
	if err != nil {
		return nil, err
	}
	registration, err := value.registration()
	if err != nil {
		return nil, err
	}
	key, err := RGB11RegistryKey(registration.ContractID)
	if err != nil || key != record.Key {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	return registration, nil
}

func validateRGB11RegistryEnvelope(record *wire.DKVSRecord) error {
	if record == nil || record.Seq != 1 || record.TTL != 0 || record.Flags != 0 || len(record.FeeProof) != 0 || len(record.PubKey) != 33 || len(record.Value) == 0 {
		return dkvsindexer.ErrInvalidRecord
	}
	return nil
}

// VerifyRGB11RegistrationForClient authenticates immutable business facts using
// a caller-configured authority, never one selected by the serving endpoint.
func VerifyRGB11RegistrationForClient(record *wire.DKVSRecord, contractID string, system dkvsindexer.SystemVerifier) (*RGB11Registration, error) {
	key, err := RGB11RegistryKey(contractID)
	if err != nil {
		return nil, err
	}
	if record == nil || record.Key != key {
		return nil, dkvsindexer.ErrInvalidKey
	}
	err = dkvsindexer.VerifyRecordForClient(record, dkvsindexer.RecordVerificationOptions{ExpectedKey: key})
	parsed, parseErr := dkvsindexer.ParseKey(record.Key)
	if parseErr != nil {
		return nil, parseErr
	}
	if err != nil {
		return nil, err
	}
	if !IsRGB11RegistryKey(parsed) {
		return nil, dkvsindexer.ErrInvalidKey
	}
	if err := validateRGB11RegistryEnvelope(record); err != nil {
		return nil, err
	}
	if system == nil {
		return nil, dkvsindexer.ErrPermissionDenied
	}
	if err := system.CanWriteSystem(record.Key, record.PubKey); err != nil {
		return nil, err
	}
	return RGB11RegistrationFromRecord(record)
}

func IsRGB11RegistryNotFound(err error) bool { return errors.Is(err, dkvsindexer.ErrRecordNotFound) }

// ValidateRGB11Registry validates a complete authority view in the business
// layer. An authenticated individual record does not prove view completeness.
func ValidateRGB11Registry(records []*wire.DKVSRecord, authority dkvsindexer.SystemVerifier) ([]*RGB11Registration, error) {
	registrations := make([]*RGB11Registration, 0, len(records))
	seen := make(map[string]bool, len(records))
	for _, record := range records {
		if record == nil || seen[record.Key] {
			return nil, dkvsindexer.ErrInvalidRecord
		}
		seen[record.Key] = true
		id := strings.TrimPrefix(record.Key, RGB11RegistryPath+"/")
		reg, err := VerifyRGB11RegistrationForClient(record, id, authority)
		if err != nil {
			return nil, err
		}
		registrations = append(registrations, reg)
	}
	return registrations, nil
}

// NewRGB11ContractValue derives technical identity from the public contract.
// No issuer registration or DID proof is required to create the value.
func NewRGB11ContractValue(content []byte) (RGB11ContractValue, *RGB11Registration, error) {
	public, err := PublicRGB11Contract(content)
	if err != nil {
		return RGB11ContractValue{}, nil, err
	}
	value := RGB11ContractValue{ContractContent: append([]byte(nil), public...)}
	registration, err := value.registration()
	if err != nil {
		return RGB11ContractValue{}, nil, err
	}
	return value, registration, nil
}
