<template>
  <LayoutSecond :title="$t('subWalletManager.title')" hide-nav>

    <main class="flex-1 overflow-y-auto">
      <div class="space-y-4">
        <div class="space-y-4">
          <div class="text-sm font-medium text-muted-foreground">
            {{ $t('subWalletManager.currentAccount') }}
          </div>

          <!-- Sub-wallet List -->
          <div class="space-y-2">
            <div v-for="account in accountsWithAddress" :key="account.index"
              class="flex min-w-0 flex-wrap items-center justify-between gap-2 rounded-xl border border-border bg-card p-3 hover:bg-accent/50"
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
                  <div class="flex items-center gap-1 font-medium">
                    {{ account.displayName }}
                    <Icon v-if="isSwitchingAccount && account.index === accountIndex" icon="lucide:loader-2" class="w-3 h-3 animate-spin" />
                    <Button v-if="account.index === accountIndex && !isSwitchingAccount" variant="ghost" size="icon" class="size-10"
                      :aria-label="$t('subWalletManager.editAccountName')"
                      @click.stop="showEditNameDialog(account)">
                      <Icon icon="lucide:pencil" class="w-3 h-3" />
                    </Button>
                  </div>
                  <div class="text-xs text-muted-foreground">#{{ account.index }} · {{ hideAddress(account.address) }}</div>
                </div>
              </div>
              <div class="flex items-center gap-2">
                <span v-if="account.index === accountIndex" class="rounded-full bg-muted px-2 py-1 text-xs">{{ $t('subWalletManager.current') }}</span>
                <Button v-else size="sm" variant="outline" :disabled="isSwitchingAccount" @click.stop="selectAccount(account)">
                  {{ $t('subWalletManager.switch') }}
                </Button>
                <Button variant="ghost" size="icon" :aria-label="$t('subWalletManager.copyAddress')"
                  @click.stop="copyAddress(account.address)" class="size-10 hover:text-primary">
                  <Icon icon="lucide:copy" class="w-3 h-3" />
                </Button>
              </div>
            </div>
          </div>
        </div>
      </div>
    </main>

    <!-- Bottom Buttons -->
    <footer class="mt-4 border-t border-border pt-4">
      <div>
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

  </LayoutSecond>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useRouter } from 'vue-router'
import { Icon } from '@iconify/vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
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
import { storeToRefs } from 'pinia'
import type { WalletAccount } from '@/types'
import { hideAddress } from '@/utils'
import LayoutSecond from '@/components/layout/LayoutSecond.vue'
import walletManager from '@/utils/sat20'
import { sendAccountsChangedEvent } from '@/lib/utils'
import { useNameManager } from '@/composables/useNameManager'
import { useDebounceFn } from '@vueuse/core'
import { useI18n } from 'vue-i18n'
import { confirmPendingBackupRisk } from '@/utils/pendingBackupRisk'

const router = useRouter()
const { t } = useI18n()
const { toast } = useToast()
const walletStore = useWalletStore()
const { isSwitchingAccount } = storeToRefs(walletStore)

// 名字管理
const { getCurrentName } = useNameManager()

// State
const isCreateAccountDialogOpen = ref(false)
const newAccountName = ref('')
const isCreating = ref(false)
// 扩展 WalletAccount 类型以包含 displayName
interface WalletAccountWithDisplay extends WalletAccount {
  address: string
  displayName: string
}

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
  newAccountName.value = t('walletComponents.accountNumber', { number: (accounts.value?.length || 0) + 1 })
}

function showEditNameDialog(account: WalletAccountWithDisplay) {
  // 跳转到名字选择页面
  router.push('/wallet/name-select')
}

async function createAccount() {
  if (!newAccountName.value) {
    toast({
      title: t('walletComponents.error'),
      description: t('walletComponents.enterAccountName'),
      variant: 'destructive',
    })
    return
  }

  try {
    isCreating.value = true
    const newAccountId = accounts.value?.length || 0
    const accountName = newAccountName.value.trim() || t('walletComponents.accountNumber', { number: newAccountId + 1 })
    await walletStore.addAccount(accountName, newAccountId)
    try {
      await sendAccountsChangedEvent(newAccountId)
    } catch (error) {
      console.error('sendAccountsChangedEvent error', error);
    }
    toast({
      title: t('walletComponents.success'),
      description: t('walletComponents.accountCreated'),
      variant: 'success'
    })
    isCreateAccountDialogOpen.value = false
    console.log('router.go(-1)');
    router.go(-1)
  } catch (error) {
    console.log('error', error);

    isCreating.value = false
    toast({
      title: t('walletComponents.error'),
      description: t('walletComponents.accountCreateFailed'),
      variant: 'destructive',
    })
  } finally {
    isCreating.value = false
  }
}

// 创建防抖的切换账户函数
const debouncedSelectAccount = useDebounceFn(async (account: WalletAccountWithDisplay) => {
  try {
    if (!await confirmPendingBackupRisk(key => t(key))) return
    await walletStore.switchToAccount(account.index)
    toast({
      title: t('walletComponents.success'),
      description: t('walletComponents.accountSwitched'),
      variant: 'success'
    })
    console.log(router.currentRoute.value.path);
    setTimeout(() => {
      router.go(-1)
    }, 300)
  } catch (error) {
    toast({
      title: t('walletComponents.error'),
      description: t('walletComponents.accountSwitchFailed'),
      variant: 'destructive',
    })
  }
}, 300)

async function selectAccount(account: WalletAccountWithDisplay) {
  // 如果正在切换账户，直接返回
  if (isSwitchingAccount.value) {
    toast({
      title: t('walletComponents.wait'),
      description: t('walletComponents.accountSwitching'),
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
      title: t('walletComponents.success'),
      description: t('walletComponents.addressCopied'),
    })
  } catch (error) {
    toast({
      title: t('walletComponents.error'),
      description: t('walletComponents.copyFailed'),
      variant: 'destructive',
    })
  }
}
</script>
