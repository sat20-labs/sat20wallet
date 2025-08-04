<template>
  <div class="flex justify-center mb-2">
    <Icon icon="material-symbols:content-cut" color="green" class="w-6 h-6 mr-1" />  
    <span class= "text-zinc-400 text-base">{{ $t('splitAsset.description') }}</span>
  </div>
  <div>
    <form @submit="onSubmit">
      <div class="space-y-1">
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
      </div>

      <!-- Submit Button -->
      <div class="mt-6">
        <Button
          class="w-full h-11 mb-2"
          :loading="loading"
          :disabled="!form.values.amt || loading"
          type="submit"
        >
          {{ $t('assetOperationDialog.confirm') }}
        </Button>
      </div>
    </form>
  </div>
</template>

<script lang="ts" setup>
import { ref, onMounted, reactive, computed, watch } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import { storeToRefs } from 'pinia';
import { useWalletStore } from '@/store';
import { useL2Assets } from '@/composables/hooks/useL2Assets';
import { useToast } from '@/components/ui/toast-new';
import satsnetStp from '@/utils/stp';
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
const { address } = storeToRefs(useWalletStore());
const { refreshL2Assets } = useL2Assets();
const balance = ref({
  availableAmt: 0,
  lockedAmt: 0,
});
const { toast } = useToast();

// Form Validation Schema
const splitSchema = z.object({
  assetName: z.string().min(1, 'Asset name is required'),
  amt: z.preprocess((v) => Number(v), z.number().positive('Amount must be positive')),
  n: z.preprocess((v) => Number(v), z.number().int().positive('Repeat count must be positive')),
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
});

// Fetch Asset Balance
const fetchAssetBalance = async (assetName: string) => {
  if (!assetName || !address.value) return;

  try {
    const [err, result] = await satsnetStp.getAssetAmount(address.value, assetName);
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

// Form Submission
const onSubmit = form.handleSubmit(async (values) => {
  console.log('Submitting form with values:', values);
  errorMessage.value = null;
  loading.value = true;

  try {
    const [err, result] = await satsnetStp.batchSendAssets(
      values.destAddr,
      values.assetName,
      values.amt.toString(),
      Number(values.n),
      0
    );
    console.log('batchSendAssets result:', { err, result });

    if (err) {
      let detail = 'L2资产拆分失败。';
      if (err.message) detail = err.message;
      else if (typeof err === 'string') detail = err;
      throw new Error(detail);
    }

    toast({
      title: 'Success',
      description: `Successfully initiated split: ${values.assetName} : ${values.amt} x ${values.n} `,
      variant: 'success'
    });
    await refreshL2Assets();

    form.resetForm();
    isOpen.value = false;
    setTimeout(() => router.back(), 300);
  } catch (error: any) {
    console.error('L2 Split Error:', error);
    const description = error.message || 'An unknown error occurred during the split.';
    toast({ title: 'Error', description, variant: 'destructive' });
    errorMessage.value = description;
  } finally {
    loading.value = false;
  }
});

</script>