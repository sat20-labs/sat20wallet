package rgb11wallet

import (
	"github.com/sat20-labs/rgb11/consignment"
	"github.com/sat20-labs/rgb11/seals"
	"github.com/sat20-labs/rgb11/strict_types"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

// PublicRGB11Contract keeps the genesis commitment and schema while excluding
// ownership disclosures and transfer history. It is public contract metadata,
// not an ownership proof or a replacement for the wallet's recovery material.
func PublicRGB11Contract(content []byte) ([]byte, error) {
	c, err := consignment.DecodeFile(content)
	if err != nil || c.Armor == nil || c.Armor.Type != "contract" || !c.GenesisValid {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	value := c.Value.Clone()
	for i := range value.Fields {
		f := &value.Fields[i]
		switch f.Name {
		case "terminals":
			f.Value = strict_types.Value{Kind: strict_types.ValueMap}
		case "bundles":
			f.Value = strict_types.Value{Kind: strict_types.ValueList}
		case "genesis":
			if err := concealPublicGenesis(&f.Value); err != nil {
				return nil, err
			}
		case "version", "transfer", "schema", "types", "scripts":
		default:
			return nil, dkvsindexer.ErrInvalidRecord
		}
	}
	armor, err := consignment.EncodeArmor(value)
	if err != nil {
		return nil, err
	}
	public, err := consignment.DecodeArmor(armor)
	if err != nil {
		return nil, err
	}
	if !public.GenesisValid || public.ContractID != c.ContractID || public.SchemaID != c.SchemaID {
		return nil, dkvsindexer.ErrInvalidRecord
	}
	return consignment.EncodeFile(public)
}

func publicField(v *strict_types.Value, name string) *strict_types.Value {
	v = publicUnwrap(v)
	if v == nil || v.Kind != strict_types.ValueStruct {
		return nil
	}
	for i := range v.Fields {
		if v.Fields[i].Name == name {
			return &v.Fields[i].Value
		}
	}
	return nil
}

func publicUnwrap(v *strict_types.Value) *strict_types.Value {
	for v != nil && v.Kind == strict_types.ValueTuple && len(v.Items) == 1 {
		v = &v.Items[0]
	}
	return v
}

func concealPublicGenesis(genesis *strict_types.Value) error {
	assignments := publicUnwrap(publicField(genesis, "assignments"))
	if assignments == nil || assignments.Kind != strict_types.ValueMap {
		return dkvsindexer.ErrInvalidRecord
	}
	for i := range assignments.Entries {
		typed := publicUnwrap(&assignments.Entries[i].Value)
		if typed.Kind != strict_types.ValueUnion || typed.Inner == nil {
			return dkvsindexer.ErrInvalidRecord
		}
		list := publicUnwrap(typed.Inner)
		if list.Kind != strict_types.ValueList {
			return dkvsindexer.ErrInvalidRecord
		}
		for j := range list.Items {
			a := publicUnwrap(&list.Items[j])
			if a.Kind != strict_types.ValueUnion || a.Inner == nil {
				return dkvsindexer.ErrInvalidRecord
			}
			if a.Name == "confidentialSeal" {
				continue
			}
			if a.Name != "revealed" {
				return dkvsindexer.ErrInvalidRecord
			}
			seal, state := publicField(a.Inner, "seal"), publicField(a.Inner, "state")
			txid, vout, blinding := publicField(seal, "txid"), publicField(seal, "vout"), publicField(seal, "blinding")
			if txid == nil || vout == nil || blinding == nil || state == nil {
				return dkvsindexer.ErrInvalidRecord
			}
			raw, ok := txid.Bytes()
			n, nOK := vout.Unwrap().Uint64()
			b, bOK := blinding.Unwrap().Uint64()
			if !ok || !nOK || !bOK || n > uint64(^uint32(0)) {
				return dkvsindexer.ErrInvalidRecord
			}
			revealed, err := seals.NewBlindSeal(raw, uint32(n), b)
			if err != nil {
				return err
			}
			secret, err := revealed.Conceal()
			if err != nil {
				return err
			}
			inner := strict_types.Value{Kind: strict_types.ValueStruct, Fields: []strict_types.Field{
				{Name: "seal", Value: strict_types.Value{Kind: strict_types.ValueBytes, Raw: append([]byte(nil), secret[:]...)}},
				{Name: "state", Value: state.Clone()},
			}}
			*a = strict_types.Value{Kind: strict_types.ValueUnion, Name: "confidentialSeal", Tag: 1, Inner: &inner}
		}
	}
	return nil
}
