<template>
  <LayoutApprove @confirm="confirm" @cancel="cancel">
    <div class="p-4">
      <h2 class="text-2xl font-semibold text-center mb-4">{{ $t('signMessage.title') }}</h2>
      <p class="text-xs text-gray-400 text-center mb-2">
        {{ $t('signMessage.warning') }}
      </p>
      <p class="text-center text-base mb-2">{{ $t('signMessage.signing') }}</p>
      <Alert>
        <AlertTitle class="text-center text-base break-all">{{ props.data.message }}</AlertTitle>
      </Alert>
    </div>
  </LayoutApprove>
</template>

<script setup lang="ts">
import LayoutApprove from '@/components/layout/LayoutApprove.vue'
import { Alert, AlertTitle } from '@/components/ui/alert'
import walletManager from '@/utils/sat20'
import { useWalletStore } from '@/store'
import { storeToRefs } from 'pinia'
import service from '@/lib/service'

interface Props {
  data: any
}

const props = defineProps<Props>()
const emit = defineEmits(['confirm', 'cancel'])

const walletStore = useWalletStore()
const { network } = storeToRefs(walletStore)

const confirm = async () => {
  // await walletStore.setNetwork(props.data.network)
  const [err, res] = await walletManager.signMessage(props.data.message)
  console.log(err, res);
  if (res?.signature) {
    emit('confirm', res?.signature)
  }
  
}
const cancel = () => {
  emit('cancel')
}
</script>

<style lang="less" scoped></style>
