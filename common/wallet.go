package common

import (
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"

	"github.com/sat20-labs/satsnet_btcd/btcec"
	"github.com/sat20-labs/satsnet_btcd/btcec/ecdsa"
	
	spsbt "github.com/sat20-labs/satsnet_btcd/btcutil/psbt"
	stxscript "github.com/sat20-labs/satsnet_btcd/txscript"
	swire "github.com/sat20-labs/satsnet_btcd/wire"
)

type Wallet interface {
	GetP2TRAddress() string
	GetCommitRootKey(peer []byte) (*secp256k1.PrivateKey, *secp256k1.PublicKey)
	GetCommitSecret(peer []byte, index int) *secp256k1.PrivateKey
	DeriveRevocationPrivKey(commitsecret *btcec.PrivateKey) *btcec.PrivateKey
	GetRevocationBaseKey() *secp256k1.PublicKey
	GetNodePubKey() *secp256k1.PublicKey
	GetPaymentPubKey() *secp256k1.PublicKey

	SignTxInput(tx *wire.MsgTx, prevFetcher txscript.PrevOutputFetcher,
		sigHashes *txscript.TxSigHashes,
		index int, witnessScript []byte) ([]byte, error)
	SignTxInput_SatsNet(tx *swire.MsgTx, prevFetcher stxscript.PrevOutputFetcher,
		sigHashes *stxscript.TxSigHashes,
		index int, witnessScript []byte) ([]byte, error)
	SignTx(tx *wire.MsgTx, prevFetcher txscript.PrevOutputFetcher) error
	SignTx_SatsNet(tx *swire.MsgTx, prevFetcher stxscript.PrevOutputFetcher) error
	SignTxWithPeer(tx *wire.MsgTx, prevFetcher txscript.PrevOutputFetcher,
		witnessScript []byte, peerPubKey []byte, peerSig [][]byte) ([][]byte, error)
	SignTxWithPeer_SatsNet(tx *swire.MsgTx, prevFetcher stxscript.PrevOutputFetcher,
		witnessScript []byte, peerPubKey []byte, peerSig [][]byte) ([][]byte, error)
	PartialSignTx(tx *wire.MsgTx, prevFetcher txscript.PrevOutputFetcher,
		witnessScript []byte, pos int) ([][]byte, error)
	PartialSignTx_SatsNet(tx *swire.MsgTx, prevFetcher stxscript.PrevOutputFetcher,
		witnessScript []byte, pos int) ([][]byte, error)
	SignMessage(msg []byte) (*ecdsa.Signature, error)
	SignPsbt(packet *psbt.Packet) (error)
	SignPsbt_SatsNet(packet *spsbt.Packet) (error)
}
