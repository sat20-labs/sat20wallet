<template>
  <LayoutApprove @confirm="confirm" @cancel="cancel">
    <div class="p-4 space-y-4">
      <h2 class="text-2xl font-semibold text-center">{{ $t('registerAsReferrer.title', '注册推荐人') }}</h2>
      <p class="text-xs text-gray-400 text-center mb-4">
        {{ $t('registerAsReferrer.warning', '请确认以下推荐人注册信息，确认后将发起注册') }}
      </p>

      <div class="space-y-4">
        <!-- Basic Info Section -->
        <div class="bg-muted/50 rounded-lg p-4 space-y-3">
          <div class="flex items-center justify-between">
            <span class="text-sm text-muted-foreground">{{ $t('registerAsReferrer.name', '推荐人名称') }}</span>
            <span class="font-medium">{{ props.data?.name || '-' }}</span>
          </div>
          <div class="flex items-center justify-between">
            <span class="text-sm text-muted-foreground">{{ $t('registerAsReferrer.feeRate', '费率') }}</span>
            <span class="font-medium">{{ props.data?.feeRate || '-' }}%</span>
          </div>
        </div>
      </div>

      <div v-if="isLoading" class="text-center text-muted-foreground mt-4">
        <span class="animate-spin inline-block mr-2">⏳</span> {{ $t('registerAsReferrer.registering', '正在注册...') }}
      </div>
      <div v-if="registerError && !isLoading" class="text-center text-destructive mt-4">
        {{ registerError }}
      </div>
      <div v-if="registerSuccess && !isLoading" class="text-center text-green-400 mt-4">
        <div class="space-y-2">
          <div class="text-lg font-medium">注册成功！</div>
          <div class="flex items-center justify-center gap-2">
            <span class="text-sm">交易ID:</span>
            <button 
              @click="handleMempoolClick(registerTxId)"
              class="text-primary hover:text-primary/80 underline text-left"
              :title="`查看交易 ${registerTxId}`"
            >
              {{ shortenTxId(registerTxId) }}
            </button>
            <Icon icon="lucide:external-link" class="w-3 h-3 text-primary" />
          </div>
        </div>
      </div>
    </div>
  </LayoutApprove>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import LayoutApprove from '@/components/layout/LayoutApprove.vue'
import { useToast } from '@/components/ui/toast-new'
import sat20 from '@/utils/sat20'
import { useWalletStore } from '@/store/wallet'
import { storeToRefs } from 'pinia'
import { useReferrerManager } from '@/composables/useReferrerManager'
import { useGlobalStore } from '@/store/global'
import { generateMempoolUrl } from '@/utils'

interface Props {
  data: {
    name: string;
    feeRate: number;
  }
}

const props = defineProps<Props>()
const emit = defineEmits(['confirm', 'cancel'])
const toast = useToast()

const isLoading = ref(false)
const registerError = ref('')
const registerSuccess = ref(false)
const registerTxId = ref('')

const walletStore = useWalletStore()
const globalStore = useGlobalStore()
const { address, network } = storeToRefs(walletStore)
const { env } = storeToRefs(globalStore)
const { addLocalReferrerName } = useReferrerManager()

// 缩短显示txId
function shortenTxId(txId: string, startLength = 8, endLength = 8): string {
  if (!txId || txId.length <= startLength + endLength) {
    return txId
  }
  return `${txId.slice(0, startLength)}...${txId.slice(-endLength)}`
}

// 处理点击mempool链接
function handleMempoolClick(txId: string) {
  if (txId) {
    // 使用generateMempoolUrl生成mempool链接
    const mempoolUrl = generateMempoolUrl({
      network: network.value,
      path: `tx/${txId}`,
    })
    
    // 在新标签页中打开mempool链接
    window.open(mempoolUrl, '_blank', 'noopener,noreferrer')
  }
}

const confirm = async () => {
  if (!props.data?.name || typeof props.data?.feeRate !== 'number') {
    toast.toast({
      title: '参数缺失',
      description: '推荐人名称或费率缺失',
      variant: 'destructive',
    })
    return
  }
  isLoading.value = true
  registerError.value = ''
  registerSuccess.value = false
  registerTxId.value = ''
  try {
    const [err, res] = await sat20.registerAsReferrer(
      props.data.name,
      props.data.feeRate
    )
    if (err) {
      registerError.value = err.message || '注册失败'
      toast.toast({
        title: '注册失败',
        description: err.message,
        variant: 'destructive',
      })
    } else if (res && res.txId) {
      // 只有存在txId才表示注册成功
      registerSuccess.value = true
      registerTxId.value = res.txId
      // 使用推荐人管理器保存注册的name到本地存储
      if (address.value) {
        await addLocalReferrerName(address.value, props.data.name)
      }
      emit('confirm', { txId: res.txId })
    } else {
      // 没有错误但没有txId，表示注册失败
      registerError.value = '注册失败：未获取到交易ID'
      toast.toast({
        title: '注册失败',
        description: '未获取到交易ID',
        variant: 'destructive',
      })
    }
  } catch (e: any) {
    registerError.value = e?.message || '注册异常'
    toast.toast({
      title: '注册异常',
      description: e?.message,
      variant: 'destructive',
    })
  } finally {
    isLoading.value = false
  }
}

const cancel = () => {
  emit('cancel')
}
</script> 