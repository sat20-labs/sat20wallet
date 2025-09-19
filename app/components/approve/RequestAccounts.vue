<template>
  <LayoutApprove @confirm="confirm" @cancel="cancel">
    <div class="p-4">
      <AccountCard :address="address" />
    </div>
  </LayoutApprove>
</template>

<script setup lang="ts">
import LayoutApprove from '@/components/layout/LayoutApprove.vue'
import AccountCard from '@/components/wallet/AccountCard.vue'
import { useWalletStore } from '@/store'
import { storeToRefs } from 'pinia'
import service from '@/lib/service'
import { addAuthorizedOrigin } from '@/lib/authorized-origins'

interface Props {
  data: any
  metadata: any
}

const props = defineProps<Props>()
console.log(props)
const walletStore = useWalletStore()

const { address } = storeToRefs(walletStore)
const emit = defineEmits(['confirm', 'cancel'])

const confirm = async () => {
  const accounts = await service.getAccounts()
  // 添加当前 origin 到授权列表
  await addAuthorizedOrigin(props.metadata.origin)
  emit('confirm', accounts)
}
const cancel = () => {
  emit('cancel')
}
</script>

<style lang="less" scoped></style>
