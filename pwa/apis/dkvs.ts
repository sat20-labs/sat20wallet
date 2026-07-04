import { useGlobalStore } from '@/store/global'

class DKVSApi {
  generatePath(path: string, network: string) {
    const globalStore = useGlobalStore()
    const config = globalStore.config
    const baseUrl = config.satnetBaseUrl
    return `${baseUrl}/satsnet${network === 'testnet' ? '/testnet' : '/mainnet'}/${path}`
  }

  async getRecord({ key, hash, network }: { key?: string; hash?: string; network: string }): Promise<any> {
    const params = new URLSearchParams()
    if (key) params.set('key', key)
    if (hash) params.set('hash', hash)
    const response = await fetch(this.generatePath(`v3/dkvs/records?${params.toString()}`, network))
    return response.json()
  }

  async listRecords({ prefix, start, limit, network }: { prefix: string; start: number; limit: number; network: string }): Promise<any> {
    const params = new URLSearchParams({
      prefix,
      start: String(start),
      limit: String(limit),
    })
    const response = await fetch(this.generatePath(`v3/dkvs/records/prefix?${params.toString()}`, network))
    return response.json()
  }

  async getCheckpoint({ network }: { network: string }): Promise<any> {
    const response = await fetch(this.generatePath('v3/dkvs/checkpoint', network))
    return response.json()
  }

  async putRecord({ record, network }: { record: unknown; network: string }): Promise<any> {
    const response = await fetch(this.generatePath('v3/dkvs/records', network), {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify(record),
    })
    return response.json()
  }

  async tombstone({ record, network }: { record: unknown; network: string }): Promise<any> {
    const response = await fetch(this.generatePath('v3/dkvs/tombstone', network), {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify(record),
    })
    return response.json()
  }
}

export default new DKVSApi()
