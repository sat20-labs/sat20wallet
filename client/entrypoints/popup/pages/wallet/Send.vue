<template>
  <LayoutSecond title="Send">
    <div class="p-2">
      <SendCard v-model:address="address" v-model:amount="amount" class="mb-6"/>
      <Button class="w-full">Send</Button>
    </div>
  </LayoutSecond>
</template>

<script setup lang="ts">
import LayoutSecond from '@/components/layout/LayoutSecond.vue'
import { Button } from '@/components/ui/button'
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
