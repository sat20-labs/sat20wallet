package wallet

import (
	"encoding/hex"

	swire "github.com/sat20-labs/satoshinet/wire"
)

type DisplayAsset struct {
	AssetName  swire.AssetName `json:"Name"`
	Amount     string          `json:"Amount"`
	BindingSat uint32          `json:"BindingSat"`
}

type ChannelInfo struct {
	Version        int      `json:"version"`
	ChannelId      string   `json:"channelId"`
	ShortChannelId uint64   `json:"shortChannelId"`
	RedeemScript   string   `json:"redeemScript"`
	ChannelType    int      `json:"type"`
	Contract       string   `json:"contract"`
	ChanPoint      string   `json:"chanPoint"`
	IsInitiator    bool     `json:"initiator"`
	FundingTime    int64    `json:"fundingTime"`
	FundingUtxos   []string `json:"fundingUtxos"`
	StubUtxos      []string `json:"stubUtxos"`
	PendingUtxos   []string `json:"pendingUtxos"`
	Address        string   `json:"address"`
	Status         int      `json:"status"`
	CsvDelay       int      `json:"csvDelay"`
	Peer           string   `json:"peer"`
	Capacity       int64    `json:"capacity"`

	CommitHeight    int                 `json:"commitHeight"`
	LastPaymentTxId string              `json:"lastPaymentId"`
	TotalSent       int64               `json:"totalSent"`
	TotalReceived   int64               `json:"totalRecv"`
	UtxoL2          []*TxOutput_SatsNet `json:"UtxosL2"`
	PendingUtxoL2   []*TxOutput_SatsNet `json:"pendingUtxosL2"`

	LocalBalance     []*DisplayAsset `json:"localbalanceL1"`
	RemoteBalance    []*DisplayAsset `json:"remotebalanceL1"`
	LocalCommitment  *MsgTx          `json:"localCommitment"`
	RemoteCommitment *MsgTx          `json:"remoteCommitment"`
	LocalDeAnchorTx  *MsgTx          `json:"localDeAnchorTx"`
	RemoteDeAnchorTx *MsgTx          `json:"remoteDeAnchorTx"`

	StaticMerkleRoot       string `json:"staticMerkleRoot"`
	LocalAssetsMerkleRoot  string `json:"localAssetMerkleRoot"`
	RemoteAssetsMerkleRoot string `json:"remoteAssetMerkleRoot"`
}

func convertBalance(balance map[AssetName]*Decimal) []*DisplayAsset {
	result := make([]*DisplayAsset, 0, len(balance))
	for k, v := range balance {
		amount := ""
		if v != nil {
			amount = v.String()
		}
		result = append(result, &DisplayAsset{
			AssetName:  k.AssetName,
			Amount:     amount,
			BindingSat: uint32(k.N),
		})
	}
	return result
}

func ConvertChannel(channel *Channel) *ChannelInfo {
	if channel == nil {
		return nil
	}

	channel.Mutex.RLock()
	defer channel.Mutex.RUnlock()

	info := &ChannelInfo{
		Version:                channel.Version,
		ChannelId:              channel.ChannelId,
		ShortChannelId:         channel.ShortChannelID,
		RedeemScript:           hex.EncodeToString(channel.RedeemScript),
		ChannelType:            int(channel.ChannelType),
		Contract:               hex.EncodeToString(channel.Contract),
		Address:                channel.Address,
		Status:                 int(channel.Status),
		IsInitiator:            channel.IsInitiator,
		FundingTime:            channel.FundingTime,
		CsvDelay:               int(channel.CsvDelay),
		Peer:                   hex.EncodeToString(channel.PeerNodeId),
		Capacity:               channel.Capacity,
		LastPaymentTxId:        channel.LastPaymentTxId,
		TotalSent:              channel.TotalSatSent,
		TotalReceived:          channel.TotalSatReceived,
		CommitHeight:           channel.CommitHeight,
		StaticMerkleRoot:       hex.EncodeToString(channel.StaticMerkleRoot),
		LocalAssetsMerkleRoot:  hex.EncodeToString(channel.LocalAssetsMerkleRoot),
		RemoteAssetsMerkleRoot: hex.EncodeToString(channel.RemoteAssetsMerkleRoot),
	}
	if channel.ChanPoint != nil {
		info.ChanPoint = channel.ChanPoint.OutPointStr
	}

	info.FundingUtxos = make([]string, 0)
	for _, uv := range channel.FundingUtxos {
		for _, u := range uv {
			if u != nil {
				info.FundingUtxos = append(info.FundingUtxos, u.OutPointStr)
			}
		}
	}
	info.StubUtxos = make([]string, 0)
	for _, uv := range channel.StubUtxos {
		for _, u := range uv {
			if u != nil {
				info.StubUtxos = append(info.StubUtxos, u.OutPointStr)
			}
		}
	}
	info.PendingUtxos = make([]string, 0)
	for _, u := range channel.PendingUtxos {
		if u != nil {
			info.PendingUtxos = append(info.PendingUtxos, u.OutPointStr)
		}
	}

	if channel.LocalCommitment != nil {
		info.LocalBalance = convertBalance(channel.LocalCommitment.LocalBalance)
		info.RemoteBalance = convertBalance(channel.LocalCommitment.RemoteBalance)
		info.LocalCommitment = ConvertMsgTx(channel.LocalCommitment.CommitTx)
		info.LocalDeAnchorTx = ConvertMsgTx_SatsNet(channel.LocalCommitment.DeAnchorTx)
	}
	if channel.RemoteCommitment != nil {
		info.RemoteCommitment = ConvertMsgTx(channel.RemoteCommitment.CommitTx)
		info.RemoteDeAnchorTx = ConvertMsgTx_SatsNet(channel.RemoteCommitment.DeAnchorTx)
	}

	info.UtxoL2 = channel.GetValidOutput_SatsNet()
	info.PendingUtxoL2 = channel.GetPendingOutput_SatsNet()
	return info
}
