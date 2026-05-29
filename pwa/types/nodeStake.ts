// 节点质押临时数据的接口定义
export interface NodeStakeData {
  txId: string
  resvId: string
  assetName: string
  amt: string
  isCore: boolean
  createdAt: number // 创建时间戳
  expiresAt: number // 过期时间戳
}