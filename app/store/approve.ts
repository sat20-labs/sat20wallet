import { ref, computed } from 'vue'
import { Message } from '@/types/message'

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
}

const state = ref<ApproveState>({
  currentRequest: null,
  isVisible: false
})

export const useApproveStore = () => {
  const currentRequest = computed(() => state.value.currentRequest)
  const isVisible = computed(() => state.value.isVisible)

  const showApprove = (request: Omit<ApproveRequest, 'id'> & { id?: string }) => {
    const id = request.id || `approve_${Date.now()}_${Math.random().toString(36).substr(2, 9)}`
    state.value.currentRequest = { ...request, id }
    state.value.isVisible = true

    return new Promise<any>((resolve, reject) => {
      if (state.value.currentRequest) {
        state.value.currentRequest.resolve = resolve
        state.value.currentRequest.reject = reject
      }
    })
  }

  const confirm = (result: any) => {
    if (state.value.currentRequest?.resolve) {
      state.value.currentRequest.resolve(result)
    }
    hideApprove()
  }

  const reject = (error?: Error) => {
    if (state.value.currentRequest?.reject) {
      state.value.currentRequest.reject(error || new Error('User rejected'))
    }
    hideApprove()
  }

  const hideApprove = () => {
    state.value.isVisible = false
    state.value.currentRequest = null
  }

  return {
    currentRequest,
    isVisible,
    showApprove,
    confirm,
    reject,
    hideApprove
  }
}