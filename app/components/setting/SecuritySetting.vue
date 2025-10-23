<template>
  <div class="w-full px-2  bg-zinc-700/40 rounded-lg">
    <button @click="isExpanded = !isExpanded"
      class="flex items-center justify-between w-full p-2 text-left text-primary font-medium rounded-lg">
      <div>
        <h2 class="text-lg font-bold text-zinc-200">{{ $t('securitySetting.title') }}</h2>
        <p class="text-muted-foreground">{{ $t('securitySetting.subtitle') }}</p>
      </div>
      <div class="mr-2">
        <Icon v-if="isExpanded" icon="lucide:chevrons-up" class="mr-2 h-4 w-4" />
        <Icon v-else icon="lucide:chevrons-down" class="mr-2 h-4 w-4" />
      </div>
    </button>
    <div v-if="isExpanded" class="space-y-6 px-2 py-4">
      <div class="flex items-center justify-between border-t border-zinc-900/30 pt-4">
        <div class="space-y-0.5">
          <Label>{{ $t('securitySetting.autoLockTimer') }}</Label>
          <div class="text-sm text-muted-foreground">
            {{ $t('securitySetting.autoLockDescription') }}
          </div>
        </div>
        <Select v-model="autoLockTime" default-value="5">
          <SelectTrigger class="w-[180px] bg-gray-900/30">
            <SelectValue :placeholder="$t('securitySetting.selectTime')" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="1">{{ $t('securitySetting.oneMinute') }}</SelectItem>
            <SelectItem value="5">{{ $t('securitySetting.fiveMinutes') }}</SelectItem>
            <SelectItem value="15">{{ $t('securitySetting.fifteenMinutes') }}</SelectItem>
            <SelectItem value="30">{{ $t('securitySetting.thirtyMinutes') }}</SelectItem>
          </SelectContent>
        </Select>
      </div>

      <div class="flex items-center justify-between border-t border-zinc-900/30 pt-4">
        <div class="space-y-0.5 mb-4">
          <Label>{{ $t('securitySetting.hideBalance') }}</Label>
          <div class="text-sm text-muted-foreground">
            {{ $t('securitySetting.hideBalanceDescription') }}
          </div>
        </div>
        <Button
          variant="outline"
          size="sm"
          @click="hideBalance = !hideBalance"
          class="w-24"
        >
          {{ hideBalance ? $t('securitySetting.showBalance') : $t('securitySetting.hideBalance') }}
        </Button>
      </div>

      <!-- 指纹识别设置 -->
      <div class="border-t border-zinc-900/30 pt-4">
        <div class="space-y-4">
          <div class="flex items-center justify-between">
            <div class="space-y-0.5">
              <Label>{{ $t('securitySetting.biometricUnlock') }}</Label>
              <div class="text-sm text-muted-foreground">
                {{ $t('securitySetting.biometricUnlockDescription') }}
              </div>
            </div>
            <Button
              variant="outline"
              size="sm"
              @click="handleBiometricToggle(!biometricEnabled)"
              :disabled="biometricLoading"
              class="w-24"
            >
              <Icon v-if="biometricLoading" icon="mdi:loading" class="mr-1 h-4 w-4 animate-spin" />
              <Icon v-else-if="biometricEnabled" icon="mdi:fingerprint" class="mr-1 h-4 w-4" />
              <Icon v-else icon="mdi:fingerprint-off" class="mr-1 h-4 w-4" />
              {{ biometricLoading ? $t('common.processing') : (biometricEnabled ? $t('common.disable') : $t('common.enable')) }}
            </Button>
          </div>

          <!-- 生物识别状态信息 -->
          <div v-if="biometricStatus.supported && biometricStatus.available" class="text-sm text-muted-foreground bg-zinc-800/50 p-3 rounded-lg mb-4">
            <div v-if="hasCredentials" class="text-green-400">
              <Icon icon="mdi:fingerprint" class="inline h-4 w-4 mr-2" />
              {{ biometricStatus.biometryType === 'faceID' ? t('securitySetting.faceAuth') :
                 biometricStatus.biometryType === 'touchID' ? t('securitySetting.fingerprintAuth') :
                 biometricStatus.biometryType === 'fingerprint' ? t('securitySetting.fingerprintAuth') :
                 t('securitySetting.biometricAuth') }} {{ $t('securitySetting.deviceSupportsBiometric') }}
            </div>
            <div v-else class="text-yellow-400">
              <Icon icon="mdi:alert" class="inline h-4 w-4 mr-2" />
              {{ $t('securitySetting.createCredentialPrompt', { biometryType: t('securitySetting.biometricAuth') }) }}
            </div>
          </div>

          <div v-else-if="biometricStatus.supported && !biometricStatus.available" class="text-sm text-yellow-400 bg-zinc-800/50 p-3 rounded-lg mb-4">
            <Icon icon="mdi:alert" class="inline h-4 w-4 mr-2" />
            {{ biometricStatus.error || t('securitySetting.biometricUnavailable') }}
          </div>

        <!-- 指纹识别操作按钮 -->
          <div v-if="biometricEnabled" class="space-y-2">
            <Button
              v-if="!hasCredentials"
              @click="createBiometricCredential"
              :disabled="biometricLoading"
              variant="outline"
              class="h-10 w-full"
            >
              <Icon v-if="!biometricLoading" icon="mdi:fingerprint-plus" class="mr-2 h-4 w-4" />
              <Icon v-else icon="mdi:loading" class="mr-2 h-4 w-4 animate-spin" />
              {{ $t('securitySetting.createBiometricCredential') }}
            </Button>

            <Button
              v-if="hasCredentials"
              @click="deleteBiometricCredential"
              :disabled="biometricLoading"
              variant="destructive"
              class="h-10 w-full"
            >
              <Icon v-if="!biometricLoading" icon="mdi:fingerprint-remove" class="mr-2 h-4 w-4" />
              <Icon v-else icon="mdi:loading" class="mr-2 h-4 w-4 animate-spin" />
              {{ $t('securitySetting.deleteBiometricCredential') }}
            </Button>
          </div>
        </div>
      </div>

      <div class="flex flex-col space-y-2 border-t border-zinc-900/30 pt-4">
        <Button as-child class="h-10 w-full">
          <RouterLink to="/wallet/setting/phrase" class="w-full">
            <Icon icon="lucide:eye-off" class="mr-2 h-4 w-4" /> {{ $t('securitySetting.showPhrase') }}
          </RouterLink>
        </Button>
        <Button as-child class="h-10 w-full">
          <RouterLink to="/wallet/setting/publickey" class="w-full">
            Show Public Key
          </RouterLink>
        </Button>
        <Button as-child class="h-10 w-full">
          <RouterLink to="/wallet/setting/password" class="w-full">
            Password
          </RouterLink>
        </Button>
      </div>

      <!-- 提示对话框 -->
      <Alert
        :open="alertDialog.open"
        :message="alertDialog.message"
        :type="alertDialog.type"
        @update:open="alertDialog.open = $event"
      >
      </Alert>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, watch, onActivated } from 'vue'
import { useI18n } from 'vue-i18n'

import { Label } from '@/components/ui/label'
import { Button } from '@/components/ui/button'
import { Alert } from '@/components/ui/alert-dialog'
import { Icon } from '@iconify/vue'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { biometricService } from '@/utils/biometric'
import { biometricCredentialManager } from '@/utils/biometricCredentials'
import { Storage } from '@/lib/storage-adapter'

const { t } = useI18n()

const isExpanded = ref(false)
const autoLockTime = ref('5')
const hideBalance = ref(false)

// 生物识别相关状态
const biometricEnabled = ref(false)
const biometricLoading = ref(false)
const hasCredentials = ref(false)

// 提示对话框状态
const alertDialog = ref({
  open: false,
  message: '',
  type: 'info' as 'info' | 'warning' | 'error' | 'success',
})

const biometricStatus = ref<{
  supported: boolean
  available: boolean
  biometryType?: string
  error?: string
}>({
  supported: false,
  available: false,
  biometryType: '',
  error: ''
})

// 显示提示对话框
const showAlert = (message: string, type: 'info' | 'warning' | 'error' | 'success' = 'info') => {
  alertDialog.value = {
    open: true,
    message,
    type
  }
  setTimeout(() => {
    alertDialog.value.open = false
  }, 3000)
}

// 显示确认对话框 - 使用浏览器原生confirm
const showConfirm = (message: string, type: 'info' | 'warning' | 'danger' = 'warning'): Promise<boolean> => {
  // 使用浏览器原生confirm，简单直接
  const result = confirm(message)
  return Promise.resolve(result)
}

// 检查生物识别支持
const checkBiometricSupport = async () => {
  try {
    const result = await import('@/utils/biometric').then(module => module.biometricService.checkBiometricSupport())
    biometricStatus.value = result
    // 在原生环境中检查是否有活跃凭据来确定启用状态
    if (result.supported) {
      const { biometricCredentialManager } = await import('@/utils/biometricCredentials')
      biometricEnabled.value = biometricCredentialManager.hasActiveCredentials()
    }
  } catch (error) {
    console.warn('检查生物识别支持失败:', error)
    biometricStatus.value = {
      supported: false,
      available: false,
      error: error instanceof Error ? error.message : '未知错误'
    }
  }
}

// 检查凭据状态
const checkCredentialStatus = async () => {
  try {
    const { biometricCredentialManager } = await import('@/utils/biometricCredentials')
    hasCredentials.value = biometricCredentialManager.hasActiveCredentials()
  } catch (error) {
    console.warn('检查凭据状态失败:', error)
    hasCredentials.value = false
  }
}

// 处理生物识别开关切换
const handleBiometricToggle = async (newValue: boolean) => {
  biometricLoading.value = true

  try {
    if (newValue) {
      // 用户尝试开启生物识别

      // 如果没有凭据，直接提示用户创建
      if (!hasCredentials.value) {
        const shouldCreate = await showConfirm(
          t('securitySetting.createCredentialPrompt', { biometryType: '生物识别' })
        )

        if (shouldCreate) {
          const success = await createBiometricCredential()
          if (success) {
            biometricEnabled.value = true
          }
        }
      } else {
        showAlert(t('securitySetting.biometricEnabled'), 'success')
        biometricEnabled.value = true
      }
    } else {
      // 用户关闭生物识别
      const confirmed = await showConfirm(t('securitySetting.disableBiometricConfirm'), 'warning')

      if (confirmed) {
        const credentialsModule = await import('@/utils/biometricCredentials')
        const result = credentialsModule.biometricCredentialManager.clearAllCredentials()
        if (result.success) {
          hasCredentials.value = false
          biometricEnabled.value = false
          showAlert(t('securitySetting.biometricDisabled'), 'success')
        } else {
          showAlert(t('securitySetting.clearBiometricFailed', { error: '未知错误' }), 'error')
        }
      }
    }
  } finally {
    biometricLoading.value = false
  }
}

// 创建生物识别凭据
const createBiometricCredential = async (): Promise<boolean> => {
  try {
    // 从全局存储获取当前密码
    const walletStore = (await import('@/store/wallet')).useWalletStore()

    // 检查钱包是否已解锁
    if (walletStore.locked) {
      showAlert(t('securitySetting.walletLockedError'), 'error')
      return false
    }

    // 获取当前钱包密码（已经是哈希密码）
    const currentPassword = walletStore.password || ''

    if (!currentPassword) {
      showAlert(t('securitySetting.passwordRequiredError'), 'error')
      return false
    }

    console.log('创建生物识别凭据，使用已存储的哈希密码')

    // 导入生物识别凭据管理器
    const { biometricCredentialManager } = await import('@/utils/biometricCredentials')

    // 创建生物识别凭据（传入哈希密码）
    const result = await biometricCredentialManager.createCredential(
      currentPassword,
      'SAT20 钱包生物识别凭据'
    )

    if (result.success) {
      hasCredentials.value = true
      biometricEnabled.value = true
      showAlert(t('securitySetting.createCredentialSuccess'), 'success')
      return true
    } else {
      showAlert(t('securitySetting.createCredentialFailed', { error: result.error || t('securitySetting.unknownError') }), 'error')
      return false
    }
  } catch (error) {
    showAlert(t('securitySetting.createCredentialFailed', { error: error instanceof Error ? error.message : t('securitySetting.unknownError') }), 'error')
    return false
  } finally {
    biometricLoading.value = false
  }
}

// 删除生物识别凭据
const deleteBiometricCredential = async () => {
  biometricLoading.value = true

  try {
    const confirmed = await showConfirm(t('securitySetting.deleteCredentialConfirm'), 'danger')
    if (!confirmed) {
      return
    }

    const credentialsModule = await import('@/utils/biometricCredentials')
    const credentials = credentialsModule.biometricCredentialManager.getActiveCredentials()

    if (credentials.length > 0) {
      const result = credentialsModule.biometricCredentialManager.deleteCredential(credentials[0].id)

      if (result.success) {
        hasCredentials.value = false
        biometricEnabled.value = false
        showAlert(t('securitySetting.deleteCredentialSuccess'), 'success')
      } else {
        showAlert(t('securitySetting.deleteCredentialFailed', { error: '未知错误' }), 'error')
      }
    }
  } catch (error) {
    showAlert(t('securitySetting.deleteCredentialFailed', { error: error instanceof Error ? error.message : t('securitySetting.unknownError') }), 'error')
  } finally {
    biometricLoading.value = false
  }
}

// 组件挂载时检查状态
onMounted(async () => {
  await checkBiometricSupport()
  await checkCredentialStatus()
})

// 组件激活时重新检查状态（处理从其他页面返回时的状态同步）
onActivated(async () => {
  await checkCredentialStatus()
  // 同步 biometricEnabled 状态
  const { biometricCredentialManager } = await import('@/utils/biometricCredentials')
  biometricEnabled.value = biometricCredentialManager.hasActiveCredentials()
})

// 监听存储变化，处理跨标签页状态同步
if (typeof window !== 'undefined' && window.addEventListener) {
  window.addEventListener('storage', async (e) => {
    if (e.key === 'sat20_biometric_credentials') {
      await checkCredentialStatus()
      const { biometricCredentialManager } = await import('@/utils/biometricCredentials')
      biometricEnabled.value = biometricCredentialManager.hasActiveCredentials()
    }
  })
}
</script>