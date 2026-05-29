import * as bitcoin from 'bitcoinjs-lib'
import ECPairFactory from 'ecpair'
import * as ecc from '@bitcoin-js/tiny-secp256k1-asmjs'
bitcoin.initEccLib(ecc)
var ECPair = ECPairFactory(ecc)
export { ECPair, bitcoin, ecc }
