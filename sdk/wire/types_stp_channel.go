package wire

const (
	STP_FUNDING_REQ         string = "/funding/require"
	STP_FUNDING_CREATED     string = "/funding/fundingcreated"
	STP_FUNDING_BROADCASTED string = "/funding/fundingbroadcasted"

	STP_UNLOCK_REQ                   string = "/unlock/require"
	STP_UNLOCK_COMMITSIG             string = "/unlock/commitsig"
	STP_UNLOCK_REVOKEANDACK          string = "/unlock/revokeandack"
	STP_RECOVER_PAYMENT_REQ          string = "/recover/payment/require"
	STP_RECOVER_PAYMENT_COMMITSIG    string = "/recover/payment/commitsig"
	STP_RECOVER_PAYMENT_REVOKEANDACK string = "/recover/payment/revokeandack"
	STP_LOCK_REQ                     string = "/lock/require"
	STP_LOCK_SIGANDREVOKE            string = "/lock/sigandrevoke"
	STP_LOCK_ACK                     string = "/lock/ack"

	STP_CLOSE_REQ         string = "/close/require"
	STP_CLOSE_SIGNED      string = "/close/closingsigned"
	STP_CLOSE_BROADCASTED string = "/close/closingbroadcasted"

	STP_SPLICING_IN_REQ          string = "/splicingin/require"
	STP_SPLICING_IN_COMMITSIG    string = "/splicingin/commitsig"
	STP_SPLICING_IN_REVOKEANDACK string = "/splicingin/revokeandack"

	STP_SPLICING_OUT_REQ          string = "/splicingout/require"
	STP_SPLICING_OUT_COMMITSIG    string = "/splicingout/commitsig"
	STP_SPLICING_OUT_REVOKEANDACK string = "/splicingout/revokeandack"
)

type OpenChannelRequest struct {
	MsgHeader
	NodeId              []byte   `json:"nodeId"` //*btcec.PublicKey
	ChannelType         int      `json:"channelType"`
	ChannelWalletId     int      `json:"channelWalletId"` // subAccountId
	FundingKey          []byte   `json:"fundingKey"`      //*btcec.PublicKey
	FeeRate             int64    `json:"feeRate"`
	LocalFundingAmount  int64    `json:"localFundingAmount"`
	Outpoints           []string `json:"outpoints"`
	NeedSendFundingTx   bool     `json:"needSendFundingTx"`
	SkipOpeningAnchorTx bool     `json:"skipOpeningAnchorTx,omitempty"`
	L2DrainTxId         string   `json:"l2DrainTxId,omitempty"`
	Memo                []byte   `json:"memo"`
}

type AcceptChannel struct {
	Id                  int64           `json:"id"`
	CommitHeight        int             `json:"commitHeight"`
	CsvDelay            uint16          `json:"csv"`
	OpenFee             *OpenChannelFee `json:"openFee"`
	FundingKey          []byte          `json:"fundingKey"`   //*btcec.PublicKey
	RevocationBasePoint []byte          `json:"revbasePoint"` //*btcec.PublicKey
	CommitmentPoint     []byte          `json:"commitPoint"`  //*btcec.PublicKey
	InvoiceSig          []byte          `json:"invoiceSig"`
}

type FundingCreated struct {
	Id                  int64    `json:"id"`
	FundingPoint        string   `json:"fundingPoint"`
	RevocationBasePoint []byte   `json:"revbasePoint"` //*btcec.PublicKey
	CommitmentPoint     []byte   `json:"commitPoint"`  //*btcec.PublicKey
	CommitSig           [][]byte `json:"commitSig"`
	DeAnchorSig         [][]byte `json:"deAnchorSig"`
}

type FundingSigned struct {
	Id          int64    `json:"id"`
	CommitSig   [][]byte `json:"commitSig"`
	DeAnchorSig [][]byte `json:"deAnchorSig"`
}

type FundingBroadcasted struct {
	Id          int64  `json:"id"`
	FundingTxId string `json:"fundingTxId"`
}

type CloseChannelRequest struct {
	MsgHeader
	ChannelId    string `json:"channel"`
	CommitHeight int    `json:"commitHeight"`
	FeeRate      int64  `json:"feeRate,omitempty"`
	RevealKey    []byte `json:"revealKey,omitempty"`
	NodeId       []byte `json:"nodeId,omitempty"`
}

type ClosingSigned struct {
	Id int64 `json:"id"`

	DeAnchorSig [][]byte   `json:"deAnchorSig"`
	ClosgingSig [][]byte   `json:"closingsig"`
	PrevTxSig   [][][]byte `json:"prevTxSig"`
}

type ClosingBroadcasted struct {
	Id           int64  `json:"id"`
	DeAnchorTxId string `json:"deAnchorTxId"`
}

// open protocol
type ChannelOpenReq struct {
	OpenChannelRequest
	Sig []byte `json:"msgSig"`
}

type ChannelOpenResp struct {
	BaseResp
	AcceptChannel
}

type FundingCreatedReq struct {
	FundingCreated
	Sig []byte `json:"msgSig,omitempty"`
}

type FundingCreatedResp struct {
	BaseResp
	FundingSigned
}

type FundingBroadcastedReq struct {
	FundingBroadcasted
	Sig []byte `json:"msgSig,omitempty"`
}

type FundingBroadcastedResp struct {
	BaseResp
}

// close protocol
type ChannelCloseReq struct {
	CloseChannelRequest
	Sig []byte `json:"msgSig"`
}

type ChannelCloseResp struct {
	BaseResp
	ClosingSigned
}

type ClosingSignedReq struct {
	ClosingSigned
	ChannelId string `json:"channel"`
	Sig       []byte `json:"msgSig,omitempty"`
}

type ClosingSignedResp struct {
	BaseResp
	SplicingTxId string `json:"splicingTxId"`
}

type ClosingBroadcastedReq struct {
	ClosingBroadcasted
	ChannelId string `json:"channel"`
	Sig       []byte `json:"msgSig,omitempty"`
}

type ClosingBroadcastedResp struct {
	BaseResp
}

type LockRequest struct {
	MsgHeader
	ChannelId      string   `json:"channel"`
	CommitHeight   int      `json:"commitHeight"`
	AssetName      string   `json:"assetName"`
	Amt            string   `json:"amt"`
	FeeRate        int64    `json:"feeRate"`
	LockUtxos      []string `json:"lockUtxos"`
	FeeUtxos       []string `json:"feeUtxos"`
	RevealKey      []byte   `json:"revealKey"`
	RevKey         []byte   `json:"rev"`
	NextRevKey     []byte   `json:"nextRevKey"`
	NeedSendLockTx bool     `json:"needSendLockTx"`
	Memo           []byte   `json:"memo"`
	Reason         string   `json:"reason"`
	MoreData       []byte   `json:"more"`
	NodeId         []byte   `json:"nodeId,omitempty"`
}

type LockReq struct {
	LockRequest
	Sig []byte `json:"msgSig"`
}

type CommitSigInfo struct {
	CommitSig            [][]byte     `json:"commitSig"`
	CommitDeAnchorSig    [][]byte     `json:"commitDeAnchorSig"`
	CommitPrevTxSig      [][][]byte   `json:"commitPrevTxSig"`
	CommitNextTxSig      [][][]byte   `json:"commitNextTxSig"`
	CommitOtherPrevTxSig [][][][]byte `json:"commitOtherPrevTxSig,omitempty"`
	CommitOtherTxSig     [][][]byte   `json:"commitOtherTxSig,omitempty"`
}

type LockResp struct {
	BaseResp
	Id      int64 `json:"id"`
	FeeRate int64 `json:"feeRate"`

	// CommitSig
	CommitSigInfo
	RevKey     []byte `json:"rev"`
	NextRevKey []byte `json:"nextRevKey"`
}

type LockCommitSigAndRevokeReq struct { // CommitSig and RevokeAndAck
	ChannelId string `json:"channel"`
	Id        int64  `json:"id"`

	CommitSigInfo
	Rev *RevokeAndAck `json:"rev"`
	Sig []byte        `json:"msgSig,omitempty"`
}

type LockCommitSigAndRevokeResp struct { // RevokeAndAck
	BaseResp
	Id      int64         `json:"id"`
	Rev     *RevokeAndAck `json:"rev"`
	LockSig [][]byte      `json:"lockSig"` // 拿到对方的rev后才能生成
}

type LockAckReq struct {
	ChannelId string   `json:"channel"`
	Id        int64    `json:"id"`
	LockSig   [][]byte `json:"lockSig"`
	Sig       []byte   `json:"msgSig,omitempty"`
}

type LockAckResp struct {
	BaseResp
	Id int64 `json:"id"`
}

type UnlockRequest struct {
	MsgHeader
	ChannelId    string   `json:"channel"`
	CommitHeight int      `json:"commitHeight"`
	AssetName    string   `json:"assetName"`
	Amt          []string `json:"amt"` // > 0 A->S
	FeeRate      int64    `json:"feeRate"`
	FeeUtxos     []string `json:"feeUtxos"`
	DestAddr     []string `json:"address"`
	Memo         []byte   `json:"memo"`
	Reason       string   `json:"reason"`
	MoreData     []byte   `json:"more"`
	NodeId       []byte   `json:"nodeId,omitempty"`
}

type UnlockReq struct {
	UnlockRequest
	Sig []byte `json:"msgSig"`
}

type UnlockResp struct { // 是否同意更新状态
	BaseResp
	Id         int64  `json:"id"`
	RevealKey  []byte `json:"revealKey"`
	RevKey     []byte `json:"rev"`
	NextRevKey []byte `json:"nextRevKey"`
	FeeRate    int64  `json:"feeRate"`
}

type RevokeAndAck struct {
	Revocation [32]byte `json:"revocation"` // the preimage to the revocation hash of the prior commitment tx.
	NextRevKey []byte   `json:"nextRevKey"` // the next commitment point for the next commitment tx
}

type UnlockCommitSigReq struct { // CommitSig
	ChannelId string `json:"channel"`
	Id        int64  `json:"id"`
	CommitSigInfo
	RevKey     []byte `json:"rev"`
	NextRevKey []byte `json:"nextRevKey"`
	Sig        []byte `json:"msgSig,omitempty"`
}

type UnlockCommitSigResp struct { // 包含 RevokeAndAck 和 CommitSig
	BaseResp
	Id int64 `json:"id"`
	CommitSigInfo
	Rev *RevokeAndAck `json:"rev"`
}

type UnlockRevokeAndAckReq struct { // 包含 RevokeAndAck
	ChannelId string        `json:"channel"`
	Id        int64         `json:"id"`
	Rev       *RevokeAndAck `json:"rev"`
	UnlockSig [][]byte      `json:"unlockSig"` // 拿到对方的rev后才能生成
	Sig       []byte        `json:"msgSig,omitempty"`
}

type UnlockRevokeAndAckResp struct {
	BaseResp
	Id        int64    `json:"id"`
	UnlockSig [][]byte `json:"unlockSig"`
}

type RecoverPaymentRequest struct {
	MsgHeader
	ChannelId    string `json:"channel"`
	CommitHeight int    `json:"commitHeight"`
	PaymentTxId  string `json:"paymentTxId"`
	Reason       string `json:"reason"`
	NodeId       []byte `json:"nodeId,omitempty"`
}

type RecoverPaymentRequireReq struct {
	RecoverPaymentRequest
	Sig []byte `json:"msgSig"`
}

type RecoverPaymentRequireResp struct {
	BaseResp
	Id         int64  `json:"id"`
	RevealKey  []byte `json:"revealKey"`
	RevKey     []byte `json:"rev"`
	NextRevKey []byte `json:"nextRevKey"`
	FeeRate    int64  `json:"feeRate"`
}

type RecoverPaymentCommitSigReq struct {
	ChannelId string `json:"channel"`
	Id        int64  `json:"id"`

	CommitSigInfo
	RevKey     []byte `json:"rev"`
	NextRevKey []byte `json:"nextRevKey"`
	Sig        []byte `json:"msgSig,omitempty"`
}

type RecoverPaymentCommitSigResp struct {
	BaseResp
	Id int64 `json:"id"`
	CommitSigInfo
	Rev *RevokeAndAck `json:"rev"`
}

type RecoverPaymentRevokeAndAckReq struct {
	ChannelId  string        `json:"channel"`
	Id         int64         `json:"id"`
	Rev        *RevokeAndAck `json:"rev"`
	PaymentSig [][]byte      `json:"paymentSig"`
	Sig        []byte        `json:"msgSig,omitempty"`
}

type RecoverPaymentRevokeAndAckResp struct {
	BaseResp
	Id         int64    `json:"id"`
	PaymentSig [][]byte `json:"paymentSig"`
}

// splicing in

type SplicingInRequest struct {
	MsgHeader
	ChannelId          string   `json:"channel"`
	CommitHeight       int      `json:"commitHeight"`
	AssetName          string   `json:"assetName"`
	Amt                string   `json:"amt"`
	Stub               string   `json:"stub,omitempty"`
	Utxos              []string `json:"utxos"`
	Fees               []string `json:"fees"`
	PreTxInputs        []string `json:"preTxInputs"`
	RevealKey          []byte   `json:"revealKey"`
	NeedSendSplicingTx bool     `json:"needSendSplicingTx"`
	FeeRate            int64    `json:"feeRate"`
	Reason             string   `json:"reason"`
	Memo               []byte   `json:"memo"`
	NodeId             []byte   `json:"nodeId,omitempty"`
}

type SplicingInReq struct {
	SplicingInRequest
	Sig []byte `json:"msgSig"`
}

type SplicingInResp struct { // 是否同意更新状态
	BaseResp
	Id               int64  `json:"id"`
	ServiceFee       int64  `json:"serviceFee"`
	RevKey           []byte `json:"rev"`
	NextRevKey       []byte `json:"nextRevKey"`
	NewCapacity      int64  `json:"newCapacity"`
	NewLocalBalance  string `json:"newLocalBalance"`
	NewRemoteBalance string `json:"newRemoteBalance"`
	InvoiceSig       []byte `json:"invoiceSig"`
}

type SplicingSigInfo struct {
	SplicingPrevTxSig [][][]byte `json:"splicingPrevTxSig"`
	SplicingSig       [][]byte   `json:"splicingSig"`
	DeAnchorSig       [][]byte   `json:"deAnchorSig"` // or anchor sig
}

type SplicingInCommitSigReq struct { // CommitSig
	ChannelId string `json:"channel"`
	Id        int64  `json:"id"`
	SplicingSigInfo
	CommitSigInfo
	RevKey     []byte `json:"rev"`
	NextRevKey []byte `json:"nextRevKey"`
	Sig        []byte `json:"msgSig,omitempty"`
}

type SplicingInCommitSigResp struct { // 包含 RevokeAndAck 和 CommitSig
	BaseResp
	Id int64 `json:"id"`
	SplicingSigInfo
	CommitSigInfo
	Rev *RevokeAndAck `json:"rev"`
}

type SplicingInRevokeAndAckReq struct { // 包含 RevokeAndAck
	ChannelId    string        `json:"channel"`
	Id           int64         `json:"id"`
	SplicingTxId string        `json:"txId"`
	Rev          *RevokeAndAck `json:"rev"`
	Sig          []byte        `json:"msgSig,omitempty"`
}

type SplicingInRevokeAndAckResp struct { // 仅仅简单应答
	BaseResp
	Id int64 `json:"id"`
}

// splicing out
type SplicingOutRequest struct {
	MsgHeader
	ChannelId    string   `json:"channel"`
	CommitHeight int      `json:"commitHeight"`
	AssetName    string   `json:"assetName"`
	Amt          string   `json:"amt"`
	Stub         string   `json:"stub"`
	Utxos        []string `json:"utxos"`
	Fees         []string `json:"fees"`
	PreTxInputs  []string `json:"preTxInputs"`
	RevealKey    []byte   `json:"revealKey"`
	DestAddr     string   `json:"address"`
	FeeRate      int64    `json:"feeRate"`
	Reason       string   `json:"reason"`
	Memo         []byte   `json:"memo"`
	NodeId       []byte   `json:"nodeId,omitempty"`
}

type SplicingOutReq struct {
	SplicingOutRequest
	Sig []byte `json:"msgSig"`
}

type SplicingOutResp struct { // 是否同意更新状态
	BaseResp
	Id               int64  `json:"id"`
	ServiceFee       int64  `json:"serviceFee"`
	RevKey           []byte `json:"rev"`
	NextRevKey       []byte `json:"nextRevKey"`
	NewCapacity      int64  `json:"newCapacity"`
	NewLocalBalance  string `json:"newLocalBalance"`
	NewRemoteBalance string `json:"newRemoteBalance"`
}

type SplicingOutCommitSigReq struct { // CommitSig
	ChannelId string `json:"channel"`
	Id        int64  `json:"id"`
	SplicingSigInfo
	CommitSigInfo
	RevKey     []byte `json:"rev"`
	NextRevKey []byte `json:"nextRevKey"`
	Sig        []byte `json:"msgSig,omitempty"`
}

type SplicingOutCommitSigResp struct { // 包含 RevokeAndAck 和 CommitSig
	BaseResp
	Id int64 `json:"id"`
	SplicingSigInfo
	CommitSigInfo
	Rev *RevokeAndAck `json:"rev"`
}

type SplicingOutRevokeAndAckReq struct { // 包含 RevokeAndAck
	ChannelId    string        `json:"channel"`
	Id           int64         `json:"id"`
	DeAnchorTxId string        `json:"deAnchorTxId"`
	Rev          *RevokeAndAck `json:"rev"`
	Sig          []byte        `json:"msgSig,omitempty"`
}

type SplicingOutRevokeAndAckResp struct { // 仅仅简单应答
	BaseResp
	Id int64 `json:"id"`
}
