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
import service from '@/lib/service'
const walletStore = useWalletStore()

const { address } = storeToRefs(walletStore)
const emit = defineEmits(['confirm', 'cancel'])

const confirm = async () => {
  const accounts = await service.getAccounts()
  emit('confirm', accounts)
}
const cancel = () => {
  emit('cancel')
}
</script>

<style lang="less" scoped></style>
