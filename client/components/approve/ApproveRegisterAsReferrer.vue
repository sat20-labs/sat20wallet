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
    </div>
  </LayoutApprove>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import LayoutApprove from '@/components/layout/LayoutApprove.vue'
import { useToast } from '@/components/ui/toast'
import stp from '@/utils/stp'
import { useWalletStore } from '@/store/wallet'
import { storeToRefs } from 'pinia'
import { storage } from 'wxt/storage'

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

const walletStore = useWalletStore()
const { address } = storeToRefs(walletStore)

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
  try {
    const [err, res] = await stp.registerAsReferrer(
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
    } else {
      // 保存注册的name到storage
      if (address.value) {
        const key = `local:referrer_names_${address.value}` as const
        let names = await storage.getItem<string[]>(key)
        if (!names) names = []
        if (!names.includes(props.data.name)) {
          names.push(props.data.name)
          await storage.setItem(key, names)
        }
      }
      emit('confirm', { txId: res })
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