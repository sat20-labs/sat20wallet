<template>
  <component
    :is="componentName"
    :data="data"
    :metadata="metadata"
    @cancel="cancel"
    @confirm="confirm"
  ></component>
</template>

<script setup lang="ts">
import RequestAccounts from '@/components/approve/RequestAccounts.vue'
import SwitchNetwork from '@/components/approve/SwitchNetwork.vue'
import SignMessage from '@/components/approve/SignMessage.vue'
import SignPsbt from '@/components/approve/SignPsbt.vue'
import Send from '@/components/approve/Send.vue'
import service from '@/lib/service'
import { Message } from '@/types/message'
const { approveData, approve, reject } = useApprove()

const approveComponentMap: any = {
  [Message.MessageAction.REQUEST_ACCOUNTS]: RequestAccounts,
  [Message.MessageAction.SWITCH_NETWORK]: SwitchNetwork,
  [Message.MessageAction.SIGN_MESSAGE]: SignMessage,
  [Message.MessageAction.SIGN_PSBT]: SignPsbt,
  [Message.MessageAction.SEND_BITCOIN]: Send,
}

const data = computed(() => {
  return approveData.value?.data ?? {}
})
const metadata = computed(() => {
  return approveData.value?.metadata ?? {}
})
const componentName = computed(() => {
  if (!approveData.value?.action) {
    return null
  }
  return approveComponentMap[approveData.value.action]
})

const confirm = (data: any) => {
  approve(data)
}
const cancel = () => {
  reject()
}
console.log(componentName)
</script>
