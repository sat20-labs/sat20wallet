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
          <AlertTitle>已绑定推荐人</AlertTitle>
          <AlertDescription>
            您已绑定推荐人：<span class="font-medium text-green-400">{{ localBoundReferrer }}</span>
            <div class="mt-2">
              <Button variant="outline" size="sm" @click="handleUnbind" :loading="isUnbinding">
                解除绑定
              </Button>
            </div>
          </AlertDescription>
        </Alert>
      </div>

      <div class="flex flex-col gap-4">
        <div class="grid w-full items-center gap-1.5">
          <Label for="referrerName">{{ $t('referrerManagement.referrerName') }}</Label>
          <Input id="referrerName" v-model="referrerName" :placeholder="$t('referrerManagement.referrerNamePlaceholder')" :disabled="!!localBoundReferrer" />
        </div>
        <!-- <div class="grid w-full items-center gap-1.5">
          <Label for="serverPubKey">{{ $t('referrerManagement.serverPubKey') }}</Label>
          <Input id="serverPubKey" v-model="serverPubKey" :placeholder="$t('referrerManagement.serverPubKeyPlaceholder')" />
        </div> -->
        <Button aria-label="Bind Referrer" @click="onBind" :loading="isLoading" :disabled="!!localBoundReferrer">
          {{ localBoundReferrer ? '已绑定' : '绑定推荐人' }}
        </Button>
      </div>
      <div v-if="resultMsg" class="mt-4">
        <Alert :variant="resultSuccess ? 'default' : 'destructive'">
          <AlertTitle>{{ resultSuccess ? $t('referrerManagement.BindSuccess') :
            $t('referrerManagement.BindFailure') }}</AlertTitle>
          <AlertDescription class="break-all">
            <div v-if="resultSuccess && resultTxId" class="space-y-2">
              <div>绑定成功！</div>
              <div class="flex items-center gap-2">
                <span class="text-sm">交易ID:</span>
                <button 
                  @click="handleMempoolClick(resultTxId)"
                  class="text-primary hover:text-primary/80 underline text-left"
                  :title="`查看交易 ${resultTxId}`"
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
import { ref, onMounted } from 'vue'
import { storeToRefs } from 'pinia'
import { useI18n } from 'vue-i18n'
import sat20 from '@/utils/sat20'
import LayoutSecond from '@/components/layout/LayoutSecond.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Alert, AlertTitle, AlertDescription } from '@/components/ui/alert'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter } from '@/components/ui/dialog'
import { Icon } from '@iconify/vue'
import { useGlobalStore } from '@/store/global'
import { useWalletStore } from '@/store/wallet'
import { useReferrerManager } from '@/composables/useReferrerManager'
import { getConfig } from '@/config/wasm'
import { generateMempoolUrl } from '@/utils'


const { t } = useI18n()
const isLoading = ref(false)
const isUnbinding = ref(false)
const showConfirm = ref(false)
const resultMsg = ref('')
const resultSuccess = ref(false)
const resultTxId = ref('')
const referrerName = ref('')
const serverPubKey = ref('')
const localBoundReferrer = ref<string | null>(null)

const globalStore = useGlobalStore()
const walletStore = useWalletStore()
const { env } = storeToRefs(globalStore)
const { network, address } = storeToRefs(walletStore)
const { getLocalBoundReferrer, addLocalBoundReferrer, removeLocalBoundReferrer, cacheBoundReferrerTxId, removeBoundReferrerTxId, getLocalBoundReferrerTxId } = useReferrerManager()

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
function handleMempoolClick(txId: string) {
  if (txId) {
    // 使用generateMempoolUrl生成mempool链接
    const mempoolUrl = generateMempoolUrl({
      network: network.value,
      path: `tx/${txId}`,
    })
    
    // 在新标签页中打开mempool链接
    window.open(mempoolUrl, '_blank', 'noopener,noreferrer')
  }
}

// 加载本地绑定的推荐人
async function loadLocalBoundReferrer() {
  if (!address.value) return
  
  try {
    const boundReferrer = await getLocalBoundReferrer(address.value)
    const boundReferrerTxId = await getLocalBoundReferrerTxId(address.value)
    
    // 只有当本地有绑定推荐人且有对应的txId时，才认为绑定有效
    if (boundReferrer && boundReferrerTxId) {
      localBoundReferrer.value = boundReferrer
      console.log('本地绑定的推荐人:', boundReferrer)
    } else if (boundReferrer && !boundReferrerTxId) {
      // 如果本地有绑定推荐人但没有txId，说明绑定无效，清除本地缓存
      console.log('本地绑定推荐人无效（无txId），清除缓存:', boundReferrer)
      await removeLocalBoundReferrer(address.value)
      await removeBoundReferrerTxId(address.value)
      localBoundReferrer.value = null
    } else {
      localBoundReferrer.value = null
    }
  } catch (error) {
    console.error('加载本地绑定推荐人失败:', error)
    localBoundReferrer.value = null
  }
}

function onBind() {
  if (localBoundReferrer.value) {
    resultMsg.value = t('referrerManagement.cannotRebind')
    resultSuccess.value = false
    return
  }

  if (!referrerName.value || !serverPubKey.value) {
    resultMsg.value = '请填写完整信息'
    resultSuccess.value = false
    return
  }
  showConfirm.value = true
}

async function confirmBind() {
  isLoading.value = true
  resultMsg.value = ''
  resultTxId.value = ''
  showConfirm.value = false
  try {
    const [err, res] = await sat20.bindReferrerForServer(referrerName.value, serverPubKey.value)
    console.log('bindReferrerForServer res', res);
    if (err) {
      resultMsg.value = err.message || t('referrerManagement.BindFailure')
      resultSuccess.value = false
    } else if (res && res.txId) {
      // bindReferrerForServer 返回包含txId的对象，只有存在txId才表示绑定成功
      resultMsg.value = '绑定成功！'
      resultSuccess.value = true
      resultTxId.value = res.txId
      
      // 绑定成功后，记录到本地存储
      if (address.value) {
        await addLocalBoundReferrer(address.value, referrerName.value)
        // 缓存绑定推荐人的交易ID
        await cacheBoundReferrerTxId(address.value, res.txId)
        localBoundReferrer.value = referrerName.value
      }
      
      // 清空输入
      referrerName.value = ''
      serverPubKey.value = ''
    } else {
      // 没有错误但没有交易ID，表示绑定失败
      resultMsg.value = '绑定失败：未获取到交易ID'
      resultSuccess.value = false
    }
  } catch (e: any) {
    resultMsg.value = e.message || '未知错误'
    resultSuccess.value = false
  } finally {
    isLoading.value = false
  }
}

async function handleUnbind() {
  isUnbinding.value = true
  resultMsg.value = ''
  try {
    if (!address.value) {
      resultMsg.value = '钱包地址不存在'
      return
    }
    
    // 只清除本地记录，不调用后端API
    await removeLocalBoundReferrer(address.value)
    // 同时清除绑定推荐人的交易ID
    await removeBoundReferrerTxId(address.value)
    localBoundReferrer.value = null
    resultMsg.value = t('referrerManagement.clearLocalRecord')
    resultSuccess.value = true
    
  } catch (e: any) {
    resultMsg.value = t('referrerManagement.clearLocalRecordFailed')
    resultSuccess.value = false
  } finally {
    isUnbinding.value = false
  }
}

onMounted(() => {
  loadLocalBoundReferrer()
})
</script> 