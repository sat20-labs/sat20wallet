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
console.log('🔍 RequestAccounts props:', props)
const walletStore = useWalletStore()

const { address } = storeToRefs(walletStore)
const emit = defineEmits(['confirm', 'cancel'])

const confirm = async () => {
  try {
    console.log('🔐 RequestAccounts confirm called')
    const accounts = await service.getAccounts()
    console.log('📋 Got accounts:', accounts)

    // 添加当前 origin 到授权列表，确保origin存在
    const origin = props.metadata?.origin || props.metadata?.dAppOrigin || 'inappbrowser'
    console.log('📍 Adding authorized origin:', origin)
    await addAuthorizedOrigin(origin)

    console.log('✅ RequestAccounts confirmed')
    emit('confirm', accounts)
  } catch (error) {
    console.error('❌ RequestAccounts confirm error:', error)
    throw error
  }
}
const cancel = () => {
  emit('cancel')
}
</script>

<style lang="less" scoped></style>
