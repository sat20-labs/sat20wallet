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

  async estimateEVMInvoke({
    network,
    contract,
    caller,
    calldataHex,
    sats,
    gasLimit,
    funding,
  }: {
    network: string
    contract: string
    caller: string
    calldataHex: string
    sats?: number
    gasLimit?: number
    funding?: Array<{ assetName: string; amount: string }>
  }) {
    const response = await fetch(this.generatePath(`/${encodeURIComponent(contract)}/evm/estimate-invoke`, network), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        caller,
        calldataHex,
        sats,
        gasLimit,
        funding: funding || [],
      }),
    })
    return response.json()
  }

  async estimateEVMDeploy({
    network,
    caller,
    initCodeHex,
    sats,
    gasLimit,
  }: {
    network: string
    caller: string
    initCodeHex: string
    sats?: number
    gasLimit?: number
  }) {
    const response = await fetch(this.generatePath('/evm/estimate-deploy', network), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        caller,
        initCodeHex,
        sats,
        gasLimit,
      }),
    })
    return response.json()
  }

  async getEVMCompilerConfig({ network }: { network: string }) {
    const response = await fetch(this.generatePath('/evm/compiler-config', network))
    return response.json()
  }

  async submitEVMSource({
    network,
    contract,
    metadata,
  }: {
    network: string
    contract: string
    metadata: Record<string, unknown>
  }) {
    const response = await fetch(this.generatePath(`/${encodeURIComponent(contract)}/evm/source`, network), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(metadata),
    })
    return response.json()
  }
}

export default new SmartContractApi()
