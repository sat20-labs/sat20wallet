import { computed, ref, type Ref, type ComputedRef } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { ordxApi } from '~/apis'
import { useWalletStore } from '@/store'
import { storeToRefs } from 'pinia'

/**
 * Composable for converting sats (satoshis) to USD value
 * 
 * @param sats - A ref containing the amount in satoshis (number or string)
 * @returns An object with:
 *   - usdValue: ComputedRef<number | null> - The USD value, or null if invalid/unavailable
 *   - isLoading: Ref<boolean> - Whether the BTC price is being fetched
 *   - error: Ref<Error | null> - Any error that occurred during fetching
 */
export function useSatsToUsd(sats: Ref<number | string>) {
  const walletStore = useWalletStore()
  const { network } = storeToRefs(walletStore)

  // Fetch BTC price using vue-query
  const { 
    data: btcPriceData, 
    isLoading, 
    error 
  } = useQuery({
    queryKey: ['btcPrice', network],
    queryFn: async () => {
      const response = await ordxApi.getBTCPrice({ network: network.value })
      return response.json()
    },
    refetchInterval: 1000 * 60 * 5, // Refresh every 5 minutes
    enabled: computed(() => !!network.value),
  })

  // Compute USD value from sats
  const usdValue: ComputedRef<number | null> = computed(() => {
    // Return null if price data is not available
    if (!btcPriceData.value?.data?.amount) {
      return null
    }

    // Parse sats value
    const satsNum = typeof sats.value === 'string' 
      ? parseFloat(sats.value) 
      : sats.value

    // Return null for invalid, zero, or negative values
    if (!satsNum || isNaN(satsNum) || satsNum <= 0) {
      return null
    }

    // Convert sats to BTC (divide by 1e8)
    const btcAmount = satsNum / 1e8
    
    // Get price and calculate USD value
    const price = parseFloat(btcPriceData.value.data.amount)
    const value = btcAmount * price

    return value
  })

  return {
    usdValue,
    isLoading,
    error: error as Ref<Error | null>,
  }
}
