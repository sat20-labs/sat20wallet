import { ref, computed } from 'vue'
import { Message } from '@/types/message'
import { Network } from '@/types'
import { BoundedApprovalQueue } from '@/lib/approval-queue'
import { assertWalletIdentityReady, subscribeWalletIdentity } from '@/lib/identity-boundary'
import { walletRequestSessionGuard } from '@/lib/walletSession'

export interface ApproveRequest {
  id: string
  action: Message.MessageAction
  data?: any
  metadata?: any
  resolve?: (result: any) => void
  reject?: (error: Error) => void
}

interface ApproveState {
  currentRequest: ApproveRequest | null
  isVisible: boolean
  queueSize: number
}

const state = ref<ApproveState>({
  currentRequest: null,
  isVisible: false,
  queueSize: 0,
})

const queue = new BoundedApprovalQueue<ApproveRequest>({
  onChange: () => {
    const current = queue.current
    state.value.currentRequest = current?.request ?? null
    state.value.isVisible = Boolean(current)
    state.value.queueSize = queue.size
  },
})

subscribeWalletIdentity((identity) => {
  if (identity.phase !== 'READY') {
    queue.rejectAll(new Error('Wallet identity changed; pending approvals were cancelled'))
  }
})

export const useApproveStore = () => {
  const currentRequest = computed(() => state.value.currentRequest)
  const isVisible = computed(() => state.value.isVisible)
  const queueSize = computed(() => state.value.queueSize)

  const showApprove = async (request: Omit<ApproveRequest, 'id'> & { id?: string }) => {
    walletRequestSessionGuard('approve')()
    assertWalletIdentityReady(request.metadata?.identityGeneration)
    const id = request.id || `approve_${Date.now()}_${crypto.randomUUID()}`
    const origin = String(request.metadata?.origin ?? '')
    const approvalRequest: ApproveRequest = { ...request, id }
    return queue.enqueue(id, origin, approvalRequest, request.metadata?.expiresAt)
  }

  const confirm = (id: string, result: any) => {
    queue.confirm(id, result)
  }

  const assertCurrent = (id: string) => {
    const entry = queue.current
    if (!entry || entry.id !== id) throw new Error('Approval request is no longer current')
    assertWalletIdentityReady(entry.request.metadata?.identityGeneration)
    walletRequestSessionGuard('approve')()
  }

  const executeNetworkSwitch = (id: string, switchNetwork: (network: Network) => Promise<boolean>) => {
    const entry = queue.current
    if (!entry || entry.id !== id || entry.request.action !== Message.MessageAction.SWITCH_NETWORK) {
      throw new Error('Network switch approval is no longer current')
    }
    const target = entry.request.data?.network
    if (typeof target !== 'string' || !Object.values(Network).includes(target as Network)) throw new Error('Invalid target network')
    const generation = assertWalletIdentityReady(entry.request.metadata?.identityGeneration)
    walletRequestSessionGuard('approve')()
    queue.execute(id, async () => {
      assertWalletIdentityReady(generation)
      queue.rejectAll(new Error('Wallet network is changing; pending approvals were cancelled'))
      if (!await switchNetwork(target as Network)) throw new Error('Wallet network switch did not complete')
      assertWalletIdentityReady()
      return target
    })
  }

  const reject = (id: string, error?: Error) => {
    queue.reject(id, error)
  }

  const hideApprove = () => {
    const id = state.value.currentRequest?.id
    if (id) queue.reject(id, new Error('Wallet approval dialog closed'))
  }

  return {
    currentRequest,
    isVisible,
    queueSize,
    showApprove,
    confirm,
    assertCurrent,
    executeNetworkSwitch,
    reject,
    hideApprove
  }
}
