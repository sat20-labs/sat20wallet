package wire


type AbbrChannelInfo struct {
	Version               int    `json:"version"`
	ChannelId             string `json:"channelId"`
	CommitHeight          int    `json:"commitHeight"`
	StaticMerkleRoot      []byte `json:"staticMerkleRoot"`
	LocalAssetMerkleRoot  []byte `json:"localAssetMerkleRoot"`
	RemoteAssetMerkleRoot []byte `json:"remoteAssetMerkleRoot"`
}


type PingRequest struct {
	MsgHeader
	PubKey  []byte           `json:"pubKey"`
	Mode    string           `json:"mode"`
	Channel *AbbrChannelInfo `json:"info"`
}

type PingReq struct {
	PingRequest
	Sig []byte `json:"msgSig"`
}

type PingResponse struct {
	CommitHeight int    `json:"commitHeight"`
	NextAction   string `json:"action"`
	ActionParam  string `json:"param"`
	Sig          []byte `json:"paramSig"`
}

type PingResp struct {
	BaseResp
	*PingResponse
}


type PerformActionRequest struct {
	MsgHeader
	Action      string `json:"action"` // resv type
	ActionParam []byte `json:"param"`
	FeeRate     int64  `json:"feeRate"`
	ReqTime     int64  `json:"reqTime"`
	SendTxInL1  bool   `json:"sendInL1"`
	MoreData    []byte `json:"more"`
	PubKey      []byte `json:"pubKey"`
}

type PerformActionReq struct {
	PerformActionRequest
	Sig []byte `json:"msgSig"`
}

type PerformActionResp struct {
	BaseResp
	Id             int64  `json:"id"`
	ServiceAddress string `json:"serviceAddress"`
	ServiceFee     int64  `json:"serviceFee"`
	Invoice        []byte `json:"invoice"`
	InvoiceSig     []byte `json:"invoiceSig"`
}

type PerformActionAckReq struct {
	Id      int64  `json:"id"`
	FeeTx   string `json:"tx"`
	FeeTxId string `json:"txId"`
}

type PerformActionAckResp struct {
	BaseResp
	Id           int64  `json:"id"`
	Status       int    `json:"status"`
	ActionResvId int64  `json:"actionResvId"`
	ActionStatus int    `json:"actionStatus"`
	ActionResult []byte `json:"actionResult"`
}


type ActionResultNotify struct {
	MsgHeader
	Id     int64  `json:"id"`
	Action string `json:"action"` // resv type
	Result int    `json:"result"`
	Reason string `json:"reason"`
}

type ActionResultResp struct {
	BaseResp
	Id int64 `json:"id"`
}

type ActionSyncRequest struct {
	MsgHeader
	PubKey []byte `json:"pubKey"`
	Reason string `json:"reason"`
}

type ActionSyncReq struct {
	ActionSyncRequest
	Sig []byte `json:"msgSig"`
}

type ActionSyncResp struct {
	BaseResp
	ChannelData []byte `json:"channelData"`
}

