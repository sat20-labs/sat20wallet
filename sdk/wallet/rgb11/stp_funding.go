package rgb11wallet

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/rgb11/consignment"
	"github.com/sat20-labs/rgb11/seals"
	"github.com/sat20-labs/satoshinet/anchortx/nativeproof"
)

const MaxChannelProofSize = 16_000_000

// ChannelFunding is public proof material for one exact Bitcoin carrier. Its
// claimed identity and amount must be rederived by ValidateChannelFunding.
// It contains no channel entropy, revocation secret or alternative branch.
type ChannelFunding struct {
	OutPoint    string            `json:"outpoint"`
	Value       int64             `json:"value"`
	PkScript    []byte            `json:"pk_script"`
	AssetName   indexer.AssetName `json:"asset_name"`
	Amount      indexer.Decimal   `json:"amount"`
	Consignment []byte            `json:"consignment"`
	Reveals     [][]byte          `json:"reveals,omitempty"`
}

type ChannelInventory struct {
	Version uint32           `json:"version"`
	Sources []ChannelFunding `json:"sources"`
	// LastPackage retains the last private lifecycle obligation even after a
	// full exit leaves no current funding. Public receipts export a single
	// ChannelFunding.NativeProof, never this private journal.
	LastPackage []byte `json:"last_package,omitempty"`
}

func (f ChannelFunding) NativeProof() ([]byte, error) {
	return nativeproof.BuildRGB11(f.Consignment, f.Reveals)
}

func ValidateChannelFunding(ctx context.Context, source ChannelFunding, evidence BitcoinEvidenceProvider,
	mode nativeproof.Mode, network string) (*nativeproof.Result, error) {
	proof, err := source.NativeProof()
	if err != nil {
		return nil, err
	}
	return nativeproof.Validate(proof, nativeproof.Source{Outpoint: source.OutPoint,
		Value: source.Value, PkScript: source.PkScript, BitcoinNetwork: network,
		Assets: indexer.TxAssets{{Name: source.AssetName, Amount: source.Amount, BindingSat: 0}}},
		NativeBitcoinResolver(ctx, evidence), mode)
}

// EncodeChannelInventory provides stable ordering for peer comparison and
// persistence. The initial channel adapter admits one RGB contract per channel;
// multiple source carriers and allocations of that contract are supported.
func EncodeChannelInventory(sources []ChannelFunding) ([]byte, error) {
	return EncodeChannelInventoryWithLast(sources, nil)
}

func EncodeChannelInventoryWithLast(sources []ChannelFunding, last []byte) ([]byte, error) {
	if len(last) != 0 {
		if _, err := DecodeChannelPackage(last); err != nil {
			return nil, err
		}
	}
	ordered := append([]ChannelFunding(nil), sources...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].OutPoint < ordered[j].OutPoint })
	for i := range ordered {
		if err := checkChannelFundingShape(ordered[i]); err != nil {
			return nil, err
		}
		if i > 0 && (ordered[i].OutPoint == ordered[i-1].OutPoint || ordered[i].AssetName != ordered[0].AssetName) {
			return nil, fmt.Errorf("duplicate source or mixed RGB11 contract channel inventory")
		}
	}
	raw, err := json.Marshal(ChannelInventory{Version: 1, Sources: ordered, LastPackage: last})
	if err != nil || len(raw) > MaxChannelProofSize {
		return nil, fmt.Errorf("RGB11 channel inventory exceeds limits")
	}
	return raw, nil
}

func DecodeChannelInventory(raw []byte) (*ChannelInventory, error) {
	var inventory ChannelInventory
	if err := decodeChannelJSON(raw, &inventory); err != nil {
		return nil, err
	}
	if inventory.Version != 1 || len(inventory.Sources) > 1024 {
		return nil, fmt.Errorf("unsupported RGB11 channel inventory")
	}
	canonical, err := EncodeChannelInventoryWithLast(inventory.Sources, inventory.LastPackage)
	if err != nil || !bytes.Equal(canonical, raw) {
		return nil, fmt.Errorf("noncanonical RGB11 channel inventory")
	}
	return &inventory, nil
}

func checkChannelFundingShape(f ChannelFunding) error {
	if f.OutPoint == "" || f.Value < 0 || len(f.PkScript) == 0 || f.AssetName.Protocol != Protocol ||
		f.AssetName.Type != indexer.ASSET_TYPE_FT || f.Amount.Validate() != nil || f.Amount.Sign() <= 0 ||
		len(f.Consignment) == 0 || len(f.Consignment) > nativeproof.MaxProofSize || len(f.Reveals) > 1024 {
		return fmt.Errorf("invalid RGB11 channel funding material")
	}
	key, err := hex.DecodeString(f.AssetName.Ticker)
	if err != nil || len(key) != 32 || f.AssetName.Ticker != strings.ToLower(f.AssetName.Ticker) {
		return fmt.Errorf("noncanonical RGB11 contract asset key")
	}
	return nil
}

func decodeChannelJSON(raw []byte, value any) error {
	if len(raw) == 0 || len(raw) > MaxChannelProofSize {
		return fmt.Errorf("invalid RGB11 channel proof size")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("trailing RGB11 channel proof data")
	}
	return nil
}

func canonicalChannelConsignment(container *consignment.Container) ([]byte, error) {
	armor, err := consignment.EncodeArmor(container.Value)
	if err != nil {
		return nil, err
	}
	decoded, err := consignment.Decode([]byte(armor))
	if err != nil {
		return nil, err
	}
	return consignment.EncodeFile(decoded)
}

// PublicChannelConsignment retains disclosed public channel seals in the
// standard RGB object itself. A wallet may project only its own allocation;
// that must not discard the peer's already disclosed returned allocation from
// the material needed to audit a later native mapping or pool settlement.
// This operation grants no validity or ownership: callers must revalidate the
// resulting bytes with core before recording a receipt for their new hash.
func PublicChannelConsignment(raw []byte, disclosures [][]byte) ([]byte, error) {
	container, err := consignment.Decode(raw)
	if err != nil {
		return nil, err
	}
	reveals := make([]seals.GraphBlindSeal, 0, len(disclosures))
	for _, raw := range disclosures {
		seal, err := seals.DecodeGraphBlindSeal(raw)
		if err != nil {
			return nil, err
		}
		reveals = append(reveals, seal)
	}
	if _, err := container.RevealGraphSeals(reveals); err != nil {
		return nil, err
	}
	return canonicalChannelConsignment(container)
}
