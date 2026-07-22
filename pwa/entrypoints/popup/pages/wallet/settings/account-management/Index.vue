<template>
  <LayoutScroll>
    <div class="p-4 space-y-5 max-w-2xl mx-auto">
      <div class="flex items-center gap-2">
        <Button variant="ghost" size="icon" @click="router.back()">
          <Icon icon="lucide:arrow-left" class="h-5 w-5" />
        </Button>
        <div>
          <h1 class="text-xl font-semibold">自托管账户恢复</h1>
          <p class="text-sm text-muted-foreground">加密备份当前钱包，并完成一次真实恢复演练。</p>
        </div>
      </div>

      <Alert v-if="savedState">
        <AlertDescription class="space-y-1">
          <div>当前状态：{{ savedState.status === 'active-paid' ? '付费保存' : '临时缓存' }}</div>
          <div>恢复模式：{{ savedState.recoveryMode }}</div>
          <div>上次演练：{{ new Date(savedState.lastRehearsalAt).toLocaleString() }}</div>
        </AlertDescription>
      </Alert>

      <section v-if="step === 1" class="space-y-4">
        <h2 class="font-medium">1. 确认要备份的钱包</h2>
        <p class="text-sm text-muted-foreground">
          SDK 只备份助记词、钱包名称、子账户数量和 Ordinals DID。助记词不会返回给页面。
        </p>
        <div v-for="wallet in drafts" :key="wallet.id" class="rounded-lg border p-3 space-y-3">
          <div class="font-medium">{{ wallet.name }}</div>
          <div v-for="account in wallet.accounts" :key="account.index" class="space-y-1">
            <label class="text-xs text-muted-foreground">子账户 {{ account.index }} 的 Ordinals DID</label>
            <Input v-model="account.did" placeholder="例如 alice 或 alice.btc" />
          </div>
        </div>
        <Button class="w-full" :disabled="busy" @click="runPreflight">
          <Icon v-if="busy" icon="lucide:loader-2" class="mr-2 h-4 w-4 animate-spin" />
          检查账户
        </Button>
      </section>

      <section v-else-if="step === 2" class="space-y-4">
        <h2 class="font-medium">2. 选择恢复和存储方式</h2>
        <div class="grid grid-cols-2 gap-2">
          <Button :variant="recoveryMode === '2of3' ? 'default' : 'outline'" @click="recoveryMode = '2of3'">
            2/3 便捷恢复
          </Button>
          <Button :variant="recoveryMode === '2of2' ? 'default' : 'outline'" @click="recoveryMode = '2of2'">
            2/2 增强安全
          </Button>
        </div>
        <p class="text-xs text-muted-foreground">
          2/3 使用私人知识、Guardian 和可选用户分片；2/2 必须同时提供私人知识和用户分片。
        </p>
        <div class="space-y-2">
          <button
            v-for="option in storageOptions"
            :key="option.id"
            type="button"
            class="w-full text-left rounded-lg border p-3 disabled:opacity-50"
            :class="selectedStorage === option.id ? 'border-primary' : ''"
            :disabled="!option.available"
            @click="selectedStorage = option.id"
          >
            <div class="font-medium">{{ option.title }}</div>
            <div class="text-sm text-muted-foreground">{{ option.description }}</div>
            <div v-if="option.estimated_cost" class="text-xs mt-1">
              当前报价：{{ option.estimated_cost }} {{ option.fee_asset }}；年度参考：{{ option.estimated_annual_cost }}
            </div>
            <div v-if="option.estimated_expiry_time" class="text-xs mt-1">
              预计到期：{{ new Date(option.estimated_expiry_time).toLocaleString() }}
            </div>
            <ul v-if="option.warnings?.length" class="text-xs text-amber-500 mt-1 list-disc pl-4">
              <li v-for="warning in option.warnings" :key="warning">{{ warning }}</li>
            </ul>
          </button>
        </div>
        <Button class="w-full" :disabled="busy || !selectedStorage" @click="confirmStorage">
          <Icon v-if="busy" icon="lucide:loader-2" class="mr-2 h-4 w-4 animate-spin" />
          确认存储方式
        </Button>
      </section>

      <section v-else-if="step === 3" class="space-y-4">
        <h2 class="font-medium">3. 设置私人知识问题</h2>
        <Alert>
          <AlertDescription>
            选择对你长期明确、但其他人难以枚举的问题。答案只在本地 SDK 中处理，不会保存到 PWA。
          </AlertDescription>
        </Alert>
        <div v-for="(question, index) in questions" :key="question.id" class="rounded-lg border p-3 space-y-2">
          <label class="text-sm font-medium">问题 {{ index + 1 }}</label>
          <Input v-model="question.prompt" />
          <Input v-model="question.answer" type="password" placeholder="答案" autocomplete="off" />
          <Input v-model="question.confirmation" type="password" placeholder="再次输入答案" autocomplete="off" />
        </div>
        <div v-if="recoveryMode === '2of3'" class="space-y-2">
          <label class="text-sm font-medium">Guardian 联系信息</label>
          <Textarea v-model="guardianContact" rows="5" placeholder="粘贴好友钱包生成的 Guardian contact JSON" />
        </div>
        <Button class="w-full" :disabled="busy" @click="createRecovery">
          <Icon v-if="busy" icon="lucide:loader-2" class="mr-2 h-4 w-4 animate-spin" />
          创建并保存加密账户备份
        </Button>
      </section>

      <section v-else-if="step === 4 && creation" class="space-y-4">
        <h2 class="font-medium">4. 保存恢复材料</h2>
        <div class="space-y-2">
          <label class="text-sm font-medium">公开账户恢复码</label>
          <Textarea :model-value="creation.locator" readonly rows="5" />
          <Button variant="outline" class="w-full" @click="copyText(creation.locator)">复制恢复码</Button>
        </div>
        <div class="space-y-2">
          <label class="text-sm font-medium">秘密用户分片</label>
          <Textarea :model-value="creation.user_share" readonly rows="5" />
          <Button variant="outline" class="w-full" @click="copyText(creation.user_share)">复制用户分片</Button>
          <p class="text-xs text-muted-foreground">用户分片是秘密材料，不要与公开恢复码保存在同一位置。</p>
        </div>

        <div v-if="recoveryMode === '2of3'" class="space-y-2 rounded-lg border p-3">
          <label class="text-sm font-medium">请让 Guardian 接受托管</label>
          <Textarea :model-value="creation.guardian_setup" readonly rows="7" />
          <Button variant="outline" class="w-full" @click="copyText(creation.guardian_setup)">复制 Guardian setup</Button>
          <Textarea v-model="guardianReceipt" rows="5" placeholder="粘贴 Guardian 返回的 receipt" />
          <Button class="w-full" :disabled="busy || !guardianReceipt" @click="verifyGuardian">
            验证 Guardian 分片
          </Button>
        </div>
        <Button v-else class="w-full" @click="step = 5">进入恢复演练</Button>
      </section>

      <section v-else-if="step === 5 && creation" class="space-y-4">
        <h2 class="font-medium">5. 完成恢复演练</h2>
        <p class="text-sm text-muted-foreground">请重新输入至少两个问题的答案。2/2 模式还需要重新粘贴用户分片。</p>
        <Input v-for="(answer, index) in rehearsalAnswers" :key="index" v-model="rehearsalAnswers[index]" type="password" :placeholder="`问题 ${index + 1} 的答案`" autocomplete="off" />
        <Textarea v-if="recoveryMode === '2of2'" v-model="rehearsalUserShare" rows="5" placeholder="重新粘贴用户分片" />
        <Button class="w-full" :disabled="busy" @click="rehearse">
          <Icon v-if="busy" icon="lucide:loader-2" class="mr-2 h-4 w-4 animate-spin" />
          执行恢复演练
        </Button>
      </section>

      <section v-else-if="step === 6" class="space-y-3 text-center py-8">
        <Icon icon="lucide:badge-check" class="h-14 w-14 text-green-500 mx-auto" />
        <h2 class="text-xl font-semibold">账户恢复已激活</h2>
        <p class="text-sm text-muted-foreground">账户备份已重新读取并通过恢复演练。</p>
        <Button @click="router.push('/wallet')">返回钱包</Button>
      </section>

      <Separator />

      <section class="space-y-3">
        <h2 class="font-medium">Guardian 工具</h2>
        <p class="text-xs text-muted-foreground">以下工具用于你替好友保管分片或响应好友的恢复请求。</p>
        <Button variant="outline" class="w-full" :disabled="busy" @click="generateGuardianIdentity">生成我的 Guardian 联系信息</Button>
        <Textarea v-if="guardianIdentity" :model-value="guardianIdentity" readonly rows="5" />

        <Textarea v-model="guardianSetupInput" rows="5" placeholder="好友发送的 Guardian setup JSON" />
        <select v-model="guardianStorageChoice" class="w-full rounded-md border bg-background p-2 text-sm">
          <option value="">选择托管存储方式</option>
          <option v-for="option in storageOptions.filter(o => o.available)" :key="option.id" :value="option.id">{{ option.title }}</option>
        </select>
        <Button variant="outline" class="w-full" :disabled="busy || !guardianSetupInput || !guardianStorageChoice" @click="acceptGuardianSetup">接受并保存好友分片</Button>
        <Textarea v-if="guardianReceiptOutput" :model-value="guardianReceiptOutput" readonly rows="5" />

        <Textarea v-model="guardianRecoveryRequest" rows="5" placeholder="好友发送的 Guardian 恢复请求 JSON" />
        <Button variant="outline" class="w-full" :disabled="busy || !guardianRecoveryRequest" @click="createGuardianResponse">生成加密恢复响应</Button>
        <Textarea v-if="guardianResponse" :model-value="guardianResponse" readonly rows="5" />
      </section>

      <Alert v-if="error" variant="destructive"><AlertDescription>{{ error }}</AlertDescription></Alert>
    </div>
  </LayoutScroll>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { storeToRefs } from 'pinia'
import { Icon } from '@iconify/vue'
import LayoutScroll from '@/components/layout/LayoutScroll.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Separator } from '@/components/ui/separator'
import { useWalletStore } from '@/store'
import accountSDK, { type AccountStorageOption, type AccountWalletMetadataInput } from '@/utils/accountManagement'
import { loadAccountManagementState, saveAccountManagementState } from '@/lib/account-management-state'

const router = useRouter()
const walletStore = useWalletStore()
const { wallets } = storeToRefs(walletStore)
const savedState = ref(loadAccountManagementState())
const busy = ref(false)
const error = ref('')
const step = ref(1)
const recoveryMode = ref<'2of2' | '2of3'>('2of3')
const storageOptions = ref<AccountStorageOption[]>([])
const selectedStorage = ref('')
const storageAuthorization = ref<any>(null)
const preflight = ref<any>(null)
const guardianContact = ref('')
const creation = ref<any>(null)
const guardianReceipt = ref('')
const rehearsalAnswers = ref(['', '', ''])
const rehearsalUserShare = ref('')

const drafts = ref(wallets.value.map(wallet => ({
  id: wallet.id,
  name: wallet.name,
  accounts: wallet.accounts.map(account => ({ index: account.index, did: account.name || '' })),
})))

const questions = ref([
  { id: 'book-page', prompt: '你指定版本的一本书，第十页最后十个字是什么？', answer: '', confirmation: '', ignore_punctuation: true },
  { id: 'private-phrase', prompt: '你和家人约定的一句未公开的话是什么？', answer: '', confirmation: '', ignore_punctuation: true },
  { id: 'private-note', prompt: '你长期保存的一张私人纸条上的指定句子是什么？', answer: '', confirmation: '', ignore_punctuation: true },
])

const guardianIdentity = ref('')
const guardianSetupInput = ref('')
const guardianStorageChoice = ref('')
const guardianReceiptOutput = ref('')
const guardianRecoveryRequest = ref('')
const guardianResponse = ref('')

const metadata = (): AccountWalletMetadataInput[] => drafts.value.map(wallet => ({
  id: Number(wallet.id),
  name: wallet.name,
  sub_accounts: Object.fromEntries(wallet.accounts.map(account => [account.index, account.did.trim()])),
}))

const run = async (task: () => Promise<void>) => {
  busy.value = true
  error.value = ''
  try { await task() } catch (e: any) { error.value = e?.message || '操作失败' } finally { busy.value = false }
}

const runPreflight = () => run(async () => {
  if (!walletStore.password) throw new Error('钱包尚未解锁')
  if (drafts.value.some(wallet => wallet.accounts.some(account => !account.did.trim()))) throw new Error('请填写所有子账户的 Ordinals DID')
  preflight.value = await accountSDK.preflight(walletStore.password, metadata())
  storageOptions.value = (await accountSDK.getStorageOptions()).options
  step.value = 2
})

const confirmStorage = () => run(async () => {
  storageAuthorization.value = await accountSDK.confirmStorage(selectedStorage.value)
  step.value = 3
})

const createRecovery = () => run(async () => {
  for (const q of questions.value) {
    if (!q.prompt.trim() || q.answer.length < 8 || q.answer !== q.confirmation) throw new Error('三个问题都需要至少 8 个字符且两次答案一致')
  }
  let guardian: any = undefined
  if (recoveryMode.value === '2of3') {
    try { guardian = JSON.parse(guardianContact.value) } catch { throw new Error('Guardian 联系信息不是有效 JSON') }
  }
  creation.value = await accountSDK.createRecovery({
    password: walletStore.password,
    wallets: metadata(),
    recovery_mode: recoveryMode.value,
    questions: questions.value,
    guardian,
    storage_authorization_id: storageAuthorization.value.id,
  })
  questions.value.forEach(q => { q.answer = ''; q.confirmation = '' })
  step.value = 4
})

const verifyGuardian = () => run(async () => {
  const result = await accountSDK.checkGuardianSetup(creation.value.session_id, guardianReceipt.value)
  creation.value.locator = result.locator
  step.value = 5
})

const rehearse = () => run(async () => {
  const answers = rehearsalAnswers.value.map((answer, index) => ({ question_id: questions.value[index].id, answer })).filter(item => item.answer)
  const result = await accountSDK.rehearse(creation.value.session_id, answers, rehearsalUserShare.value)
  if (!result.verified) throw new Error('恢复演练未通过')
  saveAccountManagementState({
    version: 1,
    status: selectedStorage.value === 'paid' ? 'active-paid' : 'active-temporary',
    accountId: preflight.value.account_id,
    packageId: result.summary.package_id,
    recoveryMode: recoveryMode.value,
    storageMode: selectedStorage.value as 'paid' | 'temporary',
    storageDescription: storageAuthorization.value.summary?.description,
    estimatedExpiryTime: storageAuthorization.value.summary?.estimated_expiry_time,
    guardianStatus: recoveryMode.value === '2of3' ? 'stored' : 'none',
    publicLocator: creation.value.locator,
    lastRehearsalAt: Date.now(),
  })
  rehearsalAnswers.value = ['', '', '']
  rehearsalUserShare.value = ''
  savedState.value = loadAccountManagementState()
  step.value = 6
})

const generateGuardianIdentity = () => run(async () => {
  if (!walletStore.password) throw new Error('钱包尚未解锁')
  guardianIdentity.value = (await accountSDK.guardianIdentity(walletStore.password)).contact
})

const acceptGuardianSetup = () => run(async () => {
  const authorization = await accountSDK.confirmStorage(guardianStorageChoice.value)
  guardianReceiptOutput.value = (await accountSDK.acceptGuardianSetup(walletStore.password, guardianSetupInput.value, authorization.id)).receipt
})

const createGuardianResponse = () => run(async () => {
  guardianResponse.value = (await accountSDK.createGuardianResponse(walletStore.password, guardianRecoveryRequest.value)).response
})

const copyText = async (value: string) => {
  if (value) await navigator.clipboard.writeText(value)
}

onMounted(async () => {
  try { storageOptions.value = (await accountSDK.getStorageOptions()).options } catch { /* preflight will retry */ }
})
</script>
