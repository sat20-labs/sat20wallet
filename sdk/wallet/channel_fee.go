package wallet

import wwire "github.com/sat20-labs/sat20wallet/sdk/wire"

const MIN_AVAILABLE_VALUE int64 = 20000

type ChannelFeeConfig struct {
	ManageFee         int64
	MortgageFee       int64
	MinReserveSats    int64
	CommitmentFee     int64
	CommitmentFeeRate int64
	SplicingInFee     int64
	SplicingOutFee    int64
}

func NewOldFeeConfig() *ChannelFeeConfig {
	return &ChannelFeeConfig{
		ManageFee:         3000,
		MortgageFee:       0,
		MinReserveSats:    0,
		CommitmentFee:     3000,
		CommitmentFeeRate: 5,
		SplicingInFee:     0,
		SplicingOutFee:    2000,
	}
}

func NewFeeConfig() *ChannelFeeConfig {
	return &ChannelFeeConfig{
		ManageFee:         10000,
		MortgageFee:       0,
		MinReserveSats:    0,
		CommitmentFee:     10000,
		CommitmentFeeRate: 10,
		SplicingInFee:     0,
		SplicingOutFee:    2000,
	}
}

func NewFromOpenChannelFee(fee *wwire.OpenChannelFee) *ChannelFeeConfig {
	return &ChannelFeeConfig{
		ManageFee:         fee.ManageFee,
		MortgageFee:       fee.MortgageFee,
		MinReserveSats:    fee.MinReserveSats,
		CommitmentFee:     fee.CommitmentFee,
		CommitmentFeeRate: fee.CommitmentFeeRate,
		SplicingInFee:     fee.SplicingInFee,
		SplicingOutFee:    fee.SplicingOutFee,
	}
}

func (p *ChannelFeeConfig) ToOpenChannelFee() *wwire.OpenChannelFee {
	return &wwire.OpenChannelFee{
		ManageFee:         p.ManageFee,
		MortgageFee:       p.MortgageFee,
		MinReserveSats:    p.MinReserveSats,
		CommitmentFee:     p.CommitmentFee,
		CommitmentFeeRate: p.CommitmentFeeRate,
		SplicingInFee:     p.SplicingInFee,
		SplicingOutFee:    p.SplicingOutFee,
	}
}

func (p *ChannelFeeConfig) MinCapacity() int64 {
	return p.OpenFee() + MIN_AVAILABLE_VALUE
}

func (p *ChannelFeeConfig) OpenFee() int64 {
	return p.ManageFee + p.MortgageFee + p.CommitmentFee
}

func (p *ChannelFeeConfig) FeeToDAO() int64 {
	return p.ManageFee + p.MortgageFee - p.MinReserveSats
}

func (p *ChannelFeeConfig) Clone() *ChannelFeeConfig {
	return &ChannelFeeConfig{
		ManageFee:         p.ManageFee,
		MortgageFee:       p.MortgageFee,
		MinReserveSats:    p.MinReserveSats,
		CommitmentFee:     p.CommitmentFee,
		CommitmentFeeRate: p.CommitmentFeeRate,
		SplicingInFee:     p.SplicingInFee,
		SplicingOutFee:    p.SplicingOutFee,
	}
}
