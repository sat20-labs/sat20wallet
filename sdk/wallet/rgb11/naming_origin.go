package rgb11wallet

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"sort"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/sat20-labs/rgb11/strict_types"
)

// This is a display-resolution budget, not an RGB consensus restriction.
// Contracts outside it remain usable and are displayed by full ContractID.
const maxNamingOriginOutputs = 256

// GenesisOutputEvidence supplies Bitcoin facts, not RGB ownership claims.
// Raw transaction lookup is essential for genesis outputs already spent.
type GenesisOutputEvidence interface {
	GetRawTx(txid string) ([]byte, error)
	GetUTXO(outpoint string) (*BitcoinUTXO, error)
}

// GenesisNamingOutpoints requires all initial asset-owner seals to be revealed.
// It deliberately never substitutes a current transfer allocation, recipient
// address, or an arbitrary visible subset for the genesis naming origin.
func GenesisNamingOutpoints(genesis strict_types.Value) ([]wire.OutPoint, error) {
	assignments, ok := genesis.Unwrap().Field("assignments")
	assignments = assignments.Unwrap()
	if !ok || assignments.Kind != strict_types.ValueMap {
		return nil, ErrNamingOriginUnavailable
	}
	seen := make(map[wire.OutPoint]struct{})
	var result []wire.OutPoint
	count := 0
	for _, entry := range assignments.Entries {
		assignmentType, ok := entry.Key.Unwrap().Uint64()
		if !ok {
			return nil, ErrNamingOriginUnavailable
		}
		if assignmentType != 4000 {
			continue
		}
		typed := entry.Value.Unwrap()
		if typed.Kind != strict_types.ValueUnion || typed.Inner == nil {
			return nil, ErrNamingOriginUnavailable
		}
		items := typed.Inner.Unwrap()
		if items.Kind != strict_types.ValueList || len(items.Items) == 0 {
			return nil, ErrNamingOriginUnavailable
		}
		count += len(items.Items)
		if count > maxNamingOriginOutputs {
			return nil, ErrNamingOriginUnavailable
		}
		for _, item := range items.Items {
			item = item.Unwrap()
			if item.Kind != strict_types.ValueUnion || item.Name != "revealed" || item.Inner == nil {
				return nil, ErrNamingOriginUnavailable
			}
			seal, ok := item.Inner.Unwrap().Field("seal")
			if !ok {
				return nil, ErrNamingOriginUnavailable
			}
			seal = seal.Unwrap()
			txid, txOK := seal.Field("txid")
			vout, voutOK := seal.Field("vout")
			raw, rawOK := txid.Bytes()
			index, numberOK := vout.Unwrap().Uint64()
			if !txOK || !voutOK || !rawOK || !numberOK || len(raw) != chainhash.HashSize || index > math.MaxUint32 {
				return nil, ErrNamingOriginUnavailable
			}
			var hash chainhash.Hash
			copy(hash[:], raw)
			outpoint := wire.OutPoint{Hash: hash, Index: uint32(index)}
			if _, exists := seen[outpoint]; !exists {
				seen[outpoint] = struct{}{}
				result = append(result, outpoint)
			}
		}
	}
	if len(result) == 0 {
		return nil, ErrNamingOriginUnavailable
	}
	sort.Slice(result, func(i, j int) bool { return result[i].String() < result[j].String() })
	return result, nil
}

// ResolveGenesisNamingAddress accepts multiple initial allocations only when
// every disclosed asset-owner output resolves to the same Bitcoin address.
// Ambiguous/concealed origins fail closed for naming without invalidating the
// underlying RGB contract. This helper does not prove issuer authorization.
func ResolveGenesisNamingAddress(ctx context.Context, genesis strict_types.Value,
	evidence GenesisOutputEvidence, params *chaincfg.Params) (string, error) {
	if evidence == nil || params == nil {
		return "", ErrNamingOriginUnavailable
	}
	outpoints, err := GenesisNamingOutpoints(genesis)
	if err != nil {
		return "", err
	}
	transactions := make(map[chainhash.Hash]*wire.MsgTx)
	address := ""
	for _, outpoint := range outpoints {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		var script []byte
		tx, cached := transactions[outpoint.Hash]
		if !cached {
			raw, fetchErr := evidence.GetRawTx(outpoint.Hash.String())
			if fetchErr == nil && len(raw) > 0 {
				tx = wire.NewMsgTx(wire.TxVersion)
				reader := bytes.NewReader(raw)
				if err := tx.Deserialize(reader); err != nil || reader.Len() != 0 || tx.TxHash() != outpoint.Hash {
					return "", fmt.Errorf("%w: genesis transaction mismatch", ErrNamingOriginUnavailable)
				}
			}
			transactions[outpoint.Hash] = tx
		}
		if tx != nil {
			if uint64(outpoint.Index) >= uint64(len(tx.TxOut)) {
				return "", ErrNamingOriginUnavailable
			}
			script = tx.TxOut[outpoint.Index].PkScript
		} else {
			// Legacy evidence adapters may only provide live UTXOs. This
			// fallback is exact-outpoint only; spent outputs require raw txs.
			utxo, err := evidence.GetUTXO(outpoint.String())
			if err != nil || utxo == nil || utxo.OutPoint != outpoint.String() {
				return "", ErrNamingOriginUnavailable
			}
			script = utxo.PkScript
		}
		class, addresses, required, err := txscript.ExtractPkScriptAddrs(script, params)
		if err != nil || len(addresses) != 1 || required != 1 {
			return "", ErrNamingOriginUnavailable
		}
		switch class {
		case txscript.PubKeyHashTy, txscript.ScriptHashTy, txscript.WitnessV0PubKeyHashTy, txscript.WitnessV0ScriptHashTy, txscript.WitnessV1TaprootTy:
		default:
			return "", ErrNamingOriginUnavailable
		}
		current := addresses[0].EncodeAddress()
		if address != "" && current != address {
			return "", fmt.Errorf("%w: multiple genesis addresses", ErrNamingOriginUnavailable)
		}
		address = current
	}
	return address, nil
}
