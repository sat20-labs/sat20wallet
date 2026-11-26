package wallet

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	wwire "github.com/sat20-labs/sat20wallet/sdk/wire"
	dkvs "github.com/sat20-labs/satoshinet/indexer/indexer/dkvs"
	swire "github.com/sat20-labs/satoshinet/wire"
)

// Mailbox transport IDs are not derived from the transaction. Use the
// same authenticated sending account to commit its immutable delivery archive
// to the Bitcoin transaction and to each numbered output, without decryption.
func rgb11WithdrawalMailDigest(network, contractURL, txid string, mail [][]byte) ([32]byte, error) {
	var zero [32]byte
	id, err := chainhash.NewHashFromStr(txid)
	if err != nil || id.String() != txid || network == "" || contractURL == "" || len(mail) == 0 || len(mail) > 9 {
		return zero, fmt.Errorf("invalid RGB11 withdrawal mail context")
	}
	var payload bytes.Buffer
	payload.WriteString("sat20-rgb11-withdrawal-mail-v1")
	// The variable fields are independently length-prefixed.
	if err := wire.WriteVarString(&payload, 0, network); err != nil {
		return zero, err
	}
	if err := wire.WriteVarString(&payload, 0, contractURL); err != nil {
		return zero, err
	}
	payload.Write(id[:])
	payload.WriteByte(byte(len(mail)))
	for i, raw := range mail {
		payload.Write(binary.BigEndian.AppendUint32(nil, uint32(i+1)))
		hash := sha256.Sum256(raw)
		payload.Write(hash[:])
	}
	return sha256.Sum256(payload.Bytes()), nil
}

func signRGB11WithdrawalMail(sender common.Wallet, contractURL, txid string, mail [][]byte) ([]byte, error) {
	signer, ok := sender.(dkvsAccountSchnorrSigner)
	if !ok {
		return nil, fmt.Errorf("RGB11 withdrawal sender cannot sign mail binding")
	}
	senderID, err := dkvsAccountID(sender)
	if err != nil {
		return nil, err
	}
	for _, raw := range mail {
		message, err := swire.DeserializeDirectMessage(raw)
		if err != nil || message.SenderAccount != senderID {
			return nil, fmt.Errorf("RGB11 withdrawal mail was not sent by its archiving account")
		}
		if err := verifyDirectSignature(message); err != nil {
			return nil, err
		}
	}
	digest, err := rgb11WithdrawalMailDigest(GetChainParam().Name, contractURL, txid, mail)
	if err != nil {
		return nil, err
	}
	return signer.SignSchnorrMessage(digest[:])
}

// The caller supplies the already authenticated Core key. Neither the
// attestation nor its enclosed Direct sender can select the trusted identity.
func checkRGB11WithdrawalMail(contractURL string, bitcoin *wire.MsgTx, proof *wwire.RGB11SigningProof,
	core *btcec.PublicKey) ([]string, error) {
	if bitcoin == nil || proof == nil || len(bitcoin.TxOut) <= len(proof.RecipientMail) {
		return nil, fmt.Errorf("incomplete RGB11 withdrawal recipient mail")
	}
	digest, err := rgb11WithdrawalMailDigest(GetChainParam().Name, contractURL, bitcoin.TxID(), proof.RecipientMail)
	if err != nil {
		return nil, err
	}
	sig, err := schnorr.ParseSignature(proof.RecipientMailSignature)
	if err != nil {
		return nil, fmt.Errorf("invalid RGB11 withdrawal mail binding signature")
	}
	if core == nil || !sig.Verify(digest[:], core) {
		return nil, fmt.Errorf("RGB11 withdrawal mail does not bind the authorized outputs")
	}
	senderID := hex.EncodeToString(schnorr.SerializePubKey(core))
	seen := make(map[string]bool, len(proof.RecipientMail))
	recipients := make([]string, 0, len(proof.RecipientMail))
	for i, raw := range proof.RecipientMail {
		message, err := swire.DeserializeDirectMessage(raw)
		if err != nil {
			return nil, err
		}
		if message.SenderAccount != senderID {
			return nil, fmt.Errorf("RGB11 withdrawal mail has a different sender")
		}
		if err := verifyDirectSignature(message); err != nil {
			return nil, err
		}
		pub, err := dkvs.AccountPubKey(message.RecipientAccount)
		if err != nil {
			return nil, err
		}
		address := HexPubKeyToP2TRAddress(pub)
		script, err := AddrToPkScript(address, GetChainParam())
		output := bitcoin.TxOut[i+1]
		if err != nil || output == nil || !bytes.Equal(script, output.PkScript) || seen[address] {
			return nil, fmt.Errorf("RGB withdrawal mail does not match recipient at output %d", i+1)
		}
		seen[address] = true
		recipients = append(recipients, address)
	}
	return recipients, nil
}
