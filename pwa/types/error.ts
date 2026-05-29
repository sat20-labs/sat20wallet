export enum WalletErrorCode {
  NO_WALLET,
  USER_REJECT,
}

export enum WalletErrorMessage {
  NO_WALLET = 'No wallet',
  REJECT = 'User rejected',
}

class WalletError {
  noWallet = {
    code: WalletErrorCode.NO_WALLET,
    message: WalletErrorMessage.NO_WALLET,
  }
  userReject = {
    code: WalletErrorCode.USER_REJECT,
    message: WalletErrorMessage.REJECT,
  }
}
export const walletError = new WalletError()
