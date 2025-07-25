<template>
  <Dialog :open="isOpen" @update:open="isOpen = $event">
    <DialogContent class="w-[330px] rounded-lg bg-black">
      <DialogHeader class="flex flex-row items-center justify-between">
        <div>
          <DialogTitle>{{ title }}</DialogTitle>
          <DialogDescription>
            <hr class="mb-4 mt-2 border-t-1 border-zinc-900">
            {{ description }}
          </DialogDescription>
        </div>
      </DialogHeader>

      <!-- Tabs -->
      <div v-if="props.operationType === 'send'">
        <div class="tabs gap-1 border-b-2 border-zinc-700/50">
          <button :class="[
            'w-full py-2 px-1 font-sans font-semibold text-base border border-b-transparent hover:text-primary relative rounded-t-lg',
            selectedTab === 'normal'
              ? 'bg-zinc-700/30 text-primary/80 border-zinc-600'
              : 'bg-transparent text-muted-foreground  border-zinc-700/50 hover:bg-zinc-700/50'
          ]" @click="selectedTab = 'normal'">
            {{ $t('assetOperationDialog.normalSend') }}
          </button>
          <button :class="[
            'w-full py-2 px-1 font-sans font-semibold text-base border border-b-transparent hover:text-primary relative rounded-t-lg',
            selectedTab === 'advanced'
              ? 'bg-zinc-700/30 text-primary/80 border-zinc-600'
              : 'bg-transparent text-muted-foreground  border-zinc-700/50 hover:bg-zinc-700/50'
          ]" :disabled="props.chain !== 'bitcoin' || props.assetKey?.includes('runes')"
            @click="selectedTab = 'advanced'">
            {{ $t('assetOperationDialog.advancedSend') }}
          </button>
        </div>
      </div>

      <!-- Tab Content -->
      <div class="tab-content">
        <div v-if="selectedTab === 'normal'">
          <!-- 普通发送内容 -->
          <div class="space-y-4">
            <div class="space-y-2">
              <Label>{{ $t('assetOperationDialog.amount') }}</Label>
              <div class="flex items-center gap-2">
                <Input :model-value="amount" type="number" :placeholder="$t('assetOperationDialog.enterAmount')"
                  class="h-12 bg-zinc-800" @update:modelValue="handleAmountUpdate" />
                <Button variant="outline" class="h-12 px-4 text-sm border border-zinc-600 hover:bg-zinc-700"
                  @click="setMaxAmount">
                  {{ $t('assetOperationDialog.max') }}
                </Button>
              </div>
            </div>
            <div v-if="needsAddress" class="space-y-2">
              <Label>{{ $t('assetOperationDialog.address') }}</Label>
              <Input :model-value="address" type="text" :placeholder="$t('assetOperationDialog.enterAddress')"
                class="h-12 bg-zinc-800" @update:modelValue="handleAddressUpdate" />
            </div>
          </div>
        </div>
        <div v-else-if="selectedTab === 'advanced'">
          <!-- 高级发送内容 -->
          <SplitSend :assetName="props.assetKey || ''" />
        </div>
      </div>

      <DialogFooter v-if="selectedTab === 'normal'">
        <Button class="w-full h-11 mb-2" :disabled="needsAddress && !address" @click="confirmOperation">
          {{ $t('assetOperationDialog.confirm') }}
        </Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>

  <AlertDialog v-model:open="showAlertDialog">
    <AlertDialogContent class="w-[330px] rounded-lg bg-zinc-900">
      <AlertDialogTitle class="gap-2 flex flex-col items-center">
        <span class="text-lg font-semibold">{{ $t('assetOperationDialog.pleaseConfirm') }}</span>
        <span class="mt-2 w-full">
          <Separator />
        </span>
      </AlertDialogTitle>
      <AlertDialogDesc class="flex justify-center">
        <Icon icon="prime:check-circle" class="w-12 h-12 mr-2 text-green-600" />
        {{ $t('assetOperationDialog.confirmOperation') }}
      </AlertDialogDesc>
      
      <!-- 显示 btcFeeRate 信息 -->
      <div v-if="needsBtcFeeRate" class="mt-4 p-3 bg-zinc-800 rounded-lg border border-zinc-700">
        <div class="flex items-center justify-between text-sm">
          <span class="text-zinc-300">BTC Fee Rate:</span>
          <span class="text-primary font-semibold">{{ btcFeeRate }} sats/vB</span>
        </div>
      </div>
      
      <AlertDialogFoot class="my-4 gap-2">
        <AlertDialogCancel @click="showAlertDialog = false">{{ $t('assetOperationDialog.cancel') }}</AlertDialogCancel>
        <AlertDialogAction @click="handleConfirm">{{ $t('assetOperationDialog.confirm') }}</AlertDialogAction>
      </AlertDialogFoot>
    </AlertDialogContent>
  </AlertDialog>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { Separator } from '@/components/ui/separator'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import {
  AlertDialog,
  AlertDialogContent,
  AlertDialogTitle,
  AlertDialogDescription as AlertDialogDesc,
  AlertDialogFooter as AlertDialogFoot,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogTrigger
} from '@/components/ui/alert-dialog'
import { useRouter } from 'vue-router'
import { Chain } from '@/types/index'
import { Icon } from '@iconify/vue'
import SplitSend from '@/entrypoints/popup/pages/wallet/split.vue'
import { useWalletStore } from '@/store'

interface Props {
  title: string
  description: string
  amount: string
  address: string
  maxAmount?: string // 新增的 prop
  assetType?: string
  assetTicker?: string
  assetKey?: string
  chain?: string
  operationType?: 'send' | 'deposit' | 'withdraw' | 'lock' | 'unlock' | 'splicing_in' | 'splicing_out'
}

const props = withDefaults(defineProps<Props>(), {
  address: '',
  operationType: undefined,
  chain: 'bitcoin'
})
const { maxAmount } = toRefs(props)
console.log('assetType: ', props.assetType)
console.log('props', props);

const selectedTab = ref('normal') // 默认选中普通发送

const isOpen = defineModel('open', { type: Boolean })

const showAlertDialog = ref(false)

// 获取钱包 store 中的 btcFeeRate
const walletStore = useWalletStore()
const { btcFeeRate } = storeToRefs(walletStore)

const needsAddress = computed(() => {
  return props.operationType === 'send'
})

// 判断哪些操作需要显示 btcFeeRate
const needsBtcFeeRate = computed(() => {
  const operationsNeedingBtcFeeRate = ['send', 'deposit', 'withdraw', 'splicing_in', 'splicing_out']
  return operationsNeedingBtcFeeRate.includes(props.operationType || '')
})

const emit = defineEmits<{
  'update:amount': [value: string]
  'update:address': [value: string]
  'confirm': []
}>()

const handleAmountUpdate = (value: string | number) => {
  emit('update:amount', value.toString())
}

const handleAddressUpdate = (value: string | number) => {
  emit('update:address', value.toString())
}

const confirmOperation = () => {
  showAlertDialog.value = true
}

const handleConfirm = () => {
  console.log('handleConfirm called'); // 调试日志
  emit('confirm')
  showAlertDialog.value = false
  setTimeout(() => {
    isOpen.value = false
    document.body.removeAttribute('style')
  }, 300)
}

// 设置最大值
const setMaxAmount = () => {
  console.log('maxAmount', maxAmount.value);
  
  if (maxAmount.value) {
    emit('update:amount', maxAmount.value) // 将最大值传递给父组件
  }
}

const router = useRouter()

// const goSplitAsset = () => {
//   router.push(`/wallet/split-asset?assetName=${props.assetKey}`)
// }

watch(
  () => isOpen.value,
  (newVal) => {
    if (newVal) {
      selectedTab.value = 'normal'; // 重置为默认选项
    }
  }
);

</script>
<style scoped>
.tabs-container {
  position: relative;
  border-bottom: 1px solid var(--border-muted);
  margin-bottom: 1rem;
}

.tabs {
  display: flex;
  justify-content: space-around;
  position: relative;
}

.tab-button {
  flex: 1;
  text-align: center;
  padding: 0.5rem 0;
  font-weight: bold;
  font-size: larger;
  color: var(--text-muted);
  /* 非选中状态的文字颜色 */
  background: none;
  border: 1px solid #444;
  /* 默认透明边框 */
  border-bottom: 2px solid #333;
  /* 非选中状态的底部边框颜色 */
  cursor: pointer;
  transition: color 0.3s ease, border-color 0.3s ease;
  /* 添加颜色过渡效果 */
  border-radius: 4px 4px 0 0;
  /* 添加顶部圆角 */
}

.tab-button.active {
  color: var(--text-primary);
  /* 选中状态的文字颜色 */
  border-bottom: 2px solid #556677;
  /* 选中状态的底部边框颜色 */
  border-radius: 4px 4px 0 0;
  /* 添加顶部圆角 */
}

.tab-indicator {
  position: absolute;
  bottom: 0;
  left: 0;
  width: 50%;
  /* 每个 Tab 占一半宽度 */
  height: 2px;
  background-color: var(--text-primary);
  transition: left 0.3s ease;
}

button:disabled {
  cursor: not-allowed;
  /* 禁用状态的鼠标样式 */
  opacity: 0.5;
  /* 调低透明度 */
}

button:disabled:hover {
  background: none;
  /* 禁用状态下移除背景变化 */
  color: inherit;
  /* 保持文字颜色不变 */
  border-color: inherit;
  /* 保持边框颜色不变 */
}
</style>