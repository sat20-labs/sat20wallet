package wallet

import (
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	indexer "github.com/sat20-labs/indexer/common"
)

func nativeOtherExchangeFixture(t *testing.T) (*Channel, *Channel, [][]string) {
	t.Helper()
	left, _ := btcec.PrivKeyFromBytes([]byte{1})
	right, _ := btcec.PrivKeyFromBytes([]byte{2})
	lpk, rpk := left.PubKey().SerializeCompressed(), right.PubKey().SerializeCompressed()
	redeem, script, err := GetP2WSHscript(lpk, rpk)
	if err != nil {
		t.Fatal(err)
	}
	parent := wire.NewMsgTx(2)
	parent.AddTxIn(wire.NewTxIn(&wire.OutPoint{Hash: chainhash.Hash{3}}, nil, nil))
	parent.AddTxOut(wire.NewTxOut(10000, script))
	sweep := wire.NewMsgTx(2)
	point := wire.OutPoint{Hash: parent.TxHash()}
	sweep.AddTxIn(wire.NewTxIn(&point, nil, nil))
	sweep.AddTxOut(wire.NewTxOut(9000, []byte{txscript.OP_TRUE}))
	fetcher := txscript.NewMultiPrevOutFetcher(map[wire.OutPoint]*wire.TxOut{point: parent.TxOut[0]})
	hashes := txscript.NewTxSigHashes(sweep, fetcher)
	sweep.TxIn[0].Witness = wire.TxWitness{nil, nil, nil, redeem}
	for _, key := range []*btcec.PrivateKey{left, right} {
		sig, err := txscript.RawTxInWitnessSignature(sweep, hashes, 0, 10000, redeem, txscript.SigHashAll, key)
		if err != nil {
			t.Fatal(err)
		}
		peer := rpk
		if key == right {
			peer = lpk
		}
		pos := GetCurrSignPosition2(key.PubKey().SerializeCompressed(), peer)
		sweep.TxIn[0].Witness[pos+1] = sig
	}
	newChannel := func() *Channel {
		channel := &Channel{ChannelInDB: *NewChannelInDB()}
		channel.LocalCommitment = NewChannelCommitment()
		channel.RemoteCommitment = NewChannelCommitment()
		name := AssetName{AssetName: indexer.AssetName{Protocol: indexer.PROTOCOL_NAME_ATOM, Type: indexer.ASSET_TYPE_FT, Ticker: "atom"}, N: 1}
		channel.FundingUtxos[name] = []*TxOutput{indexer.NewTxOutput(1000)}
		return channel
	}
	sender, receiver := newChannel(), newChannel()
	sender.LocalCommitment.CommitTx = parent.Copy()
	sender.LocalCommitment.Others = []*CommitmentTx{{CommitTx: sweep.Copy()}}
	receiver.RemoteCommitment.CommitTx = parent.Copy()
	receiver.RemoteCommitment.Others = []*CommitmentTx{{CommitTx: sweep.Copy()}}
	receiver.RemoteCommitment.Others[0].CommitTx.TxIn[0].Witness[1] = nil
	packages, err := encodeSTPNativeOtherTransactions(sender)
	if err != nil {
		t.Fatal(err)
	}
	return sender, receiver, packages
}

func TestSTPNativeOtherExchangeRequiresCompleteMatchingAuthorization(t *testing.T) {
	for _, scenario := range []string{"missing", "changed-output", "missing-signature", "extra-transaction"} {
		t.Run(scenario, func(t *testing.T) {
			_, receiver, packages := nativeOtherExchangeFixture(t)
			original, _ := EncodeMsgTx(receiver.RemoteCommitment.Others[0].CommitTx)
			switch scenario {
			case "missing":
				packages = nil
			case "changed-output", "missing-signature":
				tx, err := DecodeMsgTx(packages[0][0])
				if err != nil {
					t.Fatal(err)
				}
				if scenario == "changed-output" {
					tx.TxOut[0].Value--
				} else {
					tx.TxIn[0].Witness[2] = nil
				}
				packages[0][0], err = EncodeMsgTx(tx)
				if err != nil {
					t.Fatal(err)
				}
			case "extra-transaction":
				packages[0] = append(packages[0], packages[0][0])
			}
			if err := (&Manager{}).acceptSTPNativeOtherTransactions(receiver, packages); err == nil {
				t.Fatal("invalid recovery package accepted")
			}
			after, _ := EncodeMsgTx(receiver.RemoteCommitment.Others[0].CommitTx)
			if after != original {
				t.Fatal("failed validation changed the stored recovery package")
			}
		})
	}
	_, receiver, packages := nativeOtherExchangeFixture(t)
	if err := (&Manager{}).acceptSTPNativeOtherTransactions(receiver, packages); err != nil {
		t.Fatal(err)
	}
	actual, _ := EncodeMsgTx(receiver.RemoteCommitment.Others[0].CommitTx)
	if actual != packages[0][0] {
		t.Fatal("complete recovery authorization was not retained")
	}
}

func TestSTPNativeOtherExchangeCannotReleasePartialPackage(t *testing.T) {
	sender, _, _ := nativeOtherExchangeFixture(t)
	sender.LocalCommitment.Others[0].CommitTx.TxIn[0].Witness[1] = nil
	if _, err := encodeSTPNativeOtherTransactions(sender); err == nil {
		t.Fatal("partial recovery package was ready for revocation")
	}
}
