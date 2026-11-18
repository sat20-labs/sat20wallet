package wallet

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	indexer "github.com/sat20-labs/indexer/common"
	"github.com/sat20-labs/sat20wallet/sdk/common"
	"github.com/sat20-labs/sat20wallet/sdk/wallet/utils"
)

type rgb11PublicChannelBinding struct {
	Version   uint32 `json:"version"`
	ChannelID string `json:"channel_id"`
	Owner     []byte `json:"owner"`
	Peer      []byte `json:"peer"`
	Witness   []byte `json:"witness"`
}

func rgb11PublicChannelBindingKey(owner []byte, channel string) []byte {
	hash := sha256.Sum256(append(append([]byte(nil), owner...), []byte(channel)...))
	return []byte(GetDBKeyPrefix() + "rgb11-public-owner/" + _chain + "/" + hex.EncodeToString(hash[:]))
}

// The counterparty's public pool need not be the local node's outbound default
// peer (a bootstrap serves many core nodes). Bind it to the actual deployed
// contract's authenticated owner pair, never to a witness supplied by a user.
func (p *Manager) registerRGB11ContractBinding(contract ContractRuntime) error {
	if contract == nil || !contract.GetRuntimeBase().rgb11PublicContract() {
		return fmt.Errorf("invalid RGB11 public contract")
	}
	base := contract.GetRuntimeBase()
	local := base.GetLocalWallet()
	if local == nil || !bytes.Equal(local.GetPaymentPubKey().SerializeCompressed(), base.LocalPubKey) {
		return fmt.Errorf("RGB11 public contract owner mismatch")
	}
	witness, err := rgb11ContractWitness(contract)
	if err != nil {
		return err
	}
	binding := rgb11PublicChannelBinding{Version: 1, ChannelID: contract.Address(), Owner: base.LocalPubKey, Peer: base.RemotePubKey, Witness: witness}
	raw, err := json.Marshal(binding)
	if err != nil {
		return err
	}
	key := rgb11PublicChannelBindingKey(binding.Owner, binding.ChannelID)
	rgb11ChannelInventoryMu.Lock()
	defer rgb11ChannelInventoryMu.Unlock()
	old, err := p.GetDB().Read(key)
	if err == nil {
		if !bytes.Equal(old, raw) {
			return fmt.Errorf("RGB11 public owner pair is immutable")
		}
		return nil
	}
	if !errors.Is(err, indexer.ErrKeyNotFound) {
		return err
	}
	return p.GetDB().Write(key, raw)
}

func (p *Manager) channelOwnerWitness(local common.Wallet, channel string) ([]byte, []byte, error) {
	if witness, peer, err := p.channelWitness(local, channel); err == nil {
		return witness, peer, nil
	}
	if local == nil || p.GetDB() == nil {
		return nil, nil, fmt.Errorf("RGB11 public owner unavailable")
	}
	key := local.GetPaymentPubKey().SerializeCompressed()
	raw, err := p.GetDB().Read(rgb11PublicChannelBindingKey(key, channel))
	if err != nil {
		return nil, nil, err
	}
	var binding rgb11PublicChannelBinding
	if json.Unmarshal(raw, &binding) != nil || binding.Version != 1 || binding.ChannelID != channel || !bytes.Equal(binding.Owner, key) {
		return nil, nil, fmt.Errorf("invalid RGB11 public owner binding")
	}
	witness, script, err := GetP2WSHscript(key, binding.Peer)
	if err != nil || !bytes.Equal(witness, binding.Witness) {
		return nil, nil, fmt.Errorf("RGB11 public owner witness changed")
	}
	expected, err := utils.WitnessScriptHash(witness)
	if err != nil || !bytes.Equal(expected, script) {
		return nil, nil, fmt.Errorf("invalid RGB11 public script")
	}
	address, err := GetP2WSHaddressFromScript(script)
	if err != nil || address != channel {
		return nil, nil, fmt.Errorf("RGB11 public owner channel mismatch")
	}
	return witness, binding.Peer, nil
}
