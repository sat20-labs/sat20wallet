import { Buffer } from 'buffer'
import { bitcoin } from '@/lib/bitcoinjs'

const SIGHASH_SINGLE_ANYONECANPAY =
  bitcoin.Transaction.SIGHASH_SINGLE | bitcoin.Transaction.SIGHASH_ANYONECANPAY

type RawUtxoInfo = Record<string, any>

const parseJson = (value: unknown): RawUtxoInfo => {
  if (typeof value === 'string') {
    return JSON.parse(value)
  }
  if (value && typeof value === 'object') {
    return value as RawUtxoInfo
  }
  throw new Error('invalid utxo info')
}

const pickUtxo = (value: RawUtxoInfo): RawUtxoInfo => {
  return value.AssetsInUtxo && typeof value.AssetsInUtxo === 'object'
    ? { ...value.AssetsInUtxo, Price: value.Price }
    : value
}

const getOutpoint = (value: RawUtxoInfo): string => {
  const outpoint = value.Outpoint ?? value.OutPoint ?? value.outpoint
  if (typeof outpoint !== 'string' || !outpoint.includes(':')) {
    throw new Error('utxo info missing Outpoint')
  }
  return outpoint
}

const decodePkScript = (value: unknown): Buffer => {
  if (value instanceof Uint8Array) {
    return Buffer.from(value)
  }
  if (typeof value !== 'string' || value.length === 0) {
    throw new Error('utxo info missing PkScript')
  }
  if (/^[0-9a-fA-F]+$/.test(value) && value.length % 2 === 0) {
    return Buffer.from(value, 'hex')
  }
  return Buffer.from(value, 'base64')
}

const normalizePublicKey = (publicKey: string): Buffer => {
  const pubkey = Buffer.from(publicKey, 'hex')
  if (pubkey.length === 33) {
    return pubkey.subarray(1, 33)
  }
  if (pubkey.length === 32) {
    return pubkey
  }
  throw new Error('invalid public key for taproot input')
}

export const buildL1BatchSellOrder = (
  utxos: unknown[],
  address: string,
  networkName: string,
  publicKey: string,
) => {
  if (!Array.isArray(utxos) || utxos.length === 0) {
    throw new Error('utxos is required')
  }

  const network = networkName === 'mainnet'
    ? bitcoin.networks.bitcoin
    : bitcoin.networks.testnet
  const tapInternalKey = normalizePublicKey(publicKey)
  const psbt = new bitcoin.Psbt({ network })

  for (const raw of utxos) {
    const utxoInfo = pickUtxo(parseJson(raw))
    const [txid, vout] = getOutpoint(utxoInfo).split(':')
    const value = Number(utxoInfo.Value ?? utxoInfo.value)
    const price = Number(utxoInfo.Price ?? utxoInfo.price)

    if (!Number.isSafeInteger(value) || value <= 0) {
      throw new Error('utxo info has invalid Value')
    }
    if (!Number.isSafeInteger(price) || price <= 0) {
      throw new Error('utxo info has invalid Price')
    }

    psbt.addInput({
      hash: txid,
      index: Number(vout),
      witnessUtxo: {
        script: decodePkScript(utxoInfo.PkScript ?? utxoInfo.pkScript),
        value,
      },
      sighashType: SIGHASH_SINGLE_ANYONECANPAY,
      tapInternalKey,
    })

    psbt.addOutput({
      address,
      value: price,
    })
  }

  return { psbt: psbt.toHex() }
}
