package wallet

import (
	"bytes"
	"errors"
	"testing"

	"github.com/sat20-labs/satoshinet/btcec"
	dkvsindexer "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
)

func TestDKVSFreeLocalRenewalBuildsNextSignedVersion(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	walletValue, _, err := NewInternalWalletWithPrivKey(priv.Serialize(), GetChainParam())
	if err != nil {
		t.Fatal(err)
	}
	key, err := dkvsindexer.PersonalKey(walletValue.GetPubKey().SerializeCompressed(), "renew/unit")
	if err != nil {
		t.Fatal(err)
	}
	original, err := NewDKVSSignedRecord(walletValue, key, []byte("payload"), dkvsindexer.RecordOptions{
		Seq: 1, IssueHeight: 10, TTL: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	proof, err := dkvsindexer.NewFreeLocalFeeProof(
		key, "personal", uint32(dkvsindexer.RecordSize(original)), dkvsindexer.RecordExpiryHeight(original),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := AttachDKVSFeeProof(original, proof); err != nil {
		t.Fatal(err)
	}
	if err := SignDKVSRecord(walletValue, original); err != nil {
		t.Fatal(err)
	}

	renewed, err := NewDKVSSignedRenewalRecord(walletValue, original, dkvsindexer.RecordOptions{
		Seq: 99, IssueHeight: 15, TTL: 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	if renewed.Seq != 2 {
		t.Fatalf("renewed seq=%d want=2", renewed.Seq)
	}
	if renewed.IssueHeight != 15 || renewed.TTL != 20 {
		t.Fatalf("renewed lease issue=%d ttl=%d", renewed.IssueHeight, renewed.TTL)
	}
	if string(renewed.Value) != "payload" {
		t.Fatalf("renewed value=%q", renewed.Value)
	}
	if dkvsindexer.RecordExpiryHeight(renewed) <= dkvsindexer.RecordExpiryHeight(original) {
		t.Fatalf("renewal did not extend expiry: original=%d renewed=%d",
			dkvsindexer.RecordExpiryHeight(original), dkvsindexer.RecordExpiryHeight(renewed))
	}
	renewedProof, err := dkvsindexer.ParseFeeProof(renewed.FeeProof)
	if err != nil || renewedProof.Mode != dkvsindexer.FeeModeFreeLocal {
		t.Fatalf("renewed proof=%+v err=%v", renewedProof, err)
	}
	if bytes.Equal(original.Signature, renewed.Signature) {
		t.Fatal("renewal reused the original signature")
	}
	if err := dkvsindexer.VerifySignature(renewed); err != nil {
		t.Fatalf("renewed signature invalid: %v", err)
	}
}

func TestDKVSAutopayRecordRejectsRecordLevelRenewal(t *testing.T) {
	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	walletValue, _, err := NewInternalWalletWithPrivKey(priv.Serialize(), GetChainParam())
	if err != nil {
		t.Fatal(err)
	}
	key, err := dkvsindexer.PersonalKey(walletValue.GetPubKey().SerializeCompressed(), "renew/autopay")
	if err != nil {
		t.Fatal(err)
	}
	record, err := NewDKVSSignedRecord(walletValue, key, []byte("durable"), dkvsindexer.RecordOptions{
		Seq: 1, IssueHeight: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	proof, err := dkvsindexer.NewAutopayFeeProof(key, "personal", uint32(dkvsindexer.RecordSize(record)), 0, "pool", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := AttachDKVSFeeProof(record, proof); err != nil {
		t.Fatal(err)
	}
	if err := SignDKVSRecord(walletValue, record); err != nil {
		t.Fatal(err)
	}
	_, err = NewDKVSSignedRenewalRecord(walletValue, record, dkvsindexer.RecordOptions{
		IssueHeight: 20, TTL: 10,
	})
	if !errors.Is(err, dkvsindexer.ErrInvalidRecord) {
		t.Fatalf("autopay renewal err=%v", err)
	}
}
