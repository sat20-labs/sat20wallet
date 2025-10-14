package wire

type Message interface {
	GetVersion() int
	GetMsgId() string
}

type MsgHeader struct {
	Version int    `json:"version"`
	MsgId   string `json:"msgId"`
}

func (p *MsgHeader) GetVersion() int {
	return p.Version
}

func (p *MsgHeader) GetMsgId() string {
	return p.MsgId
}

type BaseResp struct {
	Code int    `json:"code" example:"0"`
	Msg  string `json:"msg" example:"ok"`
}

type ListResp struct {
	Start int64  `json:"start" example:"0"`
	Total uint64 `json:"total" example:"9992"`
}

type ContractContentResp struct {
	BaseResp
	Contracts []string `json:"contracts"`
}

type DeployedContractResp struct {
	BaseResp
	ContractURLs []string `json:"url"`
}

type ContractDeployFeeReq struct {
	TemplateName string `json:"templateName"`
	Content      string `json:"content"`
	FeeRate      int64  `json:"feeRate"`
}

type ContractDeployFeeResp struct {
	BaseResp
	Fee int64 `json:"fee"`
}

type ContractInvokeFeeReq struct {
	URL   string `json:"url"`
	Param string `json:"parameter"`
}

type ContractInvokeFeeResp struct {
	BaseResp
	Fee int64 `json:"fee"`
}

type ContractStatusResp struct {
	BaseResp
	Status string `json:"status"`
}

type ResvBaseInfo struct {
	Id     int64  `json:"id"`
	Time   string `json:"time"`
	Type   string `json:"type"`
	Status int    `json:"status"`
}


type DeployContractRequest struct {
	MsgHeader
	ChannelId       string   `json:"channel"`
	ContractName    string   `json:"contractName"`
	ContractContent []byte   `json:"contractContent"`
	Deployer        string   `json:"deployer"` // the address of the deployer
	InvoiceSig      []byte   `json:"invoiceSig"`
	Fees            []string `json:"fees"`
	FeeRate         int64    `json:"feeRate"`
	ReqTime         int64    `json:"reqTime"`
	PubKey          []byte   `json:"pubKey"`
}

type DeployContractReq struct {
	DeployContractRequest
	Sig []byte `json:"msgSig"`
}

type DeployContractResp struct {
	BaseResp
	Id          int64  `json:"id"`
	FeeRate     int64  `json:"feeRate"`
	ServiceFee  int64  `json:"serviceFee"`
	RequiredFee int64  `json:"requiredFee"`
	InvoiceSig  []byte `json:"invoiceSig"`
}

type DeployContractAckReq struct {
	Id         int64  `json:"id"`
	Status     int    `json:"status"`
	DeployTxId string `json:"txId"`
	MoreData   []byte `json:"moreData"`
}

type DeployContractAckResp struct {
	BaseResp
	Id            int64 `json:"id"`
	Status        int   `json:"status"`
	EnableBlock   int   `json:"enableBlock"`
	EnableBlockL1 int   `json:"enableBlockL1"`
}
type PerformContractRequest struct {
	MsgHeader
	ChannelId    string `json:"channel"`
	ContractName string `json:"contractName"`
	PerformParam string `json:"performParam"`
	InvoiceSig   []byte `json:"invoiceSig"`
	ReqTime      int64  `json:"reqTime"`
	PubKey       []byte `json:"pubKey"`
}

type PerformContractReq struct {
	PerformContractRequest
	Sig []byte `json:"msgSig"`
}

type PerformContractResp struct { // 是否同意更新状态
	BaseResp
	Id         int64  `json:"id"`
	InvoiceSig []byte `json:"invoiceSig"`
}

type PerformContractAckReq struct {
	Id int64 `json:"id"`
}

type PerformContractAckResp struct {
	BaseResp
	Id     int64 `json:"id"`
	Status int   `json:"status"`
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

type TxSignInfo struct {
	Tx        string   `json:"tx"`
	L1Tx      bool     `json:"l1Tx"`
	LocalSigs [][]byte `json:"sigs"`
}

// sn -> bn messages
type RemoteSignMoreData_Contract struct {
	Tx                []*TxSignInfo `json:"tx1"`
	LocalPubKey       []byte        `json:"pubkey"`
	Witness           []byte        `json:"witness"`
	ContractURL       string        `json:"contractURL"`
	InvokeCount       int64         `json:"invokeCount"`
	StaticMerkleRoot  []byte        `json:"staticMerkleRoot"`
	RuntimeMerkleRoot []byte        `json:"runtimeMerkleRoot"`
	Action            string        `json:"action"`
	MoreData          []byte        `json:"more"` // 有时候Tx中无法放入足够数据
}
type RemoteSignMoreData_Ascend struct {
	Tx          *TxSignInfo `json:"tx1"`
	LocalPubKey []byte      `json:"pubkey"`
	Witness     []byte      `json:"witness"`
	MoreData    []byte      `json:"more"` // 有时候Tx中无法放入足够数据
}
type RemoteSignMoreData_Msg struct {
	LocalPubKey []byte `json:"pubkey"`
	Action      string `json:"action"`
	Data        []byte `json:"data"`
}
type SignRequest struct {
	MsgHeader
	ChannelId    string   `json:"channel"`
	CommitHeight int      `json:"commitHeight"` // -1: 合约
	Sig          [][]byte `json:"sig"`
	Reason       string   `json:"reason"`
	MoreData     []byte   `json:"more"`
	PubKey       []byte   `json:"pubKey"`
}

type SignReq struct {
	SignRequest
	Sig []byte `json:"msgSig"`
}

type SignResp struct {
	BaseResp
	TxSig [][][]byte `json:"txSig"`
}
