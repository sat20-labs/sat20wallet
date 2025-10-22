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
        <Switch v-model="hideBalance" />
      </div>

      <!-- 生物识别设置 -->
      <div class="border-t border-zinc-900/30 pt-4">
        <div class="space-y-4">
          <div class="flex items-center justify-between">
            <div class="space-y-0.5">
              <Label>生物识别解锁</Label>
              <div class="text-sm text-muted-foreground">
                启用指纹或面容ID解锁钱包
              </div>
            </div>
            <Switch
              v-model="biometricEnabled"
              @change="handleBiometricToggle"
              :disabled="biometricLoading"
            />
          </div>

          <!-- 生物识别状态信息 -->
          <div v-if="biometricStatus.supported" class="text-sm text-muted-foreground bg-zinc-800/50 p-3 rounded">
            <div v-if="biometricStatus.available">
              <Icon icon="mdi:fingerprint" class="inline h-4 w-4 mr-1" />
              {{ biometricStatus.biometryType === 'faceID' ? '面容ID' :
                 biometricStatus.biometryType === 'touchID' ? '触控ID' : '指纹识别' }} 已启用
            </div>
            <div v-else class="text-yellow-400">
              <Icon icon="mdi:alert" class="inline h-4 w-4 mr-1" />
              {{ biometricStatus.error || '生物识别不可用' }}
            </div>
          </div>

          <!-- 生物识别操作按钮 -->
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
              创建生物识别凭据
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
              删除生物识别凭据
            </Button>
          </div>
        </div>
      </div>

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
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Button } from '@/components/ui/button'
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

const isExpanded = ref(false)
const autoLockTime = ref('5')
const hideBalance = ref(false)

// 生物识别相关状态
const biometricEnabled = ref(false)
const biometricLoading = ref(false)
const hasCredentials = ref(false)
const biometricStatus = ref({
  supported: false,
  available: false,
  biometryType: '',
  error: ''
})

// 检查生物识别支持
const checkBiometricSupport = async () => {
  try {
    const result = await biometricService.checkBiometricSupport()
    biometricStatus.value = result
    console.log('生物识别状态检查:', result)

    // 在原生环境中始终显示生物识别设置
    if (biometricService.isNativePlatform) {
      biometricStatus.value.supported = true
    }
  } catch (error) {
    console.warn('检查生物识别支持失败:', error)
    biometricStatus.value.error = error instanceof Error ? error.message : '检查失败'
  }
}

// 检查凭据状态
const checkCredentialStatus = () => {
  hasCredentials.value = biometricCredentialManager.hasActiveCredentials()
  biometricEnabled.value = hasCredentials.value
}

// 处理生物识别开关
const handleBiometricToggle = async () => {
  if (!biometricEnabled.value) {
    // 关闭生物识别，清除所有凭据
    const result = biometricCredentialManager.clearAllCredentials()
    if (result.success) {
      hasCredentials.value = false
      console.log('已清除所有生物识别凭据')
    }
  }
}

// 创建生物识别凭据
const createBiometricCredential = async () => {
  biometricLoading.value = true

  try {
    // 提示用户输入密码
    const password = prompt('请输入钱包密码以创建生物识别凭据:')
    if (!password) {
      return
    }

    const result = await biometricCredentialManager.createCredential(password)
    if (result.success) {
      hasCredentials.value = true
      alert('生物识别凭据创建成功！现在可以使用生物识别解锁钱包。')
    } else {
      alert('创建生物识别凭据失败: ' + result.error)
    }
  } catch (error) {
    console.error('创建生物识别凭据失败:', error)
    alert('创建生物识别凭据失败: ' + (error instanceof Error ? error.message : '未知错误'))
  } finally {
    biometricLoading.value = false
  }
}

// 删除生物识别凭据
const deleteBiometricCredential = async () => {
  biometricLoading.value = true

  try {
    const confirmed = confirm('确定要删除生物识别凭据吗？删除后将无法使用生物识别解锁。')
    if (!confirmed) {
      return
    }

    const credentials = biometricCredentialManager.getActiveCredentials()
    if (credentials.length > 0) {
      const result = biometricCredentialManager.deleteCredential(credentials[0].id)
      if (result.success) {
        hasCredentials.value = false
        biometricEnabled.value = false
        alert('生物识别凭据已删除')
      } else {
        alert('删除生物识别凭据失败: ' + result.error)
      }
    }
  } catch (error) {
    console.error('删除生物识别凭据失败:', error)
    alert('删除生物识别凭据失败: ' + (error instanceof Error ? error.message : '未知错误'))
  } finally {
    biometricLoading.value = false
  }
}

// 组件挂载时检查状态
onMounted(async () => {
  await checkBiometricSupport()
  checkCredentialStatus()
})
</script>