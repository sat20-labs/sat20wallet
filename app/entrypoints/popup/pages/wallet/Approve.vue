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
            :data="data"
            :metadata="metadata"
            @cancel="cancel"
            @confirm="confirm"
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
import ApproveInvokeContractV2SatsNet from "@/components/approve/ApproveInvokeContractV2SatsNet.vue";
import ApproveInvokeContractV2 from "@/components/approve/ApproveInvokeContractV2.vue";
import ApproveRegisterAsReferrer from "@/components/approve/ApproveRegisterAsReferrer.vue";
import ApproveSendAssetsSatsNet from "@/components/approve/ApproveSendAssetsSatsNet.vue";
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
  [Message.MessageAction.SIGN_PSBT]: SignPsbt,
  [Message.MessageAction.BATCH_SEND_ASSETS_SATSNET]: SplitAsset,
  [Message.MessageAction.DEPLOY_CONTRACT_REMOTE]: ApproveDeployContractRemote,
  [Message.MessageAction.INVOKE_CONTRACT_SATSNET]: ApproveInvokeContractSatsNet,
  [Message.MessageAction.INVOKE_CONTRACT_V2_SATSNET]:
    ApproveInvokeContractV2SatsNet,
  [Message.MessageAction.INVOKE_CONTRACT_V2]: ApproveInvokeContractV2,
  [Message.MessageAction.REGISTER_AS_REFERRER]: ApproveRegisterAsReferrer,
  [Message.MessageAction.SEND_ASSETS_SATSNET]: ApproveSendAssetsSatsNet,
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
    [Message.MessageAction.SIGN_PSBT]: "Sign Transaction",
    [Message.MessageAction.BATCH_SEND_ASSETS_SATSNET]: "Send Assets",
    [Message.MessageAction.DEPLOY_CONTRACT_REMOTE]: "Deploy Contract",
    [Message.MessageAction.INVOKE_CONTRACT_SATSNET]: "Execute Contract",
    [Message.MessageAction.INVOKE_CONTRACT_V2_SATSNET]: "Execute Contract",
    [Message.MessageAction.INVOKE_CONTRACT_V2]: "Execute Contract",
    [Message.MessageAction.REGISTER_AS_REFERRER]: "Register as Referrer",
    [Message.MessageAction.SEND_ASSETS_SATSNET]: "Send Assets",
  };
  return actionToTitle[currentRequest.value.action] || "Authorization Request";
});

const confirm = (result: any) => {
  approveStore.confirm(result);
};

const cancel = () => {
  approveStore.reject();
};
</script>