# Learnings

## 2026-04-19: useSatsToUsd Composable

### Pattern: BTC Price Fetching with vue-query
- Use `useQuery` from `@tanstack/vue-query` for BTC price fetching
- Query key: `['btcPrice', network]` - includes network for proper cache invalidation
- Refetch interval: 5 minutes (`1000 * 60 * 5`)
- Enabled condition: `computed(() => !!network.value)` - only fetch when network is set

### Pattern: Sats to USD Conversion
- Sats to BTC: divide by `1e8` (100,000,000 sats = 1 BTC)
- API returns price as string in `data.amount` field
- Return `null` for invalid inputs (0, negative, NaN, empty)

### Store Access Pattern
- Use `storeToRefs` from Pinia to extract reactive refs from store
- `const { network } = storeToRefs(walletStore)` maintains reactivity

### API Response Structure
- `ordxApi.getBTCPrice()` returns a `Response` object
- Need to call `.json()` to parse the response
- Parsed structure: `{ data: { amount: string } }`

## 2026-04-19: USD Display in AssetOperationDialog

### Pattern: Using toRef for Props with Composables
- Use `toRef(props, 'amount')` to create a ref from props that maintains reactivity
- Pass this ref directly to `useSatsToUsd(amountRef)` composable
- This pattern allows composables to react to prop changes

### Template Pattern for USD Display
```vue
<!-- USD value display -->
<div v-if="usdValue" class="text-sm text-zinc-400">
  ≈ ${{ usdValue.toLocaleString('en-US', { minimumFractionDigits: 3, maximumFractionDigits: 3 }) }}
</div>
```

### Key Implementation Details
- `v-if="usdValue"` handles both empty input and fetch failure (returns null)
- Format: 3 decimal places with thousand separators
- Style: `text-zinc-400` for dimmed appearance
- Place below the input field, inside the same container div

## 2026-04-19: InputSection.vue USD Display Integration

### Pattern: Using useSatsToUsd in Components
- Import: `import { useSatsToUsd } from '~/composables/useSatsToUsd'`
- Usage: `const { usdValue } = useSatsToUsd(totalAmount)` where `totalAmount` is a `Ref<string | number>`
- The composable handles all validation internally (null for empty/0/invalid)

### Template Display Pattern
```vue
<div v-if="usdValue" class="text-sm text-zinc-400 mt-1">
  ≈ ${{ usdValue.toLocaleString('en-US', { minimumFractionDigits: 3, maximumFractionDigits: 3 }) }}
</div>
```
- `v-if="usdValue"` handles hiding when value is null (empty input, 0, or price fetch failed)
- `text-zinc-400` for dimmed color
- 3 decimal places for precision
- `toLocaleString` for thousand separators

### Key Implementation Details
- Place USD display inside `<FormItem>` after `<Input>` for proper grouping
- No need for intermediate computed - pass ref directly to composable
- Composable returns `ComputedRef<number | null>` - null when should be hidden
