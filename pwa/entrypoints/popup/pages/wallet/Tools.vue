<template>
  <LayoutHome>
    <WalletHeader />
    <div class="space-y-4 px-1 pb-4">
      <header class="space-y-2">
        <h2 class="text-2xl font-medium text-zinc-600/90">{{ t('tools.title') }}</h2>
        <p class="text-xs leading-5 text-muted-foreground">
          {{ t('tools.subtitle') }}
        </p>
      </header>

      <Tabs v-model="activeTab" class="w-full">
        <TabsList class="grid w-full grid-cols-3">
          <TabsTrigger value="faucet">{{ t('tools.tabs.faucet') }}</TabsTrigger>
          <TabsTrigger value="contracts">{{ t('tools.tabs.contracts') }}</TabsTrigger>
          <TabsTrigger value="mint">{{ t('tools.tabs.mint') }}</TabsTrigger>
        </TabsList>

        <TabsContent value="faucet" class="mt-4">
          <Card>
            <CardHeader>
              <CardTitle class="text-base">{{ t('tools.faucet.title') }}</CardTitle>
              <CardDescription>
                {{ t('tools.faucet.description') }}
              </CardDescription>
            </CardHeader>
            <CardContent class="space-y-3">
              <div class="space-y-1">
                <Label>{{ t('tools.faucet.contractAddress') }}</Label>
                <Input v-model="faucetAddress" />
              </div>
              <div class="space-y-1">
                <Label>{{ t('tools.faucet.amount') }}</Label>
                <Input v-model="faucetAmount" type="number" min="1" />
              </div>
              <Button class="w-full" :disabled="isFaucetSending" @click="sendFaucetSats">
                <Icon :icon="isFaucetSending ? 'lucide:loader' : 'lucide:send'" class="h-4 w-4" :class="{ 'animate-spin': isFaucetSending }" />
                {{ t('tools.faucet.send') }}
              </Button>
              <p v-if="faucetResult" class="break-all text-xs text-muted-foreground">txid: {{ faucetResult }}</p>
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="contracts" class="mt-4 space-y-4">
          <p class="text-xs leading-5 text-muted-foreground">
            {{ t('tools.contracts.description') }}
            <a
              :href="smartContractDocUrl"
              target="_blank"
              rel="noopener noreferrer"
              class="ml-1 inline-flex items-center gap-1 text-primary hover:underline"
            >
              <Icon icon="lucide:file-text" class="h-3.5 w-3.5" />
              {{ t('tools.contracts.whitepaper') }}
            </a>
          </p>
          <Card>
            <CardHeader>
              <CardTitle class="text-base">{{ t('tools.contracts.deployTitle') }}</CardTitle>
              <CardDescription>{{ t('tools.contracts.deployDescription') }}</CardDescription>
            </CardHeader>
            <CardContent class="space-y-3">
              <div class="grid grid-cols-2 gap-3">
                <div class="space-y-1">
                  <Label>{{ t('tools.contracts.contractType') }}</Label>
                  <Select v-model="deployContractType">
                    <SelectTrigger>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="template">{{ t('tools.contractTypes.template') }}</SelectItem>
                      <SelectItem value="agent">{{ t('tools.contractTypes.agent') }}</SelectItem>
                      <SelectItem value="evm" disabled>{{ t('tools.contractTypes.evmDisabled') }}</SelectItem>
                    </SelectContent>
                  </Select>
                </div>
                <div class="space-y-1">
                  <Label>{{ t('tools.contracts.gasLimit') }}</Label>
                  <Input v-model="deployContractGasLimit" type="number" min="1" :placeholder="t('tools.common.default')" />
                </div>
              </div>

              <template v-if="deployContractType === 'template'">
                <div class="grid grid-cols-[1fr_auto] gap-2">
                  <Select v-model="selectedContractSchemaKey" @update:model-value="selectContractSchema">
                    <SelectTrigger>
                      <SelectValue :placeholder="t('tools.contracts.selectTemplate')" />
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
                    {{ t('tools.common.load') }}
                  </Button>
                </div>
                <div v-if="selectedContractSchema?.description" class="text-xs text-muted-foreground">
                  {{ selectedContractSchema.description }}
                </div>
                <div v-for="field in selectedContractSchema?.fields || []" :key="field.name" class="space-y-1">
                  <Label>{{ field.label }}</Label>
                  <Select
                    v-if="field.type === 'select'"
                    v-model="deployContractForm[field.name]"
                    @update:model-value="handleDeployContractSelectChange(field.name, $event)"
                  >
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
                        <Button size="sm" variant="ghost" @click="removeFormArrayItem(field.name, rowIndex)">{{ t('tools.common.delete') }}</Button>
                      </div>
                      <div v-for="child in visibleArrayFields(field)" :key="child.name" class="space-y-1">
                        <Label>{{ child.label }}</Label>
                        <Input
                          v-model="formArray(field.name)[rowIndex][child.name]"
                          :type="inputTypeForField(child)"
                          :placeholder="child.placeholder || child.default || ''"
                          :maxlength="child.maxLength"
                        />
                      </div>
                    </div>
                    <Button size="sm" variant="secondary" @click="addFormArrayItem(field)">{{ t('tools.common.add', { label: field.label }) }}</Button>
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
                        <SelectItem value="sats">{{ t('tools.common.sats') }}</SelectItem>
                      </SelectContent>
                    </Select>
                    <Input
                      :model-value="contractAssetTicker(field.name)"
                      :disabled="contractAssetProtocol(field.name) === 'sats'"
                      :placeholder="t('tools.common.tickerName')"
                      @update:model-value="setContractAssetTicker(field.name, $event)"
                    />
                    <Button variant="secondary" @click="checkContractAsset(field.name)">{{ t('tools.common.check') }}</Button>
                  </div>
                  <div v-else-if="isDateTimePickerField(field)" class="grid grid-cols-[1fr_auto] gap-2">
                    <Input
                      v-model="deployContractForm[field.name]"
                      type="datetime-local"
                      :data-contract-time-field="field.name"
                      :placeholder="field.placeholder || field.default || ''"
                    />
                    <Button
                      type="button"
                      variant="secondary"
                      :title="t('tools.common.pickDateTime')"
                      @click="openDateTimePicker(field.name)"
                    >
                      <Icon icon="lucide:calendar-clock" class="h-4 w-4" />
                      <span>{{ t('tools.common.pick') }}</span>
                    </Button>
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
                      <SelectValue :placeholder="t('tools.contracts.selectAgent')" />
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
                    {{ t('tools.common.load') }}
                  </Button>
                </div>
                <div v-if="selectedContractSchema?.description" class="text-xs text-muted-foreground">
                  {{ selectedContractSchema.description }}
                </div>
                <div v-for="field in selectedContractSchema?.fields || []" :key="field.name" class="space-y-1">
                  <Label>{{ field.label }}</Label>
                  <Select
                    v-if="field.type === 'select'"
                    v-model="deployContractForm[field.name]"
                    @update:model-value="handleDeployContractSelectChange(field.name, $event)"
                  >
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
                        <Button size="sm" variant="ghost" @click="removeFormArrayItem(field.name, rowIndex)">{{ t('tools.common.delete') }}</Button>
                      </div>
                      <div v-for="child in visibleArrayFields(field)" :key="child.name" class="space-y-1">
                        <Label>{{ child.label }}</Label>
                        <Input
                          v-model="formArray(field.name)[rowIndex][child.name]"
                          :type="inputTypeForField(child)"
                          :placeholder="child.placeholder || child.default || ''"
                          :maxlength="child.maxLength"
                        />
                      </div>
                    </div>
                    <Button size="sm" variant="secondary" @click="addFormArrayItem(field)">{{ t('tools.common.add', { label: field.label }) }}</Button>
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
                        <SelectItem value="sats">{{ t('tools.common.sats') }}</SelectItem>
                      </SelectContent>
                    </Select>
                    <Input
                      :model-value="contractAssetTicker(field.name)"
                      :disabled="contractAssetProtocol(field.name) === 'sats'"
                      :placeholder="t('tools.common.tickerName')"
                      @update:model-value="setContractAssetTicker(field.name, $event)"
                    />
                    <Button variant="secondary" @click="checkContractAsset(field.name)">{{ t('tools.common.check') }}</Button>
                  </div>
                  <div v-else-if="isDateTimePickerField(field)" class="grid grid-cols-[1fr_auto] gap-2">
                    <Input
                      v-model="deployContractForm[field.name]"
                      type="datetime-local"
                      :data-contract-time-field="field.name"
                      :placeholder="field.placeholder || field.default || ''"
                    />
                    <Button
                      type="button"
                      variant="secondary"
                      :title="t('tools.common.pickDateTime')"
                      @click="openDateTimePicker(field.name)"
                    >
                      <Icon icon="lucide:calendar-clock" class="h-4 w-4" />
                      <span>{{ t('tools.common.pick') }}</span>
                    </Button>
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
                {{ t('tools.contracts.evmDeployDisabled') }}
              </div>

              <Button class="w-full" :disabled="!canDeploySmartContract || isDeployingSmartContract" @click="deploySmartContract">
                <Icon :icon="isDeployingSmartContract ? 'lucide:loader' : 'lucide:rocket'" class="h-4 w-4" :class="{ 'animate-spin': isDeployingSmartContract }" />
                {{ t('tools.contracts.deployButton') }}
              </Button>
              <pre v-if="deploySmartContractResult" class="max-h-40 overflow-auto rounded-sm bg-zinc-950/60 p-3 text-xs leading-5 text-zinc-200">{{ deploySmartContractResult }}</pre>
            </CardContent>
          </Card>
          <Card>
            <CardHeader>
              <CardTitle class="text-base">{{ t('tools.contracts.searchTitle') }}</CardTitle>
              <CardDescription>{{ t('tools.contracts.searchDescription') }}</CardDescription>
            </CardHeader>
            <CardContent class="space-y-3">
              <div class="grid grid-cols-[1fr_auto] gap-2">
                <Input v-model="contractQuery" :placeholder="t('tools.contracts.contractSearchPlaceholder')" />
                <Button variant="secondary" :disabled="isContractLoading" @click="loadContract">
                  <Icon :icon="isContractLoading ? 'lucide:loader' : 'lucide:search'" class="h-4 w-4" :class="{ 'animate-spin': isContractLoading }" />
                </Button>
              </div>
              <div class="grid grid-cols-2 gap-2">
                <Button variant="outline" :disabled="isContractLoading" @click="loadContracts">{{ t('tools.contracts.loadList') }}</Button>
                <Button variant="outline" :disabled="!selectedContractAddress || isContractLoading" @click="loadContractHistory">{{ t('tools.contracts.queryHistory') }}</Button>
              </div>
              <div v-if="contractList.length" class="space-y-2">
                <Label>{{ t('tools.contracts.contractList') }}</Label>
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
                  <div class="text-muted-foreground">{{ t('tools.contracts.assetA') }}</div>
                  <div class="mt-1 break-all font-medium">{{ ammContractSummary.assetAName }}</div>
                  <div class="mt-1 text-muted-foreground">{{ ammContractSummary.assetAAmount }}</div>
                </div>
                <div>
                  <div class="text-muted-foreground">{{ t('tools.contracts.assetB') }}</div>
                  <div class="mt-1 break-all font-medium">{{ ammContractSummary.assetBName }}</div>
                  <div class="mt-1 text-muted-foreground">{{ ammContractSummary.assetBAmount }}</div>
                </div>
                <div class="col-span-2 border-t border-border pt-2">
                  <span class="text-muted-foreground">{{ t('tools.contracts.priceBA') }}</span>
                  <span class="font-medium">{{ ammContractSummary.price }}</span>
                </div>
                <div class="col-span-2">
                  <span class="text-muted-foreground">{{ t('tools.contracts.status') }}</span>
                  <span class="font-medium">{{ ammContractSummary.status }}</span>
                </div>
              </div>
              <pre v-else-if="contractStatusText" class="max-h-56 overflow-auto rounded-sm bg-zinc-950/60 p-3 text-xs leading-5 text-zinc-200">{{ contractStatusText }}</pre>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle class="text-base">{{ t('tools.contracts.invokeTitle') }}</CardTitle>
              <CardDescription>
                {{ t('tools.contracts.invokeDescription') }}
              </CardDescription>
            </CardHeader>
            <CardContent class="space-y-3">
              <div class="space-y-1">
                <Label>{{ t('tools.contracts.contractAddress') }}</Label>
                <Input v-model="invokeContractAddress" />
              </div>
              <div class="grid grid-cols-2 gap-3">
                <div class="space-y-1">
                  <Label>{{ t('tools.contracts.contractType') }}</Label>
                  <div class="flex h-10 items-center rounded-sm border border-input bg-muted/40 px-3 text-sm">
                    {{ invokeContractTypeLabel }}
                  </div>
                </div>
                <div class="space-y-1">
                  <Label>{{ t('tools.contracts.action') }}</Label>
                  <Select v-model="invokeAction" @update:model-value="loadInvokeParamTemplate">
                    <SelectTrigger>
                      <SelectValue :placeholder="t('tools.contracts.selectAction')" />
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
                <Textarea v-model="invokeEvmCalldataHex" class="min-h-20 font-mono text-xs" :placeholder="t('tools.contracts.calldataPlaceholder')" />
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
                {{ t('tools.contracts.noExtraParams') }}
              </div>
              <Button class="w-full" :disabled="isInvokingContract" @click="invokeSmartContract">
                <Icon :icon="isInvokingContract ? 'lucide:loader' : 'lucide:radio-tower'" class="h-4 w-4" :class="{ 'animate-spin': isInvokingContract }" />
                {{ t('tools.contracts.signAndBroadcast') }}
              </Button>
              <p v-if="contractInvokeResult" class="break-all text-xs text-muted-foreground">txid: {{ contractInvokeResult }}</p>
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="mint" class="mt-4 space-y-4">
          <p class="text-xs leading-5 text-muted-foreground">
            {{ t('tools.mint.description') }}
          </p>
          <Card>
            <CardHeader>
              <CardTitle class="text-base">{{ t('tools.mint.deployTickerTitle') }}</CardTitle>
            </CardHeader>
            <CardContent class="space-y-3">
              <div class="grid grid-cols-2 gap-3">
                <div class="space-y-1">
                  <Label>{{ t('tools.mint.protocol') }}</Label>
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
                  <Label>{{ t('tools.mint.feeRate') }}</Label>
                  <Input v-model="mintFeeRate" type="number" min="1" />
                </div>
              </div>
              <div class="grid grid-cols-[1fr_auto] gap-2">
                <Input
                  v-model="deployTicker"
                  :placeholder="t('tools.common.tickerName')"
                  @update:model-value="handleDeployTickerInput"
                />
                <Button variant="secondary" @click="checkDeployTicker">{{ t('tools.common.check') }}</Button>
              </div>
              <div :class="showDeployLimit ? 'grid grid-cols-2 gap-3' : 'grid grid-cols-1 gap-3'">
                <div class="space-y-1">
                  <Label>{{ t('tools.mint.maxSupply') }}</Label>
                  <Input v-model="deployMaxSupply" type="number" />
                </div>
                <div v-if="showDeployLimit" class="space-y-1">
                  <Label>{{ t('tools.mint.mintLimit') }}</Label>
                  <Input v-model="deployLimit" type="number" />
                </div>
              </div>
              <div v-if="deployProtocol === 'brc20' || deployProtocol === 'runes'" class="space-y-1">
                <Label>{{ deployProtocol === 'brc20' ? t('tools.mint.decimal') : t('tools.mint.divisibility') }}</Label>
                <Input v-model="deployDecimals" type="number" min="0" :max="deployProtocol === 'brc20' ? 18 : 38" />
              </div>
              <label v-if="showDeploySelfMint" class="flex items-center gap-2 text-sm text-muted-foreground">
                <Checkbox
                  :checked="effectiveDeploySelfMint"
                  :disabled="deployProtocol === 'brc20'"
                  @update:checked="handleDeploySelfMintChange"
                />
                {{ t('tools.mint.selfMint') }}
              </label>
              <div v-if="deployProtocol === 'ordx'" class="space-y-1">
                <Label>{{ t('tools.mint.bindingSat') }}</Label>
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
                {{ t('tools.mint.deployTicker') }}
              </Button>
              <p v-if="deployTickerResult" class="break-all text-xs text-muted-foreground">txid: {{ deployTickerResult }}</p>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle class="text-base">{{ t('tools.mint.mintAssetTitle') }}</CardTitle>
            </CardHeader>
            <CardContent class="space-y-3">
              <div class="grid grid-cols-2 gap-3">
                <div class="space-y-1">
                  <Label>{{ t('tools.mint.protocol') }}</Label>
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
                  <Label>{{ t('tools.mint.amount') }}</Label>
                  <Input v-model="mintAmount" type="number" :disabled="mintProtocol === 'runes'" />
                  <p v-if="mintProtocol === 'runes'" class="text-xs text-muted-foreground">
                    {{ t('tools.mint.runesAmountHint') }}
                  </p>
                </div>
              </div>
              <div class="grid grid-cols-[1fr_auto] gap-2">
                <Input
                  v-model="mintTicker"
                  :placeholder="t('tools.common.tickerName')"
                  @update:model-value="handleMintTickerInput"
                />
                <Button variant="secondary" @click="checkMintTicker">{{ t('tools.common.check') }}</Button>
              </div>
              <Button class="w-full" :disabled="!isMintAssetReady || isMintingAsset" @click="mintAssetAction">
                <Icon :icon="isMintingAsset ? 'lucide:loader' : 'lucide:coins'" class="h-4 w-4" :class="{ 'animate-spin': isMintingAsset }" />
                {{ t('tools.mint.mintAsset') }}
              </Button>
              <p v-if="mintAssetResult" class="break-all text-xs text-muted-foreground">txid: {{ mintAssetResult }}</p>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle class="text-base">{{ t('tools.mint.mintDidTitle') }}</CardTitle>
            </CardHeader>
            <CardContent class="space-y-3">
              <div class="grid grid-cols-[1fr_auto] gap-2">
                <Input v-model="didName" :placeholder="t('tools.mint.namePlaceholder')" />
                <Button variant="secondary" @click="checkDidName">{{ t('tools.common.check') }}</Button>
              </div>
              <Button class="w-full" :disabled="!isMintDidReady || isMintingDid" @click="mintDidAction">
                <Icon :icon="isMintingDid ? 'lucide:loader' : 'lucide:badge-check'" class="h-4 w-4" :class="{ 'animate-spin': isMintingDid }" />
                {{ t('tools.mint.mintDid') }}
              </Button>
              <p v-if="didMintResult" class="break-all text-xs text-muted-foreground">txid: {{ didMintResult }}</p>
            </CardContent>
          </Card>
        </TabsContent>
      </Tabs>
    </div>

    <Dialog :open="txConfirmOpen" @update:open="handleTxConfirmOpenChange">
      <DialogContent class="max-w-[92vw] rounded-sm border-border bg-background sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{{ t('tools.txConfirm.title') }}</DialogTitle>
          <DialogDescription>
            {{ t('tools.txConfirm.description') }}
          </DialogDescription>
        </DialogHeader>
        <div class="space-y-2 rounded-sm border border-border bg-muted/30 p-3 text-sm">
          <div v-for="row in txConfirmRows" :key="row.label" class="grid grid-cols-[88px_1fr] gap-3">
            <span class="text-muted-foreground">{{ row.label }}</span>
            <span class="break-all font-medium">{{ row.value }}</span>
          </div>
        </div>
        <DialogFooter class="grid grid-cols-2 gap-2 sm:flex sm:justify-end">
          <Button variant="outline" @click="resolveTxConfirm(false)">{{ t('common.cancel') }}</Button>
          <Button @click="resolveTxConfirm(true)">{{ t('common.confirm') }}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <Dialog :open="dateTimePickerOpen" @update:open="dateTimePickerOpen = $event">
      <DialogContent class="max-w-[92vw] rounded-sm border-border bg-background sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{{ t('tools.common.pickDateTime') }}</DialogTitle>
          <DialogDescription>
            {{ dateTimePickerFieldLabel }}
          </DialogDescription>
        </DialogHeader>
        <div class="space-y-3">
          <div class="grid grid-cols-2 gap-3">
            <div class="space-y-1">
              <Label>{{ t('tools.common.date') }}</Label>
              <Input v-model="dateTimePickerDate" type="date" />
            </div>
            <div class="space-y-1">
              <Label>{{ t('tools.common.time') }}</Label>
              <Input v-model="dateTimePickerTime" type="time" />
            </div>
          </div>
          <Button class="w-full" :disabled="!dateTimePickerDate || !dateTimePickerTime" @click="confirmDateTimePicker">
            <Icon icon="lucide:check" class="h-4 w-4" />
            {{ t('tools.common.useSelectedDateTime') }}
          </Button>
        </div>
        <DialogFooter class="sm:justify-end">
          <Button variant="outline" @click="dateTimePickerOpen = false">{{ t('common.cancel') }}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </LayoutHome>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { storeToRefs } from 'pinia'
import { Icon } from '@iconify/vue'
import LayoutHome from '@/components/layout/LayoutHome.vue'
import WalletHeader from '@/components/wallet/HomeHeader.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Checkbox } from '@/components/ui/checkbox'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Textarea } from '@/components/ui/textarea'
import { useToast } from '@/components/ui/toast-new/use-toast'
import { useI18n } from 'vue-i18n'
import { smartContractApi } from '@/apis'
import ordxApi from '@/apis/ordx'
import sat20 from '@/utils/sat20'
import { useGlobalStore, useWalletStore } from '@/store'
import { Storage } from '@/lib/storage-adapter'

const SMART_CONTRACT_DOC_URL_ZH = 'https://docs.sat20.org/protocol-xie-yi-yu-bai-pi-shu/smart-contracts'
const SMART_CONTRACT_DOC_URL_EN = 'https://docs.sat20.org/english/protocols-and-whitepapers/smart-contracts'
const TEMP_FAUCET_CONTRACT_ADDRESS = 'tb1qgxe3g7synqpszhgglk9y27u4rj46cul3mkggeresapw9kthll7vs0dr9na'
const SUPPORTED_CONTRACTS_CACHE_PREFIX = 'tools:supported_contracts'

const { toast } = useToast()
const { t, locale } = useI18n()
const walletStore = useWalletStore()
const globalStore = useGlobalStore()
const { network } = storeToRefs(walletStore)
const { env } = storeToRefs(globalStore)
const smartContractDocUrl = computed(() => String(locale.value).startsWith('en') ? SMART_CONTRACT_DOC_URL_EN : SMART_CONTRACT_DOC_URL_ZH)

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
    return invokeContractSubtype.value ? t('tools.contractTypes.templateWithSubtype', { subtype: invokeContractSubtype.value }) : t('tools.contractTypes.template')
  }
  if (invokeContractType.value === 'agent') return t('tools.contractTypes.agent')
  if (invokeContractType.value === 'evm') return t('tools.contractTypes.evm')
  return t('tools.contractTypes.unknown')
})
const deployContractType = ref<'template' | 'agent' | 'evm'>('template')
type ContractFieldSchema = {
  name: string
  label: string
  type: string
  required?: boolean
  default?: string
  placeholder?: string
  hidden?: boolean
  maxLength?: number
  minRows?: number
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
const AGENT_PREDICTION_TIME_FIELDS = ['event_time', 'bet_deadline', 'confirm_after']
const AGENT_PREDICTION_OUTCOME_TEXT_MAX_LENGTH = 128
const contractSchemas = ref<ContractSchema[]>([])
const selectedContractSchemaKey = ref('')
const deployContractForm = ref<Record<string, any>>({})
const deployContractGasLimit = ref('')
const deploySmartContractResult = ref('')
const isLoadingSupportedContracts = ref(false)
const isDeployingSmartContract = ref(false)
const dateTimePickerOpen = ref(false)
const dateTimePickerFieldName = ref('')
const dateTimePickerDate = ref('')
const dateTimePickerTime = ref('')
const schemaKey = (schema: ContractSchema) => `${schema.type}:${schema.subtype || schema.name}`
const deployableTemplateSchemas = computed(() => contractSchemas.value.filter((schema) => schema.type === 'template' && schema.enabled))
const deployableAgentSchemas = computed(() => contractSchemas.value.filter((schema) => schema.type === 'agent' && schema.enabled))
const selectedContractSchema = computed(() => contractSchemas.value.find((schema) => schemaKey(schema) === selectedContractSchemaKey.value))
const dateTimePickerFieldLabel = computed(() => {
  const field = (selectedContractSchema.value?.fields || []).find((item) => item.name === dateTimePickerFieldName.value)
  return field?.label || t('tools.common.pickDateTime')
})
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
const deployDecimals = ref('0')
const deploySelfMint = ref(false)
const bindingSatOptions = ['1', '10', '100', '1000', '10000', '100000']
const bindingSat = ref('1')
const deployTickerNormalized = computed(() => normalizeTicker(deployTicker.value, deployProtocol.value))
const brc20DeployTickerLength = computed(() => deployProtocol.value === 'brc20' ? deployTickerNormalized.value.length : 0)
const isBrc20DeploySelfMint = computed(() => deployProtocol.value === 'brc20' && brc20DeployTickerLength.value === 5)
const effectiveDeploySelfMint = computed(() => deployProtocol.value === 'brc20' ? isBrc20DeploySelfMint.value : deploySelfMint.value)
const showDeploySelfMint = computed(() => deployProtocol.value !== 'brc20' || isBrc20DeploySelfMint.value)
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

type ToolTxConfirmRow = {
  label: string
  value: string
}

type ToolTxConfirmPayload = {
  purpose: string
  to?: string
  asset?: string
  amount?: string
  network?: string
  feeRate?: string
  details?: ToolTxConfirmRow[]
}

const txConfirmOpen = ref(false)
const txConfirmPayload = ref<ToolTxConfirmPayload | null>(null)
let txConfirmResolver: ((confirmed: boolean) => void) | null = null

const compactRows = (rows: Array<ToolTxConfirmRow | null | undefined>) => (
  rows.filter((row): row is ToolTxConfirmRow => Boolean(row?.value))
)

const txConfirmRows = computed(() => {
  const payload = txConfirmPayload.value
  if (!payload) return []
  return compactRows([
    { label: t('tools.txConfirm.purpose'), value: payload.purpose },
    payload.to ? { label: t('tools.txConfirm.to'), value: payload.to } : null,
    payload.asset ? { label: t('tools.txConfirm.asset'), value: payload.asset } : null,
    payload.amount ? { label: t('tools.txConfirm.amount'), value: payload.amount } : null,
    payload.network ? { label: t('tools.txConfirm.network'), value: payload.network } : null,
    payload.feeRate ? { label: t('tools.txConfirm.feeRate'), value: payload.feeRate } : null,
    ...(payload.details || []),
  ])
})

const resolveTxConfirm = (confirmed: boolean) => {
  const resolver = txConfirmResolver
  txConfirmResolver = null
  txConfirmOpen.value = false
  if (resolver) resolver(confirmed)
}

const handleTxConfirmOpenChange = (open: boolean) => {
  if (!open) {
    resolveTxConfirm(false)
    return
  }
  txConfirmOpen.value = true
}

const confirmToolTransaction = (payload: ToolTxConfirmPayload) => new Promise<boolean>((resolve) => {
  if (txConfirmResolver) txConfirmResolver(false)
  txConfirmPayload.value = payload
  txConfirmResolver = resolve
  txConfirmOpen.value = true
})

const txDetail = (label: string, value: unknown): ToolTxConfirmRow | null => {
  const text = String(value ?? '').trim()
  return text ? { label, value: text } : null
}

const l1NetworkLabel = () => t('tools.txConfirm.bitcoinNetwork', { network: network.value || 'testnet' })
const l2NetworkLabel = () => t('tools.txConfirm.satoshiNetNetwork', { network: network.value || 'testnet' })
const currentWalletAddress = () => walletStore.address || t('tools.txConfirm.currentWallet')
const calculatedAmountLabel = () => t('tools.txConfirm.calculatedByWallet')
const contractFundingAssets = (req: Record<string, unknown>) => (
  Array.isArray(req.Assets) ? req.Assets as Record<string, unknown>[] : []
)

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
    throw new Error(t('tools.errors.mustBePositiveInteger', { field }))
  }
  return parsed
}

const parseDeployDecimals = () => {
  const value = String(deployDecimals.value || '').trim()
  const parsed = Number(value)
  const max = deployProtocol.value === 'brc20' ? 18 : 38
  if (!Number.isInteger(parsed) || parsed < 0 || parsed > max) {
    throw new Error(t('tools.errors.decimalsRange', { max }))
  }
  return String(parsed)
}

const validateDeployTickerForProtocol = (protocol: string, ticker: string) => {
  if (protocol === 'brc20' && ticker.length !== 4 && ticker.length !== 5) {
    throw new Error(t('tools.errors.brc20DeployTickerLength'))
  }
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
const normalizedContractAssetName = (assetName: unknown) => {
  const text = String(assetName ?? '').trim()
  if (!text || text === '::' || text === 'sats') return '::'
  const protocol = text.split(':')[0]
  if (protocol === 'sats') return '::'
  const ticker = contractAssetTickerFromName(text).trim()
  if (!ticker) throw new Error(t('tools.errors.enterTickerName'))
  if (contractAssetProtocols.includes(protocol)) return assetNameFor(protocol, ticker)
  return assetNameFor('ordx', ticker)
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
const handleDeploySelfMintChange = (value: boolean) => {
  if (deployProtocol.value !== 'brc20') {
    deploySelfMint.value = value
  }
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
  deployDecimals.value = protocol === 'brc20' ? '18' : '0'
})
watch([deployProtocol, deployTicker], () => {
  if (deployProtocol.value === 'brc20') {
    deploySelfMint.value = deployTickerNormalized.value.length === 5
  }
})
watch(mintProtocol, (protocol) => {
  if (protocol === 'runes') mintTicker.value = normalizeTicker(mintTicker.value, protocol)
})
watch(deployContractType, () => {
  selectedContractSchemaKey.value = ''
  deployContractForm.value = {}
  selectFirstSchemaForType()
})
watch([env, network], () => {
  selectedContractSchemaKey.value = ''
  deployContractForm.value = {}
  contractSchemas.value = []
  void restoreSupportedContractsCache()
})

onMounted(() => {
  void restoreSupportedContractsCache()
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
    const amount = String(parsePositiveInteger(faucetAmount.value, t('tools.faucet.amount')))
    const confirmed = await confirmToolTransaction({
      purpose: t('tools.txConfirm.purposes.faucetGas'),
      to: faucetAddress.value.trim(),
      asset: displayAssetName('::'),
      amount,
      network: l2NetworkLabel(),
    })
    if (!confirmed) return
    const [err, txid] = await sat20.sendAssets_SatsNet(faucetAddress.value.trim(), '::', amount, '')
    if (err) throw err
    faucetResult.value = txid || ''
    showSuccess(t('tools.messages.sendSuccess'), txid || t('tools.messages.txSubmitted'))
  } catch (error) {
    showError(t('tools.messages.sendFailed'), error)
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
  if (assetName === '::') return t('tools.common.sats')
  return assetName
}

const invokeTransactionSummary = (contract: string, req: Record<string, unknown>): ToolTxConfirmPayload => {
  const params = invokeParams()
  const details = compactRows([
    txDetail(t('tools.txConfirm.contractType'), invokeContractTypeLabel.value),
    txDetail(t('tools.txConfirm.action'), invokeAction.value),
  ])
  let asset = t('tools.txConfirm.contractFunding')
  let amount = calculatedAmountLabel()

  if (invokeContractType.value === 'template') {
    const assets = Array.isArray(req.Assets) ? req.Assets as Record<string, unknown>[] : []
    const firstAsset = assets[0] || {}
    if (firstAsset.AssetName) {
      asset = displayAssetName(String(firstAsset.AssetName))
    }
    if (firstAsset.Amount) {
      amount = String(firstAsset.Amount)
    }
    if (req.Value) {
      details.push({ label: t('tools.txConfirm.satsAmount'), value: String(req.Value) })
      if (!firstAsset.AssetName) {
        asset = displayAssetName('::')
        amount = String(req.Value)
      }
    }
  } else if (invokeContractType.value === 'agent' && params.outcome_id) {
    details.push({ label: t('tools.invoke.outcomeId'), value: String(params.outcome_id) })
  }

  return {
    purpose: t('tools.txConfirm.purposes.invokeSmartContract'),
    to: contract,
    asset,
    amount,
    network: l2NetworkLabel(),
    details,
  }
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
    assetAName: displayAssetName(contractAssetAName.value || t('tools.contracts.assetA')),
    assetAAmount: contractAssetAAmount.value,
    assetBName: displayAssetName(contractAssetBName.value),
    assetBAmount: contractAssetBAmount.value,
    price: decimalRatio(contractAssetBAmount.value, contractAssetAAmount.value),
    status: ammCanSwap.value ? t('tools.contracts.tradable') : t('tools.contracts.needLiquidity'),
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
  orderType: t('tools.invoke.orderType'),
  assetName: t('tools.invoke.assetName'),
  amt: t('tools.invoke.assetAmount'),
  unitPrice: t('tools.invoke.unitPrice'),
  value: t('tools.invoke.satsAmount'),
  lptAmt: t('tools.invoke.lptAmount'),
  minOutA: t('tools.invoke.minOutA'),
  outcome_id: t('tools.invoke.outcomeId'),
  result_type: t('tools.invoke.resultType'),
  source_url: t('tools.invoke.sourceUrl'),
  result_url: t('tools.invoke.resultUrl'),
  result_hash: t('tools.invoke.resultHash'),
  observed_at: t('tools.invoke.observedAt'),
  agent_version: t('tools.invoke.agentVersion'),
  model_version: t('tools.invoke.modelVersion'),
  core_node_pubkey: t('tools.invoke.coreNodePubkey'),
  core_node_signature: t('tools.invoke.coreNodeSignature'),
  reason: t('tools.invoke.reason'),
  checked_at: t('tools.invoke.checkedAt'),
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
  label: t('tools.invoke.orderType'),
  placeholder: '',
  defaultValue: '2',
  type: 'number',
  valueKind: 'number',
  options: [
    { label: t('tools.invoke.buy'), value: '2' },
    { label: t('tools.invoke.sell'), value: '1' },
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
          invokeTextField('assetName', t('tools.invoke.assetName'), t('tools.invoke.defaultContractAssetA'), assetA, 'string', { balanceAsset: 'assetName' }),
          invokeTextField('amt', t('tools.invoke.assetAmount'), '', '', 'string', { balanceAsset: 'assetName' }),
          invokeTextField('unitPrice', t('tools.invoke.unitPrice')),
        ]
      }
      if (action === 'refund') {
        return [invokeTextField('itemIds', t('tools.invoke.orderId'), t('tools.invoke.orderIdPlaceholder'), '', 'intList')]
      }
    }
    if (invokeContractSubtype.value === 'amm.tc') {
      if (action === 'swap') {
        return [
          invokeOrderTypeField(),
          invokeTextField('assetName', t('tools.invoke.assetName'), t('tools.invoke.defaultAssetA'), assetA, 'string', { balanceAsset: 'assetName' }),
          invokeTextField('amt', t('tools.invoke.assetAmount'), '', '', 'string', { balanceAsset: 'assetName' }),
          invokeTextField('unitPrice', t('tools.invoke.unitPriceHint')),
        ]
      }
      if (action === 'addliq') {
        return [
          invokeHiddenField('orderType', '9'),
          invokeTextField('assetName', t('tools.invoke.assetName'), t('tools.invoke.defaultAssetA'), assetA, 'string', { balanceAsset: 'assetName' }),
          invokeTextField('amt', t('tools.invoke.assetAmount'), '', '', 'string', { balanceAsset: 'assetName' }),
          invokeTextField('value', t('tools.invoke.satsAmount'), '', '', 'number', { balanceAsset: '::' }),
        ]
      }
      if (action === 'removeliq') {
        return [
          invokeHiddenField('orderType', '10'),
          invokeTextField('assetName', t('tools.invoke.assetName'), t('tools.invoke.defaultAssetA'), assetA, 'string', { balanceAsset: 'assetName' }),
          invokeTextField('lptAmt', t('tools.invoke.lptAmount'), '', '', 'string', { balanceLabel: t('tools.invoke.currentLpAvailable') }),
        ]
      }
    }
    if (invokeContractSubtype.value === 'exchange.tc' && action === 'exchange') {
      return [invokeTextField('minOutA', t('tools.invoke.minOutA'), t('tools.common.optional'))]
    }
  }
  if (invokeContractType.value === 'agent') {
    if (action === 'bet') return [invokeTextField('outcome_id', t('tools.invoke.outcomeId'))]
    if (action === 'confirm') {
      return [
        invokeTextField('result_type', t('tools.invoke.resultType'), 'outcome / invalid / cancelled'),
        invokeTextField('outcome_id', t('tools.invoke.outcomeId'), t('tools.invoke.outcomeIdWhenOutcome')),
        invokeTextField('source_url', t('tools.invoke.sourceUrl')),
        invokeTextField('result_url', t('tools.invoke.resultUrl')),
        invokeTextField('result_hash', t('tools.invoke.resultHash')),
        invokeTextField('observed_at', t('tools.invoke.observedAt'), t('tools.invoke.unixTimestamp'), '', 'number'),
        invokeTextField('agent_version', t('tools.invoke.agentVersion'), t('tools.common.optional')),
        invokeTextField('model_version', t('tools.invoke.modelVersion'), t('tools.common.optional')),
        invokeTextField('core_node_pubkey', t('tools.invoke.coreNodePubkey'), t('tools.common.optional')),
        invokeTextField('core_node_signature', t('tools.invoke.coreNodeSignature'), t('tools.common.optional')),
      ]
    }
    if (action === 'reject') {
      return [
        invokeTextField('reason', t('tools.invoke.reason')),
        invokeTextField('checked_at', t('tools.invoke.checkedAt'), t('tools.invoke.unixTimestamp'), '', 'number'),
      ]
    }
  }
  if (invokeContractType.value === 'evm' && action === 'call') {
    return [invokeTextField('calldataHex', 'Calldata Hex', t('tools.contracts.calldataPlaceholder'))]
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
    return lpBalance ? t('tools.invoke.balanceLine', { label: field.balanceLabel || t('tools.common.available'), value: lpBalance }) : ''
  }
  const assetName = resolveInvokeBalanceAsset(field)
  if (!assetName || !isQueryableInvokeAsset(assetName)) return ''
  const balance = invokeBalanceMap.value[invokeBalanceCacheKey(assetName)]
  const label = field.balanceLabel || t('tools.invoke.assetAvailableLabel', { asset: displayAssetName(assetName) })
  if (!balance || balance.loading) return t('tools.invoke.balanceLoading', { label })
  if (balance.error) return t('tools.invoke.balanceFailed', { label })
  return t('tools.invoke.balanceLine', { label, value: balance.availableAmt })
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
      source: t('tools.invoke.currentPoolRatio'),
    }
  }
  if (isPositiveDecimalString(contractRequiredAssetA.value) && isPositiveDecimalString(contractRequiredAssetB.value)) {
    return {
      assetA: contractRequiredAssetA.value,
      assetB: contractRequiredAssetB.value,
      source: t('tools.invoke.initialRequiredRatio'),
    }
  }
  return null
})

const addLiquidityRequiredText = () => {
  const requiredA = contractRequiredAssetA.value
  const requiredB = contractRequiredAssetB.value
  if (!isPositiveDecimalString(requiredA) && !isPositiveDecimalString(requiredB)) return ''
  const assetAName = displayAssetName(contractAssetAName.value || t('tools.contracts.assetA'))
  const assetBName = displayAssetName(contractAssetBName.value)
  const remainingA = subtractDecimalNonNegative(requiredA || '0', contractAssetAInPool.value)
  const remainingB = subtractDecimalNonNegative(requiredB || '0', contractAssetBInPool.value)
  const remainingText = remainingA || remainingB
    ? t('tools.invoke.currentlyNeeds', { assetA: assetAName, amountA: remainingA || '0', assetB: assetBName, amountB: remainingB || '0' })
    : ''
  return `${t('tools.invoke.poolMinimumRequirement', { assetA: assetAName, amountA: requiredA || '0', assetB: assetBName, amountB: requiredB || '0' })}${remainingText}`
}

const invokeFieldHelpText = (field: InvokeParamField) => {
  if (invokeContractSubtype.value !== 'amm.tc' || invokeAction.value !== 'addliq') return ''
  const assetAName = displayAssetName(contractAssetAName.value || t('tools.contracts.assetA'))
  const assetBName = displayAssetName(contractAssetBName.value)
  const ratio = addLiquidityRatioBase.value
  if (field.key === 'assetName') {
    const requiredText = addLiquidityRequiredText()
    if (!ratio) return requiredText
    return `${requiredText}${t('tools.invoke.ratioText', { source: ratio.source, assetA: assetAName, amountA: ratio.assetA, assetB: assetBName, amountB: ratio.assetB })}`
  }
  if (!ratio) return ''
  if (field.key === 'amt') {
    const amount = String(invokeParamForm.value.amt || '').trim()
    if (isPositiveDecimalString(amount)) {
      const matchedB = multiplyByDecimalRatioCeil(amount, ratio.assetB, ratio.assetA)
      return matchedB ? t('tools.invoke.matchedAmount', { source: ratio.source, asset: assetBName, amount: matchedB }) : ''
    }
    const remainingA = subtractDecimalNonNegative(contractRequiredAssetA.value || '0', contractAssetAInPool.value)
    return isPositiveDecimalString(remainingA) ? t('tools.invoke.minimumStillNeeded', { asset: assetAName, amount: remainingA }) : ''
  }
  if (field.key === 'value') {
    const value = String(invokeParamForm.value.value || '').trim()
    if (isPositiveDecimalString(value)) {
      const matchedA = multiplyByDecimalRatioCeil(value, ratio.assetA, ratio.assetB)
      return matchedA ? t('tools.invoke.matchedAmount', { source: ratio.source, asset: assetAName, amount: matchedA }) : ''
    }
    const remainingB = subtractDecimalNonNegative(contractRequiredAssetB.value || '0', contractAssetBInPool.value)
    return isPositiveDecimalString(remainingB) ? t('tools.invoke.minimumStillNeeded', { asset: assetBName, amount: remainingB }) : ''
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
    const req: Record<string, unknown> = {
      ContractType: 'template',
      SubType: invokeContractSubtype.value,
      ContractAddress: contract,
    }
    if (action === 'default') {
      req.DefaultInvoke = true
    } else {
      const params = invokeParams()
      req.Action = invokeParamWrapperAction.value || action
      req.Param = Object.keys(params).length ? JSON.stringify(params) : ''
      if (action === 'swap') {
        const orderType = Number(params.orderType || 0)
        if (orderType === 1) {
          req.Assets = [{ AssetName: String(params.assetName || '').trim(), Amount: String(params.amt || '').trim() }]
        } else if (orderType === 2) {
          req.Value = invokeContractSubtype.value === 'amm.tc'
            ? Number(params.unitPrice || 0)
            : limitOrderFundingValue(params.amt, params.unitPrice)
        }
      }
      if (action === 'addliq') {
        req.Assets = [{ AssetName: String(params.assetName || '').trim(), Amount: String(params.amt || '').trim() }]
        req.Value = Number(params.value || 0)
      }
    }
    return req
  }
  if (invokeContractType.value === 'agent') {
    const req: Record<string, unknown> = {
      ContractType: 'agent',
      SubType: invokeContractSubtype.value || 'prediction',
      ContractAddress: contract,
    }
    if (action === 'default') {
      req.DefaultInvoke = true
    } else {
      const params = invokeParams()
      req.Action = invokeParamWrapperAction.value || action
      req.Param = Object.keys(params).length ? JSON.stringify(params) : ''
    }
    return req
  }
  const req: Record<string, unknown> = {
    ContractType: 'evm',
    ContractAddress: contract,
  }
  if (action === 'default') {
    req.DefaultInvoke = true
  } else {
    const params = invokeParams()
    const calldataHex = String(params.calldataHex || invokeEvmCalldataHex.value).trim().replace(/^0x/i, '')
    req.Action = invokeParamWrapperAction.value || action
    req.Param = JSON.stringify({ calldataHex })
  }
  return req
}

const inputTypeForField = (field: ContractFieldSchema) => {
  if (isDateTimePickerField(field)) return 'datetime-local'
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
      label: t('tools.schemas.limitOrder'),
      enabled: true,
      fields: [{ name: 'assetName', label: t('tools.schemas.tradeAsset'), type: 'asset', required: true, placeholder: t('tools.placeholders.assetExampleDogcoin') }],
    })
  }
  if (has('amm.tc')) {
    schemas.push({
      type: 'template',
      subtype: 'amm.tc',
      name: 'amm.tc',
      label: t('tools.schemas.amm'),
      enabled: true,
      fields: [
        { name: 'assetName', label: t('tools.schemas.poolAsset'), type: 'asset', required: true, placeholder: t('tools.placeholders.assetExampleDogcoin') },
        { name: 'assetAmt', label: t('tools.schemas.initialAssetAmount'), type: 'decimal', required: true, placeholder: t('tools.placeholders.amount100000') },
        { name: 'satValue', label: t('tools.schemas.initialSatsAmount'), type: 'integer', required: true, placeholder: t('tools.placeholders.sats1000') },
        { name: 'k', label: t('tools.schemas.constantK'), type: 'computed', placeholder: t('tools.schemas.constantKPlaceholder') },
      ],
    })
  }
  if (has('exchange.tc')) {
    schemas.push({
      type: 'template',
      subtype: 'exchange.tc',
      name: 'exchange.tc',
      label: t('tools.schemas.exchange'),
      enabled: true,
      fields: [
        { name: 'assetAName', label: t('tools.contracts.assetA'), type: 'asset', required: true, placeholder: t('tools.placeholders.assetExampleA') },
        { name: 'assetBName', label: t('tools.contracts.assetB'), type: 'asset', required: true, placeholder: t('tools.placeholders.satsAsset') },
        {
          name: 'priceMode',
          label: t('tools.schemas.priceMode'),
          type: 'select',
          required: true,
          default: 'height',
          options: [
            { label: t('tools.schemas.byBlockHeight'), value: 'height' },
            { label: t('tools.schemas.bySoldAssetA'), value: 'sold_a' },
          ],
        },
        {
          name: 'steps',
          label: t('tools.schemas.priceSteps'),
          type: 'array',
          required: true,
          fields: [
            { name: 'threshold', label: t('tools.schemas.threshold'), type: 'decimal', required: true, default: '0' },
            { name: 'bPerA', label: t('tools.schemas.bPerA'), type: 'decimal', required: true, placeholder: t('tools.placeholders.decimal0001') },
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
      label: t('tools.schemas.predictionAgent'),
      enabled: true,
      fields: [
        { name: 'title', label: t('tools.schemas.title'), type: 'text', required: true },
        { name: 'description', label: t('tools.schemas.description'), type: 'textarea', required: true },
        {
          name: 'time_base',
          label: t('tools.schemas.timeType'),
          type: 'select',
          required: true,
          default: 'unix',
          options: [
            { label: t('tools.invoke.unixTimestamp'), value: 'unix' },
            { label: t('tools.schemas.blockHeight'), value: 'height' },
          ],
        },
        { name: 'event_time', label: t('tools.schemas.eventTime'), type: 'integer', required: true },
        { name: 'bet_deadline', label: t('tools.schemas.betDeadline'), type: 'integer', required: true },
        { name: 'confirm_after', label: t('tools.schemas.confirmAfter'), type: 'integer', required: true },
        { name: 'source_url', label: t('tools.schemas.sourceInfoUrl'), type: 'url', required: true },
        { name: 'bet_asset', label: t('tools.schemas.betAsset'), type: 'asset', required: true, default: '::' },
        { name: 'min_bet_unit', label: t('tools.schemas.minBetUnit'), type: 'decimal', required: true, default: '1000' },
        {
          name: 'outcomes',
          label: t('tools.schemas.outcomes'),
          type: 'array',
          required: true,
          minRows: 2,
          fields: [
            { name: 'text', label: t('tools.schemas.displayText'), type: 'text', required: true, maxLength: AGENT_PREDICTION_OUTCOME_TEXT_MAX_LENGTH },
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

const visibleArrayFields = (field: ContractFieldSchema) => (field.fields || []).filter((child) => !child.hidden)

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
  if (field.hidden) return true
  if (!field.required) return true
  if (field.type === 'array') {
    const rows = Array.isArray(value) ? value : []
    return rows.length >= (field.minRows || 1) && rows.every((row) => formHasRequiredValues(field.fields || [], row))
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
    if (!assetName) throw new Error(t('tools.errors.enterAssetName'))
    if (assetName === '::') {
      showSuccess(t('tools.messages.assetAvailable'), t('tools.messages.satsAsset'))
      return
    }
    if (!contractAssetTicker(fieldName).trim()) throw new Error(t('tools.errors.enterTickerName'))
    const [err] = await sat20.getTickerInfo(assetName)
    if (err) throw err
    showSuccess(t('tools.messages.assetAvailable'), assetName)
  } catch (error) {
    showError(t('tools.messages.assetCheckFailed'), error)
  }
}

const selectContractSchema = (value: unknown) => {
  if (value === null) return
  selectedContractSchemaKey.value = String(value)
  const schema = selectedContractSchema.value
  deployContractForm.value = {}
  for (const field of schema?.fields || []) {
    if (field.type === 'array') {
      const rows = Math.max(field.minRows || 1, 1)
      deployContractForm.value[field.name] = Array.from({ length: rows }, () => emptyArrayRow(field))
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

const isAgentPredictionSchema = () => selectedContractSchema.value?.type === 'agent' && selectedContractSchema.value?.subtype === 'prediction'
const isAgentPredictionUnixTime = () => isAgentPredictionSchema() && String(deployContractForm.value.time_base || 'unix') === 'unix'
const isAgentPredictionTimeField = (field: ContractFieldSchema) => AGENT_PREDICTION_TIME_FIELDS.includes(field.name)
const isDateTimePickerField = (field: ContractFieldSchema) => isAgentPredictionTimeField(field) && isAgentPredictionUnixTime()

const currentLocalDateTimeParts = () => {
  const now = new Date()
  const yyyy = String(now.getFullYear())
  const mm = String(now.getMonth() + 1).padStart(2, '0')
  const dd = String(now.getDate()).padStart(2, '0')
  const hh = String(now.getHours()).padStart(2, '0')
  const mi = String(now.getMinutes()).padStart(2, '0')
  return { date: `${yyyy}-${mm}-${dd}`, time: `${hh}:${mi}` }
}

const openDateTimePicker = (fieldName: string) => {
  const raw = String(deployContractForm.value[fieldName] || '')
  const fallback = currentLocalDateTimeParts()
  const [date, time] = raw.includes('T') ? raw.split('T') : ['', '']
  dateTimePickerFieldName.value = fieldName
  dateTimePickerDate.value = date || fallback.date
  dateTimePickerTime.value = (time || fallback.time).slice(0, 5)
  dateTimePickerOpen.value = true
}

const confirmDateTimePicker = () => {
  if (!dateTimePickerFieldName.value || !dateTimePickerDate.value || !dateTimePickerTime.value) return
  deployContractForm.value[dateTimePickerFieldName.value] = `${dateTimePickerDate.value}T${dateTimePickerTime.value}`
  dateTimePickerOpen.value = false
}

const resetAgentPredictionTimeFields = () => {
  for (const fieldName of AGENT_PREDICTION_TIME_FIELDS) {
    deployContractForm.value[fieldName] = ''
  }
}

const handleDeployContractSelectChange = (fieldName: string, value: unknown) => {
  deployContractForm.value[fieldName] = String(value ?? '')
  if (fieldName === 'time_base' && isAgentPredictionSchema()) {
    resetAgentPredictionTimeFields()
  }
}

const supportedContractsCacheKey = () => `${SUPPORTED_CONTRACTS_CACHE_PREFIX}:${env.value || 'prd'}:${network.value || 'testnet'}`

const applySupportedContracts = (contracts: string[]) => {
  contractSchemas.value = walletContractSchemas(contracts)
  if (contractSchemas.value.length) {
    selectFirstSchemaForType()
  }
}

const saveSupportedContractsCache = async (contracts: string[]) => {
  await Storage.set({
    key: supportedContractsCacheKey(),
    value: JSON.stringify({
      contracts,
      updatedAt: Date.now(),
    }),
  })
}

const restoreSupportedContractsCache = async () => {
  const { value } = await Storage.get({ key: supportedContractsCacheKey() })
  if (!value) return
  try {
    const cached = JSON.parse(value)
    if (!Array.isArray(cached?.contracts)) return
    applySupportedContracts(cached.contracts.filter((item: unknown) => typeof item === 'string'))
  } catch (error) {
    console.warn('[SAT20 Tools] restore supported contracts cache failed', error)
  }
}

const loadSupportedContracts = async () => {
  try {
    isLoadingSupportedContracts.value = true
    const res = await smartContractApi.getContracts({ network: network.value || 'testnet', start: 0, limit: 1 })
    if (res?.code !== 0) throw new Error(res?.msg || t('tools.errors.loadContractListFailed'))
    const contracts = Array.isArray(res.contracts) ? res.contracts.filter((item: unknown) => typeof item === 'string') : []
    applySupportedContracts(contracts)
    if (!contractSchemas.value.length) throw new Error(t('tools.errors.noDeployableContracts'))
    await saveSupportedContractsCache(contracts)
    showSuccess(t('tools.messages.loadComplete'), t('tools.messages.deployableContractsFound', { count: contractSchemas.value.filter((schema) => schema.enabled).length }))
  } catch (error) {
    showError(t('tools.messages.loadFailed'), error)
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
  if (!/^\d+(\.\d+)?$/.test(decimal)) throw new Error(t('tools.errors.initialAssetAmountNonNegative'))
  if (!/^\d+$/.test(integer)) throw new Error(t('tools.errors.initialSatsPositiveInteger'))
  const multiplier = BigInt(integer)
  if (multiplier <= 0n) throw new Error(t('tools.errors.initialSatsPositiveInteger'))
  const [integerPart, fractionPart = ''] = decimal.split('.')
  const decimalUnits = BigInt(`${integerPart || '0'}${fractionPart}`)
  if (decimalUnits <= 0n) throw new Error(t('tools.errors.initialAssetAmountPositive'))
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
      throw new Error(t('tools.errors.unsupportedTemplate', { name: schema.name }))
  }
}

const agentPredictionTimeValue = (fieldName: string, fieldLabel: string, timeBase: string) => {
  const text = String(deployContractForm.value[fieldName] ?? '').trim()
  if (timeBase !== 'unix') return Number(text)
  if (/^\d+$/.test(text)) return Number(text)
  const timestamp = new Date(text).getTime()
  if (!Number.isFinite(timestamp) || timestamp <= 0) {
    throw new Error(t('tools.errors.invalidDateTime', { field: fieldLabel }))
  }
  return Math.floor(timestamp / 1000)
}

const agentPredictionOutcomeId = (index: number) => {
  if (index < 0 || index >= 26) {
    throw new Error(t('tools.errors.tooManyPredictionOutcomes', { max: 26 }))
  }
  return String.fromCharCode('a'.charCodeAt(0) + index)
}

const buildAgentPredictionOutcomes = () => {
  const rows = Array.isArray(deployContractForm.value.outcomes) ? deployContractForm.value.outcomes : []
  return rows.map((outcome: any, index: number) => {
    const text = String(outcome.text || '').trim()
    if (text.length > AGENT_PREDICTION_OUTCOME_TEXT_MAX_LENGTH) {
      throw new Error(t('tools.errors.predictionOutcomeTooLong', { max: AGENT_PREDICTION_OUTCOME_TEXT_MAX_LENGTH }))
    }
    return {
      id: agentPredictionOutcomeId(index),
      text,
    }
  })
}

const buildAgentPrediction = () => {
  const form = deployContractForm.value
  const timeBase = String(form.time_base || 'unix').trim()
  return {
    subtype: 'prediction',
    title: String(form.title || '').trim(),
    description: String(form.description || '').trim(),
    time_base: timeBase,
    event_time: agentPredictionTimeValue('event_time', t('tools.schemas.eventTime'), timeBase),
    bet_deadline: agentPredictionTimeValue('bet_deadline', t('tools.schemas.betDeadline'), timeBase),
    confirm_after: agentPredictionTimeValue('confirm_after', t('tools.schemas.confirmAfter'), timeBase),
    source_url: String(form.source_url || '').trim(),
    bet_asset: normalizedContractAssetName(form.bet_asset),
    min_bet_unit: String(form.min_bet_unit || '').trim(),
    outcomes: buildAgentPredictionOutcomes(),
  }
}

const fetchWithTimeout = async (url: string, init: RequestInit = {}, timeoutMs = 12000) => {
  const controller = new AbortController()
  const timer = window.setTimeout(() => controller.abort(), timeoutMs)
  try {
    return await fetch(url, {
      ...init,
      redirect: 'follow',
      signal: controller.signal,
    })
  } finally {
    window.clearTimeout(timer)
  }
}

const assertPredictionSourceURLReachable = async (sourceURL: string) => {
  let parsed: URL
  try {
    parsed = new URL(sourceURL)
  } catch {
    throw new Error('Prediction source URL is invalid')
  }
  if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
    throw new Error('Prediction source URL must use http or https')
  }

  try {
    const response = await fetchWithTimeout(sourceURL, { method: 'GET', cache: 'no-store' })
    if (!response.ok) {
      throw new Error(`Prediction source URL returned HTTP ${response.status}`)
    }
    return
  } catch (error) {
    if (!(error instanceof TypeError)) {
      throw error
    }
  }

  try {
    await fetchWithTimeout(sourceURL, {
      method: 'GET',
      mode: 'no-cors',
      cache: 'no-store',
    })
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error)
    throw new Error(`Prediction source URL is not reachable: ${message}`)
  }
}

const parseReviewReadyData = (response: any) => {
  if (response?.data && typeof response.data === 'object') return response.data
  if (typeof response?.data === 'string') {
    try {
      return JSON.parse(response.data)
    } catch {
      return null
    }
  }
  if (typeof response?.status === 'string') {
    try {
      return JSON.parse(response.status)
    } catch {
      return null
    }
  }
  return null
}

const validateAgentPredictionBeforeDeploy = async (prediction: Record<string, unknown>) => {
  const sourceURL = String(prediction.source_url || '').trim()
  await assertPredictionSourceURLReachable(sourceURL)

  const response = await smartContractApi.reviewPredictionReady({
    network: network.value || 'testnet',
    contract: prediction,
  })
  if (response?.code !== 0) {
    throw new Error(response?.msg || 'Prediction ready review failed')
  }
  const review = parseReviewReadyData(response)
  if (!review?.urlReachable) {
    throw new Error(review?.reason || 'Prediction source URL is not reachable by oracle')
  }
  if (!review?.ready) {
    throw new Error(review?.reason || 'Prediction contract cannot pass oracle ready review')
  }
}

const deploySmartContract = async () => {
  try {
    isDeployingSmartContract.value = true
    deploySmartContractResult.value = ''
    const schema = selectedContractSchema.value
    if (!schema) throw new Error(t('tools.errors.selectContractType'))
    if (!formHasRequiredValues(schema.fields || [])) throw new Error(t('tools.errors.fillRequiredParams'))
    const gasLimit = parseOptionalPositiveInteger(deployContractGasLimit.value, t('tools.contracts.gasLimit'))
    let req: Record<string, unknown>
    if (schema.type === 'template') {
      const subtype = schema.subtype || schema.name
      const jsonContent = buildTemplateContractContent(schema)
      const [contentErr, contentRes] = await sat20.buildUnifiedContractContent('template', subtype, jsonContent)
      if (contentErr) throw contentErr
      req = {
        ContractType: 'template',
        SubType: subtype,
        ContractContent: contentRes?.content,
        ContentEncoding: contentRes?.contentEncoding || 'base64',
        GasLimit: gasLimit || undefined,
      }
      if (subtype === 'amm.tc') {
        req.FundingValue = Number(deployContractForm.value.satValue || 0)
        req.Assets = [{
          AssetName: normalizedContractAssetName(deployContractForm.value.assetName),
          Amount: String(deployContractForm.value.assetAmt || '').trim(),
        }]
      }
    } else if (schema.type === 'agent') {
      const subtype = schema.subtype || 'prediction'
      const prediction = buildAgentPrediction()
      if (subtype === 'prediction') {
        await validateAgentPredictionBeforeDeploy(prediction)
      }
      const [contentErr, contentRes] = await sat20.buildUnifiedContractContent('agent', subtype, JSON.stringify(prediction))
      if (contentErr) throw contentErr
      req = {
        ContractType: 'agent',
        SubType: subtype,
        ContractContent: contentRes?.content,
        ContentEncoding: contentRes?.contentEncoding || 'base64',
        GasLimit: gasLimit || undefined,
      }
    } else {
      throw new Error(t('tools.errors.evmDisabled'))
    }
    const [estimateErr, estimate] = await sat20.estimateDeployUnifiedContract(req)
    if (estimateErr) throw estimateErr
    const fundingAssets = contractFundingAssets(req)
    const confirmed = await confirmToolTransaction({
      purpose: t('tools.txConfirm.purposes.deploySmartContract'),
      to: t('tools.txConfirm.smartContractSystem'),
      asset: t('tools.txConfirm.smartContractGas'),
      amount: estimate?.gasAssetAmount || calculatedAmountLabel(),
      network: l2NetworkLabel(),
      details: compactRows([
        txDetail(t('tools.txConfirm.contractType'), schema.label || schema.name),
        txDetail(t('tools.txConfirm.schema'), schema.subtype || schema.name),
        txDetail(t('tools.txConfirm.gasFeeAmount'), estimate?.gasFeeAmount),
        txDetail(t('tools.txConfirm.gasFundAmount'), estimate?.gasFundAmount),
        gasLimit ? txDetail(t('tools.contracts.gasLimit'), gasLimit) : null,
        req.FundingValue ? txDetail(t('tools.txConfirm.satsAmount'), req.FundingValue) : null,
        ...fundingAssets.map((asset) => txDetail(
          t('tools.txConfirm.fundingAsset'),
          `${displayAssetName(String(asset.AssetName || ''))} ${String(asset.Amount || '').trim()}`
        )),
      ]),
    })
    if (!confirmed) return
    const [err, res] = await sat20.deployUnifiedContract(req)
    if (err) throw err
    deploySmartContractResult.value = JSON.stringify(res, null, 2)
    showSuccess(t('tools.messages.deploySubmitted'), res?.txid || res?.contractAddress || t('tools.messages.txBroadcasted'))
  } catch (error) {
    showError(t('tools.messages.deployFailed'), error)
  } finally {
    isDeployingSmartContract.value = false
  }
}

const loadContracts = async () => {
  try {
    isContractLoading.value = true
    const res = await smartContractApi.getContracts({ network: network.value || 'testnet', start: 0, limit: 50 })
    if (res?.code !== 0) throw new Error(res?.msg || t('tools.errors.queryContractListFailed'))
    contractList.value = res.data || []
    showSuccess(t('tools.messages.queryComplete'), t('tools.messages.contractsFound', { count: contractList.value.length }))
  } catch (error) {
    showError(t('tools.messages.queryFailed'), error)
  } finally {
    isContractLoading.value = false
  }
}

const loadContract = async () => {
  const contract = contractQuery.value.trim()
  if (!contract) {
    showError(t('tools.messages.parameterError'), t('tools.errors.enterContractAddress'))
    return
  }
  try {
    isContractLoading.value = true
    const [summary, state] = await Promise.all([
      smartContractApi.getContract({ network: network.value || 'testnet', contract }),
      smartContractApi.getContractState({ network: network.value || 'testnet', contract }),
    ])
    if (summary?.code !== 0) throw new Error(summary?.msg || t('tools.errors.queryContractFailed'))
    selectedContract.value = summary.data
    contractState.value = state?.code === 0 ? state.data || state.status : state
    contractHistory.value = null
    invokeContractAddress.value = contract
  } catch (error) {
    showError(t('tools.messages.queryFailed'), error)
  } finally {
    isContractLoading.value = false
  }
}

const loadContractHistory = async () => {
  const contract = selectedContractAddress.value
  if (!contract) {
    showError(t('tools.messages.parameterError'), t('tools.errors.selectOrEnterContractAddress'))
    return
  }
  try {
    isContractLoading.value = true
    const res = await smartContractApi.getContractHistory({ network: network.value || 'testnet', contract })
    if (res?.code !== 0) throw new Error(res?.msg || t('tools.errors.queryContractHistoryFailed'))
    contractHistory.value = res.data || []
  } catch (error) {
    showError(t('tools.messages.queryFailed'), error)
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
    if (!contract) throw new Error(t('tools.errors.enterContractAddress'))
    const req = buildUnifiedInvokeRequest(contract)
    if (import.meta.env.DEV) {
      console.log('[SAT20 Tools] invokeUnifiedContract request', req)
    }
    const confirmed = await confirmToolTransaction(invokeTransactionSummary(contract, req))
    if (!confirmed) return
    const [err, res] = await sat20.invokeUnifiedContract(req)
    if (err) throw err
    contractInvokeResult.value = res?.txid || ''
    showSuccess(t('tools.messages.invokeSubmitted'), res?.txid || t('tools.messages.txBroadcasted'))
  } catch (error) {
    showError(t('tools.messages.invokeFailed'), error)
  } finally {
    isInvokingContract.value = false
  }
}

const checkDeployTicker = async () => {
  deployCanDeploy.value = false
  deployCheckKey.value = ''
  const ticker = normalizeTicker(deployTicker.value, deployProtocol.value)
  if (!ticker) {
    showError(t('tools.messages.parameterError'), t('tools.errors.enterTicker'))
    return
  }
  try {
    validateDeployTickerForProtocol(deployProtocol.value, ticker)
  } catch (error) {
    showError(t('tools.messages.parameterError'), error)
    return
  }
  const [err] = await sat20.getTickerInfo(assetNameFor(deployProtocol.value, ticker))
  if (err) {
    deployCanDeploy.value = true
    deployCheckKey.value = currentDeployCheckKey.value
    showSuccess(t('tools.messages.canDeploy'), t('tools.messages.tickerNotDeployed', { protocol: deployProtocol.value, ticker }))
  } else {
    showError(t('tools.messages.cannotDeploy'), t('tools.errors.tickerExists'))
  }
}

const deployTickerAction = async () => {
  try {
    isDeployingTicker.value = true
    deployTickerResult.value = ''
    const ticker = normalizeTicker(deployTicker.value, deployProtocol.value)
    if (!ticker) throw new Error(t('tools.errors.enterTicker'))
    validateDeployTickerForProtocol(deployProtocol.value, ticker)
    if (!isDeployTickerReady.value) throw new Error(t('tools.errors.checkTickerBeforeDeploy'))
    const deployDetails = compactRows([
      txDetail(t('tools.txConfirm.protocol'), deployProtocol.value),
      txDetail(t('tools.txConfirm.ticker'), ticker),
      txDetail(t('tools.mint.maxSupply'), deployMaxSupply.value),
      showDeployLimit.value ? txDetail(t('tools.mint.mintLimit'), deployLimit.value) : null,
      txDetail(t('tools.mint.feeRate'), mintFeeRate.value),
    ])
    let ordxBindingSat = 0
    let deployDecimal = ''
    let runesDestAddress = ''
    let runesLimit = deployLimit.value
    if (deployProtocol.value === 'ordx') {
      ordxBindingSat = parsePositiveInteger(bindingSat.value, t('tools.mint.bindingSat'))
      if (!bindingSatOptions.includes(String(ordxBindingSat))) throw new Error(t('tools.errors.bindingSatOptions'))
      deployDetails.push({ label: t('tools.mint.bindingSat'), value: String(ordxBindingSat) })
    } else if (deployProtocol.value === 'brc20') {
      deployDecimal = parseDeployDecimals()
      deployDetails.push(
        { label: t('tools.mint.decimal'), value: deployDecimal },
        { label: t('tools.mint.selfMint'), value: effectiveDeploySelfMint.value ? t('common.enable') : t('common.disable') },
      )
    } else {
      runesDestAddress = walletStore.address || ''
      if (!runesDestAddress) throw new Error(t('tools.errors.walletAddressUnavailable'))
      runesLimit = deploySelfMint.value ? deployMaxSupply.value : deployLimit.value
      deployDecimal = parseDeployDecimals()
      deployDetails.push(
        { label: t('tools.mint.divisibility'), value: deployDecimal },
        { label: t('tools.mint.selfMint'), value: deploySelfMint.value ? t('common.enable') : t('common.disable') },
      )
    }
    const confirmed = await confirmToolTransaction({
      purpose: t('tools.txConfirm.purposes.deployTicker'),
      to: deployProtocol.value === 'runes' ? runesDestAddress : currentWalletAddress(),
      asset: displayAssetName('::'),
      amount: calculatedAmountLabel(),
      network: l1NetworkLabel(),
      feeRate: mintFeeRate.value,
      details: deployDetails,
    })
    if (!confirmed) return
    if (deployProtocol.value === 'ordx') {
      const [err, res] = await sat20.deployTickerOrdx(ticker, deployMaxSupply.value, deployLimit.value, ordxBindingSat, mintFeeRate.value)
      if (err) throw err
      deployTickerResult.value = res?.txId || ''
    } else if (deployProtocol.value === 'brc20') {
      const [err, res] = await sat20.deployTickerBrc20(ticker, deployMaxSupply.value, deployLimit.value, deployDecimal, mintFeeRate.value)
      if (err) throw err
      deployTickerResult.value = res?.txId || ''
    } else {
      const [err, res] = await sat20.DeployRunes_Remote(ticker, 0, deployMaxSupply.value, runesLimit, deploySelfMint.value, runesDestAddress, deployDecimal, mintFeeRate.value)
      if (err) throw err
      deployTickerResult.value = res?.txId || ''
    }
    showSuccess(t('tools.messages.deploySubmitted'), deployTickerResult.value || t('tools.messages.txBroadcasted'))
  } catch (error) {
    showError(t('tools.messages.deployFailed'), error)
  } finally {
    isDeployingTicker.value = false
  }
}

const checkMintTickerAvailability = async (showAvailableToast = true) => {
  mintCanMint.value = false
  mintCheckKey.value = ''
  const ticker = normalizeTicker(mintTicker.value, mintProtocol.value)
  if (!ticker) {
    showError(t('tools.messages.parameterError'), t('tools.errors.enterTicker'))
    return false
  }
  const address = walletStore.address || ''
  if (!address) {
    showError(t('tools.messages.parameterError'), t('tools.errors.walletAddressUnavailable'))
    return false
  }
  const [err, res] = await sat20.getTickerInfo(assetNameFor(mintProtocol.value, ticker))
  if (err) {
    showError(t('tools.messages.cannotMint'), t('tools.errors.tickerInfoNotFound'))
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
    showError(t('tools.messages.cannotMint'), t('tools.errors.brc20SelfMintUnsupported'))
    return false
  }

  const permission = await ordxApi.getMintPermission({
    protocol: mintProtocol.value,
    ticker,
    address,
    network: network.value,
  })
  if (permission?.code !== 0 || !permission?.data) {
    showError(t('tools.messages.cannotMint'), permission?.msg || t('tools.errors.noMintPermission'))
    return false
  }
  const permissionAmount = String(permission.data.amount ?? '')
  if (!isPositiveDecimalString(permissionAmount)) {
    showError(t('tools.messages.cannotMint'), t('tools.errors.mintableAmountZero'))
    return false
  }
  if (mintProtocol.value !== 'runes') {
    if (!isPositiveDecimalString(mintAmount.value)) {
      showError(t('tools.messages.parameterError'), t('tools.errors.enterValidMintAmount'))
      return false
    }
    if (isPositiveDecimalString(mintLimit) && compareDecimalStrings(mintAmount.value, mintLimit) > 0) {
      showError(t('tools.messages.cannotMint'), t('tools.errors.singleMintLimit', { amount: mintLimit }))
      return false
    }
    if (compareDecimalStrings(mintAmount.value, permissionAmount) > 0) {
      showError(t('tools.messages.cannotMint'), t('tools.errors.addressMintLimit', { amount: permissionAmount }))
      return false
    }
  } else if (!mintAmount.value) {
    mintAmount.value = permissionAmount
  }

  if (showAvailableToast) {
    showSuccess(t('tools.messages.canMint'), t('tools.errors.addressMintLimit', { amount: permissionAmount }))
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
    if (!ticker) throw new Error(t('tools.errors.enterTicker'))
    if (mintProtocol.value !== 'runes' && !mintAmount.value) throw new Error(t('tools.errors.enterMintAmount'))
    if (!isMintAssetReady.value) throw new Error(t('tools.errors.checkTickerBeforeMint'))
    const canMint = await checkMintTickerAvailability(false)
    if (!canMint) return
    const confirmed = await confirmToolTransaction({
      purpose: t('tools.txConfirm.purposes.mintAsset'),
      to: currentWalletAddress(),
      asset: displayAssetName(assetNameFor(mintProtocol.value, ticker)),
      amount: mintProtocol.value === 'runes' ? t('tools.txConfirm.deploymentTerms') : mintAmount.value,
      network: l1NetworkLabel(),
      feeRate: mintFeeRate.value,
      details: compactRows([
        txDetail(t('tools.txConfirm.protocol'), mintProtocol.value),
        txDetail(t('tools.txConfirm.ticker'), ticker),
      ]),
    })
    if (!confirmed) return
    const [err, res] = mintProtocol.value === 'ordx'
      ? await sat20.mintAssetOrdx(ticker, mintAmount.value, mintFeeRate.value)
      : mintProtocol.value === 'runes'
        ? await sat20.mintAssetRunes(ticker, mintFeeRate.value)
        : await sat20.mintAssetBrc20(ticker, mintAmount.value, mintFeeRate.value)
    if (err) throw err
    mintAssetResult.value = res?.txId || ''
    showSuccess(t('tools.messages.mintSubmitted'), mintAssetResult.value || t('tools.messages.txBroadcasted'))
  } catch (error) {
    showError(t('tools.messages.mintFailed'), error)
  } finally {
    isMintingAsset.value = false
  }
}

const checkDidNameAvailability = async (showAvailableToast = true) => {
  const name = didName.value.trim().toLowerCase()
  didCanMint.value = false
  didCheckKey.value = ''
  if (!name) {
    showError(t('tools.messages.parameterError'), t('tools.errors.enterName'))
    return false
  }
  if (/\s|\//.test(name)) {
    showError(t('tools.messages.parameterError'), t('tools.errors.invalidName'))
    return false
  }
  try {
    const res = await ordxApi.getNsName({ name, network: network.value })
    if (res?.code === 0 && res?.data) {
      showError(t('tools.messages.cannotMint'), t('tools.errors.nameExists'))
      return false
    }
    didCanMint.value = true
    didCheckKey.value = currentDidCheckKey.value
    if (showAvailableToast) {
      showSuccess(t('tools.messages.canMint'), t('tools.messages.nameAvailable', { name }))
    }
    return true
  } catch (error) {
    showError(t('tools.messages.checkFailed'), error)
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
    if (!isMintDidReady.value) throw new Error(t('tools.errors.checkDidBeforeMint'))
    const canMint = await checkDidNameAvailability(false)
    if (!canMint || !name) return
    const confirmed = await confirmToolTransaction({
      purpose: t('tools.txConfirm.purposes.mintDid'),
      to: currentWalletAddress(),
      asset: displayAssetName('::'),
      amount: calculatedAmountLabel(),
      network: l1NetworkLabel(),
      feeRate: mintFeeRate.value,
      details: compactRows([
        txDetail(t('tools.txConfirm.name'), name),
      ]),
    })
    if (!confirmed) return
    const [err, res] = await sat20.inscribeName(name, mintFeeRate.value)
    if (err) throw err
    didMintResult.value = res?.txId || ''
    showSuccess(t('tools.messages.didMintSubmitted'), didMintResult.value || t('tools.messages.txBroadcasted'))
  } catch (error) {
    showError(t('tools.messages.didMintFailed'), error)
  } finally {
    isMintingDid.value = false
  }
}
</script>
