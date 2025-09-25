<template>
  <LayoutSecond :title="$t('subWalletManager.title')" :back="true">

    <main class="flex-1 overflow-y-auto">
      <div class="container max-w-2xl mx-auto p-4 space-y-6">
        <div class="space-y-4">
          <div class="text-sm font-medium text-muted-foreground">
            {{ $t('subWalletManager.currentAccount') }}
          </div>

          <!-- Sub-wallet List -->
          <div class="space-y-2">
            <div v-for="account in accountsWithAddress" :key="account.index"
              class="flex items-center justify-between p-3 rounded-lg border hover:bg-accent/50 transition-colors"
              :class="{ 
                'border-primary/50': account.index === accountIndex,
                'cursor-pointer': !isSwitchingAccount && account.index !== accountIndex,
                'cursor-not-allowed opacity-50': isSwitchingAccount
              }"
              @click="account.index !== accountIndex && !isSwitchingAccount && selectAccount(account)">
              <div class="flex items-center gap-3">
                <div class="w-10 h-10 rounded-full overflow-hidden bg-muted">
                  <div class="w-full h-full flex items-center justify-center">
                    <Icon icon="lucide:user-round" class="w-5 h-5 text-white/60" />
                  </div>
                </div>
                <div>
                  <div class="font-medium flex items-center gap-2 text-white/60">
                    {{ account.displayName }}
                    <Icon v-if="isSwitchingAccount && account.index === accountIndex" icon="lucide:loader-2" class="w-3 h-3 animate-spin" />
                    <Button v-if="account.index === accountIndex && !isSwitchingAccount" variant="ghost" size="icon" class="h-2 w-2"
                      @click.stop="showEditNameDialog(account)">
                      <Icon icon="lucide:pencil" class="w-3 h-3" />
                    </Button>
                  </div>
                  <div class="text-sm text-muted-foreground">{{ hideAddress(account.address) }}</div>
                </div>
              </div>
              <div class="flex items-center gap-2">
                <Button variant="ghost" size="icon" :aria-label="$t('subWalletManager.copyAddress')"
                  @click.stop="copyAddress(account.address)" class="hover:text-primary">
                  <Icon icon="lucide:copy" class="w-3 h-3" />
                </Button>
                <Button v-if="account.index !== accountIndex" variant="ghost" size="icon"
                  class="text-destructive hover:text-destructive" @click.stop="confirmDeleteAccount(account)">
                  <Icon icon="lucide:trash-2" class="w-3 h-3" />
                </Button>
              </div>
            </div>
          </div>
        </div>
      </div>
    </main>

    <!-- Bottom Buttons -->
    <footer class="flex-none z-40 border-t">
      <div class="container max-w-2xl mx-auto p-4">
        <div class="flex gap-2">
          <Button class="flex-1 gap-2 h-12 flex items-center w-full" variant="default" @click="showCreateAccountDialog">
            <Icon icon="lucide:plus-circle" class="w-6 h-6 flex-shrink-0" />
            {{ $t('subWalletManager.createNewAccount') }}
          </Button>
        </div>
      </div>
    </footer>

    <!-- Create Dialog -->
    <Dialog :open="isCreateAccountDialogOpen" @update:open="isCreateAccountDialogOpen = $event">
      <DialogContent class="sm:max-w-[425px]">
        <DialogHeader>
          <DialogTitle>{{ $t('subWalletManager.createNewAccount') }}</DialogTitle>
          <DialogDescription>
            <hr class="mb-6 mt-1 border-t-1 border-accent">
            {{ $t('subWalletManager.setupNewAccount') }}
          </DialogDescription>
        </DialogHeader>
        <form @submit.prevent="createAccount" class="space-y-4">
          <div class="space-y-4">
            <div class="space-y-2">
              <Label for="AccountName">{{ $t('subWalletManager.name') }}</Label>
              <Input id="AccountName" v-model="newAccountName" :placeholder="$t('subWalletManager.enterAccountName')" />
            </div>
          </div>
          <DialogFooter>
            <Button type="submit" :disabled="isCreating" class="h-12">
              {{ $t('subWalletManager.createAccount') }}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>


    <!-- Delete Dialog -->
    <Dialog :open="isDeleteDialogOpen" @update:open="isDeleteDialogOpen = $event">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{{ $t('subWalletManager.deleteAccount') }}</DialogTitle>
          <DialogDescription>
            <hr class="mb-6 mt-1 border-t-1 border-accent">
            {{ $t('subWalletManager.confirmDeleteAccount') }}
          </DialogDescription>
        </DialogHeader>
        <div class="space-y-4">
          <Alert variant="destructive">
            <Icon icon="lucide:alert-triangle" class="w-4 h-4" />
            <AlertDescription>
              {{ $t('subWalletManager.backupRecoveryPhrase') }}
            </AlertDescription>
          </Alert>
        </div>
        <DialogFooter>
          <Button variant="secondary" @click="isDeleteDialogOpen = false" class="h-12 mb-4">
            {{ $t('subWalletManager.cancel') }}
          </Button>
          <Button variant="default" @click="deleteAccount" :disabled="isDeleting" class="h-12 mb-4">
            {{ $t('subWalletManager.delete') }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </LayoutSecond>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useRouter } from 'vue-router'
import { Icon } from '@iconify/vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Alert, AlertDescription } from '@/components/ui/alert'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { useToast } from '@/components/ui/toast-new'
import { useWalletStore } from '@/store/wallet'
import type { WalletAccount } from '@/types'
import { hideAddress } from '@/utils'
import LayoutSecond from '@/components/layout/LayoutSecond.vue'
import walletManager from '@/utils/sat20'
import { sendAccountsChangedEvent } from '@/lib/utils'
import { useNameManager } from '@/composables/useNameManager'
import { useDebounceFn } from '@vueuse/core'

const router = useRouter()
const { toast } = useToast()
const walletStore = useWalletStore()
const { isSwitchingAccount } = storeToRefs(walletStore)

// 名字管理
const { getCurrentName } = useNameManager()

// State
const isCreateAccountDialogOpen = ref(false)
const isDeleteDialogOpen = ref(false)
const newAccountName = ref('')
const isCreating = ref(false)
const isDeleting = ref(false)
// 扩展 WalletAccount 类型以包含 displayName
interface WalletAccountWithDisplay extends WalletAccount {
  address: string
  displayName: string
}

const accountToDelete = ref<WalletAccountWithDisplay | null>(null)
const accountsWithAddress = ref<WalletAccountWithDisplay[]>([])
const isLoadingAccounts = ref(false)

// Computed
const { accountIndex, accounts } = storeToRefs(walletStore)

watch(accounts, async (newAccounts) => {
  if (!newAccounts) {
    accountsWithAddress.value = []
    return
  }
  isLoadingAccounts.value = true
  try {
    const results = await Promise.all(
      newAccounts.map(async (item) => {
        const [_, addressRes] = await walletManager.getWalletAddress(item.index)
        if (addressRes) {
          // 获取该地址设置的名字
          const savedName = await getCurrentName(addressRes.address)
          return {
            ...item,
            address: addressRes.address,
            displayName: savedName || item.name, // 如果有保存的名字就显示，否则显示 account.name
          }
        }
        return {
          ...item,
          address: '',
          displayName: item.name, // 如果没有地址，使用默认的 account.name
        }
      })
    )
    accountsWithAddress.value = results
  } finally {
    isLoadingAccounts.value = false
  }
}, { immediate: true })

// Methods

function showCreateAccountDialog() {
  isCreateAccountDialogOpen.value = true
  newAccountName.value = `Account ${(accounts.value?.length || 0) + 1}`
}

function showEditNameDialog(account: WalletAccountWithDisplay) {
  // 跳转到名字选择页面
  router.push('/wallet/name-select')
}

async function createAccount() {
  if (!newAccountName.value) {
    toast({
      title: '错误',
      description: '请输入账户名称',
      variant: 'destructive',
    })
    return
  }

  try {
    isCreating.value = true
    const newAccountId = accounts.value?.length || 0
    const accountName = newAccountName.value.trim() || `Account ${newAccountId + 1}`
    await walletStore.addAccount(accountName, newAccountId)
    try {
      await sendAccountsChangedEvent(newAccountId)
    } catch (error) {
      console.error('sendAccountsChangedEvent error', error);
    }
    toast({
      title: '成功',
      description: '账户创建成功',
      variant: 'success'
    })
    isCreateAccountDialogOpen.value = false
    console.log('router.go(-1)');
    router.go(-1)
  } catch (error) {
    console.log('error', error);
    
    isCreating.value = false
    toast({
      title: '错误',
      description: '账户创建失败',
      variant: 'destructive',
    })
  } finally {
    isCreating.value = false
  }
}

function confirmDeleteAccount(account: WalletAccountWithDisplay) {
  accountToDelete.value = account
  isDeleteDialogOpen.value = true
}

async function deleteAccount() {
  if (!accountToDelete.value) return

  try {
    isDeleting.value = true
    await walletStore.deleteAccount(accountToDelete.value.index)

    toast({
      title: '成功',
      description: '账户删除成功',
      variant: 'success'
    })
    isDeleteDialogOpen.value = false
    // 发送 accountsChanged 事件（封装函数）
    await sendAccountsChangedEvent(accounts.value)
    setTimeout(() => {
      router.back()
    }, 300)
  } catch (error) {
    toast({
      title: '错误',
      description: '账户删除失败',
      variant: 'destructive',
    })
  } finally {
    isDeleting.value = false
    accountToDelete.value = null
  }
}

// 创建防抖的切换账户函数
const debouncedSelectAccount = useDebounceFn(async (account: WalletAccountWithDisplay) => {
  try {
    await walletStore.switchToAccount(account.index)
    toast({
      title: '成功',
      description: '切换账户成功',
      variant: 'success'
    })
    console.log(router.currentRoute.value.path);
    setTimeout(() => {
      router.go(-1)
    }, 300)
  } catch (error) {
    toast({
      title: '错误',
      description: '切换账户失败',
      variant: 'destructive',
    })
  }
}, 300)

async function selectAccount(account: WalletAccountWithDisplay) {
  // 如果正在切换账户，直接返回
  if (isSwitchingAccount.value) {
    toast({
      title: '请稍等',
      description: '账户切换进行中',
      variant: 'default'
    })
    return
  }

  // 调用防抖函数
  debouncedSelectAccount(account)
}

async function copyAddress(address: string) {
  try {
    await navigator.clipboard.writeText(address)
    toast({
      title: '成功',
      description: '地址已复制',
    })
  } catch (error) {
    toast({
      title: '错误',
      description: '复制失败',
      variant: 'destructive',
    })
  }
}
</script>
