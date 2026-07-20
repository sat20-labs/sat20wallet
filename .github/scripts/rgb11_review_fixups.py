from pathlib import Path
import json
import re


def write_ts(path: str, content: str) -> None:
    Path(path).write_text(content.replace("\n        ", "\n").lstrip())


def patch_send_dialog() -> None:
    path = Path("pwa/components/wallet/RGB11SendDialog.vue")
    text = path.read_text()
    if "import rgb11Address from '@/utils/rgb11Address'" not in text:
        text = text.replace(
            "import walletManager from '@/utils/sat20'\n",
            "import walletManager from '@/utils/sat20'\nimport rgb11Address from '@/utils/rgb11Address'\n",
        )
    text = text.replace(
        "walletManager.getRGB11AddressCarrierWarning()",
        "rgb11Address.carrierWarning()",
    )
    text = text.replace(
        "walletManager.prepareRGB11AddressTransfer({",
        "rgb11Address.prepareTransfer({",
    )
    text = text.replace(
        "walletManager.deliverAndBroadcastRGB11AddressTransfer({",
        "rgb11Address.deliverAndBroadcast({",
    )
    path.write_text(text)


def write_rgb11_assets_hook() -> None:
    write_ts(
        "pwa/composables/hooks/useRgb11Assets.ts",
        r'''import { computed, watch } from 'vue'
        import { useQuery } from '@tanstack/vue-query'
        import { storeToRefs } from 'pinia'
        import walletManager from '@/utils/sat20'
        import rgb11Address from '@/utils/rgb11Address'
        import { useGlobalStore, useL1Store, useRGB11Store, useWalletStore } from '@/store'
        import type { RGB11StateDTO } from '@/store/rgb11'

        interface UseAssetQueryOptions {
          enabled?: boolean | { value: boolean }
        }

        const decimalText = (amount: any): string => {
          const value = String(amount?.Value ?? amount?.value ?? '0')
          const precision = Number(amount?.Precision ?? amount?.precision ?? 0)
          if (!precision) return value
          const negative = value.startsWith('-')
          const digits = negative ? value.slice(1) : value
          const padded = digits.padStart(precision + 1, '0')
          const split = padded.length - precision
          const text = `${padded.slice(0, split)}.${padded.slice(split)}`.replace(/\.?0+$/, '')
          return negative ? `-${text}` : text
        }

        const outputHasAsset = (output: any, name: any) => (
          (output?.Assets || []).some((asset: any) => (
            asset?.Name?.Protocol === name?.Protocol &&
            asset?.Name?.Type === name?.Type &&
            asset?.Name?.Ticker === name?.Ticker
          ))
        )

        const assetNameOf = (value: any) => value?.Name || value?.name || value?.AssetName || {}

        const tickerInfoFor = (state: RGB11StateDTO, name: any) => (
          (state.ticker_infos || []).find((info: any) => {
            const infoName = assetNameOf(info)
            return infoName?.Protocol === name?.Protocol &&
              infoName?.Type === name?.Type &&
              infoName?.Ticker === name?.Ticker
          })
        )

        const officialContractID = (ticker: unknown) => {
          const value = String(ticker || '')
          return value.startsWith('rgb:') ? value : `rgb:${value}`
        }

        const toAssetItems = (state: RGB11StateDTO) => (state.assets || []).map((asset: any) => {
          const name = asset?.Name || {}
          const tickerInfo = tickerInfoFor(state, name)
          const contractId = officialContractID(name.Ticker)
          const displayName = String(tickerInfo?.displayname || tickerInfo?.DisplayName || '').trim()
          const symbol = String(tickerInfo?.ticker || tickerInfo?.Ticker || '').trim()
          const key = `rgb11:${name.Type || 'f'}:${name.Ticker || ''}`
          return {
            id: contractId,
            key,
            protocol: 'rgb11',
            type: name.Type || 'f',
            label: symbol || displayName || contractId,
            symbol,
            ticker: name.Ticker || '',
            contract_id: contractId,
            display_name: displayName,
            utxos: (state.outputs || [])
              .filter((output: any) => outputHasAsset(output, name))
              .map((output: any) => output.OutPointStr),
            amount: decimalText(asset?.Amount),
            precision: Number(asset?.Amount?.Precision ?? asset?.Amount?.precision ?? tickerInfo?.divisibility ?? 0),
          }
        })

        export const useRgb11Assets = (options: UseAssetQueryOptions = {}) => {
          const walletStore = useWalletStore()
          const globalStore = useGlobalStore()
          const l1Store = useL1Store()
          const rgb11Store = useRGB11Store()
          const { walletId, accountIndex, network, address } = storeToRefs(walletStore)
          const { env } = storeToRefs(globalStore)

          const queryEnabled = computed(() => {
            const enabled = options.enabled
            if (typeof enabled === 'boolean') return enabled
            return enabled?.value ?? true
          })

          const stateQuery = useQuery({
            queryKey: ['rgb11-state', walletId, accountIndex, network, address, env],
            queryFn: async (): Promise<RGB11StateDTO> => {
              // Process encrypted RGB11 delivery before ordinary Bitcoin UTXOs
              // are refreshed or exposed to coin selection.
              const [mailboxError] = await rgb11Address.syncMailbox({})
              if (mailboxError) throw mailboxError

              const [refreshError] = await walletManager.refreshRGB11State()
              if (refreshError) throw refreshError

              const [err, result] = await walletManager.getRGB11State()
              if (err) throw err
              if (!result?.state) throw new Error('RGB11 Wallet state is unavailable')
              return JSON.parse(result.state) as RGB11StateDTO
            },
            enabled: computed(() => queryEnabled.value && !!walletId.value && !!address.value),
          })

          watch(
            () => stateQuery.data.value,
            (state) => {
              if (!state) return
              rgb11Store.setState(state)
              const items = toAssetItems(state)
              l1Store.setRGB11List(items)
              l1Store.setAssetList([
                ...(l1Store.assetList || []).filter((asset: any) => asset?.Name?.Protocol !== 'rgb11'),
                ...(state.assets || []),
              ])
              const withoutRGB11 = (l1Store.uniqueAssetList || []).filter((item: any) => item?.value !== 'rgb11')
              l1Store.setUniqueAssetList([
                ...withoutRGB11,
                ...(items.length ? [{ label: 'RGB11', value: 'rgb11' }] : []),
              ])
            },
            { deep: true, immediate: true }
          )

          return {
            loading: computed(() => stateQuery.isLoading.value),
            ready: computed(() => stateQuery.isSuccess.value),
            error: computed(() => stateQuery.error.value),
            refreshRGB11Assets: async () => {
              const result = await stateQuery.refetch()
              if (result.error) throw result.error
            },
          }
        }
        ''',
    )


def write_unified_assets_hook() -> None:
    write_ts(
        "pwa/composables/hooks/useUnifiedAssets.ts",
        r'''import { computed } from 'vue'
        import { useL1Assets } from './useL1Assets'
        import { useRgb11Assets } from './useRgb11Assets'

        interface UseAssetQueryOptions {
          enabled?: boolean | { value: boolean }
        }

        export const useUnifiedAssets = (options: UseAssetQueryOptions = {}) => {
          const requestedEnabled = computed(() => {
            const enabled = options.enabled
            if (typeof enabled === 'boolean') return enabled
            return enabled?.value ?? true
          })

          // The RGB11 mailbox is the safety barrier for ordinary L1 refresh.
          const rgb11 = useRgb11Assets({ enabled: requestedEnabled })
          const l1Enabled = computed(() => requestedEnabled.value && rgb11.ready.value)
          const l1 = useL1Assets({ enabled: l1Enabled })

          const refreshUnifiedAssets = async (refreshOptions: any = {}) => {
            await rgb11.refreshRGB11Assets()
            await l1.refreshL1Assets(refreshOptions)
          }

          return {
            loading: computed(() => rgb11.loading.value || (rgb11.ready.value && l1.loading.value)),
            error: rgb11.error,
            refreshL1Assets: refreshUnifiedAssets,
            refreshUnifiedAssets,
          }
        }
        ''',
    )


def patch_mailbox_noop() -> None:
    path = Path("sdk/wallet/rgb11_address_api.go")
    text = path.read_text()
    if "if !p.rgb11DKVSConfigured()" in text:
        return
    old = """\tif p == nil || p.wallet == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil {
\t\treturn nil, ErrRGB11Inconsistent
\t}
\tclient, err := p.configuredRGB11DKVSClient()"""
    new = """\tif p == nil || p.wallet == nil || p.rgbManager == nil || p.rgbManager.projectionStore == nil {
\t\treturn nil, ErrRGB11Inconsistent
\t}
\tresult := &RGB11AddressMailboxSyncResult{}
\tif !p.rgb11DKVSConfigured() {
\t\treturn result, nil
\t}
\tclient, err := p.configuredRGB11DKVSClient()"""
    if old not in text:
        raise SystemExit("missing mailbox preflight insertion point")
    text = text.replace(old, new, 1)
    text = text.replace(
        "\tresult := &RGB11AddressMailboxSyncResult{}\n\tfor start := 0;",
        "\tfor start := 0;",
        1,
    )
    path.write_text(text)


def patch_translations() -> None:
    translations = {
        "pwa/locales/en.json": {
            "addressMode": "Address",
            "invoiceMode": "Invoice",
            "receiverAddress": "Recipient Bitcoin address",
            "receiverAddressPlaceholder": "Enter the recipient SAT20 P2TR address",
            "amountRaw": "Amount (atomic units)",
            "addressModeHint": "The recipient must have enabled RGB11 receiving through DKVS. Otherwise use the traditional invoice mode.",
            "sendByAddress": "Send to Address",
            "temporaryTtlWarning": "The Consignment uses a temporary DKVS TTL. Keep this wallet data until the recipient ACKs and the Bitcoin transaction confirms.",
            "carrierWarning": "This address contains RGB11 assets. Spending its UTXO with a wallet that does not support RGB11 may permanently destroy those assets.",
            "addressUnavailable": "The recipient has not enabled DKVS RGB11 address receiving. Use the traditional invoice flow.",
            "addressBroadcasted": "RGB11 transaction broadcast: {txid}",
        },
        "pwa/locales/zh.json": {
            "addressMode": "地址转账",
            "invoiceMode": "传统 Invoice",
            "receiverAddress": "接收方比特币地址",
            "receiverAddressPlaceholder": "输入接收方 SAT20 P2TR 地址",
            "amountRaw": "数量（最小单位）",
            "addressModeHint": "接收方必须已经通过 DKVS 启用 RGB11 地址收款，否则请使用传统 Invoice 模式。",
            "sendByAddress": "发送到地址",
            "temporaryTtlWarning": "Consignment 当前仅使用临时 DKVS TTL。收到接收方 ACK 且比特币交易确认前，请勿清理本钱包数据。",
            "carrierWarning": "该地址包含 RGB11 资产。使用不支持 RGB11 的钱包花费其 UTXO，可能导致资产永久丢失。",
            "addressUnavailable": "接收方尚未启用 DKVS RGB11 地址收款，请改用传统 Invoice 流程。",
            "addressBroadcasted": "RGB11 交易已广播：{txid}",
        },
    }
    for filename, values in translations.items():
        path = Path(filename)
        text = path.read_text()
        if '"addressMode"' in text:
            continue
        match = re.search(
            r'(\s*"rgb11Transfer"\s*:\s*\{\s*\n\s*"sendTitle"\s*:\s*[^\n]+\n)',
            text,
        )
        if not match:
            raise SystemExit(f"missing rgb11Transfer/sendTitle in {filename}")
        insertion = "".join(
            f'    "{key}": {json.dumps(value, ensure_ascii=False)},\n'
            for key, value in values.items()
        )
        path.write_text(text[: match.end()] + insertion + text[match.end() :])


def patch_smoke_test() -> None:
    path = Path("pwa/scripts/verify/rgb11-l1-smoke.mjs")
    text = path.read_text()
    if "'utils/rgb11Address.ts'," not in text:
        text = text.replace(
            "  'components/wallet/RGB11SendDialog.vue',\n",
            "  'components/wallet/RGB11SendDialog.vue',\n  'utils/rgb11Address.ts',\n",
        )
    methods = [
        "enableRGB11AddressReceive",
        "resolveRGB11AddressEndpoint",
        "prepareRGB11AddressTransfer",
        "deliverAndBroadcastRGB11AddressTransfer",
        "syncRGB11AddressMailbox",
        "getRGB11AddressCarrierWarning",
    ]
    for method in methods:
        marker = f"  '{method}',\n"
        if marker not in text:
            text = text.replace(
                "  'restoreRGB11WalletState',\n",
                "  'restoreRGB11WalletState',\n" + marker,
            )
    if "await requireContains('utils/rgb11Address.ts'" not in text:
        text = text.replace(
            "await requireContains('utils/sat20.ts', requiredWasmMethods)\n",
            """await requireContains('utils/sat20.ts', requiredWasmMethods.filter((method) => ![
'enableRGB11AddressReceive',
'resolveRGB11AddressEndpoint',
'prepareRGB11AddressTransfer',
'deliverAndBroadcastRGB11AddressTransfer',
'syncRGB11AddressMailbox',
'getRGB11AddressCarrierWarning',
].includes(method)))
await requireContains('utils/rgb11Address.ts', [
'enableRGB11AddressReceive',
'resolveRGB11AddressEndpoint',
'prepareRGB11AddressTransfer',
'deliverAndBroadcastRGB11AddressTransfer',
'syncRGB11AddressMailbox',
'getRGB11AddressCarrierWarning',
])
""",
        )
    for anchor, fragments in (
        (
            "await requireContains('components/wallet/RGB11SendDialog.vue', [\n",
            ("rgb11Address.prepareTransfer", "rgb11Address.deliverAndBroadcast"),
        ),
        (
            "await requireContains('composables/hooks/useRgb11Assets.ts', [\n",
            ("rgb11Address.syncMailbox",),
        ),
    ):
        if anchor not in text:
            continue
        start = text.index(anchor)
        end = text.index("])", start)
        block = text[start:end]
        for fragment in fragments:
            line = f"  '{fragment}',\n"
            if line not in block:
                block += line
        text = text[:start] + block + text[end:]
    path.write_text(text)


patch_send_dialog()
write_rgb11_assets_hook()
write_unified_assets_hook()
patch_mailbox_noop()
patch_translations()
patch_smoke_test()
