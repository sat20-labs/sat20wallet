<template>
  <div class="flex justify-center mb-2">
    <Icon icon="material-symbols:content-cut" color="green" class="w-6 h-6 mr-1" />
    <span class= "text-zinc-400 text-base">{{ $t('splitAsset.description') }}</span>
  </div>
  <div>
    <form v-if="!review" @submit="onSubmit">
      <fieldset :disabled="loading || validating" class="space-y-1">
        <!-- Asset Name (Hidden) -->
        <FormField v-slot="{ componentField }" name="assetName">
          <FormItem class="hidden">
            <FormLabel>{{ $t('splitAsset.assetKey') }}</FormLabel>
            <FormControl>
              <Input
                type="text"
                :placeholder="$t('splitAsset.assetKey')"
                class="h-12 bg-zinc-800"
                v-bind="componentField"
                disabled
              />
            </FormControl>
          </FormItem>
        </FormField>

        <!-- Amount -->
        <FormField v-slot="{ componentField }" name="amt">
          <FormItem>
            <FormLabel>{{ $t('assetOperationDialog.amount') }} :</FormLabel>
            <FormControl>
              <Input
                type="number"
                v-model="form.values.amt"
                :placeholder="$t('assetOperationDialog.enterAmount')"
                class="h-11 bg-zinc-800"
                v-bind="componentField"
              />
            </FormControl>
          </FormItem>
        </FormField>

        <!-- Repeat Count -->
        <FormField v-slot="{ componentField }" name="n">
          <FormItem class="pt-2">
            <FormLabel>{{ $t('splitAsset.repeat') }} :</FormLabel>
            <FormControl>
              <div class="flex items-center gap-2">
                <!-- Slider -->
                <input
                  type="range"
                  min="1"
                  max="100"
                  step="1"
                  v-model="form.values.n"
                 @input="(e: Event) => form.setFieldValue('n', Number((e.target as HTMLInputElement).value))"
                  class="w-full h-2 bg-zinc-800 rounded-lg appearance-none cursor-pointer"
                />
                <!-- Input -->
                <Input
                  type="number"
                  min="1"
                  max="100"
                  v-model="form.values.n"
                    @input="(e: Event) => form.setFieldValue('n', Number((e.target as HTMLInputElement).value))"
                    :placeholder="$t('splitAsset.repeatPlaceholder')"
                  class="w-20 h-11 bg-zinc-800"
                  v-bind="componentField"
                />
              </div>
            </FormControl>
          </FormItem>
        </FormField>

        <!-- Destination Address -->
        <FormField v-slot="{ componentField }" name="destAddr">
          <FormItem>
            <FormLabel>{{ $t('assetOperationDialog.address') }}</FormLabel>
            <FormControl>
              <Input
                type="text"
                v-model="form.values.destAddr"
                :placeholder="$t('assetOperationDialog.enterAddress')"
                class="h-11 bg-zinc-800 text-zinc-400"
                v-bind="componentField"
              />
            </FormControl>
          </FormItem>
        </FormField>
        <!-- Error Message -->
        <p v-if="errorMessage" class="text-sm text-destructive">{{ errorMessage }}</p>
      </fieldset>

      <!-- Submit Button -->
      <div class="mt-6">
        <Button
          class="w-full h-11 mb-2"
          :loading="loading"
          :disabled="!form.values.amt || loading || validating"
          type="submit"
        >
          {{ $t('splitAsset.review') }}
        </Button>
      </div>
    </form>
    <section v-else class="space-y-3" aria-live="polite">
      <h3 class="font-medium">{{ $t('splitAsset.confirmTitle') }}</h3>
      <p class="text-sm text-muted-foreground">{{ $t('splitAsset.sameDestination') }}</p>
      <dl class="space-y-2 rounded border border-border p-3 text-sm">
        <div><dt>{{ $t('tools.txConfirm.wallet') }}</dt><dd class="break-all">{{ review.wallet }}</dd></div>
        <div><dt>{{ $t('tools.txConfirm.account') }}</dt><dd>{{ review.accountIndex }}</dd></div>
        <div><dt>{{ $t('tools.txConfirm.sourceAddress') }}</dt><dd class="break-all">{{ review.sourceAddress }}</dd></div>
        <div><dt>{{ $t('tools.txConfirm.network') }}</dt><dd>Bitcoin / {{ review.network }}</dd></div>
        <div><dt>{{ $t('splitAsset.assetKey') }}</dt><dd>{{ review.assetName === '::' ? 'BTC (sats)' : review.assetName }}</dd></div>
        <div><dt>{{ $t('splitAsset.amountPerOutput') }}</dt><dd>{{ review.amt }}{{ review.assetName === '::' ? ' sats' : '' }}</dd></div>
        <div><dt>{{ $t('splitAsset.repeat') }}</dt><dd>{{ review.n }}</dd></div>
        <div><dt>{{ $t('splitAsset.requestedTotal') }}</dt><dd>{{ review.total }}{{ review.assetName === '::' ? ' sats' : '' }}</dd></div>
        <div><dt>{{ $t('assetOperationDialog.address') }}</dt><dd class="break-all">{{ review.destAddr }}</dd></div>
        <div><dt>{{ $t('tools.txConfirm.feeRate') }}</dt><dd>{{ review.feeRate }} sats/vB</dd></div>
      </dl>
      <p class="text-sm text-muted-foreground">{{ $t('splitAsset.feeUnavailable') }}</p>
      <p v-if="errorMessage" role="alert" class="text-sm text-destructive">{{ errorMessage }}</p>
      <div class="grid grid-cols-2 gap-2">
        <Button variant="outline" :disabled="loading" @click="cancelReview">{{ $t('common.cancel') }}</Button>
        <Button :loading="loading" :disabled="loading" @click="confirmSplit">{{ $t('common.confirm') }}</Button>
      </div>
    </section>
  </div>
</template>

<script lang="ts" setup>
import { ref, onBeforeUnmount, reactive, computed, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRoute, useRouter } from 'vue-router';
import { storeToRefs } from 'pinia';
import { useWalletStore, useGlobalStore } from '@/store';
import { useL1Assets } from '@/composables/hooks/useL1Assets';
import { assertWalletIdentityReady, subscribeWalletIdentity } from '@/lib/identity-boundary';
import { useToast } from '@/components/ui/toast-new';
import walletManager from '@/utils/sat20';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Icon } from '@iconify/vue';
import { Form, FormItem, FormLabel, FormControl, FormField } from '@/components/ui/form';
import { useForm } from 'vee-validate';
import { toTypedSchema } from '@vee-validate/zod';
import * as z from 'zod';

// Reactive State
const isOpen = ref(true);
const route = useRoute();
const router = useRouter();
const props = defineProps({
  assetName: {
    type: String,
    required: true,
  },
});
const assetName = computed(() => props.assetName);
const loading = ref(false);
const errorMessage = ref<string | null>(null);

// Store and Hooks
const walletStore = useWalletStore();
const globalStore = useGlobalStore();
const { address } = storeToRefs(walletStore);
const { refreshL1Assets } = useL1Assets();
const { t } = useI18n();
const balance = ref({
  availableAmt: 0,
  lockedAmt: 0,
});
const { toast } = useToast();

// Form Validation Schema
const splitSchema = z.object({
  assetName: z.string().min(1, 'Asset name is required'),
  amt: z.preprocess((v) => Number(v), z.number().positive('Amount must be positive')),
  n: z.preprocess((v) => Number(v), z.number().int().positive('Repeat count must be positive').max(100)),
  destAddr: z.string().min(1, 'Address is required'),
});


// Form Initialization
const formInitialValues = reactive({
  assetName: assetName.value,
  amt: '',
  n: 1, // Default value
  destAddr: '',
});

const form = useForm({
  validationSchema: toTypedSchema(splitSchema),
  initialValues: formInitialValues,
  // Review unmounts the fields; preserve their values for its fingerprint and Cancel.
  keepValuesOnUnmount: true,
});

// Fetch Asset Balance
const fetchAssetBalance = async (assetName: string) => {
  if (!assetName || !address.value) return;

  try {
    const [err, result] = await walletManager.getAssetAmount(address.value, assetName);
    if (err) {
      console.error('Error fetching asset balance:', err);
      return;
    }

    if (result) {
      balance.value.availableAmt = result.availableAmt || 0;
      balance.value.lockedAmt = result.lockedAmt || 0;
    }
  } catch (error) {
    console.error('Unexpected error fetching asset balance:', error);
  }
};

// Watch for Changes
watch([assetName, address], () => {
  console.log('assetName or address changed:', { assetName: assetName.value, address: address.value });
  if (assetName.value) {
    fetchAssetBalance(assetName.value);
  }
  form.setFieldValue('assetName', assetName.value || '');
  form.setFieldValue('destAddr', address.value || ''); // 确保 destAddr 同步
}, { immediate: true, deep: true });

type SplitReview = Readonly<{
  fingerprint: string; generation: number; wallet: string; accountIndex: number;
  sourceAddress: string; network: string; destAddr: string; assetName: string;
  amt: string; n: number; total: string; feeRate: number;
}>;
const review = ref<SplitReview | null>(null);
const validating = ref(false);
let reviewRevision = 0;
let disposed = false;
const splitFingerprint = () => JSON.stringify([
  form.values.assetName, form.values.amt, form.values.n, form.values.destAddr,
  assetName.value, globalStore.env, walletStore.network, walletStore.rootAccountId,
  walletStore.walletId, walletStore.accountIndex, walletStore.address, walletStore.btcFeeRate,
]);
const cancelReview = () => { reviewRevision++; review.value = null; };
watch(splitFingerprint, cancelReview, { flush: 'sync' });
const unsubscribeIdentity = subscribeWalletIdentity(cancelReview);
onBeforeUnmount(() => { disposed = true; cancelReview(); unsubscribeIdentity(); });

// Review validates only; the existing send API is invoked by final Confirm.
const onSubmit = form.handleSubmit(async (values) => {
  if (loading.value || validating.value || review.value || disposed) return;
  errorMessage.value = null;
  validating.value = true;
  const revision = ++reviewRevision;
  const fingerprint = splitFingerprint();
  try {
    const generation = assertWalletIdentityReady();
    const sourceAddress = walletStore.address;
    if (!sourceAddress) throw new Error(t('tools.txConfirm.contextChanged'));
    const amt = String(values.amt);
    if (!/^\d+(?:\.\d+)?$/.test(amt) || (values.assetName === '::' && !Number.isSafeInteger(values.amt))) {
      throw new Error(t('assetOperationDialog.amountErrors.precision'));
    }
    const feeRate = Number(walletStore.btcFeeRate);
    if (!Number.isSafeInteger(feeRate) || feeRate <= 0) throw new Error(t('splitAsset.invalidFeeRate'));
    const destAddr = values.destAddr.trim();
    const [err, valid] = await walletManager.validateBitcoinAddress(destAddr);
    if (disposed || revision !== reviewRevision || fingerprint !== splitFingerprint()) return;
    assertWalletIdentityReady(generation);
    if (err || !valid?.valid) throw err || new Error(t('splitAsset.invalidAddress'));
    const [whole, fraction = ''] = amt.split('.');
    const units = (BigInt(whole + fraction) * BigInt(values.n)).toString().padStart(fraction.length + 1, '0');
    const total = fraction.length ? `${units.slice(0, -fraction.length)}.${units.slice(-fraction.length)}` : units;
    review.value = Object.freeze({
      fingerprint, generation, wallet: `${walletStore.wallet?.name || ''} (${walletStore.walletId})`,
      accountIndex: walletStore.accountIndex, sourceAddress,
      network: String(walletStore.network), destAddr, assetName: values.assetName,
      amt, n: Number(values.n), total, feeRate,
    });
  } catch (error: any) {
    if (!disposed && revision === reviewRevision) errorMessage.value = error.message;
  } finally { validating.value = false; }
});

const confirmSplit = async () => {
  if (loading.value || disposed || !review.value) return;
  const snapshot = review.value;
  if (snapshot.fingerprint !== splitFingerprint()) { cancelReview(); return; }
  errorMessage.value = null;
  loading.value = true;

  try {
    assertWalletIdentityReady(snapshot.generation);
    const [err, result] = await walletManager.batchSendAssets(
      snapshot.destAddr,
      snapshot.assetName,
      snapshot.amt,
      snapshot.n,
      snapshot.feeRate
    );
    if (err) {
      let detail = 'L1资产拆分失败。';
      if (err.message) detail = err.message;
      else if (typeof err === 'string') detail = err;
      throw new Error(detail);
    }

    toast({
      title: 'Success',
      description: `Successfully initiated split: ${snapshot.assetName} : ${snapshot.amt} x ${snapshot.n} `,
      variant: 'success'
    });
    review.value = null;
    await refreshL1Assets();

    form.resetForm();
    isOpen.value = false;
    setTimeout(() => router.back(), 300);
  } catch (error: any) {
    console.error('L1 Split Error:', error);
    const description = error.message || 'An unknown error occurred during the split.';
    toast({ title: 'Error', description, variant: 'destructive' });
    errorMessage.value = description;
  } finally {
    loading.value = false;
  }
};

</script>
