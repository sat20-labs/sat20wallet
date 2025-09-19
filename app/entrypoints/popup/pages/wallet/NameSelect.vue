<template>
  <LayoutSecond title="Name Select">
    <div class="space-y-6">
      <!-- 页面标题 -->
      <div class="text-center">
        <p class="text-muted-foreground mt-2">{{ $t('nameSelect.subtitle') }}</p>
      </div>

      <!-- 名字选择区域 -->
      <Card>
        <CardHeader>
          <CardTitle>{{ $t('nameSelect.selectName') }}</CardTitle>
          <CardDescription>{{ $t('nameSelect.description') }}</CardDescription>
        </CardHeader>
        <CardContent class="space-y-4">
          <!-- 加载状态 -->
          <div v-if="isLoadingNames" class="flex items-center justify-center py-8">
            <div class="flex items-center space-x-2">
              <div class="animate-spin rounded-full h-4 w-4 border-b-2 border-primary"></div>
              <span class="text-sm text-muted-foreground">{{ $t('nameSelect.loading') }}</span>
            </div>
          </div>

          <!-- 错误状态 -->
          <Alert v-else-if="nameError" variant="destructive">
            <Icon icon="lucide:alert-circle" class="h-4 w-4" />
            <AlertTitle>{{ $t('nameSelect.errorTitle') }}</AlertTitle>
            <AlertDescription>{{ $t('nameSelect.errorDescription') }}</AlertDescription>
          </Alert>

          <!-- 名字列表 -->
          <div v-else-if="nameList && nameList.length > 0" class="space-y-3">
            <Label for="name-select">{{ $t('nameSelect.chooseName') }}</Label>
            <Select v-model="selectedName">
              <SelectTrigger id="name-select">
                <SelectValue :placeholder="$t('nameSelect.placeholder')" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem 
                  v-for="nameItem in nameList" 
                  :key="nameItem.id" 
                  :value="nameItem.name"
                >
                  {{ nameItem.name }}
                </SelectItem>
              </SelectContent>
            </Select>
          </div>

          <!-- 无名字状态 -->
          <div v-else class="text-center py-8">
            <div class="text-muted-foreground">
              <Icon icon="lucide:user-x" class="h-12 w-12 mx-auto mb-4 opacity-50" />
              <p>{{ $t('nameSelect.noNames') }}</p>
            </div>
          </div>

          <!-- 当前选择的名字 -->
          <div v-if="currentName" class="pt-4 border-t">
            <Label class="text-sm text-muted-foreground">{{ $t('nameSelect.currentName') }}</Label>
            <div class="flex items-center space-x-2 mt-1">
              <span class="text-sm font-medium">{{ currentName }}</span>
              <Button 
                v-if="currentName" 
                variant="outline" 
                size="sm"
                @click="clearCurrentName"
              >
                {{ $t('nameSelect.clear') }}
              </Button>
            </div>
          </div>
        </CardContent>
      </Card>

      <!-- 操作按钮 -->
      <div class="flex space-x-3">
        <Button 
          variant="outline" 
          class="flex-1"
          @click="goBack"
        >
          {{ $t('nameSelect.cancel') }}
        </Button>
        <Button 
          class="flex-1"
          :disabled="!selectedName || isLoading"
          @click="saveName"
        >
          <div v-if="isLoading" class="flex items-center space-x-2">
            <div class="animate-spin rounded-full h-4 w-4 border-b-2 border-white"></div>
            <span>{{ $t('nameSelect.saving') }}</span>
          </div>
          <span v-else>{{ $t('nameSelect.save') }}</span>
        </Button>
      </div>
    </div>
  </LayoutSecond>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { useToast } from '@/components/ui/toast-new'
import { Icon } from '@iconify/vue'
import LayoutHome from '@/components/layout/LayoutHome.vue'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Label } from '@/components/ui/label'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { useNameManager } from '@/composables/useNameManager'
import { useWalletStore } from '@/store'
import { storeToRefs } from 'pinia'
import LayoutSecond from '@/components/layout/LayoutSecond.vue'

// 路由和工具
const router = useRouter()
const { toast } = useToast()

// 钱包数据
const walletStore = useWalletStore()
const { address } = storeToRefs(walletStore)

// 名字管理
const {
  currentName,
  nameList,
  isLoadingNames,
  nameError,
  setCurrentAddress,
  setCurrentName,
  clearName,
  validateAndCleanName,
} = useNameManager()

// 状态
const selectedName = ref<string>('')
const isLoading = ref(false)

// 清空当前名字
const clearCurrentName = async () => {
  if (!address.value) return
  
  try {
    await clearName(address.value)
    selectedName.value = ''
    toast({
      title: 'Success',
      description: 'Name cleared successfully',
      variant: 'success'
    })
  } catch (error) {
    toast({
      title: 'Error',
      description: 'Failed to clear name',
      variant: 'destructive',
    })
  }
}

// 保存名字
const saveName = async () => {
  if (!selectedName.value || !address.value) return

  isLoading.value = true
  try {
    await setCurrentName(address.value, selectedName.value)
    toast({
      title: 'Success',
      description: 'Name saved successfully',
      variant: 'success'
    })
    goBack()
  } catch (error) {
    toast({
      title: 'Error',
      description: 'Failed to save name',
      variant: 'destructive',
    })
  } finally {
    isLoading.value = false
  }
}

// 返回上一页
const goBack = () => {
  router.back()
}

// 初始化
onMounted(async () => {
  if (!address.value) {
    toast({
      title: 'Error',
      description: 'No address available',
      variant: 'destructive',
    })
    goBack()
    return
  }

  // 设置当前地址并加载数据
  await setCurrentAddress(address.value)
  
  // 校验并清理无效名字
  await validateAndCleanName(address.value)
  
  // 设置当前选择的名字
  selectedName.value = currentName.value
})
</script> 