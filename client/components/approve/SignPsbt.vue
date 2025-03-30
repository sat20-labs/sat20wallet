<template>
  <LayoutApprove @confirm="confirm" @cancel="cancel">
    <div class="p-4">
      <h2 class="text-2xl font-semibold text-center mb-4">Signature PSBT</h2>
      <p class="text-xs text-gray-400 text-center mb-2">
        Only sign this message if you fully understand the content and trust the
        requesting site.
      </p>
      <p class="text-center text-base">You are signing:</p>
      <Alert>
        <AlertTitle class="text-center text-base break-all">{{
          props.data.psbtHex
        }}</AlertTitle>
      </Alert>
    </div>
  </LayoutApprove>
</template>

<script setup lang="ts">
import LayoutApprove from '@/components/layout/LayoutApprove.vue'
import { Alert, AlertTitle } from '@/components/ui/alert'
import walletManager from '@/utils/sat20'
import { psbt2tx } from '@/utils/btc'
import { useToast } from '@/components/ui/toast'

interface Props {
  data: any
}

const props = defineProps<Props>()
const emit = defineEmits(['confirm', 'cancel'])
console.log(props)

const toast = useToast()
const confirm = async () => {
  const { options = {}, psbtHex } = props.data

  let result
  console.log('psbtHex', psbtHex)
  console.log('options', options)
  if (options.chain === 'btc') {
    result = await walletManager.signPsbt(psbtHex, false)
  } else {
    result = await walletManager.signPsbt_SatsNet(psbtHex, false)
  }
  console.log('chain', options.chain)

  console.log('signPsbt result', result)

  const [err, res] = result
  if (res?.psbt) {
    await psbt2tx(res.psbt)
    emit('confirm', res.psbt)
  } else {
    toast.toast({
      title: 'Sign PSBT failed',
      description: err?.message || 'Sign PSBT failed',
    })
  }
}
const cancel = () => {
  emit('cancel')
}
</script>

<style lang="less" scoped></style>
