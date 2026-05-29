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
          <div class="flex flex-col gap-4">
            <Button aria-label="become Core Node" @click="onStake(true)" :loading="isLoading && isCore">
              {{ $t('nodeSetting.becomeCoreNode') }}
            </Button>
            <Button variant="secondary" aria-label="become Miner" @click="onStake(false)"
              :loading="isLoading && !isCore">
              {{ $t('nodeSetting.becomeMiner') }}
            </Button>

          </div>
        </CardContent>
        <CardFooter v-if="resultMsg">
          <Alert :variant="resultSuccess ? 'default' : 'destructive'">
            <!-- <AlertTitle>{{ resultSuccess ? 'Operate Successfull' : 'Operation Fail' }}</AlertTitle> -->
            <AlertDescription>{{ resultMsg }}</AlertDescription>
            <div v-if="resultSuccess" class="mt-2 text-xs text-gray-500">Node Type: <span class="text-zinc-400 ml-1">{{
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
          <!-- <template v-if="resultSuccess">
            <div v-if="txId" class="mt-2 text-xs text-gray-500">交易ID：{{ hideAddress(txId) }}</div>
            <div v-if="resvId" class="mt-2 text-xs text-gray-500">预定ID：{{ hideAddress(resvId) }}</div>
            <div v-if="assetName" class="mt-2 text-xs text-gray-500">资产名称：{{ assetName }}</div>
            <div v-if="amt" class="mt-2 text-xs text-gray-500">质押数量：{{ amt }}</div>
          </template> -->
        </CardFooter>
      </Card>
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
    </div>
  </LayoutSecond>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import walletManager from '@/utils/sat20'
import LayoutSecond from '@/components/layout/LayoutSecond.vue'
import { Card, CardHeader, CardTitle, CardDescription, CardContent, CardFooter } from '@/components/ui/card'
import { Icon } from '@iconify/vue'

import { Button } from '@/components/ui/button'

import { Alert, AlertTitle, AlertDescription } from '@/components/ui/alert'

import { Accordion, AccordionItem, AccordionTrigger, AccordionContent } from '@/components/ui/accordion'

import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter } from '@/components/ui/dialog'
import { useWalletStore } from '@/store/wallet'
import { storeToRefs } from 'pinia'
import { hideAddress, generateMempoolUrl } from '@/utils'
import { nodeStakeStorage } from '@/lib/nodeStakeStorage'

// const guideText = `
// <span class="text-zinc-200 text-md font-bold mb-2">🔸 普通挖矿节点：轻量接入，人人可参与</span>

//  🔹 仅需质押$PEARL，无需 GPU 或 ASIC；
//  🔹 极低硬件要求（4核 CPU/8G RAM/100G SSD）；
//  🔹 与核心节点协同运行，参与 PoS 挖矿收益分配；
//  🔹 不同步全链、不运行索引器，部署快捷、维护简便。
//  🔹 一种全新的BTC参与方式 —— 无需矿场，只需上线。

// <span class="text-zinc-200 text-md font-bold mb-2">🔸 核心节点：构建 BTC 原生服务底座</span>

// 🔹 同时运行 BTC 主网节点与聪网全节点；
// 🔹 部署多类型索引器、智能合约、通道服务与前端 API 接入；
// 🔹 承载生态协议资产（ORDX、Runes、BRC20、Ordinals 等）的底层运行逻辑；
// 🔹 分润来自普通节点收益，具备完整服务能力与高性能要求。
// 🔹 硬件建议：16 核 CPU/64G RAM/2T SSD/高速网络`

const walletStore = useWalletStore()
const { btcFeeRate, network, publicKey } = storeToRefs(walletStore)
const isLoading = ref(false)
const isCore = ref(false)
const showConfirm = ref(false)
const resultMsg = ref('')
const resultSuccess = ref(false)
const txId = ref('')
const resvId = ref('')
const assetName = ref('')
const amt = ref('')
let pendingCore = false

const guideUrl = "https://github.com/sat20-labs/satoshinet/blob/main/install/guide.md"

function onStake(core: boolean) {
  isCore.value = core
  pendingCore = core
  showConfirm.value = true
}

async function confirmStake() {
  isLoading.value = true
  resultMsg.value = ''
  showConfirm.value = false
  try {
    const [err, res] = await walletManager.stakeToBeMiner(pendingCore, btcFeeRate.value.toString())
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
</script>

<style scoped lang="scss"></style>