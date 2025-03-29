<template>
  <div class="layout-container min-h-screen flex flex-col">
    <!-- Header -->
    <header class="flex-none z-40 flex items-center justify-between p-4 border-b bg-transparent">
      <div class="flex items-center gap-2">
        <Button variant="ghost" size="icon" @click="router.back()">
          <Icon icon="lucide:arrow-left" class="w-5 h-5" />
        </Button>
        <h1 class="text-lg font-semibold">Account Manager</h1>
      </div>
    </header>

    <!-- Scrollable Content -->
    <main class="flex-1 overflow-y-auto">
      <div class="container max-w-2xl mx-auto p-4 space-y-6">
        <div class="space-y-4">
          <div class="text-sm font-medium text-muted-foreground">
            Current Account
          </div>
          
          <!-- Sub-wallet List -->
          <div class="space-y-2">
            <div
              v-for="wallet in subWallets"
              :key="wallet.id"
              class="flex items-center justify-between p-3 rounded-lg border hover:bg-accent/50 transition-colors"
              :class="{ 'border-primary/50': Number(wallet.id) === currentSubWalletIndex }"
            >
              <div class="flex items-center gap-3">
                <div class="w-10 h-10 rounded-full overflow-hidden bg-muted">
                  <div class="w-full h-full flex items-center justify-center">
                    <Icon icon="lucide:user-round" class="w-5 h-5 text-white/60" />
                  </div>
                </div>
                <div>
                  <div class="font-medium flex items-center gap-2 text-white/60">
                    {{ wallet.name }}
                    <Button
                      v-if="Number(wallet.id) === currentSubWalletIndex"
                      variant="ghost"
                      size="icon"
                      class="h-2 w-2"
                      @click="showEditNameDialog(wallet)"
                    >
                      <Icon icon="lucide:pencil" class="w-2 h-2" />
                    </Button>
                  </div>
                  <div class="text-sm text-muted-foreground">{{ wallet.address }}</div>
                </div>
              </div>
              <div class="flex items-center gap-2">
                <Button
                  v-if="Number(wallet.id) !== currentSubWalletIndex"
                  variant="outline"
                  size="sm"
                  @click="selectWallet(wallet)"
                >
                  Switch
                </Button>
                <Button
                  v-else
                  variant="outline"
                  size="sm"
                  disabled
                >
                  Current
                </Button>
                <Button
                  v-if="Number(wallet.id) !== currentSubWalletIndex"
                  variant="ghost"
                  size="icon"
                  class="text-destructive hover:text-destructive"
                  @click="confirmDeleteWallet(wallet)"
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
            @click="showCreateWalletDialog"
          >
            <Icon icon="lucide:plus-circle" class="w-6 h-6 flex-shrink-0" />
            Create New Account
          </Button>
        </div>
      </div>
    </footer>

    <!-- Edit Name Dialog -->
    <Dialog :open="isEditNameDialogOpen" @update:open="isEditNameDialogOpen = $event">
      <DialogContent class="sm:max-w-[425px]">
        <DialogHeader>
          <DialogTitle>EDIT ACCOUNT NAME</DialogTitle>
          <DialogDescription>
            Change the name of your account
          </DialogDescription>
        </DialogHeader>
        <div class="space-y-4">
          <div class="space-y-2">
            <Label for="walletName">Account Name</Label>
            <Input
              id="walletName"
              v-model="editingName"
              placeholder="Enter account name"
            />
          </div>
        </div>
        <DialogFooter>
          <Button @click="saveWalletName" :disabled="isSaving" class="h-12">
            Save changes
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

    <!-- Create Dialog -->
    <Dialog :open="isCreateWalletDialogOpen" @update:open="isCreateWalletDialogOpen = $event">
      <DialogContent class="sm:max-w-[425px]">
        <DialogHeader>
          <DialogTitle>CREATE NEW ACCOUNT</DialogTitle>
          <DialogDescription>
            Set up your new account information.
          </DialogDescription>
        </DialogHeader>
        <form @submit.prevent="createSubWallet" class="space-y-4">
          <div class="space-y-4">
            <div class="space-y-2">
              <Label for="walletName">Name</Label>
              <Input
                id="walletName"
                v-model="newWalletName"
                placeholder="Enter account name"
              />
            </div>
            <div class="space-y-2">
              <Label for="password">Password</Label>
              <Input
                id="password"
                v-model="createPassword"
                type="password"
                placeholder="Enter account password"
              />
            </div>
            <div class="space-y-2">
              <Label for="confirmPassword">Confirm Password</Label>
              <Input
                id="confirmPassword"
                v-model="confirmPassword"
                type="password"
                placeholder="Confirm account password"
              />
            </div>
          </div>
          <DialogFooter>
            <Button type="submit" :disabled="isCreating" class="h-12">
              Create Account
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>


    <!-- Delete Dialog -->
    <Dialog :open="isDeleteDialogOpen" @update:open="isDeleteDialogOpen = $event">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>DELETE ACCOUNT</DialogTitle>
          <DialogDescription>
            Are you sure you want to delete this account? This action cannot be undone.
          </DialogDescription>
        </DialogHeader>
        <div class="space-y-4">
          <Alert variant="destructive">
            <Icon icon="lucide:alert-triangle" class="w-4 h-4" />
            <AlertDescription>
              Make sure you have backed up your recovery phrase before deleting this account.
            </AlertDescription>
          </Alert>
        </div>
        <DialogFooter>
          <Button variant="secondary" @click="isDeleteDialogOpen = false" class="h-12 mb-4">
            Cancel
          </Button>
          <Button 
            variant="default" 
            @click="deleteSubWallet"
            :disabled="isDeleting"
            class="h-12 mb-4"
          >
            Delete
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
import { Textarea } from '@/components/ui/textarea'
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

const router = useRouter()
const { toast } = useToast()
const walletStore = useWalletStore()

// State

const isEditNameDialogOpen = ref(false)
const isCreateWalletDialogOpen = ref(false)
const isDeleteDialogOpen = ref(false)
const editingName = ref('')
const createPassword = ref('')
const confirmPassword = ref('')
const newWalletName = ref('')
const isCreating = ref(false)
const isDeleting = ref(false)
const isSaving = ref(false)
const walletToEdit = ref<any>(null)
const walletToDelete = ref<any>(null)

// 模拟数据
const currentSubWalletIndex = ref(0)
const subWallets = ref([
  { id: '0', name: 'Account 1', address: 'tb1qyp...x0x7' },
  { id: '1', name: 'Account 2', address: 'tb1qzp...x0x8' },
])

// Methods
function showCreateWalletDialog() {
  isCreateWalletDialogOpen.value = true
  createPassword.value = ''
  confirmPassword.value = ''
  newWalletName.value = ''
}

function showEditNameDialog(wallet: any) {
  walletToEdit.value = wallet
  editingName.value = wallet.name
  isEditNameDialogOpen.value = true
}

async function saveWalletName() {
  if (!editingName.value || !walletToEdit.value) return

  try {
    isSaving.value = true
    // TODO: 实现保存名称的逻辑
    await new Promise(resolve => setTimeout(resolve, 1000))
    
    const wallet = subWallets.value.find(w => w.id === walletToEdit.value.id)
    if (wallet) {
      wallet.name = editingName.value
    }

    toast({
      title: 'Success',
      description: 'Wallet name updated successfully',
    })
    isEditNameDialogOpen.value = false
  } catch (error) {
    toast({
      title: 'Error',
      description: 'Failed to update wallet name',
      variant: 'destructive',
    })
  } finally {
    isSaving.value = false
    walletToEdit.value = null
  }
}

async function createSubWallet() {
  if (!newWalletName.value || !createPassword.value || createPassword.value !== confirmPassword.value) {
    toast({
      title: 'Error',
      description: createPassword.value !== confirmPassword.value ? 'Passwords do not match' : 'Please fill in all fields',
      variant: 'destructive',
    })
    return
  }

  try {
    isCreating.value = true
    // TODO: 实现创建子钱包的逻辑
    await new Promise(resolve => setTimeout(resolve, 1000))
    
    subWallets.value.push({
      id: String(subWallets.value.length),
      name: newWalletName.value,
      address: `tb1q${Math.random().toString(36).substring(2, 8)}...${Math.random().toString(36).substring(2, 6)}`,
    })

    toast({
      title: 'Success',
      description: 'Sub-wallet created successfully',
    })
    isCreateWalletDialogOpen.value = false
  } catch (error) {
    toast({
      title: 'Error',
      description: 'Failed to create sub-wallet',
      variant: 'destructive',
    })
  } finally {
    isCreating.value = false
  }
}

function confirmDeleteWallet(wallet: any) {
  walletToDelete.value = wallet
  isDeleteDialogOpen.value = true
}

async function deleteSubWallet() {
  if (!walletToDelete.value) return

  try {
    isDeleting.value = true
    // TODO: 实现删除子钱包的逻辑
    await new Promise(resolve => setTimeout(resolve, 1000))
    
    const index = subWallets.value.findIndex(w => w.id === walletToDelete.value.id)
    if (index > -1) {
      subWallets.value.splice(index, 1)
    }

    toast({
      title: 'Success',
      description: 'Sub-wallet deleted successfully',
    })
    isDeleteDialogOpen.value = false
  } catch (error) {
    toast({
      title: 'Error',
      description: 'Failed to delete sub-wallet',
      variant: 'destructive',
    })
  } finally {
    isDeleting.value = false
    walletToDelete.value = null
  }
}

function selectWallet(wallet: any) {
  currentSubWalletIndex.value = Number(wallet.id)
  toast({
    title: 'Success',
    description: 'Sub-wallet switched successfully',
  })
}
</script>
