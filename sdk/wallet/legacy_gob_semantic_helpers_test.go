package wallet

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	indexer "github.com/sat20-labs/indexer/common"
	"reflect"
	"sort"
)

// The semantic oracle preserves all exported legacy fields, including struct-key
// maps, decimals, public keys and exact transaction/signature bytes. Empty and
// nil containers are equivalent; gob and existing cleanup normalize those.
func legacyFixtureCanonical(v reflect.Value) any {
	if !v.IsValid() {
		return nil
	}
	if (v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface) && v.IsNil() {
		return nil
	}
	if v.CanInterface() {
		if key, ok := v.Interface().(interface{ SerializeCompressed() []byte }); ok {
			return map[string]any{"PublicKey": hex.EncodeToString(key.SerializeCompressed())}
		}
		if v.Type() == reflect.TypeOf(indexer.Decimal{}) {
			d := v.Interface().(indexer.Decimal)
			return map[string]any{"Precision": d.Precision, "Amount": d.String()}
		}
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		return legacyFixtureCanonical(v.Elem())
	case reflect.Struct:
		result := make(map[string]any)
		for i := 0; i < v.NumField(); i++ {
			field := v.Type().Field(i)
			if field.PkgPath != "" || legacyFixtureNativeField(v.Type().Name(), field.Name) {
				continue
			}
			result[field.Name] = legacyFixtureCanonical(v.Field(i))
		}
		return result
	case reflect.Map:
		if v.Len() == 0 {
			return nil
		}
		type entry struct {
			Key   any
			Value any
		}
		entries := make([]entry, 0, v.Len())
		for _, key := range v.MapKeys() {
			entries = append(entries, entry{legacyFixtureCanonical(key), legacyFixtureCanonical(v.MapIndex(key))})
		}
		sort.Slice(entries, func(i, j int) bool {
			a, _ := json.Marshal(entries[i].Key)
			b, _ := json.Marshal(entries[j].Key)
			return string(a) < string(b)
		})
		return entries
	case reflect.Slice, reflect.Array:
		if v.Len() == 0 {
			return nil
		}
		if v.Type().Elem().Kind() == reflect.Uint8 {
			b := make([]byte, v.Len())
			for i := range b {
				b[i] = byte(v.Index(i).Uint())
			}
			return hex.EncodeToString(b)
		}
		result := make([]any, v.Len())
		for i := range result {
			result[i] = legacyFixtureCanonical(v.Index(i))
		}
		return result
	case reflect.String:
		return v.String()
	case reflect.Bool:
		return v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return v.Uint()
	default:
		panic("unsupported fixture semantic kind: " + v.Kind().String())
	}
}

func legacyFixtureNativeField(typ, field string) bool {
	switch typ {
	case "ChannelInDB":
		return field == "AssetFunding" || field == "AssetEntropy" || field == "ClosingAssetProofs" || field == "NativeParents" || field == "NativePrevouts"
	case "ChannelCommitmentV1":
		return field == "AssetProofs" || field == "RecoveryProofs" || field == "RecoveryFeeIndexes" || field == "RGBClientRecovery"
	case "CommitmentTx":
		return field == "AssetProofs" || field == "RecoveryProofs"
	case "ClosingDataInDB":
		return field == "AssetProofs"
	case "SplicingDataInDB":
		return field == "AssetFunding" || field == "AssetProofs" || field == "AssetEntropy" || field == "NativeAnchorProof"
	}
	return false
}

// RGBClientRecovery did not exist in the pinned legacy schema. Classifying
// that additive field must not make a nonnil old-asset value invisible to the
// semantic oracle: the same explicit isolation guard is used by every legacy
// channel native-state assertion and the negative control.
func legacyFixtureRGBRecoveryIsolation(c *ChannelInDB) error {
	if c == nil {
		return fmt.Errorf("missing legacy channel")
	}
	for _, commitment := range []*ChannelCommitment{c.LocalCommitment, c.RemoteCommitment} {
		if commitment != nil && commitment.RGBClientRecovery != nil {
			return fmt.Errorf("legacy commitment acquired RGB-only client recovery state")
		}
	}
	return nil
}
