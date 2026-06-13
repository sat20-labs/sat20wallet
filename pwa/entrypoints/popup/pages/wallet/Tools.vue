<template>
  <LayoutHome>
    <WalletHeader />
    <div class="space-y-4 px-1 pb-4">
      <header class="space-y-2">
        <h2 class="text-2xl font-medium text-zinc-600/90">工具</h2>
        <p class="text-xs leading-5 text-muted-foreground">
          常用资产和合约操作集中在这里。
        </p>
      </header>

      <Tabs v-model="activeTab" class="w-full">
        <TabsList class="grid w-full grid-cols-3">
          <TabsTrigger value="faucet">水龙头</TabsTrigger>
          <TabsTrigger value="contracts">智能合约</TabsTrigger>
          <TabsTrigger value="mint">铸造</TabsTrigger>
        </TabsList>

        <TabsContent value="faucet" class="mt-4">
          <Card>
            <CardHeader>
              <CardTitle class="text-base">聪网 GAS 水龙头</CardTitle>
              <CardDescription>
                往合约地址转入一些聪，在聪网上获得智能合约的 GAS。
              </CardDescription>
            </CardHeader>
            <CardContent class="space-y-3">
              <div class="space-y-1">
                <Label>合约地址</Label>
                <Input v-model="faucetAddress" />
              </div>
              <div class="space-y-1">
                <Label>发送聪数量</Label>
                <Input v-model="faucetAmount" type="number" min="1" />
              </div>
              <Button class="w-full" :disabled="isFaucetSending" @click="sendFaucetSats">
                <Icon :icon="isFaucetSending ? 'lucide:loader' : 'lucide:send'" class="h-4 w-4" :class="{ 'animate-spin': isFaucetSending }" />
                发送聪
              </Button>
              <p v-if="faucetResult" class="break-all text-xs text-muted-foreground">txid: {{ faucetResult }}</p>
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="contracts" class="mt-4 space-y-4">
          <p class="text-xs leading-5 text-muted-foreground">
            在聪网上部署/调用智能合约。
            <a
              :href="SMART_CONTRACT_DOC_URL"
              target="_blank"
              rel="noopener noreferrer"
              class="ml-1 inline-flex items-center gap-1 text-primary hover:underline"
            >
              <Icon icon="lucide:file-text" class="h-3.5 w-3.5" />
              智能合约白皮书
            </a>
          </p>
          <Card>
            <CardHeader>
              <CardTitle class="text-base">部署智能合约</CardTitle>
              <CardDescription>目前支持模板合约和自然语言合约；EVM 合约暂未启用。</CardDescription>
            </CardHeader>
            <CardContent class="space-y-3">
              <div class="grid grid-cols-2 gap-3">
                <div class="space-y-1">
                  <Label>合约类型</Label>
                  <Select v-model="deployContractType">
                    <SelectTrigger>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="template">模板合约</SelectItem>
                      <SelectItem value="agent">自然语言合约</SelectItem>
                      <SelectItem value="evm" disabled>EVM 合约（暂未启用）</SelectItem>
                    </SelectContent>
                  </Select>
                </div>
                <div class="space-y-1">
                  <Label>Gas 上限</Label>
                  <Input v-model="deployContractGasLimit" type="number" min="1" placeholder="默认" />
                </div>
              </div>

              <template v-if="deployContractType === 'template'">
                <div class="grid grid-cols-[1fr_auto] gap-2">
                  <Select v-model="selectedContractSchemaKey" @update:model-value="selectContractSchema">
                    <SelectTrigger>
                      <SelectValue placeholder="选择模板合约" />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem
                        v-for="schema in deployableTemplateSchemas"
                        :key="schemaKey(schema)"
                        :value="schemaKey(schema)"
                      >
                        {{ schema.label || schema.name }}
                      </SelectItem>
                    </SelectContent>
                  </Select>
                  <Button variant="secondary" :disabled="isLoadingSupportedContracts" @click="loadSupportedContracts">
                    <Icon :icon="isLoadingSupportedContracts ? 'lucide:loader' : 'lucide:refresh-cw'" class="h-4 w-4" :class="{ 'animate-spin': isLoadingSupportedContracts }" />
                    加载
                  </Button>
                </div>
                <div v-if="selectedContractSchema?.description" class="text-xs text-muted-foreground">
                  {{ selectedContractSchema.description }}
                </div>
                <div v-for="field in selectedContractSchema?.fields || []" :key="field.name" class="space-y-1">
                  <Label>{{ field.label }}</Label>
                  <Select v-if="field.type === 'select'" v-model="deployContractForm[field.name]">
                    <SelectTrigger>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem v-for="option in field.options || []" :key="option.value" :value="option.value">
                        {{ option.label }}
                      </SelectItem>
                    </SelectContent>
                  </Select>
                  <div v-else-if="field.type === 'array'" class="space-y-2">
                    <div
                      v-for="(_row, rowIndex) in formArray(field.name)"
                      :key="`${field.name}-${rowIndex}`"
                      class="space-y-2 rounded-sm border border-border p-2"
                    >
                      <div class="flex items-center justify-between">
                        <span class="text-xs text-muted-foreground">{{ field.label }} #{{ rowIndex + 1 }}</span>
                        <Button size="sm" variant="ghost" @click="removeFormArrayItem(field.name, rowIndex)">删除</Button>
                      </div>
                      <div v-for="child in field.fields || []" :key="child.name" class="space-y-1">
                        <Label>{{ child.label }}</Label>
                        <Input
                          v-model="formArray(field.name)[rowIndex][child.name]"
                          :type="inputTypeForField(child)"
                          :placeholder="child.placeholder || child.default || ''"
                        />
                      </div>
                    </div>
                    <Button size="sm" variant="secondary" @click="addFormArrayItem(field)">添加{{ field.label }}</Button>
                  </div>
                  <Textarea
                    v-else-if="field.type === 'textarea'"
                    v-model="deployContractForm[field.name]"
                    class="min-h-24"
                    :placeholder="field.placeholder || field.default || ''"
                  />
                  <Input
                    v-else-if="field.type === 'computed'"
                    :model-value="computedContractFieldValue(field)"
                    disabled
                    :placeholder="field.placeholder || field.default || ''"
                  />
                  <div v-else-if="field.type === 'asset'" class="grid grid-cols-[96px_1fr_auto] gap-2">
                    <Select
                      :model-value="contractAssetProtocol(field.name)"
                      @update:model-value="setContractAssetProtocol(field.name, $event)"
                    >
                      <SelectTrigger>
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value="ordx">ordx</SelectItem>
                        <SelectItem value="runes">runes</SelectItem>
                        <SelectItem value="brc20">brc20</SelectItem>
                        <SelectItem value="sats">聪</SelectItem>
                      </SelectContent>
                    </Select>
                    <Input
                      :model-value="contractAssetTicker(field.name)"
                      :disabled="contractAssetProtocol(field.name) === 'sats'"
                      placeholder="ticker 名称"
                      @update:model-value="setContractAssetTicker(field.name, $event)"
                    />
                    <Button variant="secondary" @click="checkContractAsset(field.name)">检查</Button>
                  </div>
                  <Input
                    v-else
                    v-model="deployContractForm[field.name]"
                    :type="inputTypeForField(field)"
                    :placeholder="field.placeholder || field.default || ''"
                  />
                </div>
              </template>

              <template v-else-if="deployContractType === 'agent'">
                <div class="grid grid-cols-[1fr_auto] gap-2">
                  <Select v-model="selectedContractSchemaKey" @update:model-value="selectContractSchema">
                    <SelectTrigger>
                      <SelectValue placeholder="选择自然语言合约" />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem
                        v-for="schema in deployableAgentSchemas"
                        :key="schemaKey(schema)"
                        :value="schemaKey(schema)"
                      >
                        {{ schema.label || schema.name }}
                      </SelectItem>
                    </SelectContent>
                  </Select>
                  <Button variant="secondary" :disabled="isLoadingSupportedContracts" @click="loadSupportedContracts">
                    <Icon :icon="isLoadingSupportedContracts ? 'lucide:loader' : 'lucide:refresh-cw'" class="h-4 w-4" :class="{ 'animate-spin': isLoadingSupportedContracts }" />
                    加载
                  </Button>
                </div>
                <div v-if="selectedContractSchema?.description" class="text-xs text-muted-foreground">
                  {{ selectedContractSchema.description }}
                </div>
                <div v-for="field in selectedContractSchema?.fields || []" :key="field.name" class="space-y-1">
                  <Label>{{ field.label }}</Label>
                  <Select v-if="field.type === 'select'" v-model="deployContractForm[field.name]">
                    <SelectTrigger>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem v-for="option in field.options || []" :key="option.value" :value="option.value">
                        {{ option.label }}
                      </SelectItem>
                    </SelectContent>
                  </Select>
                  <div v-else-if="field.type === 'array'" class="space-y-2">
                    <div
                      v-for="(_row, rowIndex) in formArray(field.name)"
                      :key="`${field.name}-${rowIndex}`"
                      class="space-y-2 rounded-sm border border-border p-2"
                    >
                      <div class="flex items-center justify-between">
                        <span class="text-xs text-muted-foreground">{{ field.label }} #{{ rowIndex + 1 }}</span>
                        <Button size="sm" variant="ghost" @click="removeFormArrayItem(field.name, rowIndex)">删除</Button>
                      </div>
                      <div v-for="child in field.fields || []" :key="child.name" class="space-y-1">
                        <Label>{{ child.label }}</Label>
                        <Input
                          v-model="formArray(field.name)[rowIndex][child.name]"
                          :type="inputTypeForField(child)"
                          :placeholder="child.placeholder || child.default || ''"
                        />
                      </div>
                    </div>
                    <Button size="sm" variant="secondary" @click="addFormArrayItem(field)">添加{{ field.label }}</Button>
                  </div>
                  <Textarea
                    v-else-if="field.type === 'textarea'"
                    v-model="deployContractForm[field.name]"
                    class="min-h-24"
                    :placeholder="field.placeholder || field.default || ''"
                  />
                  <Input
                    v-else-if="field.type === 'computed'"
                    :model-value="computedContractFieldValue(field)"
                    disabled
                    :placeholder="field.placeholder || field.default || ''"
                  />
                  <div v-else-if="field.type === 'asset'" class="grid grid-cols-[96px_1fr_auto] gap-2">
                    <Select
                      :model-value="contractAssetProtocol(field.name)"
                      @update:model-value="setContractAssetProtocol(field.name, $event)"
                    >
                      <SelectTrigger>
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectItem value="ordx">ordx</SelectItem>
                        <SelectItem value="runes">runes</SelectItem>
                        <SelectItem value="brc20">brc20</SelectItem>
                        <SelectItem value="sats">聪</SelectItem>
                      </SelectContent>
                    </Select>
                    <Input
                      :model-value="contractAssetTicker(field.name)"
                      :disabled="contractAssetProtocol(field.name) === 'sats'"
                      placeholder="ticker 名称"
                      @update:model-value="setContractAssetTicker(field.name, $event)"
                    />
                    <Button variant="secondary" @click="checkContractAsset(field.name)">检查</Button>
                  </div>
                  <Input
                    v-else
                    v-model="deployContractForm[field.name]"
                    :type="inputTypeForField(field)"
                    :placeholder="field.placeholder || field.default || ''"
                  />
                </div>
              </template>

              <div v-else class="rounded-sm border border-dashed border-border p-3 text-xs text-muted-foreground">
                EVM 合约部署暂未启用。
              </div>

              <Button class="w-full" :disabled="!canDeploySmartContract || isDeployingSmartContract" @click="deploySmartContract">
                <Icon :icon="isDeployingSmartContract ? 'lucide:loader' : 'lucide:rocket'" class="h-4 w-4" :class="{ 'animate-spin': isDeployingSmartContract }" />
                部署智能合约
              </Button>
              <pre v-if="deploySmartContractResult" class="max-h-40 overflow-auto rounded-sm bg-zinc-950/60 p-3 text-xs leading-5 text-zinc-200">{{ deploySmartContractResult }}</pre>
            </CardContent>
          </Card>
          <Card>
            <CardHeader>
              <CardTitle class="text-base">智能合约检索</CardTitle>
              <CardDescription>从聪网 L2 indexer 查询智能合约列表和状态。</CardDescription>
            </CardHeader>
            <CardContent class="space-y-3">
              <div class="grid grid-cols-[1fr_auto] gap-2">
                <Input v-model="contractQuery" placeholder="输入合约地址精确检索" />
                <Button variant="secondary" :disabled="isContractLoading" @click="loadContract">
                  <Icon :icon="isContractLoading ? 'lucide:loader' : 'lucide:search'" class="h-4 w-4" :class="{ 'animate-spin': isContractLoading }" />
                </Button>
              </div>
              <div class="grid grid-cols-2 gap-2">
                <Button variant="outline" :disabled="isContractLoading" @click="loadContracts">加载列表</Button>
                <Button variant="outline" :disabled="!selectedContractAddress || isContractLoading" @click="loadContractHistory">查询历史</Button>
              </div>
              <div v-if="contractList.length" class="space-y-2">
                <Label>合约列表</Label>
                <button
                  v-for="contract in contractList"
                  :key="contract.address || contract.Address"
                  type="button"
                  class="w-full rounded-sm border border-border px-3 py-2 text-left text-xs hover:bg-accent"
                  @click="selectContract(contract)"
                >
                  <div class="font-medium">{{ contract.name || contract.Name || contract.subtype || contract.Subtype || 'contract' }}</div>
                  <div class="break-all text-muted-foreground">{{ contract.address || contract.Address }}</div>
                </button>
              </div>
              <div v-if="ammContractSummary" class="grid grid-cols-2 gap-2 rounded-sm border border-border bg-muted/30 p-3 text-xs">
                <div>
                  <div class="text-muted-foreground">资产 A</div>
                  <div class="mt-1 break-all font-medium">{{ ammContractSummary.assetAName }}</div>
                  <div class="mt-1 text-muted-foreground">{{ ammContractSummary.assetAAmount }}</div>
                </div>
                <div>
                  <div class="text-muted-foreground">资产 B</div>
                  <div class="mt-1 break-all font-medium">{{ ammContractSummary.assetBName }}</div>
                  <div class="mt-1 text-muted-foreground">{{ ammContractSummary.assetBAmount }}</div>
                </div>
                <div class="col-span-2 border-t border-border pt-2">
                  <span class="text-muted-foreground">价格（资产 B / 资产 A）：</span>
                  <span class="font-medium">{{ ammContractSummary.price }}</span>
                </div>
                <div class="col-span-2">
                  <span class="text-muted-foreground">状态：</span>
                  <span class="font-medium">{{ ammContractSummary.status }}</span>
                </div>
              </div>
              <pre v-else-if="contractStatusText" class="max-h-56 overflow-auto rounded-sm bg-zinc-950/60 p-3 text-xs leading-5 text-zinc-200">{{ contractStatusText }}</pre>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle class="text-base">构造调用交易</CardTitle>
              <CardDescription>
                查询合约后选择接口，钱包会按接口参数构造并广播聪网智能合约调用交易。
              </CardDescription>
            </CardHeader>
            <CardContent class="space-y-3">
              <div class="space-y-1">
                <Label>合约地址</Label>
                <Input v-model="invokeContractAddress" />
              </div>
              <div class="grid grid-cols-2 gap-3">
                <div class="space-y-1">
                  <Label>合约类型</Label>
                  <div class="flex h-10 items-center rounded-sm border border-input bg-muted/40 px-3 text-sm">
                    {{ invokeContractTypeLabel }}
                  </div>
                </div>
                <div class="space-y-1">
                  <Label>接口</Label>
                  <Select v-model="invokeAction" @update:model-value="loadInvokeParamTemplate">
                    <SelectTrigger>
                      <SelectValue placeholder="选择接口" />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem v-for="action in invokeActionOptions" :key="action" :value="action">
                        {{ action }}
                      </SelectItem>
                    </SelectContent>
                  </Select>
                </div>
              </div>
              <div v-if="invokeContractType === 'evm' && invokeAction === 'call'" class="space-y-1">
                <Label>Calldata Hex</Label>
                <Textarea v-model="invokeEvmCalldataHex" class="min-h-20 font-mono text-xs" placeholder="不带 0x 或带 0x 均可" />
              </div>
              <div v-else-if="invokeParamFields.length" class="space-y-3">
                <div v-for="field in invokeParamFields" :key="field.key" class="space-y-1">
                  <Label>{{ field.label }}</Label>
                  <Select v-if="field.options?.length" v-model="invokeParamForm[field.key]">
                    <SelectTrigger>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem v-for="option in field.options" :key="option.value" :value="option.value">
                        {{ option.label }}
                      </SelectItem>
                    </SelectContent>
                  </Select>
                  <Input
                    v-else
                    v-model="invokeParamForm[field.key]"
                    :type="field.type"
                    :placeholder="field.placeholder"
                    @update:model-value="onInvokeParamInput(field)"
                  />
                  <p v-if="invokeFieldBalanceText(field)" class="text-xs text-muted-foreground">
                    {{ invokeFieldBalanceText(field) }}
                  </p>
                  <p v-if="invokeFieldHelpText(field)" class="text-xs text-muted-foreground">
                    {{ invokeFieldHelpText(field) }}
                  </p>
                </div>
              </div>
              <div v-else class="rounded-sm border border-dashed border-border px-3 py-2 text-xs text-muted-foreground">
                当前接口无需填写额外参数。
              </div>
              <Button class="w-full" :disabled="isInvokingContract" @click="invokeSmartContract">
                <Icon :icon="isInvokingContract ? 'lucide:loader' : 'lucide:radio-tower'" class="h-4 w-4" :class="{ 'animate-spin': isInvokingContract }" />
                签名并广播
              </Button>
              <p v-if="contractInvokeResult" class="break-all text-xs text-muted-foreground">txid: {{ contractInvokeResult }}</p>
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="mint" class="mt-4 space-y-4">
          <p class="text-xs leading-5 text-muted-foreground">
            在比特币网络上铸造各种资产。
          </p>
          <Card>
            <CardHeader>
              <CardTitle class="text-base">部署 ticker</CardTitle>
            </CardHeader>
            <CardContent class="space-y-3">
              <div class="grid grid-cols-2 gap-3">
                <div class="space-y-1">
                  <Label>协议</Label>
                  <Select v-model="deployProtocol">
                    <SelectTrigger>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="ordx">ordx</SelectItem>
                      <SelectItem value="runes">runes</SelectItem>
                      <SelectItem value="brc20">brc20</SelectItem>
                    </SelectContent>
                  </Select>
                </div>
                <div class="space-y-1">
                  <Label>费率</Label>
                  <Input v-model="mintFeeRate" type="number" min="1" />
                </div>
              </div>
              <div class="grid grid-cols-[1fr_auto] gap-2">
                <Input
                  v-model="deployTicker"
                  placeholder="ticker 名称"
                  @update:model-value="handleDeployTickerInput"
                />
                <Button variant="secondary" @click="checkDeployTicker">检查</Button>
              </div>
              <div :class="showDeployLimit ? 'grid grid-cols-2 gap-3' : 'grid grid-cols-1 gap-3'">
                <div class="space-y-1">
                  <Label>最大数量</Label>
                  <Input v-model="deployMaxSupply" type="number" />
                </div>
                <div v-if="showDeployLimit" class="space-y-1">
                  <Label>每次铸造数量</Label>
                  <Input v-model="deployLimit" type="number" />
                </div>
              </div>
              <label class="flex items-center gap-2 text-sm text-muted-foreground">
                <Checkbox v-model:checked="deploySelfMint" />
                只能部署者铸造
              </label>
              <div v-if="deployProtocol === 'ordx'" class="space-y-1">
                <Label>每聪绑定资产份数</Label>
                <Select v-model="bindingSat">
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem v-for="option in bindingSatOptions" :key="option" :value="option">
                      {{ option }}
                    </SelectItem>
                  </SelectContent>
                </Select>
              </div>
              <Button class="w-full" :disabled="!isDeployTickerReady || isDeployingTicker" @click="deployTickerAction">
                <Icon :icon="isDeployingTicker ? 'lucide:loader' : 'lucide:upload-cloud'" class="h-4 w-4" :class="{ 'animate-spin': isDeployingTicker }" />
                部署 ticker
              </Button>
              <p v-if="deployTickerResult" class="break-all text-xs text-muted-foreground">txid: {{ deployTickerResult }}</p>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle class="text-base">铸造资产</CardTitle>
            </CardHeader>
            <CardContent class="space-y-3">
              <div class="grid grid-cols-2 gap-3">
                <div class="space-y-1">
                  <Label>协议</Label>
                  <Select v-model="mintProtocol">
                    <SelectTrigger>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="ordx">ordx</SelectItem>
                      <SelectItem value="runes">runes</SelectItem>
                      <SelectItem value="brc20">brc20</SelectItem>
                    </SelectContent>
                  </Select>
                </div>
                <div class="space-y-1">
                  <Label>数量</Label>
                  <Input v-model="mintAmount" type="number" :disabled="mintProtocol === 'runes'" />
                  <p v-if="mintProtocol === 'runes'" class="text-xs text-muted-foreground">
                    Runes 每次铸造数量由部署条款决定。
                  </p>
                </div>
              </div>
              <div class="grid grid-cols-[1fr_auto] gap-2">
                <Input
                  v-model="mintTicker"
                  placeholder="ticker 名称"
                  @update:model-value="handleMintTickerInput"
                />
                <Button variant="secondary" @click="checkMintTicker">检查</Button>
              </div>
              <Button class="w-full" :disabled="!isMintAssetReady || isMintingAsset" @click="mintAssetAction">
                <Icon :icon="isMintingAsset ? 'lucide:loader' : 'lucide:coins'" class="h-4 w-4" :class="{ 'animate-spin': isMintingAsset }" />
                铸造资产
              </Button>
              <p v-if="mintAssetResult" class="break-all text-xs text-muted-foreground">txid: {{ mintAssetResult }}</p>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle class="text-base">铸造 DID</CardTitle>
            </CardHeader>
            <CardContent class="space-y-3">
              <div class="grid grid-cols-[1fr_auto] gap-2">
                <Input v-model="didName" placeholder="输入名字" />
                <Button variant="secondary" @click="checkDidName">检查</Button>
              </div>
              <Button class="w-full" :disabled="!isMintDidReady || isMintingDid" @click="mintDidAction">
                <Icon :icon="isMintingDid ? 'lucide:loader' : 'lucide:badge-check'" class="h-4 w-4" :class="{ 'animate-spin': isMintingDid }" />
                铸造 DID
              </Button>
              <p v-if="didMintResult" class="break-all text-xs text-muted-foreground">txid: {{ didMintResult }}</p>
            </CardContent>
          </Card>
        </TabsContent>
      </Tabs>
    </div>
  </LayoutHome>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { storeToRefs } from 'pinia'
import { Icon } from '@iconify/vue'
import LayoutHome from '@/components/layout/LayoutHome.vue'
import WalletHeader from '@/components/wallet/HomeHeader.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Textarea } from '@/components/ui/textarea'
import { useToast } from '@/components/ui/toast-new/use-toast'
import { smartContractApi } from '@/apis'
import ordxApi from '@/apis/ordx'
import sat20 from '@/utils/sat20'
import { useWalletStore } from '@/store'

const SMART_CONTRACT_DOC_URL = 'https://docs.sat20.org/circulation/contract/'
const TEMP_FAUCET_CONTRACT_ADDRESS = 'tb1qtysvxt6ftg6ph8dln9e9gx8tu35ahaekckupyqqa2dz63gdmcvrsl6yvdn'

const { toast } = useToast()
const walletStore = useWalletStore()
const { network } = storeToRefs(walletStore)

const activeTab = ref('faucet')

const faucetAddress = ref(TEMP_FAUCET_CONTRACT_ADDRESS)
const faucetAmount = ref('1000')
const faucetResult = ref('')
const isFaucetSending = ref(false)

const contractQuery = ref('')
const contractList = ref<any[]>([])
const selectedContract = ref<any | null>(null)
const contractState = ref<any | null>(null)
const contractHistory = ref<any | null>(null)
const isContractLoading = ref(false)
const selectedContractAddress = computed(() => selectedContract.value?.address || selectedContract.value?.Address || contractQuery.value.trim())
const contractStatusText = computed(() => {
  const data = {
    selected: selectedContract.value,
    state: contractState.value,
    history: contractHistory.value,
  }
  return selectedContract.value || contractState.value || contractHistory.value ? JSON.stringify(data, null, 2) : ''
})

const invokeContractAddress = ref('')
const invokeContractType = ref<'template' | 'agent' | 'evm'>('template')
const invokeContractSubtype = ref('')
const invokeAction = ref('default')
const invokeParamForm = ref<Record<string, string>>({})
const invokeParamTemplate = ref<Record<string, any>>({})
const invokeParamWrapperAction = ref('')
const invokeEvmCalldataHex = ref('')
const contractInvokeResult = ref('')
const isInvokingContract = ref(false)
const templateInvokeActionsBySubtype: Record<string, string[]> = {
  'limitorder.tc': ['default', 'swap', 'refund', 'close'],
  'swap.tc': ['default', 'swap', 'refund', 'close'],
  'amm.tc': ['default', 'swap', 'addliq', 'removeliq', 'close'],
  'exchange.tc': ['exchange', 'close'],
}
const agentInvokeActions = ['default', 'ready', 'bet', 'confirm', 'reject']
const invokeActionOptions = computed(() => {
  if (invokeContractType.value === 'template') {
    if (invokeContractSubtype.value === 'amm.tc' && !ammCanSwap.value) {
      return ['addliq', 'close']
    }
    return templateInvokeActionsBySubtype[invokeContractSubtype.value] || ['default']
  }
  if (invokeContractType.value === 'agent') return agentInvokeActions
  return ['default', 'call']
})
const invokeContractTypeLabel = computed(() => {
  if (invokeContractType.value === 'template') {
    return invokeContractSubtype.value ? `模板合约 (${invokeContractSubtype.value})` : '模板合约'
  }
  if (invokeContractType.value === 'agent') return '自然语言合约'
  if (invokeContractType.value === 'evm') return 'EVM 合约'
  return '未知合约'
})
const deployContractType = ref<'template' | 'agent' | 'evm'>('template')
type ContractFieldSchema = {
  name: string
  label: string
  type: string
  required?: boolean
  default?: string
  placeholder?: string
  options?: { label: string; value: string }[]
  fields?: ContractFieldSchema[]
}
type ContractSchema = {
  type: 'template' | 'agent' | 'evm' | string
  subtype?: string
  name: string
  label: string
  enabled: boolean
  description?: string
  actions?: string[]
  fields?: ContractFieldSchema[]
}
const contractSchemas = ref<ContractSchema[]>([])
const selectedContractSchemaKey = ref('')
const deployContractForm = ref<Record<string, any>>({})
const deployContractGasLimit = ref('')
const deploySmartContractResult = ref('')
const isLoadingSupportedContracts = ref(false)
const isDeployingSmartContract = ref(false)
const schemaKey = (schema: ContractSchema) => `${schema.type}:${schema.subtype || schema.name}`
const deployableTemplateSchemas = computed(() => contractSchemas.value.filter((schema) => schema.type === 'template' && schema.enabled))
const deployableAgentSchemas = computed(() => contractSchemas.value.filter((schema) => schema.type === 'agent' && schema.enabled))
const selectedContractSchema = computed(() => contractSchemas.value.find((schema) => schemaKey(schema) === selectedContractSchemaKey.value))
const canDeploySmartContract = computed(() => {
  if (deployContractType.value === 'evm') return false
  const schema = selectedContractSchema.value
  if (!schema || !schema.enabled) return false
  return formHasRequiredValues(schema.fields || [])
})

const deployProtocol = ref<'ordx' | 'runes' | 'brc20'>('ordx')
const deployTicker = ref('')
const deployMaxSupply = ref('21000000')
const deployLimit = ref('1000')
const deploySelfMint = ref(false)
const bindingSatOptions = ['1', '10', '100', '1000', '10000', '100000']
const bindingSat = ref('1')
const showDeployLimit = computed(() => !(deployProtocol.value === 'runes' && deploySelfMint.value))
const mintFeeRate = ref('1')
const deployTickerResult = ref('')
const isDeployingTicker = ref(false)
const deployCanDeploy = ref(false)
const deployCheckKey = ref('')

const mintProtocol = ref<'ordx' | 'runes' | 'brc20'>('ordx')
const mintTicker = ref('')
const mintAmount = ref('')
const mintAssetResult = ref('')
const isMintingAsset = ref(false)
const mintCanMint = ref(false)
const mintCheckKey = ref('')

const didName = ref('')
const didCanMint = ref(false)
const didCheckKey = ref('')
const didMintResult = ref('')
const isMintingDid = ref(false)

const showError = (title: string, error: unknown) => {
  toast({
    title,
    description: error instanceof Error ? error.message : String(error),
    variant: 'destructive',
    duration: 2500,
  })
}

const showSuccess = (title: string, description: string) => {
  toast({ title, description, variant: 'success', duration: 1800 })
}

const parsePositiveInteger = (value: string, field: string) => {
  const parsed = Number(value)
  if (!Number.isInteger(parsed) || parsed <= 0) {
    throw new Error(`${field} 必须是正整数`)
  }
  return parsed
}

const RUNES_SPACER = '•'
const normalizeRunesTickerText = (ticker: string) => ticker.replace(/[.\s]+/g, RUNES_SPACER).toUpperCase()
const normalizeTickerInput = (ticker: string, protocol = '') => {
  if (protocol !== 'runes') return ticker
  return normalizeRunesTickerText(ticker).replace(new RegExp(`^${RUNES_SPACER}+`, 'g'), '')
}
const normalizeTicker = (ticker: string, protocol = '') => {
  const text = ticker.trim()
  if (protocol !== 'runes') return text
  return normalizeRunesTickerText(text)
    .replace(new RegExp(`^${RUNES_SPACER}+|${RUNES_SPACER}+$`, 'g'), '')
}
const assetNameFor = (protocol: string, ticker: string) => `${protocol}:f:${normalizeTicker(ticker, protocol)}`
const contractAssetProtocols = ['ordx', 'runes', 'brc20']
const contractAssetTickerFromName = (assetName: string) => {
  if (assetName === '::') return ''
  const parts = assetName.split(':')
  if (parts.length === 3) return parts[2]
  if (parts.length === 2) return parts[1]
  return assetName
}
const contractAssetProtocol = (fieldName: string) => {
  const assetName = String(deployContractForm.value[fieldName] || '').trim()
  const protocol = assetName.split(':')[0]
  if (assetName === '::') return 'sats'
  if (contractAssetProtocols.includes(protocol)) return protocol
  return 'ordx'
}
const contractAssetTicker = (fieldName: string) => {
  const assetName = String(deployContractForm.value[fieldName] || '').trim()
  return contractAssetTickerFromName(assetName)
}
const contractAssetNameFor = (protocol: string, ticker: string) => {
  if (protocol === 'sats') return '::'
  return assetNameFor(protocol, ticker)
}
const setContractAssetProtocol = (fieldName: string, protocolValue: unknown) => {
  const protocol = String(protocolValue || 'ordx')
  const ticker = contractAssetTicker(fieldName)
  deployContractForm.value[fieldName] = contractAssetNameFor(protocol, ticker)
}
const setContractAssetTicker = (fieldName: string, tickerValue: unknown) => {
  const protocol = contractAssetProtocol(fieldName)
  const ticker = normalizeTickerInput(String(tickerValue || ''), protocol)
  deployContractForm.value[fieldName] = contractAssetNameFor(protocol, ticker)
}
const handleDeployTickerInput = (value: string | number) => {
  deployTicker.value = normalizeTickerInput(String(value), deployProtocol.value)
}
const handleMintTickerInput = (value: string | number) => {
  mintTicker.value = normalizeTickerInput(String(value), mintProtocol.value)
}
const currentDeployCheckKey = computed(() => `${deployProtocol.value}:${normalizeTicker(deployTicker.value, deployProtocol.value)}`)
const currentMintCheckKey = computed(() => [
  mintProtocol.value,
  normalizeTicker(mintTicker.value, mintProtocol.value),
  mintProtocol.value === 'runes' ? '' : String(mintAmount.value || ''),
  walletStore.address || '',
].join(':'))
const currentDidCheckKey = computed(() => didName.value.trim().toLowerCase())
const isDeployTickerReady = computed(() => deployCanDeploy.value && deployCheckKey.value === currentDeployCheckKey.value)
const isMintAssetReady = computed(() => mintCanMint.value && mintCheckKey.value === currentMintCheckKey.value)
const isMintDidReady = computed(() => didCanMint.value && didCheckKey.value === currentDidCheckKey.value)
watch(deployProtocol, (protocol) => {
  if (protocol === 'runes') deployTicker.value = normalizeTicker(deployTicker.value, protocol)
})
watch(mintProtocol, (protocol) => {
  if (protocol === 'runes') mintTicker.value = normalizeTicker(mintTicker.value, protocol)
})
watch(deployContractType, () => {
  selectedContractSchemaKey.value = ''
  deployContractForm.value = {}
  selectFirstSchemaForType()
})
const normalizeDecimalForCompare = (value: unknown) => {
  const text = String(value ?? '').trim()
  if (!/^\d+(\.\d+)?$/.test(text)) return null
  const [integerPart, fractionPart = ''] = text.split('.')
  return {
    integer: integerPart.replace(/^0+(?=\d)/, '') || '0',
    fraction: fractionPart.replace(/0+$/, ''),
  }
}
const compareDecimalStrings = (a: unknown, b: unknown) => {
  const left = normalizeDecimalForCompare(a)
  const right = normalizeDecimalForCompare(b)
  if (!left || !right) return Number.NaN
  if (left.integer.length !== right.integer.length) return left.integer.length > right.integer.length ? 1 : -1
  if (left.integer !== right.integer) return left.integer > right.integer ? 1 : -1
  const fractionLength = Math.max(left.fraction.length, right.fraction.length)
  const leftFraction = left.fraction.padEnd(fractionLength, '0')
  const rightFraction = right.fraction.padEnd(fractionLength, '0')
  if (leftFraction === rightFraction) return 0
  return leftFraction > rightFraction ? 1 : -1
}
const isPositiveDecimalString = (value: unknown) => compareDecimalStrings(value, '0') > 0

const sendFaucetSats = async () => {
  try {
    isFaucetSending.value = true
    faucetResult.value = ''
    const amount = parsePositiveInteger(faucetAmount.value, '发送聪数量')
    const [err, txid] = await sat20.sendAssets_SatsNet(faucetAddress.value.trim(), '::', amount, '')
    if (err) throw err
    faucetResult.value = txid || ''
    showSuccess('发送成功', txid || '交易已提交')
  } catch (error) {
    showError('发送失败', error)
  } finally {
    isFaucetSending.value = false
  }
}

const parseOptionalPositiveInteger = (value: string, field: string) => {
  const text = String(value || '').trim()
  if (!text) return 0
  return parsePositiveInteger(text, field)
}

const normalizeInvokeContractType = (value: unknown): 'template' | 'agent' | 'evm' | '' => {
  const text = String(value ?? '').toLowerCase()
  if (text.includes('template') || text === '1') return 'template'
  if (text.includes('agent') || text === '2') return 'agent'
  if (text.includes('evm') || text === '3') return 'evm'
  return ''
}

const normalizeTemplateSubtype = (value: unknown) => {
  const text = String(value ?? '').trim().toLowerCase()
  if (['limitorder.tc', 'swap.tc'].includes(text)) return 'limitorder.tc'
  if (text === 'amm.tc') return 'amm.tc'
  if (text === 'exchange.tc') return 'exchange.tc'
  return ''
}

const walkValues = (value: unknown, visitor: (key: string, item: unknown) => string | undefined): string | undefined => {
  if (!value || typeof value !== 'object') return undefined
  if (Array.isArray(value)) {
    for (const item of value) {
      const found = walkValues(item, visitor)
      if (found) return found
    }
    return undefined
  }
  for (const [key, item] of Object.entries(value as Record<string, unknown>)) {
    const direct = visitor(key, item)
    if (direct) return direct
    const nested = walkValues(item, visitor)
    if (nested) return nested
  }
  return undefined
}

const contractLookupPayload = () => ({
  selected: selectedContract.value,
  state: contractState.value,
})

const findContractString = (keys: string[]) => {
  const wanted = new Set(keys.map((key) => key.toLowerCase()))
  return walkValues(contractLookupPayload(), (key, item) => (
    wanted.has(key.toLowerCase()) && typeof item === 'string' && item.trim() ? item.trim() : undefined
  ))
}

const displayAssetName = (assetName: string) => {
  if (!assetName) return '-'
  if (assetName === '::') return '聪'
  return assetName
}

const isPositiveDecimalValue = (value: unknown) => {
  const n = Number(value)
  return Number.isFinite(n) && n > 0
}

const decimalRatio = (numerator: string, denominator: string) => {
  const n = Number(numerator)
  const d = Number(denominator)
  if (!Number.isFinite(n) || !Number.isFinite(d) || d <= 0) return '-'
  const ratio = n / d
  if (!Number.isFinite(ratio)) return '-'
  return ratio.toLocaleString(undefined, { maximumFractionDigits: 10 })
}

const inferInvokeContractType = () => (
  normalizeInvokeContractType(findContractString(['contractType', 'ContractType']))
  || normalizeInvokeContractType(walkValues(contractLookupPayload(), (key, item) => (
    ['contractTypeId', 'ContractTypeID', 'contractTypeID'].includes(key) && (typeof item === 'number' || typeof item === 'string')
      ? String(item)
      : undefined
  )))
)

const inferInvokeContractSubtype = () => {
  const direct = findContractString([
    'templateName',
    'TemplateName',
    'template',
    'Template',
    'name',
    'Name',
    'subtype',
    'Subtype',
  ])
  const normalizedDirect = normalizeTemplateSubtype(direct)
  if (normalizedDirect) return normalizedDirect
  return walkValues(contractLookupPayload(), (_key, item) => {
    if (typeof item !== 'string') return undefined
    return normalizeTemplateSubtype(item) || undefined
  }) || ''
}

const contractAssetAName = computed(() => findContractString(['assetAName', 'AssetAName', 'assetName', 'AssetName']))
const contractAssetBName = computed(() => findContractString(['assetBName', 'AssetBName']) || '::')
const contractAssetAInPool = computed(() => findContractString(['assetAInPool', 'AssetAInPool']) || '0')
const contractAssetBInPool = computed(() => findContractString(['assetBInPool', 'AssetBInPool']) || '0')
const contractRequiredAssetA = computed(() => findContractString(['requiredAssetA', 'RequiredAssetA']) || '')
const contractRequiredAssetB = computed(() => findContractString(['requiredAssetB', 'RequiredAssetB']) || '')
const contractAssetAAmount = computed(() => contractAssetAInPool.value || '-')
const contractAssetBAmount = computed(() => contractAssetBInPool.value || contractRequiredAssetB.value || '-')
const ammCanSwap = computed(() => (
  invokeContractSubtype.value === 'amm.tc'
  && isPositiveDecimalValue(contractAssetAAmount.value)
  && isPositiveDecimalValue(contractAssetBAmount.value)
))
const ammContractSummary = computed(() => {
  if (invokeContractSubtype.value !== 'amm.tc') return null
  return {
    assetAName: displayAssetName(contractAssetAName.value || '资产 A'),
    assetAAmount: contractAssetAAmount.value,
    assetBName: displayAssetName(contractAssetBName.value),
    assetBAmount: contractAssetBAmount.value,
    price: decimalRatio(contractAssetBAmount.value, contractAssetAAmount.value),
    status: ammCanSwap.value ? '可交易' : '需要先添加流动性',
  }
})

type InvokeParamField = {
  key: string
  label: string
  type: string
  placeholder: string
  defaultValue?: string
  hidden?: boolean
  valueKind?: 'string' | 'number' | 'intList'
  balanceAsset?: string | 'assetName'
  balanceLabel?: string
  options?: { label: string; value: string }[]
}

const invokeParamLabels: Record<string, string> = {
  orderType: '订单类型',
  assetName: '资产名称',
  amt: '资产数量',
  unitPrice: '单价',
  value: '聪数量',
  lptAmt: '流动性资产数量',
  minOutA: '最小获得资产 A 数量',
  outcome_id: '结果 ID',
  result_type: '结果类型',
  source_url: '来源 URL',
  result_url: '结果 URL',
  result_hash: '结果哈希',
  observed_at: '观察时间',
  agent_version: 'Agent 版本',
  model_version: '模型版本',
  core_node_pubkey: 'Core Node 公钥',
  core_node_signature: 'Core Node 签名',
  reason: '原因',
  checked_at: '检查时间',
  calldataHex: 'Calldata Hex',
}

const invokeParamFieldTemplates = ref<InvokeParamField[]>([])
const invokeParamFields = computed<InvokeParamField[]>(() => invokeParamFieldTemplates.value.filter((field) => !field.hidden))
const invokeBalanceMap = ref<Record<string, { availableAmt: string; lockedAmt: string; loading?: boolean; error?: string }>>({})

watch([invokeContractType, invokeContractSubtype], () => {
  if (!invokeActionOptions.value.includes(invokeAction.value)) {
    invokeAction.value = invokeActionOptions.value[0] || 'default'
  }
  void loadInvokeParamTemplate()
})

watch([selectedContract, contractState], () => {
  const inferred = inferInvokeContractType()
  if (inferred) invokeContractType.value = inferred
  const subtype = inferInvokeContractSubtype()
  if (subtype) invokeContractSubtype.value = subtype
  void loadInvokeParamTemplate()
})

const parseMaybeJson = (value: unknown) => {
  if (typeof value !== 'string') return value
  const text = value.trim()
  if (!text) return {}
  try {
    return JSON.parse(text)
  } catch {
    return value
  }
}

const normalizeInvokeParamValue = (key: string, value: unknown) => {
  const text = String(value ?? '').trim()
  const field = invokeParamFieldTemplates.value.find((item) => item.key === key)
  if (field?.valueKind === 'intList') {
    if (!text) return []
    return text.split(',').map((item) => Number(item.trim())).filter((item) => Number.isInteger(item) && item >= 0)
  }
  if (field?.valueKind === 'number' || ['orderType', 'value', 'observed_at', 'checked_at'].includes(key)) {
    return text ? Number(text) : 0
  }
  return text
}

const invokeParams = () => Object.fromEntries(
  Object.entries(invokeParamForm.value).map(([key, value]) => [key, normalizeInvokeParamValue(key, value)])
)

const applyInvokeParamTemplate = (parameter: unknown) => {
  const parsed = parseMaybeJson(parameter)
  let fields: Record<string, unknown> = {}
  invokeParamWrapperAction.value = ''
  if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
    const wrapper = parsed as Record<string, unknown>
    if (typeof wrapper.action === 'string') {
      invokeParamWrapperAction.value = wrapper.action
      fields = parseMaybeJson(wrapper.param) as Record<string, unknown>
    } else {
      fields = wrapper
    }
  }
  if (!fields || typeof fields !== 'object' || Array.isArray(fields)) {
    fields = {}
  }
  invokeParamTemplate.value = fields
  invokeParamForm.value = Object.fromEntries(
    Object.entries(fields).map(([key, value]) => [
      key,
      key === 'assetName' && !String(value ?? '').trim()
        ? contractAssetAName.value || ''
        : String(value ?? ''),
    ])
  )
  if ('calldataHex' in fields) {
    invokeEvmCalldataHex.value = String(fields.calldataHex ?? '')
  }
}

const invokeTextField = (
  key: string,
  label: string,
  placeholder = '',
  value = '',
  valueKind: InvokeParamField['valueKind'] = 'string',
  extra: Partial<InvokeParamField> = {}
): InvokeParamField => ({
  key,
  label,
  placeholder,
  defaultValue: value,
  type: valueKind === 'number' ? 'number' : 'text',
  valueKind,
  hidden: false,
  ...extra,
})
const invokeHiddenField = (key: string, value: string, valueKind: InvokeParamField['valueKind'] = 'number'): InvokeParamField => ({
  key,
  label: invokeParamLabels[key] || key,
  placeholder: '',
  defaultValue: value,
  type: valueKind === 'number' ? 'number' : 'text',
  valueKind,
  hidden: true,
})
const invokeOrderTypeField = (): InvokeParamField => ({
  key: 'orderType',
  label: '订单类型',
  placeholder: '',
  defaultValue: '2',
  type: 'number',
  valueKind: 'number',
  options: [
    { label: '买入', value: '2' },
    { label: '卖出', value: '1' },
  ],
})

const localInvokeTemplate = () => {
  const action = invokeAction.value
  const assetA = contractAssetAName.value || ''
  if (action === 'default' || action === 'close' || action === 'ready') return []
  if (invokeContractType.value === 'template') {
    if (['limitorder.tc', 'swap.tc'].includes(invokeContractSubtype.value)) {
      if (action === 'swap') {
        return [
          invokeOrderTypeField(),
          invokeTextField('assetName', '资产名称', '默认使用合约资产 A', assetA, 'string', { balanceAsset: 'assetName' }),
          invokeTextField('amt', '资产数量', '', '', 'string', { balanceAsset: 'assetName' }),
          invokeTextField('unitPrice', '单价'),
        ]
      }
      if (action === 'refund') {
        return [invokeTextField('itemIds', '订单 ID', '可选，多个 ID 用英文逗号分隔', '', 'intList')]
      }
    }
    if (invokeContractSubtype.value === 'amm.tc') {
      if (action === 'swap') {
        return [
          invokeOrderTypeField(),
          invokeTextField('assetName', '资产名称', '默认使用资产 A', assetA, 'string', { balanceAsset: 'assetName' }),
          invokeTextField('amt', '资产数量', '', '', 'string', { balanceAsset: 'assetName' }),
          invokeTextField('unitPrice', '单价，可参考上方价格'),
        ]
      }
      if (action === 'addliq') {
        return [
          invokeHiddenField('orderType', '9'),
          invokeTextField('assetName', '资产名称', '默认使用资产 A', assetA, 'string', { balanceAsset: 'assetName' }),
          invokeTextField('amt', '资产数量', '', '', 'string', { balanceAsset: 'assetName' }),
          invokeTextField('value', '聪数量', '', '', 'number', { balanceAsset: '::' }),
        ]
      }
      if (action === 'removeliq') {
        return [
          invokeHiddenField('orderType', '10'),
          invokeTextField('assetName', '资产名称', '默认使用资产 A', assetA, 'string', { balanceAsset: 'assetName' }),
          invokeTextField('lptAmt', '流动性资产数量', '', '', 'string', { balanceLabel: '当前 LP 可用' }),
        ]
      }
    }
    if (invokeContractSubtype.value === 'exchange.tc' && action === 'exchange') {
      return [invokeTextField('minOutA', '最小获得资产 A 数量', '可选')]
    }
  }
  if (invokeContractType.value === 'agent') {
    if (action === 'bet') return [invokeTextField('outcome_id', '结果 ID')]
    if (action === 'confirm') {
      return [
        invokeTextField('result_type', '结果类型', 'outcome / invalid / cancelled'),
        invokeTextField('outcome_id', '结果 ID', 'result_type 为 outcome 时填写'),
        invokeTextField('source_url', '来源 URL'),
        invokeTextField('result_url', '结果 URL'),
        invokeTextField('result_hash', '结果哈希'),
        invokeTextField('observed_at', '观察时间', 'Unix 时间戳', '', 'number'),
        invokeTextField('agent_version', 'Agent 版本', '可选'),
        invokeTextField('model_version', '模型版本', '可选'),
        invokeTextField('core_node_pubkey', 'Core Node 公钥', '可选'),
        invokeTextField('core_node_signature', 'Core Node 签名', '可选'),
      ]
    }
    if (action === 'reject') {
      return [
        invokeTextField('reason', '原因'),
        invokeTextField('checked_at', '检查时间', 'Unix 时间戳', '', 'number'),
      ]
    }
  }
  if (invokeContractType.value === 'evm' && action === 'call') {
    return [invokeTextField('calldataHex', 'Calldata Hex', '不带 0x 或带 0x 均可')]
  }
  return []
}

const resolveInvokeBalanceAsset = (field: InvokeParamField) => {
  if (field.balanceAsset === 'assetName') {
    return String(invokeParamForm.value.assetName || contractAssetAName.value || '').trim()
  }
  return String(field.balanceAsset || '').trim()
}

const isQueryableInvokeAsset = (assetName: string) => (
  assetName === '::'
  || assetName.split(':').length >= 3
)

const invokeBalanceCacheKey = (assetName: string) => `${walletStore.address || ''}:${assetName}`

const loadInvokeAssetBalance = async (assetName: string) => {
  const address = walletStore.address || ''
  if (!address || !assetName || !isQueryableInvokeAsset(assetName)) return
  const key = invokeBalanceCacheKey(assetName)
  const previous = invokeBalanceMap.value[key]
  if (previous?.loading) return
  invokeBalanceMap.value = {
    ...invokeBalanceMap.value,
    [key]: {
      availableAmt: previous?.availableAmt || '0',
      lockedAmt: previous?.lockedAmt || '0',
      loading: true,
    },
  }
  try {
    const [err, res] = await sat20.getAssetAmount_SatsNet(address, assetName)
    if (err) throw err
    invokeBalanceMap.value = {
      ...invokeBalanceMap.value,
      [key]: {
        availableAmt: String(res?.availableAmt || '0'),
        lockedAmt: String(res?.lockedAmt || '0'),
      },
    }
  } catch (error) {
    invokeBalanceMap.value = {
      ...invokeBalanceMap.value,
      [key]: {
        availableAmt: previous?.availableAmt || '0',
        lockedAmt: previous?.lockedAmt || '0',
        error: error instanceof Error ? error.message : String(error),
      },
    }
  }
}

const loadInvokeFieldBalances = () => {
  const assets = new Set<string>()
  for (const field of invokeParamFieldTemplates.value) {
    const assetName = resolveInvokeBalanceAsset(field)
    if (assetName) assets.add(assetName)
  }
  for (const assetName of assets) {
    void loadInvokeAssetBalance(assetName)
  }
}

const findLpBalanceForWallet = () => {
  const address = String(walletStore.address || '').trim()
  if (!address) return ''
  const found = walkValues(contractLookupPayload(), (key, item) => {
    if (key.toLowerCase() !== 'lpbalances' || !item || typeof item !== 'object' || Array.isArray(item)) {
      return undefined
    }
    const balance = (item as Record<string, unknown>)[address]
    return balance === undefined || balance === null ? undefined : String(balance)
  })
  return found || ''
}

const invokeFieldBalanceText = (field: InvokeParamField) => {
  if (field.key === 'assetName') return ''
  if (field.key === 'lptAmt') {
    const lpBalance = findLpBalanceForWallet()
    return lpBalance ? `${field.balanceLabel || '可用'}：${lpBalance}` : ''
  }
  const assetName = resolveInvokeBalanceAsset(field)
  if (!assetName || !isQueryableInvokeAsset(assetName)) return ''
  const balance = invokeBalanceMap.value[invokeBalanceCacheKey(assetName)]
  const label = field.balanceLabel || `${displayAssetName(assetName)} 可用`
  if (!balance || balance.loading) return `${label}：查询中...`
  if (balance.error) return `${label}：查询失败`
  return `${label}：${balance.availableAmt}`
}

const parseDecimalUnits = (value: unknown) => {
  const text = String(value ?? '').trim()
  if (!/^\d+(\.\d+)?$/.test(text)) return null
  const [integerPart, fractionPart = ''] = text.split('.')
  return {
    units: BigInt(`${integerPart || '0'}${fractionPart}`),
    scale: fractionPart.length,
  }
}

const pow10BigInt = (scale: number) => 10n ** BigInt(Math.max(scale, 0))

const formatDecimalUnits = (units: bigint, scale: number) => {
  if (scale <= 0) return units.toString()
  const sign = units < 0n ? '-' : ''
  const text = (units < 0n ? -units : units).toString().padStart(scale + 1, '0')
  const integerPart = text.slice(0, -scale).replace(/^0+(?=\d)/, '') || '0'
  const fractionPart = text.slice(-scale).replace(/0+$/, '')
  return `${sign}${integerPart}${fractionPart ? `.${fractionPart}` : ''}`
}

const subtractDecimalNonNegative = (left: unknown, right: unknown) => {
  const a = parseDecimalUnits(left)
  const b = parseDecimalUnits(right)
  if (!a || !b) return ''
  const scale = Math.max(a.scale, b.scale)
  const leftUnits = a.units * pow10BigInt(scale - a.scale)
  const rightUnits = b.units * pow10BigInt(scale - b.scale)
  if (leftUnits <= rightUnits) return '0'
  return formatDecimalUnits(leftUnits - rightUnits, scale)
}

const multiplyByDecimalRatioCeil = (value: unknown, numerator: unknown, denominator: unknown, precision = 8) => {
  const input = parseDecimalUnits(value)
  const top = parseDecimalUnits(numerator)
  const bottom = parseDecimalUnits(denominator)
  if (!input || !top || !bottom || bottom.units <= 0n) return ''
  const scaledNumerator = input.units * top.units * pow10BigInt(bottom.scale + precision)
  const scaledDenominator = pow10BigInt(input.scale + top.scale) * bottom.units
  if (scaledDenominator <= 0n) return ''
  const resultUnits = (scaledNumerator + scaledDenominator - 1n) / scaledDenominator
  return formatDecimalUnits(resultUnits, precision)
}

const multiplyDecimalsCeilInt = (left: unknown, right: unknown) => {
  const a = parseDecimalUnits(left)
  const b = parseDecimalUnits(right)
  if (!a || !b) return 0
  const scale = pow10BigInt(a.scale + b.scale)
  if (scale <= 0n) return 0
  const product = a.units * b.units
  return Number((product + scale - 1n) / scale)
}

const limitOrderFundingValue = (amount: unknown, unitPrice: unknown) => {
  const tradingValue = multiplyDecimalsCeilInt(amount, unitPrice)
  if (tradingValue <= 0) return 0
  return tradingValue + 10 + Math.floor((tradingValue * 8) / 1000)
}

const addLiquidityRatioBase = computed(() => {
  if (invokeContractSubtype.value !== 'amm.tc') return null
  if (isPositiveDecimalString(contractAssetAInPool.value) && isPositiveDecimalString(contractAssetBInPool.value)) {
    return {
      assetA: contractAssetAInPool.value,
      assetB: contractAssetBInPool.value,
      source: '当前池子比例',
    }
  }
  if (isPositiveDecimalString(contractRequiredAssetA.value) && isPositiveDecimalString(contractRequiredAssetB.value)) {
    return {
      assetA: contractRequiredAssetA.value,
      assetB: contractRequiredAssetB.value,
      source: '初始要求比例',
    }
  }
  return null
})

const addLiquidityRequiredText = () => {
  const requiredA = contractRequiredAssetA.value
  const requiredB = contractRequiredAssetB.value
  if (!isPositiveDecimalString(requiredA) && !isPositiveDecimalString(requiredB)) return ''
  const assetAName = displayAssetName(contractAssetAName.value || '资产 A')
  const assetBName = displayAssetName(contractAssetBName.value)
  const remainingA = subtractDecimalNonNegative(requiredA || '0', contractAssetAInPool.value)
  const remainingB = subtractDecimalNonNegative(requiredB || '0', contractAssetBInPool.value)
  const remainingText = remainingA || remainingB
    ? `；当前还需：${assetAName} ${remainingA || '0'}，${assetBName} ${remainingB || '0'}`
    : ''
  return `池子最低要求：${assetAName} ${requiredA || '0'}，${assetBName} ${requiredB || '0'}${remainingText}`
}

const invokeFieldHelpText = (field: InvokeParamField) => {
  if (invokeContractSubtype.value !== 'amm.tc' || invokeAction.value !== 'addliq') return ''
  const assetAName = displayAssetName(contractAssetAName.value || '资产 A')
  const assetBName = displayAssetName(contractAssetBName.value)
  const ratio = addLiquidityRatioBase.value
  if (field.key === 'assetName') {
    const requiredText = addLiquidityRequiredText()
    if (!ratio) return requiredText
    return `${requiredText}；${ratio.source}：${assetAName} ${ratio.assetA} / ${assetBName} ${ratio.assetB}`
  }
  if (!ratio) return ''
  if (field.key === 'amt') {
    const amount = String(invokeParamForm.value.amt || '').trim()
    if (isPositiveDecimalString(amount)) {
      const matchedB = multiplyByDecimalRatioCeil(amount, ratio.assetB, ratio.assetA)
      return matchedB ? `按${ratio.source}，匹配${assetBName}数量约为：${matchedB}` : ''
    }
    const remainingA = subtractDecimalNonNegative(contractRequiredAssetA.value || '0', contractAssetAInPool.value)
    return isPositiveDecimalString(remainingA) ? `${assetAName} 最少还需：${remainingA}` : ''
  }
  if (field.key === 'value') {
    const value = String(invokeParamForm.value.value || '').trim()
    if (isPositiveDecimalString(value)) {
      const matchedA = multiplyByDecimalRatioCeil(value, ratio.assetA, ratio.assetB)
      return matchedA ? `按${ratio.source}，匹配${assetAName}数量约为：${matchedA}` : ''
    }
    const remainingB = subtractDecimalNonNegative(contractRequiredAssetB.value || '0', contractAssetBInPool.value)
    return isPositiveDecimalString(remainingB) ? `${assetBName} 最少还需：${remainingB}` : ''
  }
  return ''
}

const onInvokeParamInput = (field: InvokeParamField) => {
  if (field.key === 'assetName') {
    loadInvokeFieldBalances()
  }
  if (invokeContractSubtype.value === 'amm.tc' && invokeAction.value === 'addliq' && field.key === 'amt') {
    const amount = String(invokeParamForm.value.amt || '').trim()
    const ratio = addLiquidityRatioBase.value
    invokeParamForm.value.value = amount && ratio
      ? multiplyByDecimalRatioCeil(amount, ratio.assetB, ratio.assetA)
      : ''
  }
}

const loadInvokeParamTemplate = () => {
  invokeParamTemplate.value = {}
  invokeParamForm.value = {}
  invokeParamWrapperAction.value = ''
  invokeParamFieldTemplates.value = localInvokeTemplate()
  invokeParamTemplate.value = Object.fromEntries(invokeParamFieldTemplates.value.map((field) => [field.key, field.placeholder]))
  invokeParamForm.value = Object.fromEntries(invokeParamFieldTemplates.value.map((field) => [field.key, field.defaultValue || '']))
  invokeParamWrapperAction.value = invokeAction.value
  loadInvokeFieldBalances()
}

const buildUnifiedInvokeRequest = (contract: string) => {
  const action = invokeAction.value
  if (invokeContractType.value === 'template') {
    const templateReq: Record<string, unknown> = {
      ContractAddress: contract,
    }
    if (action === 'default') {
      templateReq.DefaultInvoke = true
    } else {
      const params = invokeParams()
      templateReq.JSONInvokeParam = JSON.stringify({
        action: invokeParamWrapperAction.value || action,
        param: Object.keys(params).length ? JSON.stringify(params) : '',
      })
      if (action === 'swap') {
        const orderType = Number(params.orderType || 0)
        if (orderType === 1) {
          templateReq.AssetName = String(params.assetName || '').trim()
          templateReq.Amount = String(params.amt || '').trim()
        } else if (orderType === 2) {
          templateReq.Value = invokeContractSubtype.value === 'amm.tc'
            ? Number(params.unitPrice || 0)
            : limitOrderFundingValue(params.amt, params.unitPrice)
        }
      }
      if (action === 'addliq') {
        templateReq.AssetName = String(params.assetName || '').trim()
        templateReq.Amount = String(params.amt || '').trim()
        templateReq.Value = Number(params.value || 0)
      }
    }
    return {
      ContractType: 'template',
      DefaultInvoke: action === 'default',
      Template: templateReq,
    }
  }
  if (invokeContractType.value === 'agent') {
    const agentReq: Record<string, unknown> = {
      ContractAddress: contract,
    }
    if (action === 'default') {
      agentReq.DefaultInvoke = true
    } else {
      const params = invokeParams()
      agentReq.JSONInvokeParam = JSON.stringify({
        action: invokeParamWrapperAction.value || action,
        param: Object.keys(params).length ? JSON.stringify(params) : '',
      })
    }
    return {
      ContractType: 'agent',
      Agent: agentReq,
    }
  }
  const evmReq: Record<string, unknown> = {
    ContractAddress: contract,
  }
  if (action === 'default') {
    evmReq.DefaultInvoke = true
  } else {
    const params = invokeParams()
    const calldataHex = String(params.calldataHex || invokeEvmCalldataHex.value).trim().replace(/^0x/i, '')
    evmReq.JSONInvokeParam = JSON.stringify({
      action: invokeParamWrapperAction.value || action,
      param: JSON.stringify({ calldataHex }),
    })
  }
  return {
    ContractType: 'evm',
    EVM: evmReq,
  }
}

const inputTypeForField = (field: ContractFieldSchema) => {
  if (field.type === 'integer' || field.type === 'decimal') return 'number'
  if (field.type === 'url') return 'url'
  return 'text'
}

const walletContractSchemas = (contracts: string[] = []): ContractSchema[] => {
  const has = (name: string) => contracts.includes(name)
  const schemas: ContractSchema[] = []
  if (has('limitorder.tc') || has('swap.tc')) {
    schemas.push({
      type: 'template',
      subtype: 'limitorder.tc',
      name: 'limitorder.tc',
      label: '限价单模板合约',
      enabled: true,
      fields: [{ name: 'assetName', label: '交易资产', type: 'asset', required: true, placeholder: '如 ordx:f:dogcoin' }],
    })
  }
  if (has('amm.tc')) {
    schemas.push({
      type: 'template',
      subtype: 'amm.tc',
      name: 'amm.tc',
      label: 'AMM 模板合约',
      enabled: true,
      fields: [
        { name: 'assetName', label: '池子资产', type: 'asset', required: true, placeholder: '如 ordx:f:dogcoin' },
        { name: 'assetAmt', label: '初始资产数量', type: 'decimal', required: true, placeholder: '如 100000' },
        { name: 'satValue', label: '初始聪数量', type: 'integer', required: true, placeholder: '如 1000' },
        { name: 'k', label: '常数 K', type: 'computed', placeholder: '根据初始资产数量和初始聪数量自动计算' },
      ],
    })
  }
  if (has('exchange.tc')) {
    schemas.push({
      type: 'template',
      subtype: 'exchange.tc',
      name: 'exchange.tc',
      label: '兑换模板合约',
      enabled: true,
      fields: [
        { name: 'assetAName', label: '资产 A', type: 'asset', required: true, placeholder: '如 ordx:f:asset_a' },
        { name: 'assetBName', label: '资产 B', type: 'asset', required: true, placeholder: '如 ::' },
        {
          name: 'priceMode',
          label: '价格模式',
          type: 'select',
          required: true,
          default: 'height',
          options: [
            { label: '按区块高度', value: 'height' },
            { label: '按已售出资产 A', value: 'sold_a' },
          ],
        },
        {
          name: 'steps',
          label: '价格阶梯',
          type: 'array',
          required: true,
          fields: [
            { name: 'threshold', label: '阈值', type: 'decimal', required: true, default: '0' },
            { name: 'bPerA', label: '每份 A 可换 B', type: 'decimal', required: true, placeholder: '如 0.0001' },
          ],
        },
      ],
    })
  }
  if (has('agent:prediction')) {
    schemas.push({
      type: 'agent',
      subtype: 'prediction',
      name: 'agent:prediction',
      label: '自然语言预测合约',
      enabled: true,
      fields: [
        { name: 'title', label: '标题', type: 'text', required: true },
        { name: 'description', label: '描述', type: 'textarea', required: true },
        {
          name: 'time_base',
          label: '时间类型',
          type: 'select',
          required: true,
          default: 'unix',
          options: [
            { label: 'Unix 时间戳', value: 'unix' },
            { label: '区块高度', value: 'height' },
          ],
        },
        { name: 'event_time', label: '事件时间', type: 'integer', required: true },
        { name: 'bet_deadline', label: '下注截止', type: 'integer', required: true },
        { name: 'confirm_after', label: '可确认时间', type: 'integer', required: true },
        { name: 'source_url', label: '信息来源 URL', type: 'url', required: true },
        { name: 'bet_asset', label: '下注资产', type: 'asset', required: true, default: '::' },
        { name: 'min_bet_unit', label: '最小下注单位', type: 'decimal', required: true, default: '1000' },
        {
          name: 'outcomes',
          label: '结果选项',
          type: 'array',
          required: true,
          fields: [
            { name: 'id', label: 'ID', type: 'text', required: true, placeholder: '如 yes' },
            { name: 'text', label: '显示文本', type: 'text', required: true },
          ],
        },
      ],
    })
  }
  return schemas
}

const formArray = (name: string): Record<string, string>[] => {
  if (!Array.isArray(deployContractForm.value[name])) {
    deployContractForm.value[name] = []
  }
  return deployContractForm.value[name]
}

const emptyArrayRow = (field: ContractFieldSchema) => Object.fromEntries(
  (field.fields || []).map((child) => [child.name, child.default || ''])
)

const addFormArrayItem = (field: ContractFieldSchema) => {
  formArray(field.name).push(emptyArrayRow(field))
}

const removeFormArrayItem = (name: string, index: number) => {
  formArray(name).splice(index, 1)
}

const fieldHasValue = (field: ContractFieldSchema, value: unknown): boolean => {
  if (!field.required) return true
  if (field.type === 'array') {
    const rows = Array.isArray(value) ? value : []
    return rows.length > 0 && rows.every((row) => formHasRequiredValues(field.fields || [], row))
  }
  if (field.type === 'asset') {
    const assetName = String(value ?? '').trim()
    if (assetName === '::') return true
    return contractAssetTickerFromName(assetName).trim() !== ''
  }
  return String(value ?? '').trim() !== ''
}

const formHasRequiredValues = (fields: ContractFieldSchema[], values = deployContractForm.value): boolean => (
  fields.every((field) => fieldHasValue(field, values[field.name]))
)

const computedContractFieldValue = (field: ContractFieldSchema) => {
  const schema = selectedContractSchema.value
  if ((schema?.subtype || schema?.name) === 'amm.tc' && field.name === 'k') {
    try {
      return multiplyDecimalByInteger(deployContractForm.value.assetAmt, deployContractForm.value.satValue)
    } catch {
      return ''
    }
  }
  return ''
}

const checkContractAsset = async (fieldName: string) => {
  try {
    const assetName = String(deployContractForm.value[fieldName] || '').trim()
    if (!assetName) throw new Error('请输入资产名称')
    if (assetName === '::') {
      showSuccess('资产可用', '聪资产')
      return
    }
    if (!contractAssetTicker(fieldName).trim()) throw new Error('请输入 ticker 名称')
    const [err] = await sat20.getTickerInfo(assetName)
    if (err) throw err
    showSuccess('资产可用', assetName)
  } catch (error) {
    showError('资产检查失败', error)
  }
}

const selectContractSchema = (value: unknown) => {
  if (value === null) return
  selectedContractSchemaKey.value = String(value)
  const schema = selectedContractSchema.value
  deployContractForm.value = {}
  for (const field of schema?.fields || []) {
    if (field.type === 'array') {
      deployContractForm.value[field.name] = [emptyArrayRow(field)]
    } else {
      deployContractForm.value[field.name] = field.default || ''
    }
  }
}

const selectFirstSchemaForType = () => {
  const candidates = deployContractType.value === 'agent'
    ? deployableAgentSchemas.value
    : deployContractType.value === 'template'
      ? deployableTemplateSchemas.value
      : []
  if (candidates.length && !candidates.some((schema) => schemaKey(schema) === selectedContractSchemaKey.value)) {
    selectContractSchema(schemaKey(candidates[0]))
  }
}

const loadSupportedContracts = async () => {
  try {
    isLoadingSupportedContracts.value = true
    const res = await smartContractApi.getContracts({ network: network.value || 'testnet', start: 0, limit: 1 })
    if (res?.code !== 0) throw new Error(res?.msg || '加载智能合约列表失败')
    contractSchemas.value = walletContractSchemas(res.contracts || [])
    if (!contractSchemas.value.length) throw new Error('当前 indexer 没有返回可部署合约参数')
    selectFirstSchemaForType()
    showSuccess('加载完成', `找到 ${contractSchemas.value.filter((schema) => schema.enabled).length} 类可部署合约`)
  } catch (error) {
    showError('加载失败', error)
  } finally {
    isLoadingSupportedContracts.value = false
  }
}

const assetNameObject = (assetName: string) => {
  const parts = assetName.split(':')
  if (parts.length === 3) return { Protocol: parts[0], Type: parts[1], Ticker: parts[2] }
  if (parts.length === 2) return { Protocol: parts[0], Type: 'f', Ticker: parts[1] }
  return { Protocol: 'ordx', Type: 'f', Ticker: assetName }
}

const multiplyDecimalByInteger = (decimalValue: unknown, integerValue: unknown) => {
  const decimal = String(decimalValue ?? '').trim()
  const integer = String(integerValue ?? '').trim()
  if (!/^\d+(\.\d+)?$/.test(decimal)) throw new Error('初始资产数量必须是非负数字')
  if (!/^\d+$/.test(integer)) throw new Error('初始聪数量必须是正整数')
  const multiplier = BigInt(integer)
  if (multiplier <= 0n) throw new Error('初始聪数量必须是正整数')
  const [integerPart, fractionPart = ''] = decimal.split('.')
  const decimalUnits = BigInt(`${integerPart || '0'}${fractionPart}`)
  if (decimalUnits <= 0n) throw new Error('初始资产数量必须大于 0')
  const product = decimalUnits * multiplier
  if (!fractionPart.length) return product.toString()
  const padded = product.toString().padStart(fractionPart.length + 1, '0')
  const whole = padded.slice(0, -fractionPart.length).replace(/^0+(?=\d)/, '') || '0'
  const fraction = padded.slice(-fractionPart.length).replace(/0+$/, '')
  return fraction ? `${whole}.${fraction}` : whole
}

const buildTemplateContractContent = (schema: ContractSchema) => {
  const form = deployContractForm.value
  switch (schema.subtype || schema.name) {
    case 'limitorder.tc':
    case 'swap.tc':
      return JSON.stringify({
        contractType: schema.subtype || schema.name,
        assetName: assetNameObject(String(form.assetName || '').trim()),
      })
    case 'amm.tc':
      return JSON.stringify({
        contractType: schema.subtype || schema.name,
        assetName: assetNameObject(String(form.assetName || '').trim()),
        assetAmt: String(form.assetAmt || '').trim(),
        satValue: Number(form.satValue),
        k: multiplyDecimalByInteger(form.assetAmt, form.satValue),
      })
    case 'exchange.tc':
      return JSON.stringify({
        assetAName: String(form.assetAName || '').trim(),
        assetBName: String(form.assetBName || '').trim(),
        priceMode: String(form.priceMode || '').trim(),
        steps: (Array.isArray(form.steps) ? form.steps : []).map((step: any) => ({
          threshold: String(step.threshold || '').trim(),
          bPerA: String(step.bPerA || '').trim(),
        })),
      })
    default:
      throw new Error(`暂不支持部署模板 ${schema.name}`)
  }
}

const buildAgentPrediction = () => {
  const form = deployContractForm.value
  return {
    subtype: 'prediction',
    title: String(form.title || '').trim(),
    description: String(form.description || '').trim(),
    time_base: String(form.time_base || 'unix').trim(),
    event_time: Number(form.event_time),
    bet_deadline: Number(form.bet_deadline),
    confirm_after: Number(form.confirm_after),
    source_url: String(form.source_url || '').trim(),
    bet_asset: String(form.bet_asset || '::').trim(),
    min_bet_unit: String(form.min_bet_unit || '').trim(),
    outcomes: (Array.isArray(form.outcomes) ? form.outcomes : []).map((outcome: any) => ({
      id: String(outcome.id || '').trim(),
      text: String(outcome.text || '').trim(),
    })),
  }
}

const deploySmartContract = async () => {
  try {
    isDeployingSmartContract.value = true
    deploySmartContractResult.value = ''
    const schema = selectedContractSchema.value
    if (!schema) throw new Error('请选择合约类型')
    if (!formHasRequiredValues(schema.fields || [])) throw new Error('请填写必填参数')
    const gasLimit = parseOptionalPositiveInteger(deployContractGasLimit.value, 'Gas 上限')
    let req: Record<string, unknown>
    if (schema.type === 'template') {
      req = {
        ContractType: 'template',
        Template: {
          TemplateName: schema.subtype || schema.name,
          ContractContent: buildTemplateContractContent(schema),
          GasLimit: gasLimit || undefined,
        },
      }
    } else if (schema.type === 'agent') {
      req = {
        ContractType: 'agent',
        Agent: {
          Subtype: schema.subtype || 'prediction',
          Prediction: buildAgentPrediction(),
          GasLimit: gasLimit || undefined,
        },
      }
    } else {
      throw new Error('EVM 合约暂未启用')
    }
    const [err, res] = await sat20.deployUnifiedContract(req)
    if (err) throw err
    deploySmartContractResult.value = JSON.stringify(res, null, 2)
    showSuccess('部署已提交', res?.txid || res?.contractAddress || '交易已广播')
  } catch (error) {
    showError('部署失败', error)
  } finally {
    isDeployingSmartContract.value = false
  }
}

const loadContracts = async () => {
  try {
    isContractLoading.value = true
    const res = await smartContractApi.getContracts({ network: network.value || 'testnet', start: 0, limit: 50 })
    if (res?.code !== 0) throw new Error(res?.msg || '查询合约列表失败')
    contractList.value = res.data || []
    showSuccess('查询完成', `找到 ${contractList.value.length} 个智能合约`)
  } catch (error) {
    showError('查询失败', error)
  } finally {
    isContractLoading.value = false
  }
}

const loadContract = async () => {
  const contract = contractQuery.value.trim()
  if (!contract) {
    showError('参数错误', '请输入合约地址')
    return
  }
  try {
    isContractLoading.value = true
    const [summary, state] = await Promise.all([
      smartContractApi.getContract({ network: network.value || 'testnet', contract }),
      smartContractApi.getContractState({ network: network.value || 'testnet', contract }),
    ])
    if (summary?.code !== 0) throw new Error(summary?.msg || '查询合约失败')
    selectedContract.value = summary.data
    contractState.value = state?.code === 0 ? state.data || state.status : state
    contractHistory.value = null
    invokeContractAddress.value = contract
  } catch (error) {
    showError('查询失败', error)
  } finally {
    isContractLoading.value = false
  }
}

const loadContractHistory = async () => {
  const contract = selectedContractAddress.value
  if (!contract) {
    showError('参数错误', '请先选择或输入合约地址')
    return
  }
  try {
    isContractLoading.value = true
    const res = await smartContractApi.getContractHistory({ network: network.value || 'testnet', contract })
    if (res?.code !== 0) throw new Error(res?.msg || '查询合约历史失败')
    contractHistory.value = res.data || []
  } catch (error) {
    showError('查询失败', error)
  } finally {
    isContractLoading.value = false
  }
}

const selectContract = (contract: any) => {
  selectedContract.value = contract
  const address = contract.address || contract.Address || ''
  contractQuery.value = address
  invokeContractAddress.value = address
}

const invokeSmartContract = async () => {
  try {
    isInvokingContract.value = true
    contractInvokeResult.value = ''
    const contract = invokeContractAddress.value.trim()
    if (!contract) throw new Error('请输入合约地址')
    const req = buildUnifiedInvokeRequest(contract)
    if (import.meta.env.DEV) {
      console.log('[SAT20 Tools] invokeUnifiedContract request', req)
    }
    const [err, res] = await sat20.invokeUnifiedContract(req)
    if (err) throw err
    contractInvokeResult.value = res?.txid || ''
    showSuccess('调用已提交', res?.txid || '交易已广播')
  } catch (error) {
    showError('调用失败', error)
  } finally {
    isInvokingContract.value = false
  }
}

const checkDeployTicker = async () => {
  deployCanDeploy.value = false
  deployCheckKey.value = ''
  const ticker = normalizeTicker(deployTicker.value, deployProtocol.value)
  if (!ticker) {
    showError('参数错误', '请输入 ticker')
    return
  }
  const [err] = await sat20.getTickerInfo(assetNameFor(deployProtocol.value, ticker))
  if (err) {
    deployCanDeploy.value = true
    deployCheckKey.value = currentDeployCheckKey.value
    showSuccess('可以部署', `${deployProtocol.value}:${ticker} 暂未查询到已部署信息`)
  } else {
    showError('不能部署', 'ticker 已存在或 indexer 返回了现有信息')
  }
}

const deployTickerAction = async () => {
  try {
    isDeployingTicker.value = true
    deployTickerResult.value = ''
    const ticker = normalizeTicker(deployTicker.value, deployProtocol.value)
    if (!ticker) throw new Error('请输入 ticker')
    if (!isDeployTickerReady.value) throw new Error('请先检查 ticker，检查通过后再部署')
    if (deployProtocol.value === 'ordx') {
      const n = parsePositiveInteger(bindingSat.value, '每聪绑定资产份数')
      if (!bindingSatOptions.includes(String(n))) throw new Error('每聪绑定资产份数只能选择 1、10、100、1000、10000、100000')
      const [err, res] = await sat20.deployTickerOrdx(ticker, deployMaxSupply.value, deployLimit.value, n, mintFeeRate.value)
      if (err) throw err
      deployTickerResult.value = res?.txId || ''
    } else if (deployProtocol.value === 'brc20') {
      if (ticker.length !== 4) throw new Error('当前钱包暂不支持部署 BRC20 self mint ticker')
      const [err, res] = await sat20.deployTickerBrc20(ticker, deployMaxSupply.value, deployLimit.value, mintFeeRate.value)
      if (err) throw err
      deployTickerResult.value = res?.txId || ''
    } else {
      const destAddress = walletStore.address || ''
      if (!destAddress) throw new Error('当前钱包地址不可用')
      const runesLimit = deploySelfMint.value ? deployMaxSupply.value : deployLimit.value
      const [err, res] = await sat20.DeployRunes_Remote(ticker, 0, deployMaxSupply.value, runesLimit, deploySelfMint.value, destAddress, mintFeeRate.value)
      if (err) throw err
      deployTickerResult.value = res?.txId || ''
    }
    showSuccess('部署已提交', deployTickerResult.value || '交易已广播')
  } catch (error) {
    showError('部署失败', error)
  } finally {
    isDeployingTicker.value = false
  }
}

const checkMintTickerAvailability = async (showAvailableToast = true) => {
  mintCanMint.value = false
  mintCheckKey.value = ''
  const ticker = normalizeTicker(mintTicker.value, mintProtocol.value)
  if (!ticker) {
    showError('参数错误', '请输入 ticker')
    return false
  }
  const address = walletStore.address || ''
  if (!address) {
    showError('参数错误', '当前钱包地址不可用')
    return false
  }
  const [err, res] = await sat20.getTickerInfo(assetNameFor(mintProtocol.value, ticker))
  if (err) {
    showError('不能铸造', '未查询到 ticker 信息')
    return false
  }
  let mintLimit = ''
  let tickerInfo: any = null
  try {
    tickerInfo = typeof res?.ticker === 'string' ? JSON.parse(res.ticker) : res
    mintAmount.value = tickerInfo?.limit || tickerInfo?.Limit || mintAmount.value
    mintLimit = String(tickerInfo?.limit || tickerInfo?.Limit || '')
  } catch {
    // Keep manual input when the response shape is unknown.
  }
  if (mintProtocol.value === 'brc20' && (ticker.length !== 4 || Number(tickerInfo?.selfmint || tickerInfo?.SelfMint || 0) > 0)) {
    showError('不能铸造', '当前钱包暂不支持 BRC20 self mint ticker')
    return false
  }

  const permission = await ordxApi.getMintPermission({
    protocol: mintProtocol.value,
    ticker,
    address,
    network: network.value,
  })
  if (permission?.code !== 0 || !permission?.data) {
    showError('不能铸造', permission?.msg || '当前地址没有铸造权限')
    return false
  }
  const permissionAmount = String(permission.data.amount ?? '')
  if (!isPositiveDecimalString(permissionAmount)) {
    showError('不能铸造', '当前地址可铸造数量为 0')
    return false
  }
  if (mintProtocol.value !== 'runes') {
    if (!isPositiveDecimalString(mintAmount.value)) {
      showError('参数错误', '请输入有效的铸造数量')
      return false
    }
    if (isPositiveDecimalString(mintLimit) && compareDecimalStrings(mintAmount.value, mintLimit) > 0) {
      showError('不能铸造', `单次最多可铸造 ${mintLimit}`)
      return false
    }
    if (compareDecimalStrings(mintAmount.value, permissionAmount) > 0) {
      showError('不能铸造', `当前地址最多可铸造 ${permissionAmount}`)
      return false
    }
  } else if (!mintAmount.value) {
    mintAmount.value = permissionAmount
  }

  if (showAvailableToast) {
    showSuccess('可以铸造', `当前地址最多可铸造 ${permissionAmount}`)
  }
  mintCanMint.value = true
  mintCheckKey.value = currentMintCheckKey.value
  return true
}

const checkMintTicker = async () => {
  await checkMintTickerAvailability(true)
}

const mintAssetAction = async () => {
  try {
    isMintingAsset.value = true
    mintAssetResult.value = ''
    const ticker = normalizeTicker(mintTicker.value, mintProtocol.value)
    if (!ticker) throw new Error('请输入 ticker')
    if (mintProtocol.value !== 'runes' && !mintAmount.value) throw new Error('请输入铸造数量')
    if (!isMintAssetReady.value) throw new Error('请先检查 ticker 和铸造权限，检查通过后再铸造')
    const canMint = await checkMintTickerAvailability(false)
    if (!canMint) return
    const [err, res] = mintProtocol.value === 'ordx'
      ? await sat20.mintAssetOrdx(ticker, mintAmount.value, mintFeeRate.value)
      : mintProtocol.value === 'runes'
        ? await sat20.mintAssetRunes(ticker, mintFeeRate.value)
        : await sat20.mintAssetBrc20(ticker, mintAmount.value, mintFeeRate.value)
    if (err) throw err
    mintAssetResult.value = res?.txId || ''
    showSuccess('铸造已提交', mintAssetResult.value || '交易已广播')
  } catch (error) {
    showError('铸造失败', error)
  } finally {
    isMintingAsset.value = false
  }
}

const checkDidNameAvailability = async (showAvailableToast = true) => {
  const name = didName.value.trim().toLowerCase()
  didCanMint.value = false
  didCheckKey.value = ''
  if (!name) {
    showError('参数错误', '请输入名字')
    return false
  }
  if (/\s|\//.test(name)) {
    showError('参数错误', '名字不能包含空白字符或 /')
    return false
  }
  try {
    const res = await ordxApi.getNsName({ name, network: network.value })
    if (res?.code === 0 && res?.data) {
      showError('不能铸造', '名字已存在')
      return false
    }
    didCanMint.value = true
    didCheckKey.value = currentDidCheckKey.value
    if (showAvailableToast) {
      showSuccess('可以铸造', `${name} 暂未查询到已注册信息`)
    }
    return true
  } catch (error) {
    showError('检查失败', error)
    return false
  }
}

const checkDidName = async () => {
  await checkDidNameAvailability(true)
}

const mintDidAction = async () => {
  try {
    isMintingDid.value = true
    didMintResult.value = ''
    const name = didName.value.trim().toLowerCase()
    if (!isMintDidReady.value) throw new Error('请先检查 DID 名字，检查通过后再铸造')
    const canMint = await checkDidNameAvailability(false)
    if (!canMint || !name) return
    const [err, res] = await sat20.inscribeName(name, mintFeeRate.value)
    if (err) throw err
    didMintResult.value = res?.txId || ''
    showSuccess('DID 铸造已提交', didMintResult.value || '交易已广播')
  } catch (error) {
    showError('DID 铸造失败', error)
  } finally {
    isMintingDid.value = false
  }
}
</script>
