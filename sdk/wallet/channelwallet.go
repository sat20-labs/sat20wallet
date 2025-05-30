package wallet

import (
	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
	"github.com/sat20-labs/satoshinet/btcec"
	spsbt "github.com/sat20-labs/satoshinet/btcutil/psbt"
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
		id: id, // 对支付签名而言，是index；对通道签名而言，是account
	}
}

func (p *channelWallet) GetId() uint32 {
	return p.id
}

func (p *channelWallet) GetCommitRootKey(subId uint32) (*secp256k1.PrivateKey, *secp256k1.PublicKey) {
	privkey := p.wallet.getCommitSecret(p.peerId, p.id, subId, 0)
	if privkey == nil {
		return nil, nil
	}
	return privkey, privkey.PubKey()
}

func (p *channelWallet) GetCommitSecret(subId, index uint32) *secp256k1.PrivateKey {
	return p.wallet.getCommitSecret(p.peerId, p.id, subId, index)
}

func (p *channelWallet) DeriveRevocationPrivKey(commitsecret *btcec.PrivateKey, subId uint32) *btcec.PrivateKey {
	revBasePrivKey, _ := p.wallet.getRevocationBaseKey(p.id, subId)
	return utils.DeriveRevocationPrivKey(revBasePrivKey, commitsecret)
}

func (p *channelWallet) GetRevocationBaseKey(subId uint32) *secp256k1.PublicKey {
	_, pubk := p.wallet.getRevocationBaseKey(p.id, subId)
	return pubk
}

func (p *channelWallet) GetPaymentPubKey() *secp256k1.PublicKey {
	key := p.wallet.getPaymentPrivKeyWithIndex(p.id)
	if key == nil {
		Log.Errorf("GetPaymentPrivKey failed.")
		return nil
	}
	return key.PubKey()
}

func (p *channelWallet) SignMessage(msg []byte) ([]byte, error) {
	privKey, err := p.wallet.deriveKeyByLocator(KeyFamilyBaseEncryption, p.id, 0, 0)
	if err != nil {
		return nil, err
	}
	return p.wallet.signMessage(privKey, msg).Serialize(), nil
}

func (p *channelWallet) SignPsbt(packet *psbt.Packet) (error) {
	privKey := p.wallet.getPaymentPrivKeyWithIndex(p.id)
	return p.wallet.signPsbt(privKey, packet)
}

func (p *channelWallet) SignPsbt_SatsNet(packet *spsbt.Packet) error {
	privKey := p.wallet.getPaymentPrivKeyWithIndex(p.id)
	return p.wallet.signPsbt_SatsNet(privKey, packet)
}

func (p *channelWallet) SignPsbts(packet []*psbt.Packet) (error) {
	privKey := p.wallet.getPaymentPrivKeyWithIndex(p.id)
	return p.wallet.signPsbts(privKey, packet)
}

func (p *channelWallet) SignPsbts_SatsNet(packet []*spsbt.Packet) error {
	privKey := p.wallet.getPaymentPrivKeyWithIndex(p.id)
	return p.wallet.signPsbts_SatsNet(privKey, packet)
}
