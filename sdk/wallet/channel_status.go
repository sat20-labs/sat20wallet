package wallet

type ChannelType int

const (
	CV_INIT int = 0

	CT_NORMAL ChannelType = 0
)

type ChannelStatus int

const (
	CS_CLOSED_UNEXPECTED ChannelStatus = -2
	CS_CLOSED_FORCELY    ChannelStatus = -1

	CS_UNKNOWN ChannelStatus = 0
	CS_CLOSED  ChannelStatus = 0

	// 1-7 funding
	CS_FUNDING_BROADCASTED ChannelStatus = 1
	CS_FUNDING_CONFIRMED   ChannelStatus = 2
	CS_ANCHOR_BROADCASTED  ChannelStatus = 3
	CS_ANCHOR_CONFIRMED    ChannelStatus = 4

	// 8-15 closing
	CS_CLOSING_STARTED              ChannelStatus = 7
	CS_CLOSING_DEANCHOR_BROADCASTED ChannelStatus = 8
	CS_CLOSING_DEANCHOR_CONFIRMED   ChannelStatus = 9
	CS_CLOSING_BROADCASTED          ChannelStatus = 10 // a
	CS_CLOSING_CONFIRMED            ChannelStatus = 11 // b

	CS_CLOSE_FORCELY_BROADCASTED       ChannelStatus = 12 // c
	CS_CLOSE_FORCELY_CONFIRMED         ChannelStatus = 13 // d
	CS_CLOSE_FORCELY_SWEEP_BROADCASTED ChannelStatus = 14 // e
	CS_CLOSE_FORCELY_SWEEP_CONFIRMED   ChannelStatus = 15 // f

	CS_READY ChannelStatus = 16 // 0x10
)
