package wallet

import (
	"bytes"
	"encoding/json"
	"fmt"
)

func (p *Manager) GetChannelFromDKVS(channelId string) (*Channel, error) {
	if p.wallet == nil {
		return nil, fmt.Errorf("wallet is not created/unlocked")
	}

	pubkey := p.wallet.GetPaymentPubKey().SerializeCompressed()
	key := GetChannelKey(channelId)
	value, err := p.GetIndexerRPCClient().GetKV(pubkey, key)
	if err != nil {
		if slave := p.GetSlaveIndexerClient(); slave != nil {
			value, err = slave.GetKV(pubkey, key)
		}
		if err != nil {
			return nil, err
		}
	}

	if !bytes.Equal(value.PubKey, pubkey) {
		return nil, fmt.Errorf("invalid pubkey")
	}
	if value.Key != key {
		return nil, fmt.Errorf("invalid key %s", value.Key)
	}

	sig := value.Signature
	value.Signature = nil
	msg, err := json.Marshal(value)
	if err != nil {
		Log.Errorf("json.Marshal failed. %v", err)
		return nil, err
	}
	value.Signature = sig

	if err := VerifySignOfMessage(msg, sig, value.PubKey); err != nil {
		Log.Errorf("verify signature of key %s failed, %v", value.Key, err)
		return nil, err
	}

	raw, err := DecodeChannelDKVSValue(value.Value)
	if err != nil {
		Log.Errorf("DecodeChannelDKVSValue failed. %v", err)
		return nil, err
	}
	var newChannel ChannelInDB
	if err := DecodeFromBytes(raw, &newChannel); err != nil {
		Log.Errorf("DecodeFromBytes failed. %v", err)
		return nil, err
	}
	if ok, _ := newChannel.CheckDataWithCleanup(); !ok {
		Log.Errorf("channel %s is modified", newChannel.ChannelId)
		return nil, fmt.Errorf("channel %s CheckData failed", newChannel.ChannelId)
	}

	return NewChannel(&newChannel, p), nil
}

func (p *Manager) CleanChannelData(channelId string) error {
	if c := p.GetChannel(channelId); c != nil {
		p.DisableChannel(c)
	}
	p.DeleteChannelInDB(channelId)

	for k, v := range p.GetPaymentReservations() {
		if v.ChannelId == channelId {
			p.DelResvWithId(k)
			_ = DeleteReservation(p.db, RESV_TYPE_PAYMENT, v.Id)
		}
	}
	for k, v := range p.GetFundingReservations() {
		if v.ChannelId == channelId {
			p.DelResvWithId(k)
			_ = DeleteReservation(p.db, RESV_TYPE_OPEN, v.Id)
		}
	}
	for k, v := range p.GetClosingReservations() {
		if v.ChannelId == channelId {
			p.DelResvWithId(k)
			_ = DeleteReservation(p.db, RESV_TYPE_CLOSE, v.Id)
		}
	}
	for k, v := range p.GetSplicingReservations() {
		if v.ChannelId == channelId {
			p.DelResvWithId(k)
			_ = DeleteReservation(p.db, RESV_TYPE_SPLICING, v.Id)
		}
	}

	Log.Infof("channel %s deleted.", channelId)
	return nil
}
