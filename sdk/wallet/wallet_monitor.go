package wallet

import (
	"fmt"

	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	spsbt "github.com/sat20-labs/satoshinet/btcutil/psbt"
)


type MonitorWallet struct {
	address string
}


func NewMonitorWallet(address string,) (*MonitorWallet) {
	return &MonitorWallet{
		address: address,
	}
}

func (p *MonitorWallet) Clone() common.Wallet {
	return &MonitorWallet{
		address: p.address,
	}
}

func (p *MonitorWallet) SetSubAccount(id uint32) {
}
func (p *MonitorWallet) GetSubAccount() uint32 {
	return 0
}
	
func (p *MonitorWallet) GetPubKey() *secp256k1.PublicKey {
	return nil
}
func (p *MonitorWallet) GetAddress() string {
	return p.address
}
func (p *MonitorWallet) GetPubKeyByIndex(uint32) *secp256k1.PublicKey {
	return nil
}
func (p *MonitorWallet) GetAddressByIndex(uint32) string {
	return p.address
}
func (p *MonitorWallet) GetNodePubKey() *secp256k1.PublicKey {
	return nil
}

// default channel wallet, CWId = 0
func (p *MonitorWallet) GetCommitSecret(peer []byte, index uint32) *secp256k1.PrivateKey {
	return nil
}
func (p *MonitorWallet) DeriveRevocationPrivKey(commitsecret *secp256k1.PrivateKey) *secp256k1.PrivateKey {
	return nil
}
func (p *MonitorWallet) GetRevocationBaseKey() *secp256k1.PublicKey {
	return nil
}
func (p *MonitorWallet) GetPaymentPubKey() *secp256k1.PublicKey {
	return nil
}

func (p *MonitorWallet) SignMessage(msg []byte) ([]byte, error) {
	return nil, fmt.Errorf("not implemented")
}
func (p *MonitorWallet) SignWalletMessage(msg string) ([]byte, error) {
	return nil, fmt.Errorf("not implemented")
}
func (p *MonitorWallet) SignPsbt(packet *psbt.Packet) (error) {
	return fmt.Errorf("not implemented")
}
func (p *MonitorWallet) SignPsbt_SatsNet(packet *spsbt.Packet) error {
	return fmt.Errorf("not implemented")
}
func (p *MonitorWallet) SignPsbts(packet []*psbt.Packet) (error) {
	return fmt.Errorf("not implemented")
}
func (p *MonitorWallet) SignPsbts_SatsNet(packet []*spsbt.Packet) error {
	return fmt.Errorf("not implemented")
}

func (p *MonitorWallet) SignMessageWithIndex(msg []byte, index uint32) ([]byte, error) {
	return nil, fmt.Errorf("not implemented")
}
func (p *MonitorWallet) SignWalletMessageWithIndex(msg string, index uint32) ([]byte, error) {
	return nil, fmt.Errorf("not implemented")
}
func (p *MonitorWallet) SignPsbtWithIndex(packet *psbt.Packet, index uint32) (error) {
	return fmt.Errorf("not implemented")
}
func (p *MonitorWallet) SignPsbtWithIndex_SatsNet(packet *spsbt.Packet, index uint32) error {
	return fmt.Errorf("not implemented")
}
func (p *MonitorWallet) SignPsbtsWithIndex(packet []*psbt.Packet, index uint32) (error) {
	return fmt.Errorf("not implemented")
}
func (p *MonitorWallet) SignPsbtsWithIndex_SatsNet(packet []*spsbt.Packet, index uint32) error {
	return fmt.Errorf("not implemented")
}
	
// special channel wallet
func (p *MonitorWallet) CreateChannelWallet(peer []byte, id uint32) common.ChannelWallet {
	return nil
}
func (p *MonitorWallet) GetChannelWallet(id uint32) common.ChannelWallet {
	return nil
}

