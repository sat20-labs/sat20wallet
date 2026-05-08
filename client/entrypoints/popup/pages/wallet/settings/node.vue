<template>
  <LayoutSecond title="Node Setting">
    <div class="max-w-xl mx-auto my-8">
      <Card>
        <CardHeader>
          <CardTitle class="text-lg">{{ $t('nodeSetting.selectNodeType') }}</CardTitle>
          <CardDescription>
            <Accordion type="single" collapsible>
              <AccordionItem value="guide">
                <AccordionTrigger>
                  <p class="flex justify-start items-center text-sm text-muted-foreground">
                    <!-- <Icon icon="lucide:message-circle-warning" class="w-6 h-6 mr-1 text-green-500" /> -->
                    <span class="text-green-500 mr-1">
                      <Icon icon="lucide:link" class="w-5 h-5 mr-1 text-green-500" />
                    </span>
                    <a :href="guideUrl" target="_blank" class="text-sky-500">{{ $t('nodeSetting.guideTitle') }} -></a>
                  </p>
                </AccordionTrigger>
                <AccordionContent>
                  <div class="text-sm whitespace-pre-line" v-html="$t('nodeSetting.guideText')"></div>
                </AccordionContent>
              </AccordionItem>
            </Accordion>
          </CardDescription>
        </CardHeader>
        <CardContent>
          <!-- 已质押状态：显示取消质押按钮 -->
          <template v-if="isStaked">
            <div class="text-sm text-muted-foreground mb-4 p-3 bg-zinc-800/60 rounded">
              <div class="flex items-center gap-2 text-green-400 mb-2">
                <Icon icon="lucide:check-circle" class="w-4 h-4" />
                <span>{{ $t('nodeSetting.alreadyStaked') }}</span>
              </div>
            </div>
            <Button 
              variant="destructive" 
              class="w-full" 
              @click="onUnstake"
              :loading="isUnstaking"
            >
              {{ $t('nodeSetting.unstake') }}
            </Button>
          </template>
          
          <!-- 未质押状态：显示质押按钮 -->
          <template v-else>
            <div class="flex flex-col gap-4">
              <Button aria-label="become Core Node" disabled @click="onStake(true)" :loading="isLoading && isCore">
                {{ $t('nodeSetting.becomeCoreNode') }}
              </Button>
              <Button variant="secondary" aria-label="become Miner" @click="onStake(false)"
                :loading="isLoading && !isCore">
                {{ $t('nodeSetting.becomeMiner') }}
              </Button>
            </div>
          </template>
        </CardContent>
        <CardFooter v-if="resultMsg">
          <Alert :variant="resultSuccess ? 'default' : 'destructive'">
            <!-- <AlertTitle>{{ resultSuccess ? 'Operate Successfull' : 'Operation Fail' }}</AlertTitle> -->
            <AlertDescription>{{ resultMsg }}</AlertDescription>
            <div v-if="resultSuccess && !isUnstaking" class="mt-2 text-xs text-gray-500">Node Type: <span class="text-zinc-400 ml-1">{{
              isCore ? 'Core Node' : 'Mining Node' }}</span></div>
            <div v-if="txId" class="mt-2 text-xs text-gray-500">Transaction ID:<span class="text-zinc-400 ml-1">
                <a :href="generateMempoolUrl({ network: network, path: `tx/${txId}` })" target="_blank"
                  class="text-sky-500 hover:text-sky-400 underline cursor-pointer">
                  {{ hideAddress(txId) }}
                </a>
              </span></div>
            <div v-if="resvId" class="mt-2 text-xs text-gray-500">Reservation ID: <span class="text-zinc-400 ml-1">{{
              hideAddress(resvId) }}</span></div>
            <div v-if="assetName" class="mt-2 text-xs text-gray-500">Asset Name: <span class="text-zinc-400 ml-1">{{
              assetName }}</span></div>
            <div v-if="amt" class="mt-2 text-xs text-gray-500">Staked Amount: <span class="text-zinc-400 ml-1">{{ amt
            }}</span></div>
          </Alert>
        </CardFooter>
      </Card>
      
      <!-- 质押确认弹窗 -->
      <Dialog v-model:open="showConfirm">
        <DialogContent class="w-[95%]">
          <DialogHeader>
            <DialogTitle><span class="flex justify-center items-center text-zinc-300 text-lg">
                <Icon icon="lucide:message-circle-question-mark" class="w-8 h-8 mr-1 text-green-500" /> {{ isCore ?
                  $t('nodeSetting.confirmCoreDescription') : $t('nodeSetting.confirmMinerDescription') }}<br>
              </span></DialogTitle>
            <hr class="my-2 border-zinc-950" />
            <DialogDescription>
              <p class="text-center py-2">
                {{ $t('nodeSetting.confirmDescription') }}
              </p>
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <div class="flex justify-end gap-3">
              <Button @click="confirmStake" :loading="isLoading" class="w-36">{{ $t('nodeSetting.confirm') }}</Button>
              <Button variant="secondary" @click="showConfirm = false" class="w-36">{{ $t('nodeSetting.cancel')
              }}</Button>
            </div>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      
      <!-- 取消质押确认弹窗 -->
      <Dialog v-model:open="showUnstakeConfirm">
        <DialogContent class="w-[95%]">
          <DialogHeader>
            <DialogTitle class="text-yellow-400 flex items-center gap-2">
              <Icon icon="lucide:alert-triangle" class="w-6 h-6" />
              {{ $t('nodeSetting.confirmUnstake') }}
            </DialogTitle>
            <hr class="my-2 border-zinc-950" />
            <DialogDescription>
              <div class="space-y-3 py-2">
                <p class="text-center">
                  {{ $t('nodeSetting.confirmUnstakeDescription') }}
                </p>
                <div class="bg-yellow-500/10 border border-yellow-500/30 rounded-lg p-3 text-sm">
                  <div class="flex items-start gap-2 text-yellow-400">
                    <Icon icon="lucide:alert-circle" class="w-5 h-5 mt-0.5 flex-shrink-0" />
                    <span class="text-yellow-300">{{ $t('nodeSetting.unstakeWarning') }}</span>
                  </div>
                </div>
              </div>
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <div class="flex justify-end gap-3">
              <Button variant="destructive" @click="confirmUnstake" :loading="isUnstaking" class="w-36">
                {{ $t('nodeSetting.confirm') }}
              </Button>
              <Button variant="secondary" @click="showUnstakeConfirm = false" class="w-36">
                {{ $t('nodeSetting.cancel') }}
              </Button>
            </div>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      
      <!-- 取消质押进行中提示 -->
      <Dialog v-model:open="showUnstakingProgress">
        <DialogContent class="w-[95%]" :onPointerDownOutside="(e: Event) => e.preventDefault()">
          <DialogHeader>
            <DialogTitle class="text-yellow-400 flex items-center gap-2">
              <Icon icon="lucide:loader-2" class="w-6 h-6 animate-spin" />
              {{ $t('nodeSetting.unstaking') }}
            </DialogTitle>
          </DialogHeader>
          <div class="py-4">
            <div class="bg-yellow-500/10 border border-yellow-500/30 rounded-lg p-4 text-sm">
              <div class="flex items-start gap-2 text-yellow-400 mb-3">
                <Icon icon="lucide:alert-circle" class="w-5 h-5 mt-0.5 flex-shrink-0" />
                <span class="text-yellow-300">{{ $t('nodeSetting.unstakeWarning') }}</span>
              </div>
              <div class="flex items-center gap-2 text-zinc-300">
                <Icon icon="lucide:loader-2" class="w-4 h-4 animate-spin" />
                <span>{{ $t('nodeSetting.unstakeProcessing') }}</span>
              </div>
            </div>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  </LayoutSecond>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { storeToRefs } from 'pinia'
import { useQuery } from '@tanstack/vue-query'
import stp from '@/utils/stp'
import LayoutSecond from '@/components/layout/LayoutSecond.vue'
import { Card, CardHeader, CardTitle, CardDescription, CardContent, CardFooter } from '@/components/ui/card'

import { Button } from '@/components/ui/button'

import { Alert, AlertTitle, AlertDescription } from '@/components/ui/alert'

import { Accordion, AccordionItem, AccordionTrigger, AccordionContent } from '@/components/ui/accordion'

import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter } from '@/components/ui/dialog'
import { useWalletStore } from '@/store/wallet'
import { hideAddress, generateMempoolUrl } from '@/utils'
import { nodeStakeStorage } from '@/lib/nodeStakeStorage'
import { satnetApi } from '@/apis'

const walletStore = useWalletStore()
const { btcFeeRate, network, publicKey } = storeToRefs(walletStore)
const isLoading = ref(false)
const isCore = ref(false)
const showConfirm = ref(false)
const showUnstakeConfirm = ref(false)
const showUnstakingProgress = ref(false)
const isUnstaking = ref(false)
const resultMsg = ref('')
const resultSuccess = ref(false)
const txId = ref('')
const resvId = ref('')
const assetName = ref('')
const amt = ref('')
let pendingCore = false

const guideUrl = "https://github.com/sat20-labs/satoshinet/blob/main/install/guide.md"

// 查询矿工信息以判断是否已质押
const { data: minerInfoRes, refetch: refetchMinerInfo } = useQuery({
  queryKey: ['minerInfo', publicKey, network],
  queryFn: () => {
    if (!publicKey.value || !network.value) return Promise.resolve({})
    return satnetApi.getMinerInfo({ pubkey: publicKey.value, network: network.value })
  },
  enabled: computed(() => !!publicKey.value && !!network.value),
  refetchInterval: 30000,
})

// 判断是否已质押
const isStaked = computed(() => {
  const minerInfo = minerInfoRes.value?.data
  return !!(minerInfo?.ServerNode)
})

function onStake(core: boolean) {
  isCore.value = core
  pendingCore = core
  showConfirm.value = true
}

function onUnstake() {
  showUnstakeConfirm.value = true
}

async function confirmStake() {
  isLoading.value = true
  resultMsg.value = ''
  showConfirm.value = false
  try {
    const [err, res] = await stp.stakeToBeMiner(pendingCore, btcFeeRate.value.toString())
    console.log('res', res);

    if (err) {
      resultMsg.value = err.message || '操作失败'
      resultSuccess.value = false
      txId.value = ''
      resvId.value = ''
      assetName.value = ''
      amt.value = ''
    } else {
      resultMsg.value = '操作成功，节点质押已提交！'
      resultSuccess.value = true
      txId.value = res && res.txId ? res.txId : ''
      resvId.value = res && res.resvId ? res.resvId : ''
      assetName.value = res && res.assetName ? res.assetName : ''
      amt.value = res && res.amt ? res.amt : ''

      // 保存节点质押数据到本地存储
      if (res && publicKey.value) {
        try {
          await nodeStakeStorage.saveNodeStakeData(publicKey.value, {
            txId: res.txId || '',
            resvId: res.resvId || '',
            assetName: res.assetName || '',
            amt: res.amt || '',
            isCore: pendingCore
          })
          console.log('Node stake data saved successfully')
        } catch (storageError) {
          console.error('Failed to save node stake data:', storageError)
        }
      }
    }
  } catch (e: any) {
    resultMsg.value = e.message || '未知错误'
    resultSuccess.value = false
    txId.value = ''
    resvId.value = ''
    assetName.value = ''
    amt.value = ''
  } finally {
    isLoading.value = false
  }
}

async function confirmUnstake() {
  isUnstaking.value = true
  showUnstakeConfirm.value = false
  showUnstakingProgress.value = true
  resultMsg.value = ''
  
  try {
    const [err, res] = await stp.minerUnstake(btcFeeRate.value.toString())
    console.log('minerUnstake res', res)

    if (err) {
      resultMsg.value = err.message || $t('nodeSetting.unstakeFailed')
      resultSuccess.value = false
      txId.value = ''
    } else {
      resultMsg.value = $t('nodeSetting.unstakeSuccess')
      resultSuccess.value = true
      txId.value = res && res.txId ? res.txId : ''
      
      // 清除本地质押数据
      if (publicKey.value) {
        try {
          await nodeStakeStorage.deleteNodeStakeData(publicKey.value)
          console.log('Node stake data deleted successfully')
        } catch (storageError) {
          console.error('Failed to delete node stake data:', storageError)
        }
      }
      
      // 刷新矿工信息
      await refetchMinerInfo()
    }
  } catch (e: any) {
    resultMsg.value = e.message || $t('nodeSetting.unstakeFailed')
    resultSuccess.value = false
    txId.value = ''
  } finally {
    isUnstaking.value = false
    showUnstakingProgress.value = false
  }
}

// 简单的翻译函数
function $t(key: string): string {
  const translations: Record<string, string> = {
    'nodeSetting.unstakeFailed': '取消质押失败',
    'nodeSetting.unstakeSuccess': '取消质押成功！',
  }
  return translations[key] || key
}
</script>

<style scoped lang="scss"></style>
