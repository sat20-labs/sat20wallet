let mnemonicViewCount = 0

export const beginMnemonicView = () => {
  mnemonicViewCount += 1
  return () => {
    mnemonicViewCount = Math.max(0, mnemonicViewCount - 1)
  }
}

export const isMnemonicViewActive = () => mnemonicViewCount > 0

export const assertNoMnemonicView = () => {
  if (isMnemonicViewActive()) throw new Error('DApp requests are unavailable while recovery words are visible')
}

