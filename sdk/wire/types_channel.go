package wire

type OpenChannelFee struct {
	ManageFee         int64 `json:"manageFee"`
	MortgageFee       int64 `json:"mortgageFee"`
	MinReserveSats    int64 `json:"minReserveSats"`
	CommitmentFee     int64 `json:"commitmentFee"`
	CommitmentFeeRate int64 `json:"commitmentFeeRate"`
	SplicingInFee     int64 `json:"splicingInFee"`
	SplicingOutFee    int64 `json:"splicingOutFee"`
}
