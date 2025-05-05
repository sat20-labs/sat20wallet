<template>
  <div class="layout-container min-h-screen flex flex-col">
    <!-- Header -->
    <header class="flex-none z-40 flex items-center justify-between p-4 border-b bg-transparent">
      <div class="flex items-center gap-2">
        <Button variant="ghost" size="icon" @click="router.back()">
          <Icon icon="lucide:arrow-left" class="w-5 h-5" />
        </Button>
        <h1 class="text-lg font-semibold">{{ $t('subWalletManager.title') }}</h1>
      </div>
    </header>

    <!-- Scrollable Content -->
    <main class="flex-1 overflow-y-auto">
      <div class="container max-w-2xl mx-auto p-4 space-y-6">
        <div class="space-y-4">
          <div class="text-sm font-medium text-muted-foreground">
            {{ $t('subWalletManager.currentAccount') }}
          </div>
          
          <!-- Sub-wallet List -->
          <div class="space-y-2">
            <div
              v-for="account in accounts"
              :key="account.index"
              class="flex items-center justify-between p-3 rounded-lg border hover:bg-accent/50 transition-colors"
              :class="{ 'border-primary/50': account.index === accountIndex }"
            >
              <div class="flex items-center gap-3">
                <div class="w-10 h-10 rounded-full overflow-hidden bg-muted">
                  <div class="w-full h-full flex items-center justify-center">
                    <Icon icon="lucide:user-round" class="w-5 h-5 text-white/60" />
                  </div>
                </div>
                <div>
                  <div class="font-medium flex items-center gap-2 text-white/60">
                    {{ account.name }}
                    <Button
                      v-if="account.index === accountIndex"
                      variant="ghost"
                      size="icon"
                      class="h-2 w-2"
                      @click="showEditNameDialog(account)"
                    >
                      <Icon icon="lucide:pencil" class="w-2 h-2" />
                    </Button>
                  </div>
                  <div class="text-sm text-muted-foreground">{{ hideAddress(account.address) }}</div>
                </div>
              </div>
              <div class="flex items-center gap-2">
                <Button
                  v-if="account.index !== accountIndex"
                  variant="outline"
                  size="sm"
                  @click="selectAccount(account)"
                >
                  {{ $t('subWalletManager.switch') }}
                </Button>
                <Button
                  v-else
                  variant="outline"
                  size="sm"
                  disabled
                >
                  {{ $t('subWalletManager.current') }}
                </Button>
                <Button
                  v-if="account.index !== accountIndex"
                  variant="ghost"
                  size="icon"
                  class="text-destructive hover:text-destructive"
                  @click="confirmDeleteAccount(account)"
                >
                  <Icon icon="lucide:trash-2" class="w-4 h-4" />
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
          <Button
            class="flex-1 gap-2 h-12 flex items-center w-full"
            variant="default"
            @click="showCreateAccountDialog"
          >
            <Icon icon="lucide:plus-circle" class="w-6 h-6 flex-shrink-0" />
            {{ $t('subWalletManager.createNewAccount') }}
          </Button>
        </div>
      </div>
    </footer>

    <!-- Edit Name Dialog -->
    <Dialog :open="isEditNameDialogOpen" @update:open="isEditNameDialogOpen = $event">
      <DialogContent class="sm:max-w-[425px]">
        <DialogHeader>
          <DialogTitle>{{ $t('subWalletManager.editAccountName') }}</DialogTitle>
          <DialogDescription>
            <hr class="mb-6 mt-1 border-t-1 border-accent">
            {{ $t('subWalletManager.changeAccountName') }}
          </DialogDescription>
        </DialogHeader>
        <div class="space-y-4">
          <div class="space-y-2">
            <Label for="AccountName">{{ $t('subWalletManager.accountName') }}</Label>
            <Input
              id="AccountName"
              v-model="editingName"
              :placeholder="$t('subWalletManager.enterAccountName')"
            />
          </div>
        </div>
        <DialogFooter>
          <Button @click="saveAccountName" :disabled="isSaving" class="h-12">
            {{ $t('subWalletManager.saveChanges') }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

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
              <Input
                id="AccountName"
                v-model="newAccountName"
                :placeholder="$t('subWalletManager.enterAccountName')"
              />
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
          <Button 
            variant="default" 
            @click="deleteAccount"
            :disabled="isDeleting"
            class="h-12 mb-4"
          >
            {{ $t('subWalletManager.delete') }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
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
import { useToast } from '@/components/ui/toast/use-toast'
import { useWalletStore } from '@/store/wallet'
import type { WalletAccount } from '@/types'
import { hideAddress } from '@/utils'

const router = useRouter()
const { toast } = useToast()
const walletStore = useWalletStore()

// State
const isEditNameDialogOpen = ref(false)
const isCreateAccountDialogOpen = ref(false)
const isDeleteDialogOpen = ref(false)
const editingName = ref('')
const newAccountName = ref('')
const isCreating = ref(false)
const isDeleting = ref(false)
const isSaving = ref(false)
const accountToEdit = ref<WalletAccount | null>(null)
const accountToDelete = ref<WalletAccount | null>(null)

// Computed
const { accountIndex, accounts } = storeToRefs(walletStore)
// Methods
function showCreateAccountDialog() {
  isCreateAccountDialogOpen.value = true
  newAccountName.value = `Account ${(accounts.value?.length || 0) + 1}`
}

function showEditNameDialog(account: WalletAccount) {
  accountToEdit.value = account
  editingName.value = account.name
  isEditNameDialogOpen.value = true
}

async function saveAccountName() {
  if (!editingName.value || !accountToEdit.value) return

  try {
    isSaving.value = true
    await walletStore.updateAccountName(accountToEdit.value.index, editingName.value)
    
    toast({
      title: 'Success',
      description: 'Account name updated successfully',
    })
    isEditNameDialogOpen.value = false
  } catch (error) {
    toast({
      title: 'Error',
      description: 'Failed to update Account name',
      variant: 'destructive',
    })
  } finally {
    isSaving.value = false
    accountToEdit.value = null
  }
}

async function createAccount() {
  if (!newAccountName.value) {
    toast({
      title: 'Error',
      description: 'Please fill in Account name',
      variant: 'destructive',
    })
    return
  }

  try {
    isCreating.value = true
    const newAccountId = accounts.value?.length || 0
    const accountName = newAccountName.value.trim() || `Account ${newAccountId + 1}`
    await walletStore.addAccount(accountName, newAccountId)
    
    toast({
      title: 'Success',
      description: 'Account created successfully',
    })
    isCreateAccountDialogOpen.value = false
  } catch (error) {
    toast({
      title: 'Error',
      description: 'Failed to create new account',
      variant: 'destructive',
    })
  } finally {
    isCreating.value = false
  }
}

function confirmDeleteAccount(account: WalletAccount) {
  accountToDelete.value = account
  isDeleteDialogOpen.value = true
}

async function deleteAccount() {
  if (!accountToDelete.value) return

  try {
    isDeleting.value = true
    await walletStore.deleteAccount(accountToDelete.value.index)
    
    toast({
      title: 'Success',
      description: 'Account deleted successfully',
    })
    isDeleteDialogOpen.value = false
  } catch (error) {
    toast({
      title: 'Error',
      description: 'Failed to delete account',
      variant: 'destructive',
    })
  } finally {
    isDeleting.value = false
    accountToDelete.value = null
  }
}

async function selectAccount(account: WalletAccount) {
  try {
    await walletStore.switchToAccount(account.index)
    toast({
      title: 'Success',
      description: 'Account switched successfully',
    })
  } catch (error) {
    toast({
      title: 'Error',
      description: 'Failed to switch account',
      variant: 'destructive',
    })
  }
}
</script>
