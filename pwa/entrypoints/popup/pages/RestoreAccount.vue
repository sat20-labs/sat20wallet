<template>
  <LayoutScroll>
    <div class="p-4 space-y-5 max-w-xl mx-auto">
      <div class="text-center space-y-1">
        <h1 class="text-2xl font-semibold">{{ rehearsalOnly ? '只读恢复演练' : '恢复自托管账户' }}</h1>
        <p v-if="rehearsalOnly" class="text-sm text-muted-foreground">验证两份恢复材料并预览备份，不覆盖当前钱包，也不修改本地密码。</p>
        <template v-else>
          <p class="text-sm text-muted-foreground">通过公开恢复码及两份恢复材料恢复全部钱包。</p>
          <p class="text-sm text-muted-foreground">如上次恢复中断，请使用同一恢复码和原恢复时设置的本地密码继续。</p>
        </template>
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
          恢复加密分片
        </Button>
        <Button v-if="loaded.locator.recovery_mode === '2of3' && loaded.has_guardian_location"
          variant="outline" class="w-full" :disabled="busy" @click="step = 3">
          使用用户分片和 Guardian
        </Button>
      </section>

      <section v-else-if="step === 3" class="space-y-4">
        <h2 class="font-medium">提供恢复材料</h2>
        <p class="text-sm text-muted-foreground">{{ knowledgeReady ? '可以粘贴用户分片，或者使用 Guardian 恢复。' : '需要同时提供用户分片和 Guardian 响应。' }}</p>
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
        <h2 class="font-medium">{{ rehearsalOnly ? '恢复材料验证通过' : '确认恢复账户' }}</h2>
        <div v-for="wallet in preview.wallets" :key="wallet.name" class="rounded-lg border p-3">
          <div class="font-medium">{{ wallet.name }}</div>
          <div class="text-sm text-muted-foreground">{{ wallet.account_count }} 个子账户</div>
          <div class="text-xs mt-1">DID：{{ wallet.dids.join('、') }}</div>
        </div>
        <template v-if="rehearsalOnly">
          <p class="text-sm text-muted-foreground">已成功解密并读取备份。请核对钱包与子账户是否完整；本次只读演练不会更新配置时的演练时间。</p>
          <Button class="w-full" :disabled="busy" @click="cancelRecovery">完成演练并返回</Button>
        </template>
        <template v-else>
          <Input v-model="password" type="password" placeholder="设置新的本地钱包密码" autocomplete="new-password" />
          <Input v-model="confirmPassword" type="password" placeholder="再次输入密码" autocomplete="new-password" />
          <Button class="w-full" :disabled="busy" @click="commitRecovery">恢复全部钱包</Button>
        </template>
      </section>

      <section v-else-if="step === 5" class="text-center space-y-3 py-10">
        <Icon icon="lucide:badge-check" class="h-14 w-14 text-green-500 mx-auto" />
        <h2 class="text-xl font-semibold">账户恢复成功</h2>
        <p class="text-sm text-muted-foreground">正在重新载入钱包。</p>
      </section>

      <section v-else-if="step === 6" class="space-y-3">
        <h2 class="font-medium">账户已恢复，界面更新未完成</h2>
        <p class="text-sm text-muted-foreground">钱包数据已保存。重新载入后可使用刚设置的密码解锁，无需再次恢复。</p>
        <Button class="w-full" :disabled="busy" @click="reloadWallet">重新载入钱包</Button>
      </section>

      <Alert v-if="error" variant="destructive"><AlertDescription>{{ error }}</AlertDescription></Alert>
      <Button v-if="step < 5" variant="ghost" class="w-full" :disabled="busy && step === 4" @click="cancelRecovery">取消</Button>
    </div>
  </LayoutScroll>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { Icon } from '@iconify/vue'
import LayoutScroll from '@/components/layout/LayoutScroll.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { Alert, AlertDescription } from '@/components/ui/alert'
import accountSDK, { type AccountRecoverySummary } from '@/utils/accountManagement'

const router = useRouter()
const route = useRoute()
const rehearsalOnly = route.query.mode === 'rehearsal'
const expectedAccountId = ref('')
const busy = ref(false)
const error = ref('')
const step = ref(1)
const locator = ref('')
const loaded = ref<any>(null)
const answers = ref<string[]>([])
const userShare = ref('')
const guardianRequest = ref('')
const guardianResponse = ref('')
const knowledgeReady = ref(false)
const userShareReady = ref(false)
const guardianReady = ref(false)
const companionReady = computed(() => Number(knowledgeReady.value) + Number(userShareReady.value) + Number(guardianReady.value) >= 2)
const preview = ref<AccountRecoverySummary | null>(null)
const password = ref('')
const confirmPassword = ref('')

const sessionId = computed(() => loaded.value?.session_id || '')
let disposed = false

const clearRecovery = () => {
  const id = sessionId.value
  step.value = 1
  loaded.value = null
  answers.value = []
  userShare.value = guardianResponse.value = password.value = confirmPassword.value = ''
  if (id) return accountSDK.abortSession(id).catch(() => undefined)
}
const cancelRecovery = async () => {
  disposed = true
  await clearRecovery()
  await router.push(rehearsalOnly ? '/wallet/setting/account-management' : '/')
}
onBeforeUnmount(() => { disposed = true; void clearRecovery() })
onMounted(async () => {
  if (!rehearsalOnly) return
  try {
    const status = await accountSDK.status()
    if (disposed) return
    expectedAccountId.value = status.account_id || ''
    locator.value = status.public_locator || ''
    if (!expectedAccountId.value || !locator.value) error.value = '当前账户尚未配置恢复材料'
  } catch (e: any) { if (!disposed) error.value = e?.message || '读取当前恢复配置失败' }
})

const run = async (task: () => Promise<void>) => {
  busy.value = true
  error.value = ''
  try { await task() } catch (e: any) { error.value = e?.message || '恢复失败' } finally { busy.value = false }
}

const loadRecovery = () => run(async () => {
  const result = await accountSDK.loadRecovery(locator.value.trim())
  if (disposed) {
    await accountSDK.abortSession(result.session_id)
    return
  }
  loaded.value = result
  answers.value = loaded.value.questions.map(() => '')
  step.value = 2
})

const recoverKnowledge = () => run(async () => {
  const attempts = answers.value.map((answer, index) => ({ question_id: loaded.value.questions[index].id, answer })).filter(item => item.answer)
  if (attempts.length < 2) throw new Error('至少回答两个问题')
  await accountSDK.recoverKnowledge(sessionId.value, attempts)
  knowledgeReady.value = true
  step.value = 3
})

const acceptUserShare = () => run(async () => {
  await accountSDK.setUserShare(sessionId.value, userShare.value.trim())
  userShareReady.value = true
})

const createGuardianRequest = () => run(async () => {
  guardianRequest.value = (await accountSDK.createGuardianRequest(sessionId.value)).request
})

const acceptGuardianResponse = () => run(async () => {
  await accountSDK.consumeGuardianResponse(sessionId.value, guardianResponse.value.trim())
  guardianReady.value = true
})

const previewRecovery = () => run(async () => {
  const summary = (await accountSDK.previewRecovery(sessionId.value)).summary
  if (rehearsalOnly && (!expectedAccountId.value || summary.account_id !== expectedAccountId.value)) {
    throw new Error('恢复码不属于当前账户，请使用当前账户的恢复材料演练')
  }
  preview.value = summary
  step.value = 4
})

const commitRecovery = () => run(async () => {
  if (rehearsalOnly) throw new Error('只读演练不能提交恢复')
  if (password.value.length < 6 || password.value !== confirmPassword.value) throw new Error('密码至少 6 个字符且两次输入必须一致')
	await accountSDK.commitRecovery(sessionId.value, password.value)
  // The SDK has consumed the session and committed the authoritative database.
  // Every subsequent failure is UI reconciliation; never offer commit again.
  step.value = 6
  loaded.value = null
  answers.value = []
  userShare.value = guardianResponse.value = password.value = confirmPassword.value = ''
  // The next unlock reads catalog and persisted selection from the SDK.
  step.value = 5
  setTimeout(reloadWallet, 500)
})

const reloadWallet = () => {
  window.location.hash = '#/unlock'
  window.location.reload()
}

const copyText = async (value: string) => {
  if (value) await navigator.clipboard.writeText(value)
}
</script>
