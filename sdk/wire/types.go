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
	PubKey  	[]byte           `json:"pubKey"`
	Mode    	string           `json:"mode"`
	Channel 	*AbbrChannelInfo `json:"info"`
	NodeId  	[]byte   		 `json:"nodeId,omitempty"`
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
	PubKey 		[]byte `json:"pubKey"`
	Reason 		string `json:"reason"`
	NodeId 	    []byte `json:"nodeId,omitempty"` 
}

type ActionSyncReq struct {
	ActionSyncRequest
	Sig []byte `json:"msgSig"`
}

type ActionSyncResp struct {
	BaseResp
	ChannelData []byte `json:"channelData"`
}

