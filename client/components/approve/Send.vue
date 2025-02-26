<template>
  <LayoutApprove @confirm="confirm" @cancel="cancel">
    <div class="p-6">
      <SendCard v-model:address="address" v-model:amount="amount" />
    </div>
  </LayoutApprove>
</template>

<script setup lang="ts">
import LayoutApprove from '@/components/layout/LayoutApprove.vue'
import SendCard from '@/components/wallet/SendCard.vue'
import { useWalletStore } from '@/store'
import service from '@/lib/service'

interface Props {
  data: any
}

const props = defineProps<Props>()

const walletStore = useWalletStore()
const { network } = storeToRefs(walletStore)
const emit = defineEmits(['confirm', 'cancel'])
const address = ref('')
const amount = ref(0)
const confirm = async () => {
  console.log(address)
  console.log(amount)
  // emit('confirm', props.data.network)
}
const cancel = () => {
  emit('cancel')
}
</script>

<style lang="less" scoped></style>
