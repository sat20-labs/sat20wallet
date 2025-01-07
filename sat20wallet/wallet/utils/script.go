package utils

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/ecdsa"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/btcec/v2/schnorr/musig2"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"golang.org/x/crypto/ripemd160"
)

var (
	// TODO(roasbeef): remove these and use the one's defined in txscript
	// within testnet-L.

	// SequenceLockTimeSeconds is the 22nd bit which indicates the lock
	// time is in seconds.
	SequenceLockTimeSeconds = uint32(1 << 22)
)

// mustParsePubKey parses a hex encoded public key string into a public key and
// panic if parsing fails.
func mustParsePubKey(pubStr string) btcec.PublicKey {
	pubBytes, err := hex.DecodeString(pubStr)
	if err != nil {
		panic(err)
	}

	pub, err := btcec.ParsePubKey(pubBytes)
	if err != nil {
		panic(err)
	}

	return *pub
}

// TaprootNUMSHex is the hex encoded version of the taproot NUMs key.
const TaprootNUMSHex = "02dca094751109d0bd055d03565874e8276dd53e926b44e3bd1bb" +
	"6bf4bc130a279"

var (
	// TaprootNUMSKey is a NUMS key (nothing up my sleeves number) that has
	// no known private key. This was generated using the following script:
	// https://github.com/lightninglabs/lightning-node-connect/tree/
	// master/mailbox/numsgen, with the seed phrase "Lightning Simple
	// Taproot".
	TaprootNUMSKey = mustParsePubKey(TaprootNUMSHex)
)

// Signature is an interface for objects that can populate signatures during
// witness construction.
type Signature interface {
	// Serialize returns a DER-encoded ECDSA signature.
	Serialize() []byte

	// Verify return true if the ECDSA signature is valid for the passed
	// message digest under the provided public key.
	Verify([]byte, *btcec.PublicKey) bool
}

// ParseSignature parses a raw signature into an input.Signature instance. This
// routine supports parsing normal ECDSA DER encoded signatures, as well as
// schnorr signatures.
func ParseSignature(rawSig []byte) (Signature, error) {
	if len(rawSig) == schnorr.SignatureSize {
		return schnorr.ParseSignature(rawSig)
	}

	return ecdsa.ParseDERSignature(rawSig)
}

// WitnessScriptHash generates a pay-to-witness-script-hash public key script
// paying to a version 0 witness program paying to the passed redeem script.
func WitnessScriptHash(witnessScript []byte) ([]byte, error) {
	bldr := txscript.NewScriptBuilder(
		txscript.WithScriptAllocSize(P2WSHSize),
	)

	bldr.AddOp(txscript.OP_0)
	scriptHash := sha256.Sum256(witnessScript)
	bldr.AddData(scriptHash[:])
	return bldr.Script()
}

// WitnessPubKeyHash generates a pay-to-witness-pubkey-hash public key script
// paying to a version 0 witness program containing the passed serialized
// public key.
func WitnessPubKeyHash(pubkey []byte) ([]byte, error) {
	bldr := txscript.NewScriptBuilder(
		txscript.WithScriptAllocSize(P2WPKHSize),
	)

	bldr.AddOp(txscript.OP_0)
	pkhash := btcutil.Hash160(pubkey)
	bldr.AddData(pkhash)
	return bldr.Script()
}

// GenerateP2SH generates a pay-to-script-hash public key script paying to the
// passed redeem script.
func GenerateP2SH(script []byte) ([]byte, error) {
	bldr := txscript.NewScriptBuilder(
		txscript.WithScriptAllocSize(NestedP2WPKHSize),
	)

	bldr.AddOp(txscript.OP_HASH160)
	scripthash := btcutil.Hash160(script)
	bldr.AddData(scripthash)
	bldr.AddOp(txscript.OP_EQUAL)
	return bldr.Script()
}

// GenerateP2PKH generates a pay-to-public-key-hash public key script paying to
// the passed serialized public key.
func GenerateP2PKH(pubkey []byte) ([]byte, error) {
	bldr := txscript.NewScriptBuilder(
		txscript.WithScriptAllocSize(P2PKHSize),
	)

	bldr.AddOp(txscript.OP_DUP)
	bldr.AddOp(txscript.OP_HASH160)
	pkhash := btcutil.Hash160(pubkey)
	bldr.AddData(pkhash)
	bldr.AddOp(txscript.OP_EQUALVERIFY)
	bldr.AddOp(txscript.OP_CHECKSIG)
	return bldr.Script()
}

// GenerateUnknownWitness generates the maximum-sized witness public key script
// consisting of a version push and a 40-byte data push.
func GenerateUnknownWitness() ([]byte, error) {
	bldr := txscript.NewScriptBuilder()

	bldr.AddOp(txscript.OP_0)
	witnessScript := make([]byte, 40)
	bldr.AddData(witnessScript)
	return bldr.Script()
}

// GenMultiSigScript generates the non-p2sh'd multisig script for 2 of 2
// pubkeys.
func GenMultiSigScript(aPub, bPub []byte) ([]byte, error) {
	if len(aPub) != 33 || len(bPub) != 33 {
		return nil, fmt.Errorf("pubkey size error: compressed " +
			"pubkeys only")
	}

	// Swap to sort pubkeys if needed. Keys are sorted in lexicographical
	// order. The signatures within the scriptSig must also adhere to the
	// order, ensuring that the signatures for each public key appears in
	// the proper order on the stack.
	if bytes.Compare(aPub, bPub) == 1 {
		aPub, bPub = bPub, aPub
	}

	bldr := txscript.NewScriptBuilder(txscript.WithScriptAllocSize(
		MultiSigSize,
	))
	bldr.AddOp(txscript.OP_2)
	bldr.AddData(aPub) // Add both pubkeys (sorted).
	bldr.AddData(bPub)
	bldr.AddOp(txscript.OP_2)
	bldr.AddOp(txscript.OP_CHECKMULTISIG)
	return bldr.Script()
}

// GenFundingPkScript creates a redeem script, and its matching p2wsh
// output for the funding transaction.
func GenFundingPkScript(aPub, bPub []byte, amt int64) ([]byte, *wire.TxOut, error) {
	// As a sanity check, ensure that the passed amount is above zero.
	if amt <= 0 {
		return nil, nil, fmt.Errorf("can't create FundTx script with " +
			"zero, or negative coins")
	}

	// First, create the 2-of-2 multi-sig script itself.
	witnessScript, err := GenMultiSigScript(aPub, bPub)
	if err != nil {
		return nil, nil, err
	}

	// With the 2-of-2 script in had, generate a p2wsh script which pays
	// to the funding script.
	pkScript, err := WitnessScriptHash(witnessScript)
	if err != nil {
		return nil, nil, err
	}

	return witnessScript, wire.NewTxOut(amt, pkScript), nil
}

// GenTaprootFundingScript constructs the taproot-native funding output that
// uses musig2 to create a single aggregated key to anchor the channel.
func GenTaprootFundingScript(aPub, bPub *btcec.PublicKey,
	amt int64) ([]byte, *wire.TxOut, error) {

	// Similar to the existing p2wsh funding script, we'll always make sure
	// we sort the keys before any major operations. In order to ensure
	// that there's no other way this output can be spent, we'll use a BIP
	// 86 tweak here during aggregation.
	//
	// TODO(roasbeef): revisit if BIP 86 is needed here?
	combinedKey, _, _, err := musig2.AggregateKeys(
		[]*btcec.PublicKey{aPub, bPub}, true,
		musig2.WithBIP86KeyTweak(),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("unable to combine keys: %w", err)
	}

	// Now that we have the combined key, we can create a taproot pkScript
	// from this, and then make the txout given the amount.
	pkScript, err := PayToTaprootScript(combinedKey.FinalKey)
	if err != nil {
		return nil, nil, fmt.Errorf("unable to make taproot "+
			"pkscript: %w", err)
	}

	txOut := wire.NewTxOut(amt, pkScript)

	// For the "witness program" we just return the raw pkScript since the
	// output we create can _only_ be spent with a musig2 signature.
	return pkScript, txOut, nil
}

// SpendMultiSig generates the witness stack required to redeem the 2-of-2 p2wsh
// multi-sig output.
func SpendMultiSig(witnessScript, pubA []byte, sigA Signature,
	pubB []byte, sigB Signature) [][]byte {

	witness := make([][]byte, 4)

	// When spending a p2wsh multi-sig script, rather than an OP_0, we add
	// a nil stack element to eat the extra pop.
	witness[0] = nil

	// When initially generating the witnessScript, we sorted the serialized
	// public keys in descending order. So we do a quick comparison in order
	// ensure the signatures appear on the Script Virtual Machine stack in
	// the correct order.
	if bytes.Compare(pubA, pubB) == 1 {
		witness[1] = append(sigB.Serialize(), byte(txscript.SigHashAll))
		witness[2] = append(sigA.Serialize(), byte(txscript.SigHashAll))
	} else {
		witness[1] = append(sigA.Serialize(), byte(txscript.SigHashAll))
		witness[2] = append(sigB.Serialize(), byte(txscript.SigHashAll))
	}

	// Finally, add the preimage as the last witness element.
	witness[3] = witnessScript

	return witness
}

// FindScriptOutputIndex finds the index of the public key script output
// matching 'script'. Additionally, a boolean is returned indicating if a
// matching output was found at all.
//
// NOTE: The search stops after the first matching script is found.
func FindScriptOutputIndex(tx *wire.MsgTx, script []byte) (bool, uint32) {
	found := false
	index := uint32(0)
	for i, txOut := range tx.TxOut {
		if bytes.Equal(txOut.PkScript, script) {
			found = true
			index = uint32(i)
			break
		}
	}

	return found, index
}

// Ripemd160H calculates the ripemd160 of the passed byte slice. This is used to
// calculate the intermediate hash for payment pre-images. Payment hashes are
// the result of ripemd160(sha256(paymentPreimage)). As a result, the value
// passed in should be the sha256 of the payment hash.
func Ripemd160H(d []byte) []byte {
	h := ripemd160.New()
	h.Write(d)
	return h.Sum(nil)
}

// SenderHTLCScript constructs the public key script for an outgoing HTLC
// output payment for the sender's version of the commitment transaction. The
// possible script paths from this output include:
//
//   - The sender timing out the HTLC using the second level HTLC timeout
//     transaction.
//   - The receiver of the HTLC claiming the output on-chain with the payment
//     preimage.
//   - The receiver of the HTLC sweeping all the funds in the case that a
//     revoked commitment transaction bearing this HTLC was broadcast.
//
// If confirmedSpend=true, a 1 OP_CSV check will be added to the non-revocation
// cases, to allow sweeping only after confirmation.
//
// Possible Input Scripts:
//
//	SENDR: <0> <sendr sig>  <recvr sig> <0> (spend using HTLC timeout transaction)
//	RECVR: <recvr sig>  <preimage>
//	REVOK: <revoke sig> <revoke key>
//	 * receiver revoke
//
// Offered HTLC Output Script:
//
//	 OP_DUP OP_HASH160 <revocation key hash160> OP_EQUAL
//	 OP_IF
//		OP_CHECKSIG
//	 OP_ELSE
//		<recv htlc key>
//		OP_SWAP OP_SIZE 32 OP_EQUAL
//		OP_NOTIF
//		    OP_DROP 2 OP_SWAP <sender htlc key> 2 OP_CHECKMULTISIG
//		OP_ELSE
//		    OP_HASH160 <ripemd160(payment hash)> OP_EQUALVERIFY
//		    OP_CHECKSIG
//		OP_ENDIF
//		[1 OP_CHECKSEQUENCEVERIFY OP_DROP] <- if allowing confirmed
//		spend only.
//	 OP_ENDIF
func SenderHTLCScript(senderHtlcKey, receiverHtlcKey,
	revocationKey *btcec.PublicKey, paymentHash []byte,
	confirmedSpend bool) ([]byte, error) {

	builder := txscript.NewScriptBuilder(txscript.WithScriptAllocSize(
		OfferedHtlcScriptSizeConfirmed,
	))

	// The opening operations are used to determine if this is the receiver
	// of the HTLC attempting to sweep all the funds due to a contract
	// breach. In this case, they'll place the revocation key at the top of
	// the stack.
	builder.AddOp(txscript.OP_DUP)
	builder.AddOp(txscript.OP_HASH160)
	builder.AddData(btcutil.Hash160(revocationKey.SerializeCompressed()))
	builder.AddOp(txscript.OP_EQUAL)

	// If the hash matches, then this is the revocation clause. The output
	// can be spent if the check sig operation passes.
	builder.AddOp(txscript.OP_IF)
	builder.AddOp(txscript.OP_CHECKSIG)

	// Otherwise, this may either be the receiver of the HTLC claiming with
	// the pre-image, or the sender of the HTLC sweeping the output after
	// it has timed out.
	builder.AddOp(txscript.OP_ELSE)

	// We'll do a bit of set up by pushing the receiver's key on the top of
	// the stack. This will be needed later if we decide that this is the
	// sender activating the time out clause with the HTLC timeout
	// transaction.
	builder.AddData(receiverHtlcKey.SerializeCompressed())

	// Atm, the top item of the stack is the receiverKey's so we use a swap
	// to expose what is either the payment pre-image or a signature.
	builder.AddOp(txscript.OP_SWAP)

	// With the top item swapped, check if it's 32 bytes. If so, then this
	// *may* be the payment pre-image.
	builder.AddOp(txscript.OP_SIZE)
	builder.AddInt64(32)
	builder.AddOp(txscript.OP_EQUAL)

	// If it isn't then this might be the sender of the HTLC activating the
	// time out clause.
	builder.AddOp(txscript.OP_NOTIF)

	// We'll drop the OP_IF return value off the top of the stack so we can
	// reconstruct the multi-sig script used as an off-chain covenant. If
	// two valid signatures are provided, then the output will be deemed as
	// spendable.
	builder.AddOp(txscript.OP_DROP)
	builder.AddOp(txscript.OP_2)
	builder.AddOp(txscript.OP_SWAP)
	builder.AddData(senderHtlcKey.SerializeCompressed())
	builder.AddOp(txscript.OP_2)
	builder.AddOp(txscript.OP_CHECKMULTISIG)

	// Otherwise, then the only other case is that this is the receiver of
	// the HTLC sweeping it on-chain with the payment pre-image.
	builder.AddOp(txscript.OP_ELSE)

	// Hash the top item of the stack and compare it with the hash160 of
	// the payment hash, which is already the sha256 of the payment
	// pre-image. By using this little trick we're able to save space
	// on-chain as the witness includes a 20-byte hash rather than a
	// 32-byte hash.
	builder.AddOp(txscript.OP_HASH160)
	builder.AddData(Ripemd160H(paymentHash))
	builder.AddOp(txscript.OP_EQUALVERIFY)

	// This checks the receiver's signature so that a third party with
	// knowledge of the payment preimage still cannot steal the output.
	builder.AddOp(txscript.OP_CHECKSIG)

	// Close out the OP_IF statement above.
	builder.AddOp(txscript.OP_ENDIF)

	// Add 1 block CSV delay if a confirmation is required for the
	// non-revocation clauses.
	if confirmedSpend {
		builder.AddOp(txscript.OP_1)
		builder.AddOp(txscript.OP_CHECKSEQUENCEVERIFY)
		builder.AddOp(txscript.OP_DROP)
	}

	// Close out the OP_IF statement at the top of the script.
	builder.AddOp(txscript.OP_ENDIF)

	return builder.Script()
}


// LockTimeToSequence converts the passed relative locktime to a sequence
// number in accordance to BIP-68.
// See: https://github.com/bitcoin/bips/blob/master/bip-0068.mediawiki
//   - (Compatibility)
func LockTimeToSequence(isSeconds bool, locktime uint32) uint32 {
	if !isSeconds {
		// The locktime is to be expressed in confirmations.
		return locktime
	}

	// Set the 22nd bit which indicates the lock time is in seconds, then
	// shift the locktime over by 9 since the time granularity is in
	// 512-second intervals (2^9). This results in a max lock-time of
	// 33,554,431 seconds, or 1.06 years.
	return SequenceLockTimeSeconds | (locktime >> 9)
}

// CommitScriptToSelf constructs the public key script for the output on the
// commitment transaction paying to the "owner" of said commitment transaction.
// If the other party learns of the preimage to the revocation hash, then they
// can claim all the settled funds in the channel, plus the unsettled funds.
//
// Possible Input Scripts:
//
//	REVOKE:     <sig> 1
//	SENDRSWEEP: <sig> <emptyvector>
//
// Output Script:
//
//	OP_IF
//	    <revokeKey>
//	OP_ELSE
//	    <numRelativeBlocks> OP_CHECKSEQUENCEVERIFY OP_DROP
//	    <selfKey>
//	OP_ENDIF
//	OP_CHECKSIG
func CommitScriptToSelf(csvTimeout uint32, selfKey, revokeKey *btcec.PublicKey) ([]byte, error) {
	// This script is spendable under two conditions: either the
	// 'csvTimeout' has passed and we can redeem our funds, or they can
	// produce a valid signature with the revocation public key. The
	// revocation public key will *only* be known to the other party if we
	// have divulged the revocation hash, allowing them to homomorphically
	// derive the proper private key which corresponds to the revoke public
	// key.
	builder := txscript.NewScriptBuilder(txscript.WithScriptAllocSize(
		ToLocalScriptSize,
	))

	builder.AddOp(txscript.OP_IF)

	// If a valid signature using the revocation key is presented, then
	// allow an immediate spend provided the proper signature.
	builder.AddData(revokeKey.SerializeCompressed())

	builder.AddOp(txscript.OP_ELSE)

	// Otherwise, we can re-claim our funds after a CSV delay of
	// 'csvTimeout' timeout blocks, and a valid signature.
	builder.AddInt64(int64(csvTimeout))
	builder.AddOp(txscript.OP_CHECKSEQUENCEVERIFY)
	builder.AddOp(txscript.OP_DROP)
	builder.AddData(selfKey.SerializeCompressed())

	builder.AddOp(txscript.OP_ENDIF)

	// Finally, we'll validate the signature against the public key that's
	// left on the top of the stack.
	builder.AddOp(txscript.OP_CHECKSIG)

	return builder.Script()
}

func CommitScriptToSelf2(csvTimeout uint32, selfKey, bootstrapKey, revokeKey *btcec.PublicKey) ([]byte, error) {
	// This script is spendable under two conditions: either the
	// 'csvTimeout' has passed and we can redeem our funds, or they can
	// produce a valid signature with the revocation public key. The
	// revocation public key will *only* be known to the other party if we
	// have divulged the revocation hash, allowing them to homomorphically
	// derive the proper private key which corresponds to the revoke public
	// key.
	aPub := selfKey.SerializeCompressed()
	bPub := bootstrapKey.SerializeCompressed()
	if bytes.Compare(aPub, bPub) == 1 {
		aPub, bPub = bPub, aPub
	}

	builder := txscript.NewScriptBuilder(txscript.WithScriptAllocSize(
		ToLocalScriptSize,
	))

	builder.AddOp(txscript.OP_IF)

	// If a valid signature using the revocation key is presented, then
	// allow an immediate spend provided the proper signature.
	builder.AddData(revokeKey.SerializeCompressed())
	builder.AddOp(txscript.OP_CHECKSIG)
	

	builder.AddOp(txscript.OP_ELSE)

	// Otherwise, we can re-claim our funds after a CSV delay of
	// 'csvTimeout' timeout blocks, and a valid signature.
	builder.AddInt64(int64(csvTimeout))
	builder.AddOp(txscript.OP_CHECKSEQUENCEVERIFY)
	builder.AddOp(txscript.OP_DROP)

	// Add the 2-of-2 multi-signature pubkeys.
	builder.AddInt64(2) // Number of required signatures
	builder.AddData(aPub) // First key
	builder.AddData(bPub) // Second key
	builder.AddInt64(2) // Total number of keys
	builder.AddOp(txscript.OP_CHECKMULTISIG)

	builder.AddOp(txscript.OP_ENDIF)

	

	return builder.Script()
}


// CommitScriptTree holds the taproot output key (in this case the revocation
// key, or a NUMs point for the remote output) along with the tapscript leaf
// that can spend the output after a delay.
type CommitScriptTree struct {
	ScriptTree

	// SettleLeaf is the leaf used to settle the output after the delay.
	SettleLeaf txscript.TapLeaf

	// RevocationLeaf is the leaf used to spend the output with the
	// revocation key signature.
	RevocationLeaf txscript.TapLeaf
}

// A compile time check to ensure CommitScriptTree implements the
// TapscriptDescriptor interface.
var _ TapscriptDescriptor = (*CommitScriptTree)(nil)

// WitnessScript returns the witness script that we'll use when signing for the
// remote party, and also verifying signatures on our transactions. As an
// example, when we create an outgoing HTLC for the remote party, we want to
// sign their success path.
func (c *CommitScriptTree) WitnessScriptToSign() []byte {
	// TODO(roasbeef): abstraction leak here? always dependent
	return nil
}

// WitnessScriptForPath returns the witness script for the given spending path.
// An error is returned if the path is unknown.
func (c *CommitScriptTree) WitnessScriptForPath(path ScriptPath,
) ([]byte, error) {

	switch path {
	// For the commitment output, the delay and success path are the same,
	// so we'll fall through here to success.
	case ScriptPathDelay:
		fallthrough
	case ScriptPathSuccess:
		return c.SettleLeaf.Script, nil
	case ScriptPathRevocation:
		return c.RevocationLeaf.Script, nil
	default:
		return nil, fmt.Errorf("unknown script path: %v", path)
	}
}

// CtrlBlockForPath returns the control block for the given spending path. For
// script types that don't have a control block, nil is returned.
func (c *CommitScriptTree) CtrlBlockForPath(path ScriptPath,
) (*txscript.ControlBlock, error) {

	switch path {
	case ScriptPathDelay:
		fallthrough
	case ScriptPathSuccess:
		return MakeTaprootCtrlBlock(
			c.SettleLeaf.Script, c.InternalKey,
			c.TapscriptTree,
		), nil
	case ScriptPathRevocation:
		return MakeTaprootCtrlBlock(
			c.RevocationLeaf.Script, c.InternalKey,
			c.TapscriptTree,
		), nil
	default:
		return nil, fmt.Errorf("unknown script path: %v", path)
	}
}

// NewLocalCommitScriptTree returns a new CommitScript tree that can be used to
// create and spend the commitment output for the local party.
func NewLocalCommitScriptTree(csvTimeout uint32,
	selfKey, revokeKey *btcec.PublicKey) (*CommitScriptTree, error) {

	// First, we'll need to construct the tapLeaf that'll be our delay CSV
	// clause.
	delayScript, err := TaprootLocalCommitDelayScript(csvTimeout, selfKey)
	if err != nil {
		return nil, err
	}

	// Next, we'll need to construct the revocation path, which is just a
	// simple checksig script.
	revokeScript, err := TaprootLocalCommitRevokeScript(selfKey, revokeKey)
	if err != nil {
		return nil, err
	}

	// With both scripts computed, we'll now create a tapscript tree with
	// the two leaves, and then obtain a root from that.
	delayTapLeaf := txscript.NewBaseTapLeaf(delayScript)
	revokeTapLeaf := txscript.NewBaseTapLeaf(revokeScript)
	tapScriptTree := txscript.AssembleTaprootScriptTree(
		delayTapLeaf, revokeTapLeaf,
	)
	tapScriptRoot := tapScriptTree.RootNode.TapHash()

	// Now that we have our root, we can arrive at the final output script
	// by tweaking the internal key with this root.
	toLocalOutputKey := txscript.ComputeTaprootOutputKey(
		&TaprootNUMSKey, tapScriptRoot[:],
	)

	return &CommitScriptTree{
		ScriptTree: ScriptTree{
			TaprootKey:    toLocalOutputKey,
			TapscriptTree: tapScriptTree,
			TapscriptRoot: tapScriptRoot[:],
			InternalKey:   &TaprootNUMSKey,
		},
		SettleLeaf:     delayTapLeaf,
		RevocationLeaf: revokeTapLeaf,
	}, nil
}

// TaprootLocalCommitDelayScript builds the tap leaf with the CSV delay script
// for the to-local output.
func TaprootLocalCommitDelayScript(csvTimeout uint32,
	selfKey *btcec.PublicKey) ([]byte, error) {

	builder := txscript.NewScriptBuilder()
	builder.AddData(schnorr.SerializePubKey(selfKey))
	builder.AddOp(txscript.OP_CHECKSIG)
	builder.AddInt64(int64(csvTimeout))
	builder.AddOp(txscript.OP_CHECKSEQUENCEVERIFY)
	builder.AddOp(txscript.OP_DROP)

	return builder.Script()
}

// TaprootLocalCommitRevokeScript builds the tap leaf with the revocation path
// for the to-local output.
func TaprootLocalCommitRevokeScript(selfKey, revokeKey *btcec.PublicKey) (
	[]byte, error) {

	builder := txscript.NewScriptBuilder()
	builder.AddData(schnorr.SerializePubKey(selfKey))
	builder.AddOp(txscript.OP_DROP)
	builder.AddData(schnorr.SerializePubKey(revokeKey))
	builder.AddOp(txscript.OP_CHECKSIG)

	return builder.Script()
}

// TaprootCommitScriptToSelf creates the taproot witness program that commits
// to the revocation (script path) and delay path (script path) in a single
// taproot output key. Both the delay script and the revocation script are part
// of the tapscript tree to ensure that the internal key (the local delay key)
// is always revealed.  This ensures that a 3rd party can always sweep the set
// of anchor outputs.
//
// For the delay path we have the following tapscript leaf script:
//
//	<local_delayedpubkey> OP_CHECKSIG
//	<to_self_delay> OP_CHECKSEQUENCEVERIFY OP_DROP
//
// This can then be spent with just:
//
//	<local_delayedsig> <to_delay_script> <delay_control_block>
//
// Where the to_delay_script is listed above, and the delay_control_block
// computed as:
//
//	delay_control_block = (output_key_y_parity | 0xc0) || taproot_nums_key
//
// The revocation path is simply:
//
//	<local_delayedpubkey> OP_DROP
//	<revocationkey> OP_CHECKSIG
//
// The revocation path can be spent with a control block similar to the above
// (but contains the hash of the other script), and with the following witness:
//
//	<revocation_sig>
//
// We use a noop data push to ensure that the local public key is also revealed
// on chain, which enables the anchor output to be swept.
func TaprootCommitScriptToSelf(csvTimeout uint32,
	selfKey, revokeKey *btcec.PublicKey) (*btcec.PublicKey, error) {

	commitScriptTree, err := NewLocalCommitScriptTree(
		csvTimeout, selfKey, revokeKey,
	)
	if err != nil {
		return nil, err
	}

	return commitScriptTree.TaprootKey, nil
}

// MakeTaprootSCtrlBlock takes a leaf script, the internal key (usually the
// revoke key), and a script tree and creates a valid control block for a spend
// of the leaf.
func MakeTaprootCtrlBlock(leafScript []byte, internalKey *btcec.PublicKey,
	scriptTree *txscript.IndexedTapScriptTree) *txscript.ControlBlock {

	tapLeafHash := txscript.NewBaseTapLeaf(leafScript).TapHash()
	scriptIdx := scriptTree.LeafProofIndex[tapLeafHash]
	settleMerkleProof := scriptTree.LeafMerkleProofs[scriptIdx]

	cb := settleMerkleProof.ToControlBlock(internalKey)
	return &cb
}

func maybeAppendSighash(sig Signature, sigHash txscript.SigHashType) []byte {
	sigBytes := sig.Serialize()
	if sigHash == txscript.SigHashDefault {
		return sigBytes
	}

	return append(sigBytes, byte(sigHash))
}

// // TaprootCommitSpendSuccess constructs a valid witness allowing a node to
// // sweep the settled taproot output after the delay has passed for a force
// // close.
// func TaprootCommitSpendSuccess(signer Signer, signDesc *SignDescriptor,
// 	sweepTx *wire.MsgTx,
// 	scriptTree *txscript.IndexedTapScriptTree) (wire.TxWitness, error) {

// 	// First, we'll need to construct a valid control block to execute the
// 	// leaf script for sweep settlement.
// 	//
// 	// TODO(roasbeef); make into closure instead? only need reovke key and
// 	// scriptTree to make the ctrl block -- then default version that would
// 	// take froms ign desc?
// 	var ctrlBlockBytes []byte
// 	if signDesc.ControlBlock == nil {
// 		settleControlBlock := MakeTaprootCtrlBlock(
// 			signDesc.WitnessScript, &TaprootNUMSKey, scriptTree,
// 		)
// 		ctrlBytes, err := settleControlBlock.ToBytes()
// 		if err != nil {
// 			return nil, err
// 		}

// 		ctrlBlockBytes = ctrlBytes
// 	} else {
// 		ctrlBlockBytes = signDesc.ControlBlock
// 	}

// 	// With the control block created, we'll now generate the signature we
// 	// need to authorize the spend.
// 	sweepSig, err := signer.SignOutputRaw(sweepTx, signDesc)
// 	if err != nil {
// 		return nil, err
// 	}

// 	// The final witness stack will be:
// 	//
// 	//  <sweep sig> <sweep script> <control block>
// 	witnessStack := make(wire.TxWitness, 3)
// 	witnessStack[0] = maybeAppendSighash(sweepSig, signDesc.HashType)
// 	witnessStack[1] = signDesc.WitnessScript
// 	witnessStack[2] = ctrlBlockBytes
// 	if err != nil {
// 		return nil, err
// 	}

// 	return witnessStack, nil
// }

// // TaprootCommitSpendRevoke constructs a valid witness allowing a node to sweep
// // the revoked taproot output of a malicious peer.
// func TaprootCommitSpendRevoke(signer Signer, signDesc *SignDescriptor,
// 	revokeTx *wire.MsgTx,
// 	scriptTree *txscript.IndexedTapScriptTree) (wire.TxWitness, error) {

// 	// First, we'll need to construct a valid control block to execute the
// 	// leaf script for revocation path.
// 	var ctrlBlockBytes []byte
// 	if signDesc.ControlBlock == nil {
// 		revokeCtrlBlock := MakeTaprootCtrlBlock(
// 			signDesc.WitnessScript, &TaprootNUMSKey, scriptTree,
// 		)
// 		revokeBytes, err := revokeCtrlBlock.ToBytes()
// 		if err != nil {
// 			return nil, err
// 		}

// 		ctrlBlockBytes = revokeBytes
// 	} else {
// 		ctrlBlockBytes = signDesc.ControlBlock
// 	}

// 	// With the control block created, we'll now generate the signature we
// 	// need to authorize the spend.
// 	revokeSig, err := signer.SignOutputRaw(revokeTx, signDesc)
// 	if err != nil {
// 		return nil, err
// 	}

// 	// The final witness stack will be:
// 	//
// 	//  <revoke sig sig> <revoke script> <control block>
// 	witnessStack := make(wire.TxWitness, 3)
// 	witnessStack[0] = maybeAppendSighash(revokeSig, signDesc.HashType)
// 	witnessStack[1] = signDesc.WitnessScript
// 	witnessStack[2] = ctrlBlockBytes

// 	return witnessStack, nil
// }

// LeaseCommitScriptToSelf constructs the public key script for the output on the
// commitment transaction paying to the "owner" of said commitment transaction.
// If the other party learns of the preimage to the revocation hash, then they
// can claim all the settled funds in the channel, plus the unsettled funds.
//
// Possible Input Scripts:
//
//	REVOKE:     <sig> 1
//	SENDRSWEEP: <sig> <emptyvector>
//
// Output Script:
//
//	OP_IF
//	    <revokeKey>
//	OP_ELSE
//	    <absoluteLeaseExpiry> OP_CHECKLOCKTIMEVERIFY OP_DROP
//	    <numRelativeBlocks> OP_CHECKSEQUENCEVERIFY OP_DROP
//	    <selfKey>
//	OP_ENDIF
//	OP_CHECKSIG
func LeaseCommitScriptToSelf(selfKey, revokeKey *btcec.PublicKey,
	csvTimeout, leaseExpiry uint32) ([]byte, error) {

	// This script is spendable under two conditions: either the
	// 'csvTimeout' has passed and we can redeem our funds, or they can
	// produce a valid signature with the revocation public key. The
	// revocation public key will *only* be known to the other party if we
	// have divulged the revocation hash, allowing them to homomorphically
	// derive the proper private key which corresponds to the revoke public
	// key.
	builder := txscript.NewScriptBuilder(txscript.WithScriptAllocSize(
		ToLocalScriptSize + LeaseWitnessScriptSizeOverhead,
	))

	builder.AddOp(txscript.OP_IF)

	// If a valid signature using the revocation key is presented, then
	// allow an immediate spend provided the proper signature.
	builder.AddData(revokeKey.SerializeCompressed())

	builder.AddOp(txscript.OP_ELSE)

	// Otherwise, we can re-claim our funds after once the CLTV lease
	// maturity has been met, along with the CSV delay of 'csvTimeout'
	// timeout blocks, and a valid signature.
	builder.AddInt64(int64(leaseExpiry))
	builder.AddOp(txscript.OP_CHECKLOCKTIMEVERIFY)
	builder.AddOp(txscript.OP_DROP)

	builder.AddInt64(int64(csvTimeout))
	builder.AddOp(txscript.OP_CHECKSEQUENCEVERIFY)
	builder.AddOp(txscript.OP_DROP)

	builder.AddData(selfKey.SerializeCompressed())

	builder.AddOp(txscript.OP_ENDIF)

	// Finally, we'll validate the signature against the public key that's
	// left on the top of the stack.
	builder.AddOp(txscript.OP_CHECKSIG)

	return builder.Script()
}

// CommitSpendTimeout constructs a valid witness allowing the owner of a
// particular commitment transaction to spend the output returning settled
// funds back to themselves after a relative block timeout.  In order to
// properly spend the transaction, the target input's sequence number should be
// set accordingly based off of the target relative block timeout within the
// redeem script.  Additionally, OP_CSV requires that the version of the
// transaction spending a pkscript with OP_CSV within it *must* be >= 2.
func CommitSpendTimeout(signer Signer, signDesc *SignDescriptor,
	sweepTx *wire.MsgTx) (wire.TxWitness, error) {

	// Ensure the transaction version supports the validation of sequence
	// locks and CSV semantics.
	if sweepTx.Version < 2 {
		return nil, fmt.Errorf("version of passed transaction MUST "+
			"be >= 2, not %v", sweepTx.Version)
	}

	// With the sequence number in place, we're now able to properly sign
	// off on the sweep transaction.
	sweepSig, err := signer.SignOutputRaw(sweepTx, signDesc)
	if err != nil {
		return nil, err
	}

	// Place an empty byte as the first item in the evaluated witness stack
	// to force script execution to the timeout spend clause. We need to
	// place an empty byte in order to ensure our script is still valid
	// from the PoV of nodes that are enforcing minimal OP_IF/OP_NOTIF.
	witnessStack := wire.TxWitness(make([][]byte, 3))
	witnessStack[0] = append(sweepSig.Serialize(), byte(signDesc.HashType))
	witnessStack[1] = nil
	witnessStack[2] = signDesc.WitnessScript

	return witnessStack, nil
}

// CommitSpendRevoke constructs a valid witness allowing a node to sweep the
// settled output of a malicious counterparty who broadcasts a revoked
// commitment transaction.
//
// NOTE: The passed SignDescriptor should include the raw (untweaked)
// revocation base public key of the receiver and also the proper double tweak
// value based on the commitment secret of the revoked commitment.
func CommitSpendRevoke(signer Signer, signDesc *SignDescriptor,
	sweepTx *wire.MsgTx) (wire.TxWitness, error) {

	sweepSig, err := signer.SignOutputRaw(sweepTx, signDesc)
	if err != nil {
		return nil, err
	}

	// Place a 1 as the first item in the evaluated witness stack to
	// force script execution to the revocation clause.
	witnessStack := wire.TxWitness(make([][]byte, 3))
	witnessStack[0] = append(sweepSig.Serialize(), byte(signDesc.HashType))
	witnessStack[1] = []byte{1}
	witnessStack[2] = signDesc.WitnessScript

	return witnessStack, nil
}

// CommitSpendNoDelay constructs a valid witness allowing a node to spend their
// settled no-delay output on the counterparty's commitment transaction. If the
// tweakless field is true, then we'll omit the set where we tweak the pubkey
// with a random set of bytes, and use it directly in the witness stack.
//
// NOTE: The passed SignDescriptor should include the raw (untweaked) public
// key of the receiver and also the proper single tweak value based on the
// current commitment point.
func CommitSpendNoDelay(signer Signer, signDesc *SignDescriptor,
	sweepTx *wire.MsgTx, tweakless bool) (wire.TxWitness, error) {

	if signDesc.KeyDesc.PubKey == nil {
		return nil, fmt.Errorf("cannot generate witness with nil " +
			"KeyDesc pubkey")
	}

	// This is just a regular p2wkh spend which looks something like:
	//  * witness: <sig> <pubkey>
	sweepSig, err := signer.SignOutputRaw(sweepTx, signDesc)
	if err != nil {
		return nil, err
	}

	// Finally, we'll manually craft the witness. The witness here is the
	// exact same as a regular p2wkh witness, depending on the value of the
	// tweakless bool.
	witness := make([][]byte, 2)
	witness[0] = append(sweepSig.Serialize(), byte(signDesc.HashType))

	switch tweakless {
	// If we're tweaking the key, then we use the tweaked public key as the
	// last item in the witness stack which was originally used to created
	// the pkScript we're spending.
	case false:
		witness[1] = TweakPubKeyWithTweak(
			signDesc.KeyDesc.PubKey, signDesc.SingleTweak,
		).SerializeCompressed()

	// Otherwise, we can just use the raw pubkey, since there's no random
	// value to be combined.
	case true:
		witness[1] = signDesc.KeyDesc.PubKey.SerializeCompressed()
	}

	return witness, nil
}

// CommitScriptUnencumbered constructs the public key script on the commitment
// transaction paying to the "other" party. The constructed output is a normal
// p2wkh output spendable immediately, requiring no contestation period.
func CommitScriptUnencumbered(key *btcec.PublicKey) ([]byte, error) {
	// This script goes to the "other" party, and is spendable immediately.
	builder := txscript.NewScriptBuilder(txscript.WithScriptAllocSize(
		P2WPKHSize,
	))
	builder.AddOp(txscript.OP_0)
	builder.AddData(btcutil.Hash160(key.SerializeCompressed()))

	return builder.Script()
}

// CommitScriptToRemoteConfirmed constructs the script for the output on the
// commitment transaction paying to the remote party of said commitment
// transaction. The money can only be spend after one confirmation.
//
// Possible Input Scripts:
//
//	SWEEP: <sig>
//
// Output Script:
//
//	<key> OP_CHECKSIGVERIFY
//	1 OP_CHECKSEQUENCEVERIFY
func CommitScriptToRemoteConfirmed(key *btcec.PublicKey) ([]byte, error) {
	builder := txscript.NewScriptBuilder(txscript.WithScriptAllocSize(
		ToRemoteConfirmedScriptSize,
	))

	// Only the given key can spend the output.
	builder.AddData(key.SerializeCompressed())
	builder.AddOp(txscript.OP_CHECKSIGVERIFY)

	// Check that the it has one confirmation.
	builder.AddOp(txscript.OP_1)
	builder.AddOp(txscript.OP_CHECKSEQUENCEVERIFY)

	return builder.Script()
}

// NewRemoteCommitScriptTree constructs a new script tree for the remote party
// to sweep their funds after a hard coded 1 block delay.
func NewRemoteCommitScriptTree(remoteKey *btcec.PublicKey,
) (*CommitScriptTree, error) {

	// First, construct the remote party's tapscript they'll use to sweep
	// their outputs.
	builder := txscript.NewScriptBuilder()
	builder.AddData(schnorr.SerializePubKey(remoteKey))
	builder.AddOp(txscript.OP_CHECKSIG)
	builder.AddOp(txscript.OP_1)
	builder.AddOp(txscript.OP_CHECKSEQUENCEVERIFY)
	builder.AddOp(txscript.OP_DROP)

	remoteScript, err := builder.Script()
	if err != nil {
		return nil, err
	}

	// With this script constructed, we'll map that into a tapLeaf, then
	// make a new tapscript root from that.
	tapLeaf := txscript.NewBaseTapLeaf(remoteScript)
	tapScriptTree := txscript.AssembleTaprootScriptTree(tapLeaf)
	tapScriptRoot := tapScriptTree.RootNode.TapHash()

	// Now that we have our root, we can arrive at the final output script
	// by tweaking the internal key with this root.
	toRemoteOutputKey := txscript.ComputeTaprootOutputKey(
		&TaprootNUMSKey, tapScriptRoot[:],
	)

	return &CommitScriptTree{
		ScriptTree: ScriptTree{
			TaprootKey:    toRemoteOutputKey,
			TapscriptTree: tapScriptTree,
			TapscriptRoot: tapScriptRoot[:],
			InternalKey:   &TaprootNUMSKey,
		},
		SettleLeaf: tapLeaf,
	}, nil
}

// TaprootCommitScriptToRemote constructs a taproot witness program for the
// output on the commitment transaction for the remote party. For the top level
// key spend, we'll use a NUMs key to ensure that only the script path can be
// taken. Using a set NUMs key here also means that recovery solutions can scan
// the chain given knowledge of the public key for the remote party. We then
// commit to a single tapscript leaf that holds the normal CSV 1 delay
// script.
//
// Our single tapleaf will use the following script:
//
//	<remotepubkey> OP_CHECKSIG
//	1 OP_CHECKSEQUENCEVERIFY OP_DROP
func TaprootCommitScriptToRemote(remoteKey *btcec.PublicKey,
) (*btcec.PublicKey, error) {

	commitScriptTree, err := NewRemoteCommitScriptTree(remoteKey)
	if err != nil {
		return nil, err
	}

	return commitScriptTree.TaprootKey, nil
}

// TaprootCommitRemoteSpend allows the remote party to sweep their output into
// their wallet after an enforced 1 block delay.
func TaprootCommitRemoteSpend(signer Signer, signDesc *SignDescriptor,
	sweepTx *wire.MsgTx,
	scriptTree *txscript.IndexedTapScriptTree) (wire.TxWitness, error) {

	// First, we'll need to construct a valid control block to execute the
	// leaf script for sweep settlement.
	var ctrlBlockBytes []byte
	if signDesc.ControlBlock == nil {
		settleControlBlock := MakeTaprootCtrlBlock(
			signDesc.WitnessScript, &TaprootNUMSKey, scriptTree,
		)
		ctrlBytes, err := settleControlBlock.ToBytes()
		if err != nil {
			return nil, err
		}

		ctrlBlockBytes = ctrlBytes
	} else {
		ctrlBlockBytes = signDesc.ControlBlock
	}

	// With the control block created, we'll now generate the signature we
	// need to authorize the spend.
	sweepSig, err := signer.SignOutputRaw(sweepTx, signDesc)
	if err != nil {
		return nil, err
	}

	// The final witness stack will be:
	//
	//  <sweep sig> <sweep script> <control block>
	witnessStack := make(wire.TxWitness, 3)
	witnessStack[0] = maybeAppendSighash(sweepSig, signDesc.HashType)
	witnessStack[1] = signDesc.WitnessScript
	witnessStack[2] = ctrlBlockBytes

	return witnessStack, nil
}

// LeaseCommitScriptToRemoteConfirmed constructs the script for the output on
// the commitment transaction paying to the remote party of said commitment
// transaction. The money can only be spend after one confirmation.
//
// Possible Input Scripts:
//
//	SWEEP: <sig>
//
// Output Script:
//
//		<key> OP_CHECKSIGVERIFY
//	     <lease maturity in blocks> OP_CHECKLOCKTIMEVERIFY OP_DROP
//		1 OP_CHECKSEQUENCEVERIFY
func LeaseCommitScriptToRemoteConfirmed(key *btcec.PublicKey,
	leaseExpiry uint32) ([]byte, error) {

	builder := txscript.NewScriptBuilder(txscript.WithScriptAllocSize(45))

	// Only the given key can spend the output.
	builder.AddData(key.SerializeCompressed())
	builder.AddOp(txscript.OP_CHECKSIGVERIFY)

	// The channel initiator always has the additional channel lease
	// expiration constraint for outputs that pay to them which must be
	// satisfied.
	builder.AddInt64(int64(leaseExpiry))
	builder.AddOp(txscript.OP_CHECKLOCKTIMEVERIFY)
	builder.AddOp(txscript.OP_DROP)

	// Check that it has one confirmation.
	builder.AddOp(txscript.OP_1)
	builder.AddOp(txscript.OP_CHECKSEQUENCEVERIFY)

	return builder.Script()
}

// CommitSpendToRemoteConfirmed constructs a valid witness allowing a node to
// spend their settled output on the counterparty's commitment transaction when
// it has one confirmetion. This is used for the anchor channel type. The
// spending key will always be non-tweaked for this output type.
func CommitSpendToRemoteConfirmed(signer Signer, signDesc *SignDescriptor,
	sweepTx *wire.MsgTx) (wire.TxWitness, error) {

	if signDesc.KeyDesc.PubKey == nil {
		return nil, fmt.Errorf("cannot generate witness with nil " +
			"KeyDesc pubkey")
	}

	// Similar to non delayed output, only a signature is needed.
	sweepSig, err := signer.SignOutputRaw(sweepTx, signDesc)
	if err != nil {
		return nil, err
	}

	// Finally, we'll manually craft the witness. The witness here is the
	// signature and the redeem script.
	witnessStack := make([][]byte, 2)
	witnessStack[0] = append(sweepSig.Serialize(), byte(signDesc.HashType))
	witnessStack[1] = signDesc.WitnessScript

	return witnessStack, nil
}

// CommitScriptAnchor constructs the script for the anchor output spendable by
// the given key immediately, or by anyone after 16 confirmations.
//
// Possible Input Scripts:
//
//	By owner:				<sig>
//	By anyone (after 16 conf):	<emptyvector>
//
// Output Script:
//
//	<funding_pubkey> OP_CHECKSIG OP_IFDUP
//	OP_NOTIF
//	  OP_16 OP_CSV
//	OP_ENDIF
func CommitScriptAnchor(key *btcec.PublicKey) ([]byte, error) {
	builder := txscript.NewScriptBuilder(txscript.WithScriptAllocSize(
		AnchorScriptSize,
	))

	// Spend immediately with key.
	builder.AddData(key.SerializeCompressed())
	builder.AddOp(txscript.OP_CHECKSIG)

	// Duplicate the value if true, since it will be consumed by the NOTIF.
	builder.AddOp(txscript.OP_IFDUP)

	// Otherwise spendable by anyone after 16 confirmations.
	builder.AddOp(txscript.OP_NOTIF)
	builder.AddOp(txscript.OP_16)
	builder.AddOp(txscript.OP_CHECKSEQUENCEVERIFY)
	builder.AddOp(txscript.OP_ENDIF)

	return builder.Script()
}

// AnchorScriptTree holds all the contents needed to sweep a taproot anchor
// output on chain.
type AnchorScriptTree struct {
	ScriptTree

	// SweepLeaf is the leaf used to settle the output after the delay.
	SweepLeaf txscript.TapLeaf
}

// NewAnchorScriptTree makes a new script tree for an anchor output with the
// passed anchor key.
func NewAnchorScriptTree(anchorKey *btcec.PublicKey,
) (*AnchorScriptTree, error) {

	// The main script used is just a OP_16 CSV (anyone can sweep after 16
	// blocks).
	builder := txscript.NewScriptBuilder()
	builder.AddOp(txscript.OP_16)
	builder.AddOp(txscript.OP_CHECKSEQUENCEVERIFY)

	anchorScript, err := builder.Script()
	if err != nil {
		return nil, err
	}

	// With the script, we can make our sole leaf, then derive the root
	// from that.
	tapLeaf := txscript.NewBaseTapLeaf(anchorScript)
	tapScriptTree := txscript.AssembleTaprootScriptTree(tapLeaf)
	tapScriptRoot := tapScriptTree.RootNode.TapHash()

	// Now that we have our root, we can arrive at the final output script
	// by tweaking the internal key with this root.
	anchorOutputKey := txscript.ComputeTaprootOutputKey(
		anchorKey, tapScriptRoot[:],
	)

	return &AnchorScriptTree{
		ScriptTree: ScriptTree{
			TaprootKey:    anchorOutputKey,
			TapscriptTree: tapScriptTree,
			TapscriptRoot: tapScriptRoot[:],
			InternalKey:   anchorKey,
		},
		SweepLeaf: tapLeaf,
	}, nil
}

// WitnessScript returns the witness script that we'll use when signing for the
// remote party, and also verifying signatures on our transactions. As an
// example, when we create an outgoing HTLC for the remote party, we want to
// sign their success path.
func (a *AnchorScriptTree) WitnessScriptToSign() []byte {
	return a.SweepLeaf.Script
}

// WitnessScriptForPath returns the witness script for the given spending path.
// An error is returned if the path is unknown.
func (a *AnchorScriptTree) WitnessScriptForPath(path ScriptPath,
) ([]byte, error) {

	switch path {
	case ScriptPathDelay:
		fallthrough
	case ScriptPathSuccess:
		return a.SweepLeaf.Script, nil

	default:
		return nil, fmt.Errorf("unknown script path: %v", path)
	}
}

// CtrlBlockForPath returns the control block for the given spending path. For
// script types that don't have a control block, nil is returned.
func (a *AnchorScriptTree) CtrlBlockForPath(path ScriptPath,
) (*txscript.ControlBlock, error) {

	switch path {
	case ScriptPathDelay:
		fallthrough
	case ScriptPathSuccess:
		return MakeTaprootCtrlBlock(
			a.SweepLeaf.Script, a.InternalKey,
			a.TapscriptTree,
		), nil

	default:
		return nil, fmt.Errorf("unknown script path: %v", path)
	}
}

// A compile time check to ensure AnchorScriptTree implements the
// TapscriptDescriptor interface.
var _ TapscriptDescriptor = (*AnchorScriptTree)(nil)

// TaprootOutputKeyAnchor returns the segwit v1 (taproot) witness program that
// encodes the anchor output spending conditions: the passed key can be used
// for keyspend, with the OP_CSV 16 clause living within an internal tapscript
// leaf.
//
// Spend paths:
//   - Key spend: <key_signature>
//   - Script spend: OP_16 CSV <control_block>
func TaprootOutputKeyAnchor(key *btcec.PublicKey) (*btcec.PublicKey, error) {
	anchorScriptTree, err := NewAnchorScriptTree(key)
	if err != nil {
		return nil, err
	}

	return anchorScriptTree.TaprootKey, nil
}

// TaprootAnchorSpend constructs a valid witness allowing a node to sweep their
// anchor output.
func TaprootAnchorSpend(signer Signer, signDesc *SignDescriptor,
	sweepTx *wire.MsgTx) (wire.TxWitness, error) {

	// For this spend type, we only need a single signature which'll be a
	// keyspend using the anchor private key.
	sweepSig, err := signer.SignOutputRaw(sweepTx, signDesc)
	if err != nil {
		return nil, err
	}

	// The witness stack in this case is pretty simple: we only need to
	// specify the signature generated.
	witnessStack := make(wire.TxWitness, 1)
	witnessStack[0] = maybeAppendSighash(sweepSig, signDesc.HashType)

	return witnessStack, nil
}

// TaprootAnchorSpendAny constructs a valid witness allowing anyone to sweep
// the anchor output after 16 blocks.
func TaprootAnchorSpendAny(anchorKey *btcec.PublicKey) (wire.TxWitness, error) {
	anchorScriptTree, err := NewAnchorScriptTree(anchorKey)
	if err != nil {
		return nil, err
	}

	// For this spend, the only thing we need to do is create a valid
	// control block. Other than that, there're no restrictions to how the
	// output can be spent.
	scriptTree := anchorScriptTree.TapscriptTree
	sweepLeaf := anchorScriptTree.SweepLeaf
	sweepIdx := scriptTree.LeafProofIndex[sweepLeaf.TapHash()]
	sweepMerkleProof := scriptTree.LeafMerkleProofs[sweepIdx]
	sweepControlBlock := sweepMerkleProof.ToControlBlock(anchorKey)

	// The final witness stack will be:
	//
	//  <sweep script> <control block>
	witnessStack := make(wire.TxWitness, 2)
	witnessStack[0] = sweepLeaf.Script
	witnessStack[1], err = sweepControlBlock.ToBytes()
	if err != nil {
		return nil, err
	}

	return witnessStack, nil
}

// CommitSpendAnchor constructs a valid witness allowing a node to spend their
// anchor output on the commitment transaction using their funding key. This is
// used for the anchor channel type.
func CommitSpendAnchor(signer Signer, signDesc *SignDescriptor,
	sweepTx *wire.MsgTx) (wire.TxWitness, error) {

	if signDesc.KeyDesc.PubKey == nil {
		return nil, fmt.Errorf("cannot generate witness with nil " +
			"KeyDesc pubkey")
	}

	// Create a signature.
	sweepSig, err := signer.SignOutputRaw(sweepTx, signDesc)
	if err != nil {
		return nil, err
	}

	// The witness here is just a signature and the redeem script.
	witnessStack := make([][]byte, 2)
	witnessStack[0] = append(sweepSig.Serialize(), byte(signDesc.HashType))
	witnessStack[1] = signDesc.WitnessScript

	return witnessStack, nil
}

// CommitSpendAnchorAnyone constructs a witness allowing anyone to spend the
// anchor output after it has gotten 16 confirmations. Since no signing is
// required, only knowledge of the redeem script is necessary to spend it.
func CommitSpendAnchorAnyone(script []byte) (wire.TxWitness, error) {
	// The witness here is just the redeem script.
	witnessStack := make([][]byte, 2)
	witnessStack[0] = nil
	witnessStack[1] = script

	return witnessStack, nil
}

// SingleTweakBytes computes set of bytes we call the single tweak. The purpose
// of the single tweak is to randomize all regular delay and payment base
// points. To do this, we generate a hash that binds the commitment point to
// the pay/delay base point. The end result is that the basePoint is
// tweaked as follows:
//
//   - key = basePoint + sha256(commitPoint || basePoint)*G
func SingleTweakBytes(commitPoint, basePoint *btcec.PublicKey) []byte {
	h := sha256.New()
	h.Write(commitPoint.SerializeCompressed())
	h.Write(basePoint.SerializeCompressed())
	return h.Sum(nil)
}

// TweakPubKey tweaks a public base point given a per commitment point. The per
// commitment point is a unique point on our target curve for each commitment
// transaction. When tweaking a local base point for use in a remote commitment
// transaction, the remote party's current per commitment point is to be used.
// The opposite applies for when tweaking remote keys. Precisely, the following
// operation is used to "tweak" public keys:
//
//	tweakPub := basePoint + sha256(commitPoint || basePoint) * G
//	         := G*k + sha256(commitPoint || basePoint)*G
//	         := G*(k + sha256(commitPoint || basePoint))
//
// Therefore, if a party possess the value k, the private key of the base
// point, then they are able to derive the proper private key for the
// revokeKey by computing:
//
//	revokePriv := k + sha256(commitPoint || basePoint) mod N
//
// Where N is the order of the sub-group.
//
// The rationale for tweaking all public keys used within the commitment
// contracts is to ensure that all keys are properly delinearized to avoid any
// funny business when jointly collaborating to compute public and private
// keys. Additionally, the use of the per commitment point ensures that each
// commitment state houses a unique set of keys which is useful when creating
// blinded channel outsourcing protocols.
//
// TODO(roasbeef): should be using double-scalar mult here
func TweakPubKey(basePoint, commitPoint *btcec.PublicKey) *btcec.PublicKey {
	tweakBytes := SingleTweakBytes(commitPoint, basePoint)
	return TweakPubKeyWithTweak(basePoint, tweakBytes)
}

// TweakPubKeyWithTweak is the exact same as the TweakPubKey function, however
// it accepts the raw tweak bytes directly rather than the commitment point.
func TweakPubKeyWithTweak(pubKey *btcec.PublicKey,
	tweakBytes []byte) *btcec.PublicKey {

	var (
		pubKeyJacobian btcec.JacobianPoint
		tweakJacobian  btcec.JacobianPoint
		resultJacobian btcec.JacobianPoint
	)
	tweakKey, _ := btcec.PrivKeyFromBytes(tweakBytes)
	btcec.ScalarBaseMultNonConst(&tweakKey.Key, &tweakJacobian)

	pubKey.AsJacobian(&pubKeyJacobian)
	btcec.AddNonConst(&pubKeyJacobian, &tweakJacobian, &resultJacobian)

	resultJacobian.ToAffine()
	return btcec.NewPublicKey(&resultJacobian.X, &resultJacobian.Y)
}

// TweakPrivKey tweaks the private key of a public base point given a per
// commitment point. The per commitment secret is the revealed revocation
// secret for the commitment state in question. This private key will only need
// to be generated in the case that a channel counter party broadcasts a
// revoked state. Precisely, the following operation is used to derive a
// tweaked private key:
//
//   - tweakPriv := basePriv + sha256(commitment || basePub) mod N
//
// Where N is the order of the sub-group.
func TweakPrivKey(basePriv *btcec.PrivateKey,
	commitTweak []byte) *btcec.PrivateKey {

	// tweakInt := sha256(commitPoint || basePub)
	tweakScalar := new(btcec.ModNScalar)
	tweakScalar.SetByteSlice(commitTweak)

	tweakScalar.Add(&basePriv.Key)

	return &btcec.PrivateKey{Key: *tweakScalar}
}

// DeriveRevocationPubkey derives the revocation public key given the
// counterparty's commitment key, and revocation preimage derived via a
// pseudo-random-function. In the event that we (for some reason) broadcast a
// revoked commitment transaction, then if the other party knows the revocation
// preimage, then they'll be able to derive the corresponding private key to
// this private key by exploiting the homomorphism in the elliptic curve group:
//   - https://en.wikipedia.org/wiki/Group_homomorphism#Homomorphisms_of_abelian_groups
//
// The derivation is performed as follows:
//
//	revokeKey := revokeBase * sha256(revocationBase || commitPoint) +
//	             commitPoint * sha256(commitPoint || revocationBase)
//
//	          := G*(revokeBasePriv * sha256(revocationBase || commitPoint)) +
//	             G*(commitSecret * sha256(commitPoint || revocationBase))
//
//	          := G*(revokeBasePriv * sha256(revocationBase || commitPoint) +
//	                commitSecret * sha256(commitPoint || revocationBase))
//
// Therefore, once we divulge the revocation secret, the remote peer is able to
// compute the proper private key for the revokeKey by computing:
//
//	revokePriv := (revokeBasePriv * sha256(revocationBase || commitPoint)) +
//	              (commitSecret * sha256(commitPoint || revocationBase)) mod N
//
// Where N is the order of the sub-group.
func DeriveRevocationPubkey(revokeBase,
	commitPoint *btcec.PublicKey) *btcec.PublicKey {

	// R = revokeBase * sha256(revocationBase || commitPoint)
	revokeTweakBytes := SingleTweakBytes(revokeBase, commitPoint)
	revokeTweakScalar := new(btcec.ModNScalar)
	revokeTweakScalar.SetByteSlice(revokeTweakBytes)

	var (
		revokeBaseJacobian btcec.JacobianPoint
		rJacobian          btcec.JacobianPoint
	)
	revokeBase.AsJacobian(&revokeBaseJacobian)
	btcec.ScalarMultNonConst(
		revokeTweakScalar, &revokeBaseJacobian, &rJacobian,
	)

	// C = commitPoint * sha256(commitPoint || revocationBase)
	commitTweakBytes := SingleTweakBytes(commitPoint, revokeBase)
	commitTweakScalar := new(btcec.ModNScalar)
	commitTweakScalar.SetByteSlice(commitTweakBytes)

	var (
		commitPointJacobian btcec.JacobianPoint
		cJacobian           btcec.JacobianPoint
	)
	commitPoint.AsJacobian(&commitPointJacobian)
	btcec.ScalarMultNonConst(
		commitTweakScalar, &commitPointJacobian, &cJacobian,
	)

	// Now that we have the revocation point, we add this to their commitment
	// public key in order to obtain the revocation public key.
	//
	// P = R + C
	var resultJacobian btcec.JacobianPoint
	btcec.AddNonConst(&rJacobian, &cJacobian, &resultJacobian)

	resultJacobian.ToAffine()
	return btcec.NewPublicKey(&resultJacobian.X, &resultJacobian.Y)
}

// DeriveRevocationPrivKey derives the revocation private key given a node's
// commitment private key, and the preimage to a previously seen revocation
// hash. Using this derived private key, a node is able to claim the output
// within the commitment transaction of a node in the case that they broadcast
// a previously revoked commitment transaction.
//
// The private key is derived as follows:
//
//	revokePriv := (revokeBasePriv * sha256(revocationBase || commitPoint)) +
//	              (commitSecret * sha256(commitPoint || revocationBase)) mod N
//
// Where N is the order of the sub-group.
func DeriveRevocationPrivKey(revokeBasePriv *btcec.PrivateKey,
	commitSecret *btcec.PrivateKey) *btcec.PrivateKey {

	// r = sha256(revokeBasePub || commitPoint)
	revokeTweakBytes := SingleTweakBytes(
		revokeBasePriv.PubKey(), commitSecret.PubKey(),
	)
	revokeTweakScalar := new(btcec.ModNScalar)
	revokeTweakScalar.SetByteSlice(revokeTweakBytes)

	// c = sha256(commitPoint || revokeBasePub)
	commitTweakBytes := SingleTweakBytes(
		commitSecret.PubKey(), revokeBasePriv.PubKey(),
	)
	commitTweakScalar := new(btcec.ModNScalar)
	commitTweakScalar.SetByteSlice(commitTweakBytes)

	// Finally to derive the revocation secret key we'll perform the
	// following operation:
	//
	//  k = (revocationPriv * r) + (commitSecret * c) mod N
	//
	// This works since:
	//  P = (G*a)*b + (G*c)*d
	//  P = G*(a*b) + G*(c*d)
	//  P = G*(a*b + c*d)
	revokeHalfPriv := revokeTweakScalar.Mul(&revokeBasePriv.Key)
	commitHalfPriv := commitTweakScalar.Mul(&commitSecret.Key)

	revocationPriv := revokeHalfPriv.Add(commitHalfPriv)

	return &btcec.PrivateKey{Key: *revocationPriv}
}

// ComputeCommitmentPoint generates a commitment point given a commitment
// secret. The commitment point for each state is used to randomize each key in
// the key-ring and also to used as a tweak to derive new public+private keys
// for the state.
func ComputeCommitmentPoint(commitSecret []byte) *btcec.PublicKey {
	_, pubKey := btcec.PrivKeyFromBytes(commitSecret)
	return pubKey
}
