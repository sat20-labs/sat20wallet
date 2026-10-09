package rgb11wallet

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/sat20-labs/rgb11/strict_types"
)

type namingOriginEvidence struct {
	raw       map[string][]byte
	utxos     map[string]*BitcoinUTXO
	rawCalls  int
	utxoCalls int
}

func (e *namingOriginEvidence) GetRawTx(txid string) ([]byte, error) {
	e.rawCalls++
	if raw := e.raw[txid]; raw != nil {
		return raw, nil
	}
	return nil, errors.New("raw transaction unavailable")
}

func (e *namingOriginEvidence) GetUTXO(outpoint string) (*BitcoinUTXO, error) {
	e.utxoCalls++
	return e.utxos[outpoint], nil
}

func namingGenesisFixture(outpoints ...wire.OutPoint) strict_types.Value {
	kind := uint64(4000)
	items := make([]strict_types.Value, 0, len(outpoints))
	for _, outpoint := range outpoints {
		index := uint64(outpoint.Index)
		seal := strict_types.Value{Kind: strict_types.ValueStruct, Fields: []strict_types.Field{
			{Name: "txid", Value: strict_types.Value{Kind: strict_types.ValueBytes, Raw: append([]byte(nil), outpoint.Hash[:]...)}},
			{Name: "vout", Value: strict_types.Value{Kind: strict_types.ValueNumber, Unsigned: &index}},
		}}
		fields := strict_types.Value{Kind: strict_types.ValueStruct, Fields: []strict_types.Field{{Name: "seal", Value: seal}}}
		items = append(items, strict_types.Value{Kind: strict_types.ValueUnion, Name: "revealed", Inner: &fields})
	}
	list := strict_types.Value{Kind: strict_types.ValueList, Items: items}
	assignments := strict_types.Value{Kind: strict_types.ValueMap, Entries: []strict_types.Entry{{
		Key:   strict_types.Value{Kind: strict_types.ValueNumber, Unsigned: &kind},
		Value: strict_types.Value{Kind: strict_types.ValueUnion, Name: "fungible", Inner: &list},
	}}}
	return strict_types.Value{Kind: strict_types.ValueStruct, Fields: []strict_types.Field{{Name: "assignments", Value: assignments}}}
}

func namingTransaction(t *testing.T, addresses ...string) (*wire.MsgTx, []byte) {
	t.Helper()
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{Index: 0xffffffff}, []byte{1, 1}, nil))
	for _, text := range addresses {
		address, err := btcutil.DecodeAddress(text, &chaincfg.MainNetParams)
		if err != nil {
			t.Fatal(err)
		}
		script, err := txscript.PayToAddrScript(address)
		if err != nil {
			t.Fatal(err)
		}
		tx.AddTxOut(wire.NewTxOut(1000, script))
	}
	var raw bytes.Buffer
	if err := tx.Serialize(&raw); err != nil {
		t.Fatal(err)
	}
	return tx, raw.Bytes()
}

func TestGenesisNamingUsesSpentOriginNotCurrentRecipient(t *testing.T) {
	issuer := namingTestAddress(t, &chaincfg.MainNetParams, 7)
	recipient := namingTestAddress(t, &chaincfg.MainNetParams, 8)
	tx, raw := namingTransaction(t, issuer, recipient)
	origin := wire.OutPoint{Hash: tx.TxHash(), Index: 0}
	evidence := &namingOriginEvidence{raw: map[string][]byte{tx.TxHash().String(): raw}}
	address, err := ResolveGenesisNamingAddress(context.Background(), namingGenesisFixture(origin), evidence, &chaincfg.MainNetParams)
	if err != nil || address != issuer || evidence.utxoCalls != 0 {
		t.Fatalf("spent origin not resolved from raw tx: address=%q err=%v UTXO calls=%d", address, err, evidence.utxoCalls)
	}
}

func TestGenesisNamingRequiresAllOriginsAtSameAddress(t *testing.T) {
	issuer := namingTestAddress(t, &chaincfg.MainNetParams, 9)
	other := namingTestAddress(t, &chaincfg.MainNetParams, 10)
	tx, raw := namingTransaction(t, issuer, issuer, other)
	evidence := &namingOriginEvidence{raw: map[string][]byte{tx.TxHash().String(): raw}}
	first := wire.OutPoint{Hash: tx.TxHash(), Index: 0}
	second := wire.OutPoint{Hash: tx.TxHash(), Index: 1}
	third := wire.OutPoint{Hash: tx.TxHash(), Index: 2}
	address, err := ResolveGenesisNamingAddress(context.Background(), namingGenesisFixture(second, first, first), evidence, &chaincfg.MainNetParams)
	if err != nil || address != issuer || evidence.rawCalls != 1 {
		t.Fatalf("same-address genesis: address=%q err=%v raw calls=%d", address, err, evidence.rawCalls)
	}
	if _, err := ResolveGenesisNamingAddress(context.Background(), namingGenesisFixture(first, third), evidence, &chaincfg.MainNetParams); !errors.Is(err, ErrNamingOriginUnavailable) {
		t.Fatalf("ambiguous genesis accepted: %v", err)
	}
}

func TestGenesisNamingRejectsConcealedSealsBeforeNetworkIO(t *testing.T) {
	genesis := namingGenesisFixture(wire.OutPoint{})
	genesis.Fields[0].Value.Entries[0].Value.Inner.Items[0] = strict_types.Value{Kind: strict_types.ValueUnion, Name: "confidential"}
	evidence := &namingOriginEvidence{}
	if _, err := ResolveGenesisNamingAddress(context.Background(), genesis, evidence, &chaincfg.MainNetParams); !errors.Is(err, ErrNamingOriginUnavailable) || evidence.rawCalls != 0 || evidence.utxoCalls != 0 {
		t.Fatalf("concealed origin must not pick an arbitrary address: %v %+v", err, evidence)
	}
}

func TestGenesisNamingRejectsSubstitutedRawTransaction(t *testing.T) {
	issuer := namingTestAddress(t, &chaincfg.MainNetParams, 11)
	other := namingTestAddress(t, &chaincfg.MainNetParams, 12)
	original, _ := namingTransaction(t, issuer)
	_, wrongRaw := namingTransaction(t, other)
	origin := wire.OutPoint{Hash: original.TxHash(), Index: 0}
	evidence := &namingOriginEvidence{raw: map[string][]byte{original.TxHash().String(): wrongRaw}}
	if _, err := ResolveGenesisNamingAddress(context.Background(), namingGenesisFixture(origin), evidence, &chaincfg.MainNetParams); !errors.Is(err, ErrNamingOriginUnavailable) || evidence.utxoCalls != 0 {
		t.Fatalf("substituted tx must fail without fallback: %v", err)
	}
}

func TestGenesisNamingExactOutpointFallbackAndCancellation(t *testing.T) {
	issuer := namingTestAddress(t, &chaincfg.MainNetParams, 13)
	tx, _ := namingTransaction(t, issuer)
	origin := wire.OutPoint{Hash: tx.TxHash(), Index: 0}
	evidence := &namingOriginEvidence{utxos: map[string]*BitcoinUTXO{origin.String(): {OutPoint: origin.String(), PkScript: tx.TxOut[0].PkScript}}}
	address, err := ResolveGenesisNamingAddress(context.Background(), namingGenesisFixture(origin), evidence, &chaincfg.MainNetParams)
	if err != nil || address != issuer {
		t.Fatalf("exact unspent origin: %q %v", address, err)
	}
	evidence.utxos[origin.String()].OutPoint = "wrong:0"
	if _, err := ResolveGenesisNamingAddress(context.Background(), namingGenesisFixture(origin), evidence, &chaincfg.MainNetParams); err == nil {
		t.Fatal("wrong UTXO accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ResolveGenesisNamingAddress(ctx, namingGenesisFixture(origin), evidence, &chaincfg.MainNetParams); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation ignored: %v", err)
	}
}
