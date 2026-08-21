package wire

const QUERY_INFO_CHANNEL_FEE string = "/info/channel/fee"

type OpenChannelFee struct {
	ManageFee         int64 `json:"manageFee"`
	MortgageFee       int64 `json:"mortgageFee"`
	MinReserveSats    int64 `json:"minReserveSats"`
	CommitmentFee     int64 `json:"commitmentFee"`
	CommitmentFeeRate int64 `json:"commitmentFeeRate"`
	SplicingInFee     int64 `json:"splicingInFee"`
	SplicingOutFee    int64 `json:"splicingOutFee"`
}

// ChannelOpenFeeInfo is the read-only fee and funding preview returned by a
// service node before a channel is opened.  The fee configuration is the
// server's source of truth; the amount fields are populated by the SDK when a
// client asks for a local funding preview.
type ChannelOpenFeeInfo struct {
	OpenFee             *OpenChannelFee `json:"openFee"`
	OpenFeeTotal        int64           `json:"openFeeTotal"`
	FeeToDAO            int64           `json:"feeToDao"`
	MinCapacity         int64           `json:"minCapacity"`
	MinAvailableValue   int64           `json:"minAvailableValue"`
	Amount              int64           `json:"amount,omitempty"`
	ChannelCapacity     int64           `json:"channelCapacity,omitempty"`
	EstimatedNetworkFee int64           `json:"estimatedNetworkFee,omitempty"`
	RequiredInputSats   int64           `json:"requiredInputSats,omitempty"`
	SelectedInputSats   int64           `json:"selectedInputSats,omitempty"`
	ChangeSats          int64           `json:"changeSats,omitempty"`
	Valid               bool            `json:"valid"`
	ValidationError     string          `json:"validationError,omitempty"`
}

type ChannelOpenFeeResp struct {
	BaseResp
	Info *ChannelOpenFeeInfo `json:"info"`
}
