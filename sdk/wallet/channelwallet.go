package wallet

import (
	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
	"github.com/sat20-labs/satsnet_btcd/btcec"
	"github.com/sat20-labs/satsnet_btcd/btcec/ecdsa"
	spsbt "github.com/sat20-labs/satsnet_btcd/btcutil/psbt"
)

type channelWallet struct {
	wallet *InternalWallet
	peerId []byte
	id     uint32
}

func NewChannelWallet(wallet *InternalWallet, peerId []byte, id uint32) *channelWallet {
	return &channelWallet{
		wallet: wallet,
		peerId: peerId,
		id: id,
	}
}

func (p *channelWallet) GetId() uint32 {
	return p.id
}

func (p *channelWallet) GetCommitRootKey() (*secp256k1.PrivateKey, *secp256k1.PublicKey) {
	return p.wallet.GetCommitRootKey(p.peerId)
}

func (p *channelWallet) GetCommitSecret(index int) *secp256k1.PrivateKey {
	return p.wallet.GetCommitSecret(p.peerId, index)
}

func (p *channelWallet) DeriveRevocationPrivKey(commitsecret *btcec.PrivateKey) *btcec.PrivateKey {
	revBasePrivKey, _ := p.wallet.getRevocationBaseKey(p.id)
	return utils.DeriveRevocationPrivKey(revBasePrivKey, commitsecret)
}

func (p *channelWallet) GetRevocationBaseKey() *secp256k1.PublicKey {
	_, pubk := p.wallet.getRevocationBaseKey(p.id)
	return pubk
}

func (p *channelWallet) GetPaymentPubKey() *secp256k1.PublicKey {
	key := p.wallet.getPaymentPrivKeyWithChange(p.id)
	if key == nil {
		Log.Errorf("GetPaymentPrivKey failed.")
		return nil
	}
	return key.PubKey()
}

func (p *channelWallet) SignMessage(msg []byte) (*ecdsa.Signature, error) {
	privKey, err := p.wallet.deriveKeyByLocator(KeyFamilyBaseEncryption, p.id, 0)
	if err != nil {
		return nil, err
	}
	return p.wallet.signMessage(privKey, msg)
}

func (p *channelWallet) SignPsbt(packet *psbt.Packet) (error) {
	privKey := p.wallet.getPaymentPrivKeyWithChange(p.id)
	return p.wallet.signPsbt(privKey, packet)
}

func (p *channelWallet) SignPsbt_SatsNet(packet *spsbt.Packet) error {
	privKey := p.wallet.getPaymentPrivKeyWithChange(p.id)
	return p.wallet.signPsbt_SatsNet(privKey, packet)
}
