<template>
  <LayoutApprove @confirm="confirm" @cancel="cancel">
    <div class="p-4 space-y-4">
      <h2 class="text-2xl font-semibold text-center">{{ $t('registerAsReferrer.title') }}</h2>
      <p class="text-xs text-gray-400 text-center mb-4">
        {{ $t('registerAsReferrer.warning') }}
      </p>

      <div class="space-y-4">
        <!-- Basic Info Section -->
        <div class="bg-muted/50 rounded-lg p-4 space-y-3">
          <div class="flex items-center justify-between">
            <span class="text-sm text-muted-foreground">{{ $t('registerAsReferrer.name') }}</span>
            <span class="font-medium">{{ props.data?.name || '-' }}</span>
          </div>
          <div class="flex items-center justify-between">
            <span class="text-sm text-muted-foreground">{{ $t('registerAsReferrer.feeRate') }}</span>
            <span class="font-medium">{{ props.data?.feeRate || '-' }}%</span>
          </div>
        </div>
      </div>

      <div v-if="isLoading" class="text-center text-muted-foreground mt-4">
        <span class="animate-spin inline-block mr-2">⏳</span> {{ $t('registerAsReferrer.registering') }}
      </div>
      <div v-if="registerError && !isLoading" class="text-center text-destructive mt-4">
        {{ registerError }}
      </div>
    </div>
  </LayoutApprove>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
const { t } = useI18n()
import { ref } from 'vue'
import LayoutApprove from '@/components/layout/LayoutApprove.vue'
import { useToast } from '@/components/ui/toast-new'
import sat20 from '@/utils/sat20'
import { useWalletStore } from '@/store/wallet'
import { storeToRefs } from 'pinia'
import { useReferrerManager } from '@/composables/useReferrerManager'

interface Props {
  data: {
    name: string;
    feeRate: string;
  }
}

const props = defineProps<Props>()
const emit = defineEmits(['confirm', 'cancel'])
const toast = useToast()

const isLoading = ref(false)
const registerError = ref('')

const walletStore = useWalletStore()
const { address } = storeToRefs(walletStore)
const { addLocalReferrerName } = useReferrerManager()

const confirm = async () => {
  if (!props.data?.name || typeof props.data?.feeRate !== 'string' ||
      !/^(0|[1-9][0-9]*)$/.test(props.data.feeRate)) {
    toast.toast({
      title: t('approvalUi.missingParameters'),
      description: t('approvalUi.missingReferrerParameters'),
      variant: 'destructive',
    })
    return
  }
  isLoading.value = true
  registerError.value = ''
  try {
    const [err, res] = await sat20.registerAsReferrer(
      props.data.name,
      props.data.feeRate
    )
    if (err) {
      registerError.value = err.message || t('settingsUi.registerFailed')
      toast.toast({
        title: t('settingsUi.registerFailed'),
        description: err.message,
        variant: 'destructive',
      })
    } else {
      // 使用推荐人管理器保存注册的name到本地存储
      if (address.value) {
        await addLocalReferrerName(address.value, props.data.name)
      }
      emit('confirm', { txId: res })
    }
  } catch (e: any) {
    registerError.value = e?.message || t('approvalUi.registerException')
    toast.toast({
      title: t('approvalUi.registerException'),
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
