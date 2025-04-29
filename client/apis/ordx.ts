import { config as configMap } from '@/config'
import { walletStorage } from '@/lib/walletStorage'
class OrdxApi {
  generatePath(path: string, network: string) {
    const env = walletStorage.getValue('env')
    const config = configMap[env]
    const BASE_URL = config.ordxBaseUrl
    return `${BASE_URL}${
      network === 'testnet' ? '/btc/testnet' : '/btc/mainnet'
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
    const url = `https://apidev.ordx.market/${
      network === 'livenet' ? 'btc' : 'testnet/'
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
      this.generatePath(`v3/address/summary/${address}`, network)
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
        `v3/address/asset/${address}/${ticker}?start=${start}&limit=${limit}`,
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
}

export default new OrdxApi()
