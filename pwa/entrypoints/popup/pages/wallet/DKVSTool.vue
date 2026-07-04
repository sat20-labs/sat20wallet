<template>
  <LayoutHome>
    <WalletHeader />
    <div class="space-y-4 px-1 pb-4">
      <header class="flex items-center gap-2">
        <Button variant="ghost" size="icon" @click="router.back()">
          <Icon icon="lucide:arrow-left" class="h-4 w-4" />
        </Button>
        <div>
          <h2 class="text-xl font-medium text-zinc-600/90">{{ t('dkvsTool.title') }}</h2>
          <p class="text-xs text-muted-foreground">{{ t('dkvsTool.network', { network }) }}</p>
        </div>
      </header>

      <Tabs v-model="activeTab" class="w-full">
        <TabsList class="grid w-full grid-cols-4">
          <TabsTrigger value="record">{{ t('dkvsTool.tabs.record') }}</TabsTrigger>
          <TabsTrigger value="prefix">{{ t('dkvsTool.tabs.prefix') }}</TabsTrigger>
          <TabsTrigger value="write">{{ t('dkvsTool.tabs.write') }}</TabsTrigger>
          <TabsTrigger value="checkpoint">{{ t('dkvsTool.tabs.checkpoint') }}</TabsTrigger>
        </TabsList>

        <TabsContent value="record" class="mt-4">
          <Card>
            <CardHeader>
              <CardTitle class="text-base">{{ t('dkvsTool.record.title') }}</CardTitle>
            </CardHeader>
            <CardContent class="space-y-3">
              <div class="space-y-1">
                <Label>{{ t('dkvsTool.record.key') }}</Label>
                <Input v-model="recordKey" placeholder="/personal/..." />
              </div>
              <div class="space-y-1">
                <Label>{{ t('dkvsTool.record.hash') }}</Label>
                <Input v-model="recordHash" placeholder="record hash" />
              </div>
              <Button class="w-full" :disabled="isLoadingRecord || (!recordKey.trim() && !recordHash.trim())" @click="loadRecord">
                <Icon :icon="isLoadingRecord ? 'lucide:loader' : 'lucide:search'" class="h-4 w-4" :class="{ 'animate-spin': isLoadingRecord }" />
                {{ t('dkvsTool.actions.load') }}
              </Button>
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="prefix" class="mt-4">
          <Card>
            <CardHeader>
              <CardTitle class="text-base">{{ t('dkvsTool.prefix.title') }}</CardTitle>
            </CardHeader>
            <CardContent class="space-y-3">
              <div class="space-y-1">
                <Label>{{ t('dkvsTool.prefix.prefix') }}</Label>
                <Input v-model="prefix" placeholder="/personal/..." />
              </div>
              <div class="grid grid-cols-2 gap-3">
                <div class="space-y-1">
                  <Label>{{ t('dkvsTool.prefix.start') }}</Label>
                  <Input v-model="start" type="number" min="0" />
                </div>
                <div class="space-y-1">
                  <Label>{{ t('dkvsTool.prefix.limit') }}</Label>
                  <Input v-model="limit" type="number" min="1" max="100" />
                </div>
              </div>
              <Button class="w-full" :disabled="isListingRecords || !prefix.trim()" @click="listPrefix">
                <Icon :icon="isListingRecords ? 'lucide:loader' : 'lucide:list-tree'" class="h-4 w-4" :class="{ 'animate-spin': isListingRecords }" />
                {{ t('dkvsTool.actions.list') }}
              </Button>
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="write" class="mt-4">
          <Card>
            <CardHeader>
              <CardTitle class="text-base">{{ t('dkvsTool.write.title') }}</CardTitle>
            </CardHeader>
            <CardContent class="space-y-3">
              <div class="space-y-1">
                <Label>{{ t('dkvsTool.write.recordJson') }}</Label>
                <Textarea v-model="recordJson" class="min-h-52 font-mono text-xs" spellcheck="false" />
              </div>
              <div class="grid grid-cols-2 gap-2">
                <Button :disabled="isWritingRecord || !recordJson.trim()" @click="submitRecord">
                  <Icon :icon="isWritingRecord ? 'lucide:loader' : 'lucide:upload'" class="h-4 w-4" :class="{ 'animate-spin': isWritingRecord }" />
                  {{ t('dkvsTool.actions.put') }}
                </Button>
                <Button variant="secondary" :disabled="isWritingRecord || !recordJson.trim()" @click="submitTombstone">
                  <Icon :icon="isWritingRecord ? 'lucide:loader' : 'lucide:archive-x'" class="h-4 w-4" :class="{ 'animate-spin': isWritingRecord }" />
                  {{ t('dkvsTool.actions.tombstone') }}
                </Button>
              </div>
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="checkpoint" class="mt-4">
          <Card>
            <CardHeader>
              <CardTitle class="text-base">{{ t('dkvsTool.checkpoint.title') }}</CardTitle>
            </CardHeader>
            <CardContent>
              <Button class="w-full" :disabled="isLoadingCheckpoint" @click="loadCheckpoint">
                <Icon :icon="isLoadingCheckpoint ? 'lucide:loader' : 'lucide:git-commit-horizontal'" class="h-4 w-4" :class="{ 'animate-spin': isLoadingCheckpoint }" />
                {{ t('dkvsTool.actions.load') }}
              </Button>
            </CardContent>
          </Card>
        </TabsContent>
      </Tabs>

      <Card>
        <CardHeader>
          <CardTitle class="text-base">{{ t('dkvsTool.result.title') }}</CardTitle>
        </CardHeader>
        <CardContent>
          <pre class="max-h-80 overflow-auto whitespace-pre-wrap break-words rounded-sm border border-border bg-muted/30 p-3 text-xs">{{ resultText }}</pre>
        </CardContent>
      </Card>
    </div>
  </LayoutHome>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { storeToRefs } from 'pinia'
import { Icon } from '@iconify/vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import LayoutHome from '@/components/layout/LayoutHome.vue'
import WalletHeader from '@/components/wallet/HomeHeader.vue'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Textarea } from '@/components/ui/textarea'
import { dkvsApi } from '@/apis'
import { useWalletStore } from '@/store'

const { t } = useI18n()
const router = useRouter()
const walletStore = useWalletStore()
const { network } = storeToRefs(walletStore)

const activeTab = ref('record')
const recordKey = ref('')
const recordHash = ref('')
const prefix = ref('/personal/')
const start = ref('0')
const limit = ref('20')
const recordJson = ref('{\n  "Version": 1,\n  "Key": "",\n  "Value": "",\n  "Data": "",\n  "PubKey": "",\n  "Signature": "",\n  "Seq": 1,\n  "IssueTime": 0,\n  "TTL": 60000,\n  "ExpiryHeight": 0,\n  "FeeProof": "",\n  "Flags": 0\n}')
const result = ref<unknown>({ status: 'idle' })
const isLoadingRecord = ref(false)
const isListingRecords = ref(false)
const isWritingRecord = ref(false)
const isLoadingCheckpoint = ref(false)

const activeNetwork = computed(() => network.value || 'testnet')
const resultText = computed(() => JSON.stringify(result.value, null, 2))

function parsePositiveInt(value: string, fallback: number) {
  const parsed = Number.parseInt(value, 10)
  return Number.isFinite(parsed) && parsed >= 0 ? parsed : fallback
}

async function run(action: () => Promise<unknown>, loading: { value: boolean }) {
  loading.value = true
  try {
    result.value = await action()
  } catch (error: any) {
    result.value = {
      error: error?.message || String(error),
    }
  } finally {
    loading.value = false
  }
}

async function loadRecord() {
  await run(() => dkvsApi.getRecord({
    key: recordKey.value.trim() || undefined,
    hash: recordHash.value.trim() || undefined,
    network: activeNetwork.value,
  }), isLoadingRecord)
}

async function listPrefix() {
  await run(() => dkvsApi.listRecords({
    prefix: prefix.value.trim(),
    start: parsePositiveInt(start.value, 0),
    limit: parsePositiveInt(limit.value, 20),
    network: activeNetwork.value,
  }), isListingRecords)
}

async function submitRecord() {
  await run(() => dkvsApi.putRecord({
    record: JSON.parse(recordJson.value),
    network: activeNetwork.value,
  }), isWritingRecord)
}

async function submitTombstone() {
  await run(() => dkvsApi.tombstone({
    record: JSON.parse(recordJson.value),
    network: activeNetwork.value,
  }), isWritingRecord)
}

async function loadCheckpoint() {
  await run(() => dkvsApi.getCheckpoint({ network: activeNetwork.value }), isLoadingCheckpoint)
}
</script>
