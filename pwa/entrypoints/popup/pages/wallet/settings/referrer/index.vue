<template>
  <LayoutSecond :title="$t('referrerManagement.registerAsReferrer')" class="max-w-2xl mx-auto">
    <div class="max-w-xl mx-auto my-8">
      <div class="flex flex-col gap-4">
        <div class="mb-4">
          <p class="flex justify-start items-center text-sm text-muted-foreground">
            <Icon icon="lucide:badge-info" class="w-12 h-12 mr-2 text-green-600" />
            {{ $t('referrerManagement.referrerRegistrationDescription') }}
          </p>
        </div>
        <div class="grid w-full items-center gap-1.5">
          <Label for="name">{{ $t('referrerManagement.referrerName') }}</Label>
          <Select v-model="name" :disabled="isLoadingNames || !canRegister">
            <SelectTrigger>
              <SelectValue :placeholder="$t('referrerManagement.referrerNamePlaceholder')" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem v-if="nameList && nameList.length > 0" v-for="nameItem in nameList" :key="nameItem.id" :value="nameItem.name">
                {{ nameItem.name }}
              </SelectItem>
              <SelectItem v-else value="no-names" disabled>
                {{ isLoadingNames ? t('settingsUi.loading') : t('settingsUi.noNames') }}
              </SelectItem>
            </SelectContent>
          </Select>
        </div>
        <Alert v-if="registeredNames.length" variant="default">
          <AlertTitle>{{ $t('referrerManagement.alreadyRegistered') }}</AlertTitle>
          <AlertDescription>{{ registeredNames.join(', ') }}</AlertDescription>
        </Alert>
        <p v-else-if="!isCheckingRegistration && !registrationKnown" role="alert" class="text-sm text-red-400">
          {{ $t('referrerManagement.statusLookupFailed') }}
        </p>
        <!-- 当nameList为空且不在加载状态时显示提示 -->
        <Alert v-if="!isLoadingNames && (!nameList || nameList.length === 0)" variant="default">
          <AlertTitle>{{ t('approvalUi.info') }}</AlertTitle>
          <AlertDescription>
            {{ t('settingsUi.registerNameFirst') }}
          </AlertDescription>
        </Alert>
        <div class="grid w-full items-center gap-1.5">
          <Label for="feeRate">{{ $t('referrerManagement.gasFeeRate') }}</Label>
          <Input id="feeRate" v-model="btcFeeRate" type="number" min="0" max="100"
            :placeholder="$t('referrerManagement.gasFeeRatePlaceHolder')" />
        </div>
        <Button :aria-label="$t('referrerManagement.registerAsReferrer')" @click="onRegister" :loading="isLoading"
          :disabled="!canRegister || isLoadingNames || !name">
          {{ $t('referrerManagement.registerAsReferrer') }}
        </Button>
        <Alert v-if="resultMsg" :variant="resultSuccess ? 'default' : 'destructive'">
          <AlertTitle>{{ resultSuccess ? $t('referrerManagement.RegistrationSuccess') :
            $t('referrerManagement.RegistrationFailure') }}</AlertTitle>
          <AlertDescription class="break-all">{{ resultMsg }}</AlertDescription>
        </Alert>
      </div>
      <Dialog v-model:open="showConfirm">
        <DialogContent>
          <DialogHeader>
            <DialogTitle><span class="flex justify-center items-center text-zinc-300 text-lg">
                <Icon icon="lucide:message-circle-question-mark" class="w-12 h-12 mr-1 text-red-500 break-all" />{{
                  $t('referrerManagement.confirmRegisterDescription') }}
              </span></DialogTitle>
            <hr class="my-2 border-zinc-950" />
            <DialogDescription class="text-zinc-300">
              <p class="py-1 mt-4">
                <span class="text-zinc-500 mr-4">{{ $t('referrerManagement.referrerName') }} :</span> <span
                  class="text-zinc-300 break-all">{{ name }}</span>
              </p>
              <p class="py-1">
                <span class="text-zinc-500 mr-4">{{ $t('referrerManagement.gasFeeRate') }} :</span> <span
                  class="text-zinc-300 mr-2 break-all">{{ btcFeeRate }} </span> sats/Vb
              </p>
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <div class="flex justify-end gap-3">
              <Button @click="confirmRegister" :loading="isLoading" :disabled="!canRegister" class="w-36">{{ $t('referrerManagement.confirm')
                }}</Button>
              <Button variant="secondary" @click="showConfirm = false" class="w-36">{{ $t('referrerManagement.cancel')
                }}</Button>
            </div>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  </LayoutSecond>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
const { t } = useI18n()
import { computed, ref, watch } from 'vue'
import { storeToRefs } from 'pinia'
import sat20 from '@/utils/sat20'
import LayoutSecond from '@/components/layout/LayoutSecond.vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Icon } from '@iconify/vue'
import { Alert, AlertTitle, AlertDescription } from '@/components/ui/alert'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogFooter } from '@/components/ui/dialog'
import { useWalletStore } from '@/store/wallet'
import { useNameManager } from '@/composables/useNameManager'
import { useReferrerManager } from '@/composables/useReferrerManager'
import { useGlobalStore } from '@/store/global'
import { getConfig } from '@/config/wasm'

const walletStore = useWalletStore()
const { nameList, isLoadingNames } = useNameManager()
const { addLocalReferrerName } = useReferrerManager()
const isLoading = ref(false)
const showConfirm = ref(false)
const resultMsg = ref('')
const resultSuccess = ref(false)
const name = ref('')
const registeredNames = ref<string[]>([])
const registrationKnown = ref(false)
const isCheckingRegistration = ref(false)
const canRegister = computed(() => registrationKnown.value && !isCheckingRegistration.value && !isLoading.value && !registeredNames.value.length)

const { btcFeeRate, address, network } = storeToRefs(walletStore)
const { env } = storeToRefs(useGlobalStore())
const statusContext = () => [env.value, network.value, address.value].join('|')

async function loadRegistrationStatus() {
  const context = statusContext()
  registrationKnown.value = false
  isCheckingRegistration.value = true
  try {
    if (!address.value) throw new Error(t('settingsUi.walletAddressUnavailable'))
    const config = getConfig(env.value, network.value)
    const serverPubKey = (config.Peers[1] || config.Peers[0] || '').split('@')[1]
    if (!serverPubKey) throw new Error(t('settingsUi.referrerUnavailable'))
    const [err, res] = await sat20.getAllRegisteredReferrerName(serverPubKey)
    if (context !== statusContext()) return false
    if (err || !res || !('names' in res) || (res.names !== null &&
      (!Array.isArray(res.names) || !res.names.every(name => typeof name === 'string' && name.trim())))) {
      throw err || new Error(t('settingsUi.unverifiedRegistration'))
    }
    registeredNames.value = res.names || []
    registrationKnown.value = true
    return true
  } catch (error) {
    console.error('加载推荐人注册状态失败:', error)
    return false
  } finally {
    if (context === statusContext()) isCheckingRegistration.value = false
  }
}

function onRegister() {
  if (!canRegister.value || isLoadingNames.value) return
  if (!name.value) {
    resultMsg.value = t('settingsUi.completeInfo')
    resultSuccess.value = false
    return
  }
  showConfirm.value = true
}

async function confirmRegister() {
  if (!canRegister.value || !name.value) return
  const context = statusContext()
  const submittedName = name.value
  if (!await loadRegistrationStatus() || !canRegister.value || context !== statusContext() || name.value !== submittedName) return
  isLoading.value = true
  resultMsg.value = ''
  showConfirm.value = false
  try {
    const [err, res] = await sat20.registerAsReferrer(submittedName, btcFeeRate.value)
    if (context !== statusContext()) return
    if (err) {
      resultMsg.value = err.message || t('settingsUi.registerFailed')
      resultSuccess.value = false
    } else {
      resultMsg.value = t('settingsUi.registerSuccess')
      resultSuccess.value = true
      registeredNames.value = [submittedName]
      // 使用推荐人管理器保存注册的name到本地存储
      if (address.value) {
        await addLocalReferrerName(address.value, submittedName)
      }
      // 清空输入
      name.value = ''
    }
  } catch (e: any) {
    resultMsg.value = e.message || t('settingsUi.unknownError')
    resultSuccess.value = false
  } finally {
    isLoading.value = false
  }
}

watch([address, network, env], () => {
  showConfirm.value = false
  name.value = ''
  registeredNames.value = []
  registrationKnown.value = false
  loadRegistrationStatus()
}, { immediate: true })
</script>
