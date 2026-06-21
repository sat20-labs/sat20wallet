import { useGlobalStore } from '@/store/global'

class SmartContractApi {
  private generatePath(path: string, network: string) {
    const globalStore = useGlobalStore()
    const baseUrl = globalStore.config.satnetBaseUrl
    return `${baseUrl}/satsnet${network === 'testnet' ? '/testnet' : '/mainnet'}/v3/contracts${path}`
  }

  async getContracts({ network, start = 0, limit = 50 }: { network: string; start?: number; limit?: number }) {
    const response = await fetch(this.generatePath(`?start=${start}&limit=${limit}`, network))
    return response.json()
  }

  async getContract({ network, contract }: { network: string; contract: string }) {
    const response = await fetch(this.generatePath(`/${encodeURIComponent(contract)}`, network))
    return response.json()
  }

  async getContractState({ network, contract }: { network: string; contract: string }) {
    const response = await fetch(this.generatePath(`/${encodeURIComponent(contract)}/state`, network))
    return response.json()
  }

  async getContractHistory({
    network,
    contract,
    start = 0,
    limit = 20,
  }: {
    network: string
    contract: string
    start?: number
    limit?: number
  }) {
    const response = await fetch(this.generatePath(`/${encodeURIComponent(contract)}/history?start=${start}&limit=${limit}`, network))
    return response.json()
  }

  async getContractAnalytics({ network, contract }: { network: string; contract: string }) {
    const response = await fetch(this.generatePath(`/${encodeURIComponent(contract)}/analytics`, network))
    return response.json()
  }

  async reviewPredictionReady({
    network,
    contract,
    checkedAt,
  }: {
    network: string
    contract: Record<string, unknown>
    checkedAt?: number
  }) {
    const response = await fetch(this.generatePath('/prediction/review-ready', network), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        contract,
        checkedAt,
      }),
    })
    return response.json()
  }
}

export default new SmartContractApi()
