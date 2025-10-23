<template>
  <div class="layout-container min-h-screen flex items-center justify-center p-4 pb-36">
    <div class="max-w-md w-full space-y-8">
      <div class="text-center">
        <div class="flex flex-col items-center justify-center gap-2 mb-6">
          <img src="@/assets/sat20-logo.svg" alt="ORDX" class="w-14 h-14 mb-2" />
          <h1 class="text-2xl font-semibold mb-2 text-center">{{ $t('unlock.title') }}</h1>
        </div>
        <!-- <p class="text-gray-600 dark:text-gray-400 mb-4">
          {{ $t('unlock.subtitle') }}
        </p> -->
      </div>
      <div>
        <!-- 优先显示生物识别解锁 -->
        <div v-if="showBiometricButton && !showPasswordInput" class="space-y-6 mb-2 p-2">
          <div class="text-center mb-6">
            <p class="text-gray-600 dark:text-gray-400 mb-4">
              {{ t('unlock.biometricPrompt') }}
            </p>
          </div>

          <Button
            type="button"
            @click="performBiometricUnlock"
            :disabled="biometricLoading"
            variant="default"
            size="lg"
            class="w-full"
          >
            <Icon
              v-if="!biometricLoading"
              :inline="true"
              class="mr-2 h-4 w-4"
              icon="mdi:fingerprint"
            />
            <Icon
              v-else
              :inline="true"
              class="mr-2 h-4 w-4 animate-spin"
              icon="mdi:loading"
            />
            {{ biometricButtonText }}
          </Button>

          <Button
            type="button"
            @click="showPasswordInput = true"
            variant="ghost"
            size="sm"
            class="w-full text-gray-500 hover:text-gray-700"
          >
            {{ t('unlock.usePasswordInstead') }}
          </Button>
        </div>

        <!-- 密码输入界面 -->
        <form v-else @submit="onSubmit" class="space-y-6 mb-2 p-2">
          <FormField v-slot="{ componentField }" name="password">
            <FormItem>
              <FormLabel class="text-gray-300 dark:text-gray-200">{{ $t('unlock.enterPassword') }}</FormLabel>
              <div class="relative">
                <FormControl>
                  <Input
                    :type="showPassword ? 'text' : 'password'"
                    class="h-12"
                    :placeholder="$t('unlock.passwordPlaceholder')"
                    v-bind="componentField"
                    autofocus
                  >
                  </Input>
                </FormControl>
                <button
                  type="button"
                  @click="showPassword = !showPassword"
                  size="sm"
                  class="absolute right-3 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
                >
                  <Icon
                    v-if="showPassword"
                    :inline="true"
                    icon="mdi:eye"
                    class="h-4 w-4 opacity-50"
                  />
                  <Icon
                    v-else
                    :inline="true"
                    icon="mdi:eye-off"
                    class="h-4 w-4 opacity-50"
                  />
                </button>
              </div>
              <FormMessage class="text-red-500"/>
            </FormItem>
          </FormField>

          <div class="grid grid-cols-1 gap-2">
            <Button type="submit" :disabled="loading" size="lg">
              <Icon
                v-if="!loading"
                :inline="true"
                class="mr-2 h-4 w-4"
                icon="mdi:lock-open"
              />
              <Icon
                v-else
                :inline="true"
                class="mr-2 h-4 w-4 animate-spin"
                icon="mdi:loading"
              />
              {{ t('unlock.unlockButton') }}
            </Button>

            <!-- 如果支持生物识别，显示切换按钮 -->
            <Button
              v-if="showBiometricButton"
              type="button"
              @click="showPasswordInput = false"
              variant="outline"
              size="lg"
              class="mt-2"
            >
              <Icon
                :inline="true"
                class="mr-2 h-4 w-4"
                icon="mdi:fingerprint"
              />
              {{ biometricButtonText }}
            </Button>
          </div>
        </form>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { useToast } from '@/components/ui/toast-new'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { useForm } from 'vee-validate'
import { Icon } from '@iconify/vue'
import { toTypedSchema } from '@vee-validate/zod'
import * as z from 'zod'
import {
  FormField,
  FormItem,
  FormLabel,
  FormControl,
  FormMessage,
} from '@/components/ui/form'

import { useWalletStore } from '@/store'
import walletManager from '@/utils/sat20'
import { unlockPasswordSchema } from '@/utils/validation'
import { hashPassword } from '@/utils/crypto'
import { useI18n } from 'vue-i18n'
import { biometricService } from '@/utils/biometric'
import { biometricCredentialManager } from '@/utils/biometricCredentials'

const formSchema = toTypedSchema(unlockPasswordSchema)

const form = useForm({
  validationSchema: formSchema,
})

const walletStore = useWalletStore()
const router = useRouter()
const route = useRoute()
const { toast } = useToast()
const { t } = useI18n()
const loading = ref(false)
const biometricLoading = ref(false)
const showBiometricButton = ref(false)
const biometricButtonText = ref(t('unlock.useBiometricUnlock'))
const showPasswordInput = ref(false)

const showPassword = ref(false)

const showToast = (
  variant: 'default' | 'destructive' | 'success',
  title: string,
  description: string | Error
) => {
  console.log('showToast called with:', { variant, title, description })
  toast({
    variant,
    title,
    description:
      typeof description === 'string' ? description : description.message,
  })
}

// 测试函数
// 检查生物识别支持
const checkBiometricSupport = async () => {
  try {
    const supportResult = await biometricService.checkBiometricSupport()
    console.log('生物识别支持检查结果:', supportResult)

    if (supportResult.supported && supportResult.available) {
      // 检查是否有活跃的生物识别凭据
      const hasCredentials = biometricCredentialManager.hasActiveCredentials()
      console.log('是否有活跃的生物识别凭据:', hasCredentials)

      if (hasCredentials) {
        showBiometricButton.value = true
        // 根据生物识别类型设置按钮文本
        const biometryType = supportResult.biometryType
        if (biometryType === 'faceID') {
          biometricButtonText.value = t('unlock.useFaceID')
        } else if (biometryType === 'touchID') {
          biometricButtonText.value = t('unlock.useTouchID')
        } else {
          biometricButtonText.value = t('unlock.useFingerprint')
        }
      }
    }
  } catch (error) {
    console.warn('检查生物识别支持失败:', error)
  }
}

// 生物识别解锁
const performBiometricUnlock = async () => {
  biometricLoading.value = true

  try {
    console.log('开始生物识别验证')

    // 直接进行生物识别验证，不需要输入密码
    const credentialResult = await biometricCredentialManager.verifyCredential()
    console.log('生物识别凭据验证结果:', credentialResult)

    if (!credentialResult.valid) {
      showToast('destructive', t('common.error'), credentialResult.error || t('unlock.biometricAuthFailed'))
      // 生物识别失败，显示密码输入界面
      showPasswordInput.value = true
      biometricLoading.value = false
      return
    }

    // 生物识别成功，使用获取的密码解锁钱包
    const password = credentialResult.password
    if (!password) {
      showToast('destructive', t('common.error'), t('unlock.cannotGetStoredPassword'))
      showPasswordInput.value = true
      biometricLoading.value = false
      return
    }

    const hashedPassword = await hashPassword(password)
    console.log('生物识别验证成功，开始解锁钱包')

    const [err, result] = await walletStore.unlockWallet(hashedPassword)
    console.log('钱包解锁结果:', { err, result })

    if (!err && result) {
      console.log('生物识别解锁成功，准备跳转')
      const redirectPath = route.query.redirect as string
      router.push(redirectPath || '/wallet')
      showToast('success', t('unlock.biometricUnlockSuccess'), t('unlock.biometricVerifySuccess'))
    } else if (err) {
      console.log('钱包解锁失败:', err)
      const errorMessage = err instanceof Error ? err.message : String(err)
      let localizedMessage = t('unlock.unlockFailed')

      if (errorMessage.includes('invalid password') || errorMessage.includes('密码错误')) {
        localizedMessage = t('unlock.invalidPassword')
        showToast('destructive', t('common.error'), localizedMessage)
        // 密码错误，显示密码输入界面
        showPasswordInput.value = true
      } else {
        localizedMessage = errorMessage
        showToast('destructive', t('common.error'), localizedMessage)
        showPasswordInput.value = true
      }
    } else {
      showToast('destructive', t('common.error'), t('unlock.unlockFailed'))
      showPasswordInput.value = true
    }
  } catch (error) {
    console.error('生物识别解锁失败:', error)
    showToast('destructive', t('common.error'), error instanceof Error ? error.message : t('unlock.biometricUnlockFailed'))
    // 发生异常，显示密码输入界面
    showPasswordInput.value = true
  } finally {
    biometricLoading.value = false
  }
}


const testToast = () => {
  console.log('测试 toast 被调用')
  showToast('destructive', t('common.error'), t('unlock.invalidPassword'))
}


const onSubmit = form.handleSubmit(async (values) => {
  loading.value = true

  // Hash the password using the imported function
  const hashedPassword = await hashPassword(values.password)

  console.log('开始解锁，密码哈希值:', hashedPassword.substring(0, 20) + '...')

  const [err, result] = await walletStore.unlockWallet(hashedPassword)

  console.log('解锁结果:', { err, result })

  if (!err && result) {
    console.log('解锁成功，准备跳转')

    // 如果有生物识别凭据但没有存储密码，则存储当前密码
    if (showBiometricButton.value && !biometricCredentialManager.getStoredPassword()) {
      await biometricCredentialManager.storePassword(values.password)
      console.log('密码已存储，下次可使用生物识别解锁')
    }

    const redirectPath = route.query.redirect as string
    router.push(redirectPath || '/wallet')
  } else if (err) {
    console.log('解锁失败，错误对象:', err)
    // 使用本地化的错误消息
    const errorMessage = err instanceof Error ? err.message : String(err)
    console.log('错误消息:', errorMessage)
    let localizedMessage = t('unlock.unlockFailed')

    // 检查是否是密码错误
    if (errorMessage.includes('invalid password') || errorMessage.includes('密码错误')) {
      localizedMessage = t('unlock.invalidPassword')
      console.log('检测到密码错误，使用本地化消息:', localizedMessage)
    } else if (errorMessage.includes('failed') || errorMessage.includes('失败')) {
      localizedMessage = t('unlock.unlockFailed')
      console.log('检测到一般失败，使用本地化消息:', localizedMessage)
    } else {
      // 如果是其他错误，显示原始错误消息
      localizedMessage = errorMessage
      console.log('其他错误，使用原始消息:', localizedMessage)
    }

    console.log('准备显示 toast，参数:', {
      variant: 'destructive',
      title: t('common.error'),
      description: localizedMessage
    })

    showToast('destructive', t('common.error'), localizedMessage)
    loading.value = false
  } else {
    console.log('未知错误：无错误也无结果')
    showToast('destructive', t('common.error'), t('unlock.unlockFailed'))
    loading.value = false
  }
})

// 组件挂载时检查生物识别支持
onMounted(async () => {
  console.log('Unlock页面已挂载，检查生物识别支持')
  await checkBiometricSupport()

  // 如果支持生物识别且有存储的密码，默认显示生物识别界面
  if (showBiometricButton.value && biometricCredentialManager.getStoredPassword()) {
    showPasswordInput.value = false
  } else {
    showPasswordInput.value = true
  }
})

// const deleteWallet = async () => {
//   // localStorage.clear()
//   // location.href = '/'
// }
</script>
