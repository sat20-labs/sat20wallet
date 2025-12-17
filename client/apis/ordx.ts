import { config as configMap } from '@/config'
import { walletStorage } from '@/lib/walletStorage'
class OrdxApi {
  generatePath(path: string, network: string) {
    const env = walletStorage.getValue('env')
    const config = configMap[env]
    const BASE_URL = config.ordxBaseUrl
    return `${BASE_URL}${network === 'testnet' ? '/btc/testnet' : '/btc/mainnet'
      }/${path}`
  }

  async getUtxos({ address, network }: any): Promise<any> {
    const response = await fetch(
      this.generatePath(`allutxos/address/${address}`, network)
    )
    return response.json()
  }

  async getTxRaw({ txid, network }: any): Promise<any> {
    const response = await fetch(
      this.generatePath(`btc/rawtx/${txid}`, network)
    )
    return response.json()
  }

  async getPlainUtxos({ address, network }: any): Promise<any> {
    const response = await fetch(
      this.generatePath(`utxo/address/${address}/0`, network)
    )
    return response.json()
  }

  async getRareUtxos({ address, network }: any): Promise<any> {
    const response = await fetch(
      this.generatePath(`exotic/address/${address}`, network)
    )
    return response.json()
  }

  async getRecommendedFees({ network }: any): Promise<any> {
    const url = `https://apiprd.ordx.market/${network === 'livenet' ? '' : 'testnet/'
      }ordx/GetRecommendedFees`
    const response = await fetch(url)
    return response.json()
  }
  async getUtxo({ utxo, network }: any): Promise<any> {
    const response = await fetch(
      this.generatePath(`utxo/range/${utxo}`, network)
    )
    return response.json()
  }

  async getNsName({ name, network }: any): Promise<any> {
    const response = await fetch(this.generatePath(`ns/name/${name}`, network))
    return response.json()
  }

  async getAddressSummary({ address, network }: any): Promise<any> {
    const response = await fetch(
      this.generatePath(`v3/address/summary/${address}?type=client`, network)
    )
    return response.json()
  }

  async getNsListByAddress({ address, network }: any): Promise<any> {
    const response = await fetch(
      this.generatePath(`ns/address/${address}`, network)
    )
    return response.json()
  }
  async pushTx({ hex, network }: any) {
    const response = await fetch(this.generatePath(`btc/tx`, network), {
      method: 'POST',
      body: JSON.stringify({ SignedTxHex: hex }),
    })
    console.log('response', response)
    return response.json()
  }
  async getOrdxAddressHolders({
    address,
    ticker,
    network,
    start,
    limit,
  }: any): Promise<any> {
    const response = await fetch(
      this.generatePath(
        `v3/address/asset/${address}/${ticker}?start=${start}&limit=${limit}&test=12`,
        network
      )
    )
    return response.json()
  }

  async getOrdxNsUxtos({
    address,
    sub,
    network,
    page,
    pagesize,
  }: any): Promise<any> {
    const start = (page - 1) * pagesize
    const limit = pagesize
    const response = await fetch(
      this.generatePath(
        `ns/address/${address}/${sub}?start=${start}&limit=${limit}`,
        network
      )
    )
    return response.json()
  }
  async getMinerInfo({ pubkey, network }: any): Promise<any> {
    const response = await fetch(
      this.generatePath(`v3/miner/info/${pubkey}`, network)
    )
    return response.json()
  }



  async getReferreeByName({ name, network, start, limit }: any): Promise<any> {
    const response = await fetch(
      this.generatePath(
        `v3/referree/${name}?start=${start}&limit=${limit}`,
        network
      )
    )
    return response.json()
  }

  // --- UTXO Management for Ordinals ---
  async getLockedUtxos({ address, network }: any): Promise<any> {
    const response = await fetch(
      this.generatePath(`v3/utxos/locked/${address}`, network)
    )
    return response.json()
  }
  async getBTCPrice({ network }: any) {
    const res = await fetch('https://apiprd.ordx.market/ordx/GetBTCPrice');
    return res;
  };
  async unlockOrdinals({ utxos, pubKey, sig, network }: any): Promise<any> {
    const response = await fetch(
      this.generatePath(`v3/utxo/unlock`, network),
      {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({
          utxos,
          pubKey, // Use hex string directly
          sig, // Use hex string directly
        }),
      }
    )
    return response.json()
  }
}

export default new OrdxApi()
