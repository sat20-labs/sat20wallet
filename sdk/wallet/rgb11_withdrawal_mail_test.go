package wallet

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/wire"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
	swire "github.com/sat20-labs/satoshinet/wire"
	"github.com/stretchr/testify/require"
)

type withdrawalMailTestWallet struct {
	common.Wallet
	key *btcec.PrivateKey
}

func (w withdrawalMailTestWallet) GetPubKey() *btcec.PublicKey { return w.key.PubKey() }
func (w withdrawalMailTestWallet) SignSchnorrMessage(digest []byte) ([]byte, error) {
	sig, err := schnorr.Sign(w.key, digest)
	if err != nil {
		return nil, err
	}
	return sig.Serialize(), nil
}

func TestRGB11WithdrawalMailBindsExactArchive(t *testing.T) {
	from, sender := btcec.PrivKeyFromBytes(bytes.Repeat([]byte{31}, 32))
	_, recipient := btcec.PrivKeyFromBytes(bytes.Repeat([]byte{32}, 32))
	_, other := btcec.PrivKeyFromBytes(bytes.Repeat([]byte{33}, 32))
	address := HexPubKeyToP2TRAddress(recipient.SerializeCompressed())
	script, err := AddrToPkScript(address, GetChainParam())
	require.NoError(t, err)
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(&wire.OutPoint{}, nil, nil))
	tx.AddTxOut(wire.NewTxOut(0, []byte{0x6a}))
	tx.AddTxOut(wire.NewTxOut(330, script))

	msg := &swire.DirectMessage{
		SenderAccount: hex.EncodeToString(schnorr.SerializePubKey(sender)), SenderMsgID: 1,
		MessageID: strings.Repeat("01", 16), RecipientAccount: hex.EncodeToString(schnorr.SerializePubKey(recipient)),
		Ciphertext: []byte("dummy ciphertext"),
	}
	direct, err := swire.DirectMessageSigningHash(msg)
	require.NoError(t, err)
	local := withdrawalMailTestWallet{key: from}
	msg.SenderSignature, err = local.SignSchnorrMessage(direct[:])
	require.NoError(t, err)
	raw, err := swire.SerializeDirectMessage(msg, true)
	require.NoError(t, err)
	mail := [][]byte{raw}
	binding, err := signRGB11WithdrawalMail(local, "test-contract", tx.TxID(), mail)
	require.NoError(t, err)
	proof := &wwire.RGB11SigningProof{RecipientMail: mail, RecipientMailSignature: binding}

	got, err := checkRGB11WithdrawalMail("test-contract", tx, proof, sender)
	require.NoError(t, err)
	require.Equal(t, []string{address}, got)

	// The archive must not be reusable for another withdrawal, even if the
	// Bitcoin recipient and all the encrypted mail bytes are unchanged.
	copy := tx.Copy()
	copy.LockTime++
	_, err = checkRGB11WithdrawalMail("test-contract", copy, proof, sender)
	require.ErrorContains(t, err, "does not bind")
	_, err = checkRGB11WithdrawalMail("another-contract", tx, proof, sender)
	require.ErrorContains(t, err, "does not bind")
	_, err = checkRGB11WithdrawalMail("test-contract", tx, proof, other)
	require.ErrorContains(t, err, "does not bind")
}
