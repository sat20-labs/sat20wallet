<template>
  <Dialog v-model:open="isVisible">
    <DialogScrollContent class="max-w-md mx-4">
      <DialogHeader class="mb-4 pr-10">
        <DialogTitle class="text-lg">{{ title }}</DialogTitle>
      </DialogHeader>
      <div
        class="max-h-[65vh] overflow-y-auto overscroll-y-contain touch-pan-y"
      >
        <div class="px-2 py-1">
          <component
            :is="componentName"
            :key="currentRequest?.id"
            :request-id="currentRequest?.id"
            :data="data"
            :metadata="metadata"
            v-on="requestHandlers"
          />
        </div>
      </div>
    </DialogScrollContent>
  </Dialog>
</template>

<script setup lang="ts">
import RequestAccounts from "@/components/approve/RequestAccounts.vue";
import SwitchNetwork from "@/components/approve/SwitchNetwork.vue";
import SignMessage from "@/components/approve/SignMessage.vue";
import SignPsbt from "@/components/approve/SignPsbt.vue";
import SplitAsset from "@/components/approve/SplitAsset.vue";
import ApproveDeployContractRemote from "@/components/approve/ApproveDeployContractRemote.vue";
import ApproveInvokeContractSatsNet from "@/components/approve/ApproveInvokeContractSatsNet.vue";
import ApproveInvokeUnifiedContract from "@/components/approve/ApproveInvokeUnifiedContract.vue";
import ApproveInvokeContractV2SatsNet from "@/components/approve/ApproveInvokeContractV2SatsNet.vue";
import ApproveInvokeContractV2 from "@/components/approve/ApproveInvokeContractV2.vue";
import ApproveRegisterAsReferrer from "@/components/approve/ApproveRegisterAsReferrer.vue";
import ApproveSendAssetsSatsNet from "@/components/approve/ApproveSendAssetsSatsNet.vue";
import ApproveBatchSendAssetsV2SatsNet from "@/components/approve/ApproveBatchSendAssetsV2SatsNet.vue";
import ApproveDappOperation from "@/components/approve/ApproveDappOperation.vue";
import { Message } from "@/types/message";
import { computed } from "vue";
import { useApproveStore } from "@/store";
import {
  Dialog,
  DialogScrollContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

const approveStore = useApproveStore();

const approveComponentMap: any = {
  [Message.MessageAction.REQUEST_ACCOUNTS]: RequestAccounts,
  [Message.MessageAction.SWITCH_NETWORK]: SwitchNetwork,
  [Message.MessageAction.SIGN_MESSAGE]: SignMessage,
  [Message.MessageAction.SIGN_DATA]: SignMessage,
  [Message.MessageAction.SIGN_PSBT]: SignPsbt,
  [Message.MessageAction.SIGN_PSBTS]: SignPsbt,
  [Message.MessageAction.LOCK_UTXO]: ApproveDappOperation,
  [Message.MessageAction.LOCK_UTXO_SATSNET]: ApproveDappOperation,
  [Message.MessageAction.UNLOCK_UTXO]: ApproveDappOperation,
  [Message.MessageAction.UNLOCK_UTXO_SATSNET]: ApproveDappOperation,
  [Message.MessageAction.PUSH_TX]: ApproveDappOperation,
  [Message.MessageAction.PUSH_PSBT]: ApproveDappOperation,
  [Message.MessageAction.BATCH_SEND_ASSETS_SATSNET]: SplitAsset,
  [Message.MessageAction.SPLIT_ASSET]: SplitAsset,
  [Message.MessageAction.DEPLOY_CONTRACT_REMOTE]: ApproveDeployContractRemote,
  [Message.MessageAction.INVOKE_CONTRACT_SATSNET]: ApproveInvokeContractSatsNet,
  [Message.MessageAction.INVOKE_UNIFIED_CONTRACT]: ApproveInvokeUnifiedContract,
  [Message.MessageAction.INVOKE_CONTRACT_V2_SATSNET]:
    ApproveInvokeContractV2SatsNet,
  [Message.MessageAction.INVOKE_CONTRACT_V2]: ApproveInvokeContractV2,
  [Message.MessageAction.REGISTER_AS_REFERRER]: ApproveRegisterAsReferrer,
  [Message.MessageAction.SEND_ASSETS_SATSNET]: ApproveSendAssetsSatsNet,
  [Message.MessageAction.BATCH_SEND_ASSETS_V2_SATSNET]:
    ApproveBatchSendAssetsV2SatsNet,
};

const { currentRequest, isVisible } = approveStore;

const data = computed(() => {
  return currentRequest.value?.data ?? {};
});

const metadata = computed(() => {
  return currentRequest.value?.metadata ?? {};
});

const componentName = computed(() => {
  if (!currentRequest.value?.action) {
    return null;
  }
  return approveComponentMap[currentRequest.value.action];
});

const title = computed(() => {
  if (!currentRequest.value?.action) {
    return "Authorization Request";
  }
  // 简单的标题映射，可以根据需要扩展
  const actionToTitle: Record<string, string> = {
    [Message.MessageAction.REQUEST_ACCOUNTS]: "Connect Wallet",
    [Message.MessageAction.SWITCH_NETWORK]: "Switch Network",
    [Message.MessageAction.SIGN_MESSAGE]: "Sign Message",
    [Message.MessageAction.SIGN_DATA]: "Sign Data",
    [Message.MessageAction.SIGN_PSBT]: "Sign Transaction",
    [Message.MessageAction.SIGN_PSBTS]: "Sign Transactions",
    [Message.MessageAction.LOCK_UTXO]: "Lock UTXO",
    [Message.MessageAction.LOCK_UTXO_SATSNET]: "Lock UTXO",
    [Message.MessageAction.UNLOCK_UTXO]: "Unlock UTXO",
    [Message.MessageAction.UNLOCK_UTXO_SATSNET]: "Unlock UTXO",
    [Message.MessageAction.PUSH_TX]: "Broadcast Transaction",
    [Message.MessageAction.PUSH_PSBT]: "Broadcast Transaction",
    [Message.MessageAction.BATCH_SEND_ASSETS_SATSNET]: "Send Assets",
    [Message.MessageAction.SPLIT_ASSET]: "Split Asset",
    [Message.MessageAction.DEPLOY_CONTRACT_REMOTE]: "Deploy Contract",
    [Message.MessageAction.INVOKE_CONTRACT_SATSNET]: "Execute Contract",
    [Message.MessageAction.INVOKE_UNIFIED_CONTRACT]: "Execute Contract",
    [Message.MessageAction.INVOKE_CONTRACT_V2_SATSNET]: "Execute Contract",
    [Message.MessageAction.INVOKE_CONTRACT_V2]: "Execute Contract",
    [Message.MessageAction.REGISTER_AS_REFERRER]: "Register as Referrer",
    [Message.MessageAction.SEND_ASSETS_SATSNET]: "Send Assets",
    [Message.MessageAction.BATCH_SEND_ASSETS_V2_SATSNET]: "Send Assets",
  };
  return actionToTitle[currentRequest.value.action] || "Authorization Request";
});

// Each rendered component retains callbacks for its own request, including
// asynchronous events emitted after the queue has advanced.
const requestHandlers = computed(() => {
  const id = currentRequest.value?.id;
  return {
    confirm: (result: any) => {
      if (id && currentRequest.value?.id === id) approveStore.confirm(id, result);
    },
    cancel: () => {
      if (id && currentRequest.value?.id === id) approveStore.reject(id);
    },
  };
});
</script>
