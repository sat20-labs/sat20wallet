<template>
  <div class="w-full px-2  bg-zinc-700/40 rounded-lg">
    <button @click="isExpanded = !isExpanded"
      class="flex items-center justify-between w-full p-2 text-left text-primary font-medium rounded-lg">
      <div>
        <h2 class="text-lg font-bold text-zinc-200">Escape Hatch Options</h2>
        <p class="text-muted-foreground">Channel Info & Secure Exit from Channel</p>
      </div>
      <div class="mr-2">
        <Icon v-if="isExpanded" icon="lucide:chevrons-up" class="mr-2 h-4 w-4" />
        <Icon v-else icon="lucide:chevrons-down" class="mr-2 h-4 w-4" />
      </div>
    </button>
    <div v-if="isExpanded" class="space-y-6 py-2 px-2 mb-4">
      <div class="w-full px-4 py-4 bg-zinc-700/40 rounded-lg">
        <h2 class="text-lg font-bold text-zinc-200">Asset Safety</h2>
        <p class="text-sm text-muted-foreground mt-2">
          Your assets are safe. They are secured by your commitment transaction. By broadcasting the commitment transaction, you can reclaim your funds at any time without third-party permission.
        </p>

        <!-- Broadcast Button -->
        <div class="mt-6">
          <Button class="w-full bg-purple-600 text-white" @click="closeChannel">BROADCAST TX</Button>
        </div>

        <!-- Current Commitment Transaction -->
        <div class="mt-6">
          <h3 class="text-md font-bold text-zinc-200">Current Commitment Transaction</h3>

          <!-- Your Assets Section -->
          <div class="mt-6">
            <h4 class="text-sm font-bold text-zinc-200">Your Assets in This Channel</h4>
            <div class="overflow-x-auto custom-scrollbar">
              <div class="min-w-max grid grid-cols-2 gap-4 text-sm text-muted-foreground mt-2 whitespace-nowrap">
                <div class="flex flex-col">
                  <span class="font-medium">Asset</span>
                  <template v-for="(input, index) in parsedInputs" :key="`input-${index}`">
                    <template v-if="input.Assets && input.Assets.length">
                      <div v-for="(asset, assetIndex) in input.Assets" :key="`input-asset-${assetIndex}`">
                        {{ asset.Name.Ticker }}
                      </div>
                    </template>
                  </template>
                </div>
                <div class="flex flex-col">
                  <span class="font-medium">Amount</span>
                  <template v-for="(input, index) in parsedInputs" :key="`input-${index}`">
                    <template v-if="input.Assets && input.Assets.length">
                      <div v-for="(asset, assetIndex) in input.Assets" :key="`input-asset-${assetIndex}`">
                        {{ asset.Amount }}
                      </div>
                    </template>
                  </template>
                </div>
              </div>
            </div>
          </div>

          <!-- Inputs Section -->
          <div class="mt-4">
            <h4 class="text-sm font-bold text-zinc-200">Inputs</h4>
            <div class="overflow-x-auto custom-scrollbar">
              <div v-if="!commitTxData">Loading commitment data...</div>
              <div v-else-if="parsedInputs.length === 0">No input data available.</div>
              <div v-else class="min-w-max grid grid-cols-4 gap-4 text-sm text-muted-foreground mt-2 whitespace-nowrap">
                <div class="font-medium">Outpoint</div>
                <div class="font-medium">Value (Sats)</div>
                <div class="font-medium">Assets</div>
                <div class="font-medium">PkScript</div>

                <template v-for="(input, index) in parsedInputs" :key="`input-${index}`">
                  <div class="truncate">
                    <a :href="generateMempoolUrl({ network: 'testnet', path: input.Outpoint })" target="_blank">
                      {{ hideAddress(input.Outpoint) }}
                    </a>
                  </div>
                  <div class="truncate">{{ input.Value }}</div>
                  <div class="truncate">
                    <template v-if="input.Assets && input.Assets.length">
                      <div v-for="(asset, assetIndex) in input.Assets" :key="`input-asset-${assetIndex}`">
                        {{ asset.Name.Ticker }}: {{ asset.Amount }}
                      </div>
                    </template>
                    <template v-else>-</template>
                  </div>
                  <div class="truncate">{{ input.PkScript }}</div>
                </template>
              </div>
            </div>
          </div>

          <!-- Outputs Section -->
          <div class="mt-6">
            <h4 class="text-sm font-bold text-zinc-200">Outputs</h4>
            <div class="overflow-x-auto custom-scrollbar">
              <div v-if="!commitTxData">Loading commitment data...</div>
              <div v-else-if="parsedOutputs.length === 0">No output data available.</div>
              <div v-else class="min-w-max grid grid-cols-4 gap-4 text-sm text-muted-foreground mt-2 whitespace-nowrap">
                <div class="font-medium border-b border-zinc-600/30">Outpoint</div>
                <div class="font-medium border-b border-zinc-600/30">Value (Sats)</div>
                <div class="font-medium border-b border-zinc-600/30">Assets</div>
                <div class="font-medium border-b border-zinc-600/30">Address/PkScript</div>

                <template v-for="(output) in parsedOutputs" :key="`output-${output.index}`">
                  <div class="truncate">
                    <a :href="generateMempoolUrl({ network: 'testnet', path: output.Outpoint })" target="_blank">
                      {{ hideAddress(commitTxData.txId + ':' + output.index) }}
                    </a>
                  </div>
                  <div class="truncate border-b border-zinc-600/30">{{ output.Value }}</div>
                  <div class="truncate border-b border-zinc-600/30">
                    <template v-if="output.Assets && output.Assets.length">
                      <div v-for="(asset, assetIndex) in output.Assets" :key="`output-asset-${assetIndex}`">
                        {{ asset.Name.Ticker }}: {{ asset.Amount }}
                      </div>
                    </template>
                    <template v-else>-</template>
                  </div>
                  <div class="truncate border-b border-zinc-600/30">{{ output.PkScript }}</div>
                </template>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { storeToRefs } from 'pinia'
import { Button } from '@/components/ui/button'
import { Icon } from '@iconify/vue'
import { useChannelStore } from '@/store'
import satsnetStp from '@/utils/stp'
import { useToast } from '@/components/ui/toast'
import { hideAddress } from '~/utils'
import { getChannelStatusText } from '~/composables'
import { useGlobalStore, type Env } from '@/store/global'

const loading = ref(false)
const isExpanded = ref(false)
const commitTxData = ref<any>(null)

const channelStore = useChannelStore()
const { channel } = storeToRefs(channelStore)
const { toast } = useToast()

const parsedInputs = computed(() => {
  if (!commitTxData.value?.inputs) return [];
  try {
    const inputsArray = JSON.parse(commitTxData.value.inputs);
    return inputsArray.map((input: any) => {
      // 对每个输入项进行反序列化
      return {
        ...input,
        // 这里可以添加其他需要的字段处理
      };
    });
  } catch (error) {
    console.error("Failed to parse inputs:", error);
    return [];
  }
});

const parsedOutputs = computed(() => {
  if (!commitTxData.value?.outputs) return [];
  try {
    const outputsArray = JSON.parse(commitTxData.value.outputs);
    return outputsArray.map((output: any) => {
      // 对每个输出项进行反序列化
      return {
        ...output,
        // 这里可以添加其他需要的字段处理
      };
    });
  } catch (error) {
    console.error("Failed to parse outputs:", error);
    return [];
  }
});

// 添加 formatAssets 函数
const formatAssets = (assets: any): string => {
  if (!assets) return '-';
  // 假设 assets 是一个数组，格式化为字符串
  return assets.map((asset: any) => asset.name || 'Unknown').join(', ');
};

const channelId = computed(() => {
  return channel.value?.channelId
})

const closeChannel = async () => {
  loading.value = true;
  if (!channelId.value) return
  // 这里可以添加关闭通道的逻辑
  const [err] = await satsnetStp.closeChannel(channelId.value, 1, false);
  loading.value = false;

  if (err) {
    const [forceErr] = await satsnetStp.closeChannel(channelId.value, 1, true);
    if (forceErr) {
      toast({
        title: 'Error',
        description: 'Failed to close the channel.',
        variant: 'destructive',
      });
    } else {
      toast({
        title: 'Success',
        description: 'Channel closed successfully.',
      });
    }
  }
};


watch(channelId, async () => {
  console.log('channelId', channelId.value)

  if (!channelId.value) return
  const [err, result] = await satsnetStp.getCommitTxAssetInfo(channelId.value)
  console.log('result', result)
  console.log('err', err)
  if (err) {
    return false
  }
  commitTxData.value = result
}, {
  immediate: true,
})

onMounted(() => {
  channelStore.getAllChannels()
})
</script>

<style scoped>
/* 自定义滚动条样式 */
.custom-scrollbar::-webkit-scrollbar {
  width: 8px;
  background-color: transparent;
}

.custom-scrollbar::-webkit-scrollbar-thumb {
  background-color: rgba(255, 255, 255, 0.03);
  height: 4px;
  border-radius: 4px;
}

.custom-scrollbar::-webkit-scrollbar-thumb:hover {
  background-color: rgba(255, 255, 255, 0.219);
}

.custom-scrollbar::-webkit-scrollbar-track {
  height: 4px;
  background-color: transparent;
}
</style>