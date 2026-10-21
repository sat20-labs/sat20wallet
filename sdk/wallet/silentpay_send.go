package wallet

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
)

// SilentPaySenderInput represents one eligible, fully selected input. The key
// is the private key for the actual input public key used by BIP352 (the
// even-Y normalized Taproot OUTPUT key for P2TR, not its BIP86 internal key).
type SilentPaySenderInput struct {
	Outpoint wire.OutPoint
	Key      *btcec.PrivateKey
}

// SilentPayDestinationScript must only be called AFTER input selection.
// An RBF or fee adjustment that changes inputs requires re-running it.
func SilentPayDestinationScript(destination SilentPayAddress, inputs []SilentPaySenderInput, k uint32) ([]byte, error) {
	if destination.Scan == nil || destination.Spend == nil || len(inputs) == 0 {
		return nil, fmt.Errorf("silentpay: invalid sender inputs or destination")
	}
	var aggregate btcec.ModNScalar
	var lowest [36]byte
	for i, input := range inputs {
		if input.Key == nil {
			return nil, fmt.Errorf("silentpay: input private key missing")
		}
		value := input.Key.Key
		// Each Taproot input contributes the even-Y representative, i.e. the
		// x-only key visible in the previous output script.
		if input.Key.PubKey().SerializeCompressed()[0] == 3 {
			value.Negate()
		}
		aggregate.Add(&value)
		var serialized [36]byte
		copy(serialized[:32], input.Outpoint.Hash[:])
		binary.LittleEndian.PutUint32(serialized[32:], input.Outpoint.Index)
		if i == 0 || bytes.Compare(serialized[:], lowest[:]) < 0 {
			lowest = serialized
		}
	}
	if aggregate.IsZero() {
		return nil, fmt.Errorf("silentpay: invalid aggregate input key")
	}
	aggregatePub := btcec.PrivKeyFromScalar(&aggregate).PubKey()
	inputHash, err := silentPayCheckedScalar(silentPayTaggedHash(
		"BIP0352/Inputs", lowest[:], aggregatePub.SerializeCompressed(),
	))
	if err != nil {
		return nil, err
	}
	aggregate.Mul(&inputHash)
	shared, err := silentPaySharedSecretPoint(&aggregate, destination.Scan)
	if err != nil {
		return nil, err
	}
	tweak, err := silentPayOutputScalar(shared, k)
	if err != nil {
		return nil, err
	}
	publicOutput, err := silentPayTweakPoint(destination.Spend, &tweak)
	if err != nil {
		return nil, err
	}
	return append([]byte{txscript.OP_1, 0x20}, schnorr.SerializePubKey(publicOutput)...), nil
}

// BuildSilentPayment assembles a real BTC transaction using the existing
// plain-sats selection and asset exclusion. We verify that every selected
// input is an ordinary key-path BIP86 P2TR output controlled by this account,
// rather than guessing private keys for arbitrary UTXOs.
//
// No SP output is allowed to be finalized before the last input is known.
func (p *Manager) BuildSilentPayment(address string, amount, feeRate int64) (*wire.MsgTx, *txscript.MultiPrevOutFetcher, error) {
	dest, err := DecodeSilentPayAddress(address, GetChainParam())
	if err != nil {
		return nil, nil, err
	}
	if p == nil || p.wallet == nil {
		return nil, nil, ErrSilentPayUnsupported
	}
	wallet, ok := p.wallet.(*InternalWallet)
	if !ok {
		return nil, nil, ErrSilentPayUnsupported
	}
	index := wallet.GetSubAccount()
	// Sending to a BIP352 address only requires the recipient's public
	// Scan/Spend keys and the sender's eligible P2TR funding keys. A sender
	// does not need a receiving Scan Key or SP account activation.
	if amount < 330 {
		return nil, nil, fmt.Errorf("silentpay: BTC amount too small")
	}
	if feeRate <= 0 {
		feeRate = p.GetFeeRate()
	}
	if feeRate <= 0 {
		return nil, nil, fmt.Errorf("silentpay: invalid fee rate")
	}

	// Address is just a same-sized P2TR placeholder for the existing fee
	// estimator and coin selector. It is NEVER broadcast or exposed.
	sourceAddress := wallet.GetAddress()
	tx, prev, _, err := p.BuildBatchSendTx_btc(
		sourceAddress, sourceAddress, amount, 1, nil, feeRate, nil, false,
	)
	if err != nil {
		return nil, nil, err
	}
	if len(tx.TxOut) == 0 || len(tx.TxIn) == 0 || tx.TxOut[0].Value != amount {
		return nil, nil, fmt.Errorf("silentpay: invalid selected transaction")
	}

	wallet.mutex.Lock()
	basePrivate := wallet.getPaymentPrivKeyWithIndex(index)
	wallet.mutex.Unlock()
	if basePrivate == nil {
		return nil, nil, ErrSilentPayUnsupported
	}
	// The wallet signs ordinary BIP86 outputs through TapTweak. For SP input
	// aggregation BIP352 needs the resulting output key, normalized even-Y.
	taprootKey := txscript.TweakTaprootPrivKey(*basePrivate, nil)
	defer taprootKey.Zero()
	taprootPublic := taprootKey.PubKey()
	expectedScript := append([]byte{txscript.OP_1, 0x20}, schnorr.SerializePubKey(taprootPublic)...)
	inputs := make([]SilentPaySenderInput, 0, len(tx.TxIn))
	for _, in := range tx.TxIn {
		out := prev.FetchPrevOutput(in.PreviousOutPoint)
		if out == nil || !bytes.Equal(out.PkScript, expectedScript) {
			return nil, nil, fmt.Errorf("silentpay: selected input is not an eligible owned P2TR output")
		}
		inputs = append(inputs, SilentPaySenderInput{
			Outpoint: in.PreviousOutPoint,
			Key:      taprootKey,
		})
	}
	script, err := SilentPayDestinationScript(dest, inputs, 0)
	if err != nil {
		return nil, nil, err
	}
	tx.TxOut[0].PkScript = script
	// There must be no more input selection or fee/RBF mutation after this.
	return tx, prev, nil
}

func (p *Manager) SendSilentPayment(address string, amount, feeRate int64) (string, error) {
	tx, prev, err := p.BuildSilentPayment(address, amount, feeRate)
	if err != nil {
		return "", err
	}
	signed, err := p.SignTx(tx, prev)
	if err != nil {
		return "", err
	}
	return p.BroadcastTx(signed)
}

// Verify a Bitcoin TxID returned by the Indexer against the corresponding
// original wire transaction, not a normalized or arbitrary hash string.
func SilentPayVerifyTxID(txid string, tx *wire.MsgTx) error {
	if tx == nil {
		return fmt.Errorf("silentpay: missing raw transaction")
	}
	expected, err := chainhash.NewHashFromStr(txid)
	if err != nil || tx.TxHash() != *expected {
		return fmt.Errorf("silentpay: transaction identity mismatch")
	}
	return nil
}
