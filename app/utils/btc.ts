// import { bitcoin } from '@/lib/bitcoinjs'
import walletManager from '@/utils/sat20'
export const psbt2tx = async (psbtHex: string) => {
  const [err, res] = await walletManager.extractTxFromPsbt(psbtHex)
  console.log('res', res)
  if (err) {
    console.error(err)
    return null
  }
  return res
}
