<template>
  <LayoutScroll>
    <div class="p-4 space-y-5 max-w-xl mx-auto">
      <div class="text-center space-y-1">
        <h1 class="text-2xl font-semibold">恢复自托管账户</h1>
        <p class="text-sm text-muted-foreground">通过公开恢复码、私人知识和 Guardian 或用户分片恢复全部钱包。</p>
      </div>

      <section v-if="step === 1" class="space-y-3">
        <label class="font-medium">账户恢复码</label>
        <Textarea v-model="locator" rows="6" placeholder="粘贴 sat20account1:..." />
        <Button class="w-full" :disabled="busy || !locator" @click="loadRecovery">
          <Icon v-if="busy" icon="lucide:loader-2" class="mr-2 h-4 w-4 animate-spin" />
          加载加密账户备份
        </Button>
      </section>

      <section v-else-if="step === 2" class="space-y-4">
        <h2 class="font-medium">回答私人知识问题</h2>
        <div v-for="(question, index) in loaded.questions" :key="question.id" class="space-y-1">
          <label class="text-sm">{{ question.prompt }}</label>
          <Input v-model="answers[index]" type="password" autocomplete="off" />
        </div>
        <Button class="w-full" :disabled="busy" @click="recoverKnowledge">
          恢复 DKVS 分片
        </Button>
      </section>

      <section v-else-if="step === 3" class="space-y-4">
        <h2 class="font-medium">提供第二份恢复材料</h2>
        <p class="text-sm text-muted-foreground">可以粘贴用户分片，或者使用 Guardian 恢复。</p>
        <Textarea v-model="userShare" rows="5" placeholder="可选：粘贴 sat20share1:..." />
        <Button variant="outline" class="w-full" :disabled="busy || !userShare" @click="acceptUserShare">使用用户分片</Button>

        <div v-if="loaded.guardian && loaded.has_guardian_location" class="rounded-lg border p-3 space-y-2">
          <Button variant="outline" class="w-full" :disabled="busy" @click="createGuardianRequest">生成 Guardian 恢复请求</Button>
          <Textarea v-if="guardianRequest" :model-value="guardianRequest" readonly rows="6" />
          <Button v-if="guardianRequest" variant="ghost" class="w-full" @click="copyText(guardianRequest)">复制请求</Button>
          <Textarea v-model="guardianResponse" rows="6" placeholder="粘贴 Guardian 返回的加密响应" />
          <Button variant="outline" class="w-full" :disabled="busy || !guardianResponse" @click="acceptGuardianResponse">使用 Guardian 响应</Button>
        </div>

        <Button class="w-full" :disabled="busy || !companionReady" @click="previewRecovery">预览恢复内容</Button>
      </section>

      <section v-else-if="step === 4 && preview" class="space-y-4">
        <h2 class="font-medium">确认恢复账户</h2>
        <div v-for="wallet in preview.wallets" :key="wallet.name" class="rounded-lg border p-3">
          <div class="font-medium">{{ wallet.name }}</div>
          <div class="text-sm text-muted-foreground">{{ wallet.account_count }} 个子账户</div>
          <div class="text-xs mt-1">DID：{{ wallet.dids.join('、') }}</div>
        </div>
        <Input v-model="password" type="password" placeholder="设置新的本地钱包密码" autocomplete="new-password" />
        <Input v-model="confirmPassword" type="password" placeholder="再次输入密码" autocomplete="new-password" />
        <Button class="w-full" :disabled="busy" @click="commitRecovery">恢复全部钱包</Button>
      </section>

      <section v-else-if="step === 5" class="text-center space-y-3 py-10">
        <Icon icon="lucide:badge-check" class="h-14 w-14 text-green-500 mx-auto" />
        <h2 class="text-xl font-semibold">账户恢复成功</h2>
        <p class="text-sm text-muted-foreground">正在重新载入钱包。</p>
      </section>

      <Alert v-if="error" variant="destructive"><AlertDescription>{{ error }}</AlertDescription></Alert>
      <Button v-if="step < 5" variant="ghost" class="w-full" @click="router.push('/')">取消</Button>
    </div>
  </LayoutScroll>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import { Icon } from '@iconify/vue'
import LayoutScroll from '@/components/layout/LayoutScroll.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { Alert, AlertDescription } from '@/components/ui/alert'
import accountSDK, { type AccountRecoverySummary } from '@/utils/accountManagement'
import { hashPassword } from '@/utils/crypto'
import { walletStorage } from '@/lib/walletStorage'

const router = useRouter()
const busy = ref(false)
const error = ref('')
const step = ref(1)
const locator = ref('')
const loaded = ref<any>(null)
const answers = ref<string[]>([])
const userShare = ref('')
const guardianRequest = ref('')
const guardianResponse = ref('')
const companionReady = ref(false)
const preview = ref<AccountRecoverySummary | null>(null)
const password = ref('')
const confirmPassword = ref('')

const sessionId = computed(() => loaded.value?.session_id || '')

const run = async (task: () => Promise<void>) => {
  busy.value = true
  error.value = ''
  try { await task() } catch (e: any) { error.value = e?.message || '恢复失败' } finally { busy.value = false }
}

const loadRecovery = () => run(async () => {
  loaded.value = await accountSDK.loadRecovery(locator.value.trim())
  answers.value = loaded.value.questions.map(() => '')
  step.value = 2
})

const recoverKnowledge = () => run(async () => {
  const attempts = answers.value.map((answer, index) => ({ question_id: loaded.value.questions[index].id, answer })).filter(item => item.answer)
  if (attempts.length < 2) throw new Error('至少回答两个问题')
  await accountSDK.recoverKnowledge(sessionId.value, attempts)
  step.value = 3
})

const acceptUserShare = () => run(async () => {
  await accountSDK.setUserShare(sessionId.value, userShare.value.trim())
  companionReady.value = true
})

const createGuardianRequest = () => run(async () => {
  guardianRequest.value = (await accountSDK.createGuardianRequest(sessionId.value)).request
})

const acceptGuardianResponse = () => run(async () => {
  await accountSDK.consumeGuardianResponse(sessionId.value, guardianResponse.value.trim())
  companionReady.value = true
})

const previewRecovery = () => run(async () => {
  preview.value = (await accountSDK.previewRecovery(sessionId.value)).summary
  step.value = 4
})

const commitRecovery = () => run(async () => {
  if (password.value.length < 8 || password.value !== confirmPassword.value) throw new Error('密码至少 8 个字符且两次输入必须一致')
  const hashed = await hashPassword(password.value)
  const result = await accountSDK.commitRecovery(sessionId.value, hashed)
  const wallets = result.wallets.map(wallet => ({
    id: String(wallet.id),
    name: wallet.name,
    accounts: wallet.accounts.map(account => ({
      index: account.index,
      name: account.did,
      address: account.address,
      pubKey: account.pub_key,
    })),
  }))
  if (!wallets.length || !wallets[0].accounts.length) throw new Error('恢复结果为空')
  await walletStorage.setValue('wallets', wallets)
  await walletStorage.setValue('walletId', wallets[0].id)
  await walletStorage.setValue('accountIndex', wallets[0].accounts[0].index)
  await walletStorage.setValue('address', wallets[0].accounts[0].address)
  await walletStorage.setValue('pubkey', wallets[0].accounts[0].pubKey)
  await walletStorage.setValue('hasWallet', true)
  await walletStorage.setValue('locked', true)
  step.value = 5
  setTimeout(() => {
    window.location.hash = '#/unlock'
    window.location.reload()
  }, 500)
})

const copyText = async (value: string) => {
  if (value) await navigator.clipboard.writeText(value)
}
</script>
