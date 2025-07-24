<template>
  <LayoutApprove @confirm="confirm" @cancel="cancel">
    <div class="p-4 space-y-4">
      <h2 class="text-2xl font-semibold text-center">{{ $t('signPsbt.title') }}</h2>
      <p class="text-xs text-gray-400 text-center mb-4">
        {{ $t('signPsbt.warning') }}
      </p>

      <!-- Use the new component for Inputs -->
      <TxDetailSection
        v-if="parsedInputs.length > 0"
        :title="$t('signPsbt.inputs')"
        :items="parsedInputs"
        border-color-class="border-primary"
      />
      <div v-else-if="!isLoading && !parseError" class="text-center text-muted-foreground">
        {{ $t('signPsbt.noInputs') }}
      </div>

      <!-- Use the new component for Outputs -->
      <TxDetailSection
        v-if="parsedOutputs.length > 0"
        :title="$t('signPsbt.outputs')"
        :items="parsedOutputs"
        border-color-class="border-accent"
      />
      <div v-else-if="!isLoading && !parseError" class="text-center text-muted-foreground">
        {{ $t('signPsbt.noOutputs') }}
      </div>

      <!-- Raw PSBT Hex -->
      <Accordion type="single" collapsible class="w-full">
        <AccordionItem value="item-1">
          <AccordionTrigger class="text-sm">{{ $t('signPsbt.viewRawPsbt') }}</AccordionTrigger>
          <AccordionContent>
            <Alert class="mt-2">
              <AlertTitle class="text-xs font-normal break-all">{{
                props.data?.psbtHex || $t('signPsbt.noPsbtHex')
              }}</AlertTitle>
            </Alert>
          </AccordionContent>
        </AccordionItem>
      </Accordion>

      <div v-if="isLoading" class="text-center text-muted-foreground">
        <span class="animate-spin inline-block mr-2">⏳</span> {{ $t('signPsbt.loading') }}
      </div>
      <div v-if="parseError && !isLoading" class="text-center text-destructive">
        {{ $t('signPsbt.error') }}
      </div>
    </div>
  </LayoutApprove>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { storeToRefs } from 'pinia'
import LayoutApprove from '@/components/layout/LayoutApprove.vue'
import { Alert, AlertTitle } from '@/components/ui/alert'
import { Accordion, AccordionContent, AccordionItem, AccordionTrigger } from '@/components/ui/accordion'
import walletManager from '@/utils/sat20'
import { useToast } from '@/components/ui/toast'
import { useWalletStore } from '@/store'
import TxDetailSection from './TxDetailSection.vue' // Import the new component
import stp from '@/utils/stp'
// Define stricter types if possible for Input/Output/Asset structures
interface AssetName {
  Protocol: string;
  Type: string;
  Ticker: string;
}

interface Asset {
  Name: AssetName;
  Amount: string; // Keep as string if API returns it this way
  Precision: number;
  BindingSat: number;
  Offsets: any; // Use a more specific type if known
}

interface TxDetailItem {
  UtxoId: number;
  Outpoint: string;
  Value: number;
  PkScript: string;
  Assets: Asset[] | null;
}


interface Props {
  data: {
    psbtHex: string;
    options?: {
      chain?: string;
    }
  }
}

const props = defineProps<Props>()
const emit = defineEmits(['confirm', 'cancel'])
const walletStore = useWalletStore()
const { network } = storeToRefs(walletStore)
const toast = useToast()

const isLoading = ref(false)
const parseError = ref(false) // Added state to track parsing errors
const parsedPsbt = ref<any>(null) // Store the raw response if needed later
const parsedInputs = ref<TxDetailItem[]>([]) // Use the defined interface
const parsedOutputs = ref<TxDetailItem[]>([]) // Use the defined interface

const getParsedPsbt = async () => {
  isLoading.value = true
  parseError.value = false // Reset error state
  parsedInputs.value = []
  parsedOutputs.value = []
  parsedPsbt.value = null

  if (!props.data?.psbtHex) {
      console.warn("No psbtHex provided");
      toast.toast({
          title: 'Missing Data',
          description: 'No PSBT data available to parse.',
          variant: 'destructive'
      })
      isLoading.value = false
      parseError.value = true // Mark as error
      return;
  }

  try {
    let result
    if (props.data.options?.chain === 'sat20') {
      result = await stp.getTxAssetInfoFromPsbt_SatsNet(props.data.psbtHex, network.value)
    } else {
      console.log('getTxAssetInfoFromPsbt', props.data.psbtHex, network.value);
      
      result = await stp.getTxAssetInfoFromPsbt(props.data.psbtHex, network.value)
    }
    const [err, res] = result
    console.log("Fetched PSBT Info:", res);

    if (err) {
      console.error("Error fetching PSBT info:", err);
      toast.toast({
        title: 'Error Parsing PSBT',
        description: err.message || 'Failed to retrieve transaction details.',
        variant: 'destructive'
      })
       parseError.value = true // Mark as error
    } else if (res) {
      parsedPsbt.value = res // Store raw response
      let inputParseFailed = false;
      let outputParseFailed = false;

      // Safely parse inputs
      try {
        if (res.inputs && typeof res.inputs === 'string') {
          parsedInputs.value = JSON.parse(res.inputs);
          console.log("Parsed Inputs:", parsedInputs.value);
        } else if (Array.isArray(res.inputs)) {
          parsedInputs.value = res.inputs;
        } else {
          console.warn("Inputs data is missing, not a string, or not an array:", res.inputs)
           // Optionally consider this a partial success or handle as needed
        }
      } catch (parseErr) {
        console.error("Error parsing inputs JSON:", parseErr, "Raw:", res.inputs);
        inputParseFailed = true;
      }

      // Safely parse outputs
      try {
        if (res.outputs && typeof res.outputs === 'string') {
          parsedOutputs.value = JSON.parse(res.outputs);
           console.log("Parsed Outputs:", parsedOutputs.value);
        } else if (Array.isArray(res.outputs)) {
           parsedOutputs.value = res.outputs;
           console.log("Parsed Outputs:", parsedOutputs.value);
        } else {
           console.warn("Outputs data is missing, not a string, or not an array:", res.outputs)
           // Optionally consider this a partial success or handle as needed
        }
      } catch (parseErr) {
         console.error("Error parsing outputs JSON:", parseErr, "Raw:", res.outputs);
         outputParseFailed = true;
      }

       if(inputParseFailed || outputParseFailed) {
            parseError.value = true; // Mark error if any part failed parsing
            toast.toast({
                title: 'Parsing Error',
                description: `Failed to parse ${inputParseFailed ? 'input' : ''}${inputParseFailed && outputParseFailed ? ' and ' : ''}${outputParseFailed ? 'output' : ''} data format. Some details might be missing.`,
                variant: 'destructive'
            })
       }

    } else {
        console.warn("No response data received from getTxAssetInfoFromPsbt_SatsNet");
        toast.toast({
            title: 'Info',
            description: 'No transaction details found for this PSBT.',
        })
        // Not technically an error, but no data to show
    }
  } catch (fetchError: any) {
     console.error("Exception during PSBT info fetch:", fetchError);
       toast.toast({
          title: 'Network Error',
          description: fetchError?.message || 'Failed to communicate with the server.',
          variant: 'destructive'
      });
       parseError.value = true // Mark as error
  } finally {
      isLoading.value = false
  }

}

// Watch for changes in psbtHex and trigger parsing
watch(() => props.data?.psbtHex, getParsedPsbt, { immediate: true })

const confirm = async () => {
  const { options = {}, psbtHex } = props.data

  if (!psbtHex) {
      toast.toast({
        title: 'Error',
        description: 'Cannot sign, PSBT data is missing.',
        variant: 'destructive'
      })
      return;
  }

  // Consider adding a loading state for the button during signing
  // isLoading.value = true; // Example

  let result
  console.log('Signing PSBT Hex:', psbtHex)
  console.log('Options:', options)

  try {
      if (options.chain === 'sat20') {
          result = await walletManager.signPsbt_SatsNet(psbtHex, false)
      } else {
          // Assuming default is non-sat20 or needs clarification based on walletManager capabilities
          result = await walletManager.signPsbt(psbtHex, false)
      }
      console.log('Chain used for signing:', options.chain || 'Default/BTC');
      console.log('Sign PSBT result:', result);

      const [err, res] = result;

      if (err) {
           console.error("Sign PSBT Error:", err);
           toast.toast({
              title: 'Sign PSBT Failed',
              description: err?.message || 'An unknown error occurred during signing.',
              variant: 'destructive'
          });
      } else if (res?.psbt) {
          console.log("Signed PSBT successful:", res.psbt);
          emit('confirm', res.psbt);
      } else {
           // This case might indicate an issue in the walletManager logic if no error is thrown but no PSBT is returned
           console.warn("Signing process completed but no signed PSBT was returned.", res);
           toast.toast({
              title: 'Sign PSBT Issue',
              description: 'Signing process seemed to complete, but no signed data was received. Please retry or contact support.',
              variant: 'destructive'
          });
      }
  } catch (signError: any) {
       console.error("Exception during signing:", signError);
       toast.toast({
          title: 'Sign PSBT Error',
          description: signError?.message || 'An unexpected critical error occurred during signing.',
          variant: 'destructive'
      });
  } finally {
      // isLoading.value = false; // Reset loading state
  }
}

const cancel = () => {
  emit('cancel')
}
</script>
