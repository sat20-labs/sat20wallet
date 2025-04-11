package common

import (
	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"

	"github.com/sat20-labs/satoshinet/btcec"

	spsbt "github.com/sat20-labs/satoshinet/btcutil/psbt"
)

// 方便对通道的操作进行签名和验证，id是子账户id，从0开始
type ChannelWallet interface {
	GetId() uint32
	GetCommitSecret(index uint32) *secp256k1.PrivateKey
	DeriveRevocationPrivKey(commitsecret *btcec.PrivateKey) *btcec.PrivateKey
	GetRevocationBaseKey() *secp256k1.PublicKey
	GetPaymentPubKey() *secp256k1.PublicKey

	SignMessage(msg []byte) ([]byte, error)
	SignPsbt(packet *psbt.Packet) (error)
	SignPsbt_SatsNet(packet *spsbt.Packet) error
	SignPsbts(packet []*psbt.Packet) (error)
	SignPsbts_SatsNet(packet []*spsbt.Packet) error
}

type Wallet interface {
	GetPubKey(uint32) *secp256k1.PublicKey
	GetAddress(uint32) string
	GetNodePubKey() *secp256k1.PublicKey

	// default channel wallet, CWId = 0
	GetCommitSecret(peer []byte, index uint32) *secp256k1.PrivateKey
	DeriveRevocationPrivKey(commitsecret *btcec.PrivateKey) *btcec.PrivateKey
	GetRevocationBaseKey() *secp256k1.PublicKey
	GetPaymentPubKey() *secp256k1.PublicKey

	SignMessage(msg []byte) ([]byte, error)
	SignWalletMessage(msg string) ([]byte, error)
	SignPsbt(packet *psbt.Packet) (error)
	SignPsbt_SatsNet(packet *spsbt.Packet) error
	SignPsbts(packet []*psbt.Packet) (error)
	SignPsbts_SatsNet(packet []*spsbt.Packet) error
	
	// special channel wallet
	CreateChannelWallet(peer []byte, id uint32) ChannelWallet
	GetChannelWallet(id uint32) ChannelWallet
}
