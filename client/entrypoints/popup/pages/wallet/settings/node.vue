<template>
  <LayoutSecond title="Node">
    <div class="max-w-xl mx-auto my-8">
      <Card>
        <CardHeader>
          <CardTitle>参与聪网节点质押挖矿</CardTitle>
          <CardDescription>
            <Accordion type="single" collapsible>
              <AccordionItem value="guide">
                <AccordionTrigger>聪网节点系统揭秘（点击展开）</AccordionTrigger>
                <AccordionContent>
                  <div class="text-sm whitespace-pre-line">
                    {{ guideText }}
                  </div>
                </AccordionContent>
              </AccordionItem>
            </Accordion>
          </CardDescription>
        </CardHeader>
        <CardContent>
          <div class="flex flex-col gap-4">
            <Button aria-label="成为普通挖矿节点" @click="onStake(false)" :loading="isLoading && !isCore">
              成为普通挖矿节点
            </Button>
            <Button variant="outline" aria-label="成为核心节点" @click="onStake(true)" :loading="isLoading && isCore">
              成为核心节点
            </Button>
          </div>
        </CardContent>
        <CardFooter v-if="resultMsg">
          <Alert :variant="resultSuccess ? 'default' : 'destructive'">
            <AlertTitle>{{ resultSuccess ? '操作成功' : '操作失败' }}</AlertTitle>
            <AlertDescription>{{ resultMsg }}</AlertDescription>
          </Alert>
          <template v-if="resultSuccess">
            <div v-if="txId" class="mt-2 text-xs text-gray-500">交易ID：{{ hideAddress(txId) }}</div>
            <div v-if="resvId" class="mt-2 text-xs text-gray-500">预定ID：{{ hideAddress(resvId) }}</div>
            <div v-if="assetName" class="mt-2 text-xs text-gray-500">资产名称：{{ assetName }}</div>
            <div v-if="amt" class="mt-2 text-xs text-gray-500">质押数量：{{ amt }}</div>
          </template>
        </CardFooter>
      </Card>
      <Dialog v-model:open="showConfirm">
        <DialogContent>
          <DialogHeader>
            <DialogTitle>确认操作</DialogTitle>
            <DialogDescription>
              确认要{{ isCore ? '成为核心节点' : '成为普通挖矿节点' }}吗？<br>
              操作后将调用链上质押接口。
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button @click="confirmStake" :loading="isLoading">确认</Button>
            <Button variant="outline" @click="showConfirm = false">取消</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  </LayoutSecond>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import stp from '@/utils/stp'
import LayoutSecond from '@/components/layout/LayoutSecond.vue'
import { Card, CardHeader, CardTitle, CardDescription, CardContent, CardFooter } from '@/components/ui/card'

import { Button } from '@/components/ui/button'

import { Alert, AlertTitle, AlertDescription } from '@/components/ui/alert'

import { Accordion, AccordionItem, AccordionTrigger, AccordionContent } from '@/components/ui/accordion'

import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter } from '@/components/ui/dialog'
import { useWalletStore } from '@/store/wallet'
import { hideAddress } from '@/utils'

const guideText = `聪网节点系统揭秘：BTC 生态的真正算力革命，正在悄悄开始

✍ 在 BTC 生态叙事日渐贫瘠的当下，SatoshiNet（聪网）开启了另一种可能：

一个建立在闪电通道 + L1生态资产之上的原生 L2 网络，正在用质押挖矿和节点协作系统，重塑“比特币时代的节点价值”。

聪网的挖矿机制，不靠算力，不靠电费，更不靠暴力硬分叉，它不是 BTC 的新矿机系统，而是 BTC 多资产生态的运行骨架——靠服务、靠数据、靠资产参与度驱动的原生共识机制。

聪网节点系统由两类角色构成：

🧱 普通挖矿节点：每个人都能参与的 BTC 挖矿新方式
只需质押 $PEARL，无需 GPU 或 ASIC；

不跑全链，不跑索引，硬件要求极低（4核、8G内存、100G SSD 即可）；

与连接的核心节点协同，参与聪网的 PoS 挖矿收益分配；

真正实现人人可接入、人人可产出的“轻节点挖矿”。

这是一种全新的 BTC 挖矿方式，你无需“矿场”，只需“上线”。

🧠 核心挖矿节点：BTC 生态真正的基础设施建设者
核心节点是聪网的主干服务提供者，它们运行：

主网全节点 + 聪网全节点；

多重索引器（聪索引器、主链索引器、聪穿越协议等）；

智能合约部署 + 通道；

钱包与前端应用接入。

这是 BTC 原生生态中少有的全栈节点角色，它们构成聪网的运行骨架和数据血脉。

当然，这也意味着：

更高的硬件要求（2T SSD / 64G RAM / 16核 CPU / 高带宽）；

更大的服务能力；

来自普通节点挖矿收益的一部分分润。

核心节点是基础设施，也是协议收益模型的“共识锚点”。

🧬 共识机制背后：不是PoW，也不是传统PoS
聪网的 PoS 模型基于：

$PEARL 的质押权重

节点服务类型与稳定性评分

网络运行时数据（出块、索引可用性、通道响应等）

最终将形成一个由核心节点支撑、普通节点共识验证的“多层去中心化协作网络”，这是：

BTC 网络的“应用层共识”

比特币生态原生资产的传输骨干

BTC L1 到 L2 之间的真正桥梁

💥 聪网节点测试即将上线，矿工社区即将开放入场
我们已完成普通挖矿节点的测试网联调。你只需要准备一个低配云服务器 + 公网 IP，就可以成为聪网的早期节点运营者。

即将上线的聪网主网，将开启：

全面支持ORDX, Runes资产流通，即将支持 BRC20、Ordinals、Alkanes 等协议资产；

链上 LaunchPool 智能合约一键部署；

BRC20 / ORDX / Runes / Ordinals 等资产的极速撮合交易；

节点激励机制开始运行，PEARL 链上挖矿收益定期结算。

✅ 一句话总结：
聪网不是 BTC 的新概念链，而是 BTC 的新网络层。

而运行在这个网络之上的节点，不再是“耗电机器”，而是具备资产流通 + 共识运行能力的“比特币生态服务者”。

聪网节点系统，就是 BTC 下一代基础设施的雏形。`

const walletStore = useWalletStore()
const { btcFeeRate } = storeToRefs(walletStore)
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
    const [err, res] = await stp.stakeToBeMiner(pendingCore, btcFeeRate.value.toString())
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