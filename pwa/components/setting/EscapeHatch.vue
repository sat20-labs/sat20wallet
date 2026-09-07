<template>
  <div class="w-full px-2  bg-zinc-700/40 rounded-lg">
    <button @click="isExpanded = !isExpanded"
      class="flex items-center justify-between w-full p-2 text-left text-primary font-medium rounded-lg">
      <div>
        <h2 class="text-lg font-bold text-zinc-200">{{ $t('escapeHatch.title') }}</h2>
        <p class="text-muted-foreground">{{ $t('escapeHatch.subtitle') }}</p>
      </div>
      <div class="mr-2">
        <Icon v-if="isExpanded" icon="lucide:chevrons-up" class="mr-2 h-4 w-4" />
        <Icon v-else icon="lucide:chevrons-down" class="mr-2 h-4 w-4" />
      </div>
    </button>
    <div v-if="isExpanded" class="space-y-6 py-2 px-2 mb-4">
      <div class="w-full" v-if="channel">
        <h2 class="text-md font-bold text-zinc-200">{{ $t('escapeHatch.assetSafetyTitle') }}</h2>
        <p class="text-sm text-muted-foreground mt-2">
          {{ $t('escapeHatch.assetSafetyDescription') }}
        </p>

        <!-- Close Channel Note -->
        <p class="text-sm text-muted-foreground mt-4">
          {{ reopenFeeToDao === null
            ? $t('escapeHatch.closeChannelNote')
            : $t('escapeHatch.closeChannelNoteWithFee', { fee: reopenFeeToDao.toLocaleString() }) }}
        </p>

        <!-- Broadcast Button -->
        <div class="mt-4 grid gap-2">
          <Button class="w-full bg-purple-600 text-white" :disabled="loading" @click="closeChannel">
            Cooperative Close
          </Button>
          <Button class="w-full" variant="destructive" :disabled="loading" @click="requestForceClose">
            Force Close
          </Button>
        </div>

        <!-- Current Commitment Transaction -->
        <div class="mt-6">
          <h3 class="text-base font-bold text-zinc-200">{{ $t('escapeHatch.currentCommitmentTx') }}</h3>

          <!-- Your Assets Section -->

          <div class="mt-6">
            <h4 class="text-sm font-bold text-zinc-200">{{ $t('escapeHatch.assetsInChannel') }}</h4>
            <div class="overflow-x-auto custom-scrollbar">
              <table class="w-full table-auto text-sm text-muted-foreground mt-2">
                <thead>
                  <tr>
                    <th class="text-left font-medium border-b border-zinc-600/30">{{ $t('escapeHatch.asset') }}</th>
                    <th class="text-right font-medium border-b border-zinc-600/30">{{ $t('escapeHatch.amount') }}</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="(asset, index) in mergedChannelAssets" :key="`merged-asset-${index}`">
                    <td class="truncate">{{ asset.ticker }}</td>
                    <td class="text-right truncate">{{ asset.amount }}</td>
                  </tr>
                </tbody>
              </table>
            </div>
          </div>


          <!-- Inputs Section -->

          <div class="mt-4">
            <h4 class="text-sm font-bold text-zinc-200">{{ $t('escapeHatch.inputs') }}</h4>
            <div class="overflow-x-auto custom-scrollbar">
              <table class="w-full table-auto text-sm text-muted-foreground mt-2 *:whitespace-nowrap">
                <thead>
                  <tr>
                    <th class="text-left font-medium border-b border-zinc-600/30">{{ $t('escapeHatch.outpoint') }}</th>
                    <th class="text-left font-medium border-b border-zinc-600/30">{{ $t('escapeHatch.value') }}</th>
                    <th class="text-left font-medium border-b border-zinc-600/30">{{ $t('escapeHatch.assets') }}</th>
                    <th class="text-left font-medium border-b border-zinc-600/30">{{ $t('escapeHatch.pkScript') }}</th>
                  </tr>
                </thead>
                <tbody>
                  <template v-for="(input, index) in parsedInputs" :key="`input-${index}`">
                    <tr>
                      <td class="truncate">
                        <a :href="generateMempoolUrl({ network: network, path: transactionPath(input.Outpoint) })" target="_blank">
                          {{ hideAddress(input.Outpoint) }}
                        </a>
                      </td>
                      <td class="truncate">{{ input.Value }}</td>
                      <td class="truncate">
                        <template v-if="input.Assets && input.Assets.length">
                          <div v-for="(asset, assetIndex) in input.Assets" :key="`input-asset-${assetIndex}`">
                            {{ asset.Name.Ticker }}: {{ asset.Amount }}
                          </div>
                        </template>
                        <template v-else>-</template>
                      </td>
                      <td class="truncate">{{ input.PkScript }}</td>
                    </tr>
                  </template>
                </tbody>
              </table>
            </div>
          </div>

          <!-- Outputs Section -->

          <div class="mt-6">
            <h4 class="text-sm font-bold text-zinc-200">{{ $t('escapeHatch.outputs') }}</h4>
            <div class="overflow-x-auto custom-scrollbar">
              <table class="w-full table-auto text-sm text-muted-foreground mt-2 *:whitespace-nowrap">
                <thead>
                  <tr>
                    <th class="text-left font-medium border-b border-zinc-600/30">{{ $t('escapeHatch.outpoint') }}</th>
                    <th class="text-left font-medium border-b border-zinc-600/30">{{ $t('escapeHatch.value') }}</th>
                    <th class="text-left font-medium border-b border-zinc-600/30">{{ $t('escapeHatch.assets') }}</th>
                    <th class="text-left font-medium border-b border-zinc-600/30">{{ $t('escapeHatch.addressOrPkScript') }}</th>
                  </tr>
                </thead>
                <tbody>
                  <template v-for="output in parsedOutputs" :key="`output-${output.Outpoint}`">
                    <tr>
                      <td class="truncate">
                        <a :href="generateMempoolUrl({ network: network, path: transactionPath(output.Outpoint) })" target="_blank">
                          {{ hideAddress(output.Outpoint) }}
                        </a>
                      </td>
                      <td class="truncate">{{ output.Value }}</td>
                      <td class="truncate">
                        <template v-if="output.Assets && output.Assets.length">
                          <div v-for="(asset, assetIndex) in output.Assets" :key="`output-asset-${assetIndex}`">
                            {{ asset.Name.Ticker }}: {{ asset.Amount }}
                          </div>
                        </template>
                        <template v-else>-</template>
                      </td>
                      <td class="truncate">{{ output.PkScript }}</td>
                    </tr>
                  </template>
                </tbody>
              </table>
            </div>
          </div>
        </div>
      </div>
      <div v-else>
        <h2 class="text-lg font-bold text-zinc-200">{{ $t('escapeHatch.noChannelTitle') }}</h2>
        <p class="text-sm text-muted-foreground mt-2">
          {{ $t('escapeHatch.noChannelDescription') }}
        </p>
      </div>
    </div>

    <AlertDialog v-model:open="showForceCloseConfirm">
      <AlertDialogContent class="w-[350px] rounded-lg bg-zinc-900">
        <AlertDialogHeader>
          <AlertDialogTitle>Confirm Force Close</AlertDialogTitle>
          <AlertDialogDescription>
            Force close broadcasts the latest commitment transaction and may require waiting for the CSV delay.
            Continue only if cooperative close is unavailable.
          </AlertDialogDescription>
          <div v-if="forceCloseSnapshot" class="space-y-1 rounded border border-zinc-700 p-3 text-xs text-zinc-300">
            <p>Channel: {{ forceCloseSnapshot.channel_id }}</p>
            <p>Status: {{ forceCloseSnapshot.status }}</p>
            <p>Commit height: {{ forceCloseSnapshot.commit_height }}</p>
            <p>CSV delay: {{ forceCloseSnapshot.csv_delay }}</p>
            <p>Local commitment: {{ forceCloseSnapshot.local_commitment_txid }}</p>
            <p>Remote commitment: {{ forceCloseSnapshot.remote_commitment_txid }}</p>
            <p>Punish coverage: {{ forceCloseSnapshot.punish_coverage?.status }}</p>
          </div>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel :disabled="loading">Cancel</AlertDialogCancel>
          <AlertDialogAction :disabled="loading" @click.prevent="forceCloseChannel">
            {{ loading ? 'Checking safety…' : 'Force Close' }}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { storeToRefs } from 'pinia'
import { Button } from '@/components/ui/button'
import { Icon } from '@iconify/vue'
import { useChannelStore } from '@/store'
import satsnetStp, { type CommitTxAssetInfo, type CommitTxAssetUtxo } from '@/utils/stp'
import { useToast } from '@/components/ui/toast-new'
import { generateMempoolUrl, hideAddress } from '@/utils'
import { useWalletStore } from '@/store/wallet'
import { assessStpValueMovementSafety } from '@/composables/usePwaAgentAdapterSafety'
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'

const walletStore = useWalletStore()
const { btcFeeRate, network } = storeToRefs(walletStore)

const loading = ref(false)
const isExpanded = ref(false)
const showForceCloseConfirm = ref(false)
const forceCloseSnapshot = ref<any | null>(null)
const commitTxData = ref<CommitTxAssetInfo | null>(null)
const reopenFeeToDao = ref<number | null>(null)
let reopenFeeRequestGeneration = 0

const channelStore = useChannelStore()
const { channel } = storeToRefs(channelStore)
const { toast } = useToast()

const parsedInputs = computed(() => {
  if (!commitTxData.value?.inputs) return [];
  try {
    return JSON.parse(commitTxData.value.inputs) as CommitTxAssetUtxo[];
  } catch (error) {
    console.error("Failed to parse inputs:", error);
    return [];
  }
});

const parsedOutputs = computed(() => {
  if (!commitTxData.value?.outputs) return [];
  try {
    return JSON.parse(commitTxData.value.outputs) as CommitTxAssetUtxo[];
  } catch (error) {
    console.error("Failed to parse outputs:", error);
    return [];
  }
});

// 合并通道中的资产
const mergedChannelAssets = computed(() => {
  if (!parsedInputs.value) return [];
  
  const assetMap = new Map();
  
  // 遍历所有输入中的资产
  parsedInputs.value.forEach((input: any) => {
    if (input.Assets && input.Assets.length) {
      input.Assets.forEach((asset: any) => {
        const ticker = asset.Name.Ticker;
        const amount = BigInt(asset.Amount);
        
        if (assetMap.has(ticker)) {
          // 如果已存在该资产，累加数量
          assetMap.set(ticker, assetMap.get(ticker) + amount);
        } else {
          // 如果不存在，添加新资产
          assetMap.set(ticker, amount);
        }
      });
    }
  });
  
  // 转换为数组格式
  return Array.from(assetMap.entries()).map(([ticker, amount]) => ({
    ticker,
    amount: amount.toString()
  }));
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

const transactionPath = (outpoint: string) => `tx/${outpoint.split(':', 1)[0]}`

const refreshReopenFee = async () => {
  const id = channelId.value
  const generation = ++reopenFeeRequestGeneration
  reopenFeeToDao.value = null
  if (!id) return

  // PreviewOpenChannel reads the service node's authoritative fee config before
  // validating the amount.  Amount 1 deliberately avoids local UTXO selection;
  // this view only needs feeToDao and never opens or reserves a channel.
  const [err, preview] = await satsnetStp.previewOpenChannel(btcFeeRate.value, 1)
  if (generation !== reopenFeeRequestGeneration || id !== channelId.value) return
  const fee = Number(preview?.feeToDao)
  if (!err && Number.isFinite(fee) && fee >= 0) {
    reopenFeeToDao.value = fee
  }
}

const closeChannel = async () => {
  const id = channelId.value
  if (!id) return

  loading.value = true
  try {
    const [err] = await satsnetStp.closeChannel(id, btcFeeRate.value, false)
    if (err) {
      toast({
        title: 'Error',
        description: err.message || 'Failed to close the channel cooperatively.',
        variant: 'destructive',
      })
      return
    }
    toast({
      title: 'Success',
      description: 'Cooperative channel close initiated.',
      variant: 'success',
    })
  } finally {
    loading.value = false
  }
}

const readSafeForceCloseSnapshot = async (id: string) => {
  const [snapshotErr, snapshotResult] = await satsnetStp.safetySnapshot(id)
  if (snapshotErr || !snapshotResult) {
    throw snapshotErr || new Error('Unable to read the current channel safety snapshot.')
  }

  const snapshot = typeof snapshotResult.json === 'string'
    ? JSON.parse(snapshotResult.json)
    : snapshotResult
  const safety = assessStpValueMovementSafety(snapshot)
  if (!safety.allowed) {
    throw new Error(safety.reason || 'The current channel safety snapshot does not allow force close.')
  }
  return snapshot
}

const requestForceClose = async () => {
  const id = channelId.value
  if (!id || loading.value) return

  loading.value = true
  try {
    forceCloseSnapshot.value = await readSafeForceCloseSnapshot(id)
    showForceCloseConfirm.value = true
  } catch (error) {
    toast({
      title: 'Force close blocked',
      description: error instanceof Error ? error.message : 'Unable to verify channel safety.',
      variant: 'destructive',
    })
  } finally {
    loading.value = false
  }
}

const forceCloseChannel = async () => {
  const id = channelId.value
  if (!id) return

  loading.value = true
  try {
    forceCloseSnapshot.value = await readSafeForceCloseSnapshot(id)

    const [forceErr] = await satsnetStp.closeChannel(id, btcFeeRate.value, true)
    if (forceErr) {
      toast({
        title: 'Error',
        description: forceErr.message || 'Failed to force close the channel.',
        variant: 'destructive',
      })
      return
    }
    toast({
      title: 'Success',
      description: 'Force close initiated.',
      variant: 'success',
    })
  } catch (error) {
    toast({
      title: 'Force close blocked',
      description: error instanceof Error ? error.message : 'Unable to verify channel safety.',
      variant: 'destructive',
    })
  } finally {
    loading.value = false
    showForceCloseConfirm.value = false
  }
}


watch(channelId, async () => {
  if (!channelId.value) return
  const [err, result] = await satsnetStp.getCommitTxAssetInfo(channelId.value)
  if (err) {
    return false
  }
  commitTxData.value = result ?? null
}, {
  immediate: true,
})

watch([channelId, btcFeeRate], () => {
  void refreshReopenFee()
}, { immediate: true })

onMounted(() => {
  channelStore.getCurrentChannel()
})
</script>

<style scoped>
table {
  table-layout: auto;
  /* 自动调整列宽 */
  border-collapse: collapse;
  width: 100%;
  border: 1px solid rgba(108, 122, 137, 0.425);
  /* 添加边框 */
  border-bottom: 2px solid rgba(108, 122, 137, 0.3);
  /* 加粗下边框 */
}

th {
  background-color: bg-zinc-500/40;
}

th,
td {
  padding: 0.5rem 0.5rem;
  /* 上下 0.5rem，左右 1rem */
  text-align: left;
  vertical-align: middle;
  white-space: nowrap;
  /* 禁止换行 */
}

th {
  font-weight: bold;
  border-bottom: 1px solid rgba(108, 122, 137, 0.3);
  /* 添加底部边框 */
}

td {
  border-bottom: 1px solid rgba(108, 122, 137, 0.1);
  /* 添加底部边框 */
}

/* 滚动容器样式 */
.overflow-x-auto {
  padding-bottom: 8px;
  /* 为滚动条和表格内容留出空间 */
}

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
