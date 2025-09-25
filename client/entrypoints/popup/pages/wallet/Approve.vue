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
import SplitAsset from '@/components/approve/SplitAsset.vue'
import ApproveDeployContractRemote from '@/components/approve/ApproveDeployContractRemote.vue'
import ApproveInvokeContractSatsNet from '@/components/approve/ApproveInvokeContractSatsNet.vue'
import ApproveInvokeContractV2SatsNet from '@/components/approve/ApproveInvokeContractV2SatsNet.vue'
import ApproveInvokeContractV2 from '@/components/approve/ApproveInvokeContractV2.vue'
import ApproveRegisterAsReferrer from '@/components/approve/ApproveRegisterAsReferrer.vue'
import ApproveBatchSendAssetsV2SatsNet from '@/components/approve/ApproveBatchSendAssetsV2SatsNet.vue'
import ApproveSendAssetsSatsNet from '@/components/approve/ApproveSendAssetsSatsNet.vue'
import { Message } from '@/types/message'

const { approveData, approve, reject } = useApprove()

const approveComponentMap: any = {
  [Message.MessageAction.REQUEST_ACCOUNTS]: RequestAccounts,
  [Message.MessageAction.SWITCH_NETWORK]: SwitchNetwork,
  [Message.MessageAction.SIGN_MESSAGE]: SignMessage,
  [Message.MessageAction.SIGN_PSBT]: SignPsbt,
  [Message.MessageAction.BATCH_SEND_ASSETS_SATSNET]: SplitAsset,
  [Message.MessageAction.BATCH_SEND_ASSETS_V2_SATSNET]: ApproveBatchSendAssetsV2SatsNet,
  [Message.MessageAction.DEPLOY_CONTRACT_REMOTE]: ApproveDeployContractRemote,
  [Message.MessageAction.INVOKE_CONTRACT_SATSNET]: ApproveInvokeContractSatsNet,
  [Message.MessageAction.INVOKE_CONTRACT_V2_SATSNET]: ApproveInvokeContractV2SatsNet,
  [Message.MessageAction.INVOKE_CONTRACT_V2]: ApproveInvokeContractV2,
  [Message.MessageAction.REGISTER_AS_REFERRER]: ApproveRegisterAsReferrer,
  [Message.MessageAction.SEND_ASSETS_SATSNET]: ApproveSendAssetsSatsNet,
}

const data = computed(() => {
  return approveData.value?.data ?? {}
})
const metadata = computed(() => {
  return approveData.value?.metadata ?? {}
})
console.log(approveData)
console.log(approveComponentMap)
const componentName = computed(() => {
  if (!approveData.value?.action) {
    return null
  }
  return approveComponentMap[approveData.value.action]
})
console.log(componentName)

const confirm = (data: any) => {
  approve(data)
}
const cancel = () => {
  reject()
}
</script>
