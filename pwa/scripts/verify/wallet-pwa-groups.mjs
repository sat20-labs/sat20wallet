// One grouping of the existing canonical case registries. No copied case names.
import { requiredPwaPosCases, optionalPwaEscapeCases } from './pos-pwa-e2e.mjs'
import { requiredPwaL1ProtocolCases } from './wallet-l1-assets-e2e.mjs'
import { requiredPwaWalletBasicCases } from './wallet-basic-e2e.mjs'
import { requiredPwaDappCases, requiredPwaRgbCases, requiredPwaBiometricCases } from './wallet-integrations-e2e.mjs'
import { requiredPwaToolsCases } from './wallet-tools-e2e.mjs'
import { requiredPwaMintCases } from './wallet-mint-e2e.mjs'
import { requiredPwaMiningCases } from './wallet-mining-e2e.mjs'
import { requiredPwaNodeCases } from './wallet-node-e2e.mjs'
export const walletPWARuntimeCases = ['Wallet PWA: all feature groups finish without unhandled browser errors']
export const walletPWAGroups = [
  { name: 'pos', cases: requiredPwaPosCases, optional: optionalPwaEscapeCases },
  { name: 'l1-protocols', cases: requiredPwaL1ProtocolCases },
  { name: 'wallet-basic', cases: requiredPwaWalletBasicCases },
  { name: 'dapp', cases: requiredPwaDappCases },
  { name: 'rgb-nia-workflow', cases: requiredPwaRgbCases.slice(0, 8) },
  { name: 'rgb-ifa-issue', cases: requiredPwaRgbCases.slice(8, 9) },
  { name: 'rgb-uda-issue', cases: requiredPwaRgbCases.slice(9, 10) },
  { name: 'rgb-uda-transfer', cases: requiredPwaRgbCases.slice(10) },
  { name: 'biometric', cases: requiredPwaBiometricCases },
  { name: 'tools', cases: requiredPwaToolsCases },
  { name: 'mint', cases: requiredPwaMintCases },
  { name: 'mining', cases: requiredPwaMiningCases },
  { name: 'node', cases: requiredPwaNodeCases },
]
