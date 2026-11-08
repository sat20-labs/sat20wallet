<template>
  <LayoutSecond :title="$t('referrerManagement.bindReferrer')">
    <div class="max-w-xl mx-auto my-4">
      <div class="mb-6">
        <p class="flex justify-start items-center text-sm text-muted-foreground"><Icon icon="lucide:badge-info" class="w-10 h-10 mr-2 text-green-600"/>
          {{ $t('referrerManagement.bindReferrerDescription') }}</p>
      </div>

      <!-- 显示已绑定的推荐人 -->
      <div v-if="localBoundReferrer" class="mb-4">
        <Alert variant="default">
          <AlertTitle>{{ t('settingsUi.boundReferrer') }}</AlertTitle>
          <AlertDescription>
            {{ t('settingsUi.boundReferrerLabel') }}<span class="font-medium text-green-400">{{ localBoundReferrer }}</span>
          </AlertDescription>
        </Alert>
      </div>

      <div class="flex flex-col gap-4">
        <div class="grid w-full items-center gap-1.5">
          <Label for="referrerName">{{ $t('referrerManagement.referrerName') }}</Label>
          <Input id="referrerName" v-model="referrerName" :placeholder="$t('referrerManagement.referrerNamePlaceholder')" :disabled="isLoading || !bindingKnown || !!localBoundReferrer" />
        </div>
        <!-- <div class="grid w-full items-center gap-1.5">
          <Label for="serverPubKey">{{ $t('referrerManagement.serverPubKey') }}</Label>
          <Input id="serverPubKey" v-model="serverPubKey" :placeholder="$t('referrerManagement.serverPubKeyPlaceholder')" />
        </div> -->
        <Button :aria-label="t('settingsUi.bindReferrer')" @click="onBind" :loading="isLoading" :disabled="isLoading || !bindingKnown || !!localBoundReferrer">
          {{ localBoundReferrer ? t('settingsUi.alreadyBound') : t('settingsUi.bindReferrer') }}
        </Button>
      </div>
      <div v-if="resultMsg" class="mt-4">
        <Alert :variant="resultSuccess ? 'default' : 'destructive'">
          <AlertTitle>{{ resultSuccess ? $t(resultTxId ? 'referrerManagement.BindSuccess' : 'common.success') :
            $t('referrerManagement.BindFailure') }}</AlertTitle>
          <AlertDescription class="break-all">
            <div v-if="resultSuccess && resultTxId" class="space-y-2">
              <div>{{ t('settingsUi.bindSuccess') }}</div>
              <div class="flex items-center gap-2">
                <span class="text-sm">{{ t('settingsUi.transactionId') }}</span>
                <button
                  @click="handleMempoolClick(resultTxId)"
                  class="text-primary hover:text-primary/80 underline text-left"
                  :title="t('settingsUi.viewTransaction', { txid: resultTxId })"
                >
                  {{ shortenTxId(resultTxId) }}
                </button>
                <Icon icon="lucide:external-link" class="w-3 h-3 text-primary" />
              </div>
            </div>
            <div v-else>{{ resultMsg }}</div>
          </AlertDescription>
        </Alert>
      </div>
      <Dialog v-model:open="showConfirm">
        <DialogContent>
          <DialogHeader>
            <DialogTitle><span class="flex justify-start items-center text-zinc-300 text-lg break-all"><Icon icon="lucide:message-circle-question-mark" class="w-12 h-12 mr-1 text-red-600"/>{{ $t('referrerManagement.confirmBindDescription') }}</span></DialogTitle>
            <hr class="my-2 border-zinc-950" />
            <DialogDescription class="text-zinc-300">
              <p class="py-1 mt-4">
                <span class="text-zinc-500 mr-4">{{ $t('referrerManagement.referrerName') }} :</span>  <span
                  class="text-zinc-300 break-all">{{ referrerName }}</span>
              </p>
              <!-- <p class="py-1 break-all">
                <span class="text-zinc-500 mr- break-all">{{ $t('referrerManagement.serverPubKey') }} :</span> <span
                  class="text-zinc-300 mr-2">{{ serverPubKey }} </span>
              </p> -->

            </DialogDescription>
          </DialogHeader>

          <DialogFooter>
            <div class="flex justify-end gap-3">
              <Button @click="confirmBind" :loading="isLoading" class="w-36">{{ $t('referrerManagement.confirm') }}</Button>
              <Button variant="secondary" @click="showConfirm = false" class="w-36">{{ $t('referrerManagement.cancel') }}</Button>
            </div>
          </DialogFooter>

        </DialogContent>
      </Dialog>
    </div>
  </LayoutSecond>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { storeToRefs } from 'pinia'
import { useI18n } from 'vue-i18n'
import sat20 from '@/utils/sat20'
import LayoutSecond from '@/components/layout/LayoutSecond.vue'
import { Button } from '@/components/ui/button'
import { Icon } from '@iconify/vue'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Alert, AlertTitle, AlertDescription } from '@/components/ui/alert'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter } from '@/components/ui/dialog'
import { useGlobalStore } from '@/store/global'
import { useWalletStore } from '@/store/wallet'
import { useReferrerManager } from '@/composables/useReferrerManager'
import { getConfig } from '@/config/wasm'
import { openMempoolLink } from '@/utils/browser'
import satnetApi from '@/apis/satnet'


const { t } = useI18n()
const isLoading = ref(false)
const showConfirm = ref(false)
const resultMsg = ref('')
const resultSuccess = ref(false)
const resultTxId = ref('')
const referrerName = ref('')
const serverPubKey = ref('')
const localBoundReferrer = ref<string | null>(null)
const bindingKnown = ref(false)

const globalStore = useGlobalStore()
const walletStore = useWalletStore()
const { env } = storeToRefs(globalStore)
const { network, address } = storeToRefs(walletStore)
const statusContext = () => [env.value, network.value, address.value].join('|')
const { getLocalBoundReferrer, addLocalBoundReferrer, cacheBoundReferrerTxId, getLocalBoundReferrerTxId } = useReferrerManager()

// 获取配置中的第一个Peer的公钥
const config = getConfig(env.value, network.value)
const firstPeer = config.Peers[1]
if (firstPeer) {
  // Peer格式为 "b@<pubkey>@<url>"，我们需要提取pubkey部分
  const parts = firstPeer.split('@')
  if (parts.length >= 2) {
    serverPubKey.value = parts[1]
  }
}

// 缩短显示txId
function shortenTxId(txId: string, startLength = 8, endLength = 8): string {
  if (!txId || txId.length <= startLength + endLength) {
    return txId
  }
  return `${txId.slice(0, startLength)}...${txId.slice(-endLength)}`
}

// 处理点击mempool链接
async function handleMempoolClick(txId: string) {
  if (txId) {
    try {
      // 使用统一的 mempool 链接打开函数
      await openMempoolLink(network.value, `tx/${txId}`)
    } catch (error) {
      console.error('打开 mempool 链接失败:', error)
    }
  }
}

// 加载本地绑定的推荐人
async function loadLocalBoundReferrer() {
  const context = statusContext()
  bindingKnown.value = false
  if (!address.value) return
  isLoading.value = true
  try {
    const boundReferrer = await getLocalBoundReferrer(address.value)
    const boundReferrerTxId = await getLocalBoundReferrerTxId(address.value)

    if (context !== statusContext()) return false
    // A pending local binding also prevents a duplicate submission.
    if (boundReferrer && boundReferrerTxId) {
      localBoundReferrer.value = boundReferrer
    } else {
      const response = await satnetApi.getReferrerByAddress({ address: address.value, network: network.value })
      if (context !== statusContext()) return false
      if (response?.code === 0 && typeof response.referrer?.Name === 'string' && response.referrer.Name.trim()) {
        localBoundReferrer.value = response.referrer.Name
      } else if (response?.code === -1 && response.msg === 'key not found') {
        localBoundReferrer.value = null
      } else {
        throw new Error(t('referrerManagement.bindingLookupFailed'))
      }
    }
    bindingKnown.value = true
    return true
  } catch (error) {
    console.error('加载本地绑定推荐人失败:', error)
    resultMsg.value = t('referrerManagement.bindingLookupFailed')
    resultSuccess.value = false
    return false
  } finally {
    if (context === statusContext()) isLoading.value = false
  }
}

function onBind() {
  if (!bindingKnown.value || isLoading.value) return
  if (localBoundReferrer.value) {
    resultMsg.value = t('referrerManagement.cannotRebind')
    resultSuccess.value = false
    return
  }

  if (!referrerName.value || !serverPubKey.value) {
    resultMsg.value = t('settingsUi.completeInfo')
    resultSuccess.value = false
    return
  }
  showConfirm.value = true
}

async function confirmBind() {
  if (!bindingKnown.value || localBoundReferrer.value || isLoading.value) return
  isLoading.value = true
  resultMsg.value = ''
  resultTxId.value = ''
  showConfirm.value = false
  try {
    const [err, res] = await sat20.bindReferrerForServer(referrerName.value, serverPubKey.value)
    if (err) {
      resultMsg.value = err.message || t('referrerManagement.BindFailure')
      resultSuccess.value = false
    } else {
      resultMsg.value = t('referrerManagement.BindSuccess')
      resultSuccess.value = true

      // 绑定成功后，记录到本地存储
      if (address.value && res?.txId) {
        await addLocalBoundReferrer(address.value, referrerName.value)
        await cacheBoundReferrerTxId(address.value, res.txId)
        localBoundReferrer.value = referrerName.value
        resultTxId.value = res.txId
        console.log('绑定成功，txId:', res.txId)
      }

      // 清空输入
      referrerName.value = ''
      serverPubKey.value = ''
    }
  } catch (e: any) {
    resultMsg.value = e.message || t('settingsUi.unknownError')
    resultSuccess.value = false
  } finally {
    isLoading.value = false
  }
}

watch([address, network, env], () => {
  showConfirm.value = false
  localBoundReferrer.value = null
  bindingKnown.value = false
  loadLocalBoundReferrer()
}, { immediate: true })
</script>
