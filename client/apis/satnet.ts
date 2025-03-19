class SatnetApi {
  generatePath(path: string, network: string) {
    const BASE_URL = import.meta.env.WXT_SAT20_URL
    return `${BASE_URL}/satsnet${
      network === 'testnet' ? '/testnet' : '/mainnet'
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
    console.log('address', address)
    console.log('network', network)
    const response = await fetch(
      this.generatePath(`ns/address/${address}`, network)
    )
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

export default new SatnetApi()
