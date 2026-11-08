export enum ChannelStatus {
  CS_CLOSED = 0,

  // 1-7 funding
  CS_FUNDING_BROADCASTED = 1,
  CS_FUNDING_CONFIRMED = 2,
  CS_ANCHOR_BROADCASTED = 3,
  CS_ANCHOR_CONFIRMED = 4,

  // 8-15 closing
  CS_DEANCHOR_BROADCASTED = 8,
  CS_DEANCHOR_CONFIRMED = 9,
  CS_CLOSING_BROADCASTED = 10, // a
  CS_CLOSING_CONFIRMED = 11, // b

  CS_CLOSE_FORCELY_BROADCASTED = 12, // c
  CS_CLOSE_FORCELY_CONFIRMED = 13, // d
  CS_CLOSE_FORCELY_SWEEP_BROADCASTED = 14, // e
  CS_CLOSE_FORCELY_SWEEP_CONFIRMED = 15, // f

  CS_READY = 16, // 0x10
  CS_UNLOCK_BROADCASTED = 17, // 0x11
  CS_LOCK_BROADCASTED = 21, // 0x15

  CS_SPLICINGIN_BROADCASTED = 33, // 0x21
  CS_SPLICINGIN_CONFIRMED = 34, // 0x22
  CS_SPLICINGIN_ANCHOR_BROADCASTED = 35, // 0x23
  CS_SPLICINGIN_ANCHOR_CONFIRMED = 36, // 0x24

  CS_SPLICINGOUT_ANCHOR_BROADCASTED = 49, // 0x31
  CS_SPLICINGOUT_ANCHOR_CONFIRMED = 50, // 0x32
  CS_SPLICINGOUT_BROADCASTED = 51, // 0x33
  CS_SPLICINGOUT_CONFIRMED = 52, // 0x34

  CS_CLOSED_FORCELY = 256, // 0x100
  CS_CLOSED_UNEXPECTED = 512, // 0x200
}

export function getChannelStatusText(status: ChannelStatus, t: (key: string) => string): string {
  switch (status) {
    case ChannelStatus.CS_CLOSED:
      return t('walletComponents.channelStatusClosed')
    case ChannelStatus.CS_FUNDING_BROADCASTED:
      return t('walletComponents.channelStatusFundingBroadcasted')
    case ChannelStatus.CS_FUNDING_CONFIRMED:
      return t('walletComponents.channelStatusFundingConfirmed')
    case ChannelStatus.CS_ANCHOR_BROADCASTED:
      return t('walletComponents.channelStatusAnchorBroadcasted')
    case ChannelStatus.CS_ANCHOR_CONFIRMED:
      return t('walletComponents.channelStatusAnchorConfirmed')
    case ChannelStatus.CS_DEANCHOR_BROADCASTED:
      return t('walletComponents.channelStatusDeanchorBroadcasted')
    case ChannelStatus.CS_DEANCHOR_CONFIRMED:
      return t('walletComponents.channelStatusDeanchorConfirmed')
    case ChannelStatus.CS_CLOSING_BROADCASTED:
      return t('walletComponents.channelStatusClosingBroadcasted')
    case ChannelStatus.CS_CLOSING_CONFIRMED:
      return t('walletComponents.channelStatusClosingConfirmed')
    case ChannelStatus.CS_CLOSE_FORCELY_BROADCASTED:
      return t('walletComponents.channelStatusCloseForcelyBroadcasted')
    case ChannelStatus.CS_CLOSE_FORCELY_CONFIRMED:
      return t('walletComponents.channelStatusCloseForcelyConfirmed')
    case ChannelStatus.CS_CLOSE_FORCELY_SWEEP_BROADCASTED:
      return t('walletComponents.channelStatusCloseForcelySweepBroadcasted')
    case ChannelStatus.CS_CLOSE_FORCELY_SWEEP_CONFIRMED:
      return t('walletComponents.channelStatusCloseForcelySweepConfirmed')
    case ChannelStatus.CS_READY:
      return t('walletComponents.channelStatusReady')
    case ChannelStatus.CS_UNLOCK_BROADCASTED:
      return t('walletComponents.channelStatusUnlockBroadcasted')
    case ChannelStatus.CS_LOCK_BROADCASTED:
      return t('walletComponents.channelStatusLockBroadcasted')
    case ChannelStatus.CS_SPLICINGIN_BROADCASTED:
      return t('walletComponents.channelStatusSplicingInBroadcasted')
    case ChannelStatus.CS_SPLICINGIN_CONFIRMED:
      return t('walletComponents.channelStatusSplicingInConfirmed')
    case ChannelStatus.CS_SPLICINGIN_ANCHOR_BROADCASTED:
      return t('walletComponents.channelStatusSplicingInAnchorBroadcasted')
    case ChannelStatus.CS_SPLICINGIN_ANCHOR_CONFIRMED:
      return t('walletComponents.channelStatusSplicingInAnchorConfirmed')
    case ChannelStatus.CS_SPLICINGOUT_ANCHOR_BROADCASTED:
      return t('walletComponents.channelStatusSplicingOutAnchorBroadcasted')
    case ChannelStatus.CS_SPLICINGOUT_ANCHOR_CONFIRMED:
      return t('walletComponents.channelStatusSplicingOutAnchorConfirmed')
    case ChannelStatus.CS_SPLICINGOUT_BROADCASTED:
      return t('walletComponents.channelStatusSplicingOutBroadcasted')
    case ChannelStatus.CS_SPLICINGOUT_CONFIRMED:
      return t('walletComponents.channelStatusSplicingOutConfirmed')
    case ChannelStatus.CS_CLOSED_FORCELY:
      return t('walletComponents.channelStatusClosedForcely')
    case ChannelStatus.CS_CLOSED_UNEXPECTED:
      return t('walletComponents.channelStatusClosedUnexpectedly')
    default:
      return t('walletComponents.channelStatusUnknownStatus')
  }
}
