package common

import (
	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"

	"github.com/sat20-labs/satsnet_btcd/btcec"
	"github.com/sat20-labs/satsnet_btcd/btcec/ecdsa"

	spsbt "github.com/sat20-labs/satsnet_btcd/btcutil/psbt"
)

type ChannelWallet interface {
	GetId() uint32
	GetCommitRootKey() (*secp256k1.PrivateKey, *secp256k1.PublicKey)
	GetCommitSecret(index int) *secp256k1.PrivateKey
	DeriveRevocationPrivKey(commitsecret *btcec.PrivateKey) *btcec.PrivateKey
	GetRevocationBaseKey() *secp256k1.PublicKey
	GetPaymentPubKey() *secp256k1.PublicKey

	SignMessage(msg []byte) (*ecdsa.Signature, error)
	SignPsbt(packet *psbt.Packet) (error)
	SignPsbt_SatsNet(packet *spsbt.Packet) error
}

type Wallet interface {
	GetPubKey(uint32) *secp256k1.PublicKey
	GetAddress(uint32) string
	GetNodePubKey() *secp256k1.PublicKey

	// default channel wallet, CWId = 0
	GetCommitRootKey(peer []byte) (*secp256k1.PrivateKey, *secp256k1.PublicKey)
	GetCommitSecret(peer []byte, index int) *secp256k1.PrivateKey
	DeriveRevocationPrivKey(commitsecret *btcec.PrivateKey) *btcec.PrivateKey
	GetRevocationBaseKey() *secp256k1.PublicKey
	GetPaymentPubKey() *secp256k1.PublicKey

	SignMessage(msg []byte) (*ecdsa.Signature, error)
	SignPsbt(packet *psbt.Packet) (error)
	SignPsbt_SatsNet(packet *spsbt.Packet) error
	
	// special channel wallet
	CreateChannelWallet(peer []byte, id uint32) ChannelWallet
	GetChannelWallet(id uint32) ChannelWallet
}
