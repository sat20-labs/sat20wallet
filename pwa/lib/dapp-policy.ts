import { Message } from '@/types/message'
import type { DappCapability } from './dapp-grant-model'

export type ApprovalExecution = 'none' | 'component' | 'component-then-direct' | 'fail-closed'
export interface DappActionPolicy { capability?: DappCapability; approval: ApprovalExecution }
const policy = (capability: DappCapability | undefined, approval: ApprovalExecution): DappActionPolicy => ({ capability, approval })

export const DAPP_ACTION_POLICY: Readonly<Record<string, DappActionPolicy>> = {
  [Message.MessageAction.REQUEST_ACCOUNTS]: policy(undefined, 'component'),
  [Message.MessageAction.GET_ACCOUNTS]: policy('accounts:read', 'none'),
  [Message.MessageAction.GET_PUBLIC_KEY]: policy('public-key:read', 'none'),
  [Message.MessageAction.GET_NETWORK]: policy('network:read', 'none'),
  [Message.MessageAction.SWITCH_NETWORK]: policy('network:switch', 'component'),
  [Message.MessageAction.GET_BALANCE]: policy('balance:read', 'none'),
  [Message.MessageAction.GET_UTXOS]: policy('utxo:read', 'none'),
  [Message.MessageAction.GET_UTXOS_SATSNET]: policy('utxo:read', 'none'),
  [Message.MessageAction.GET_ALL_LOCKED_UTXO]: policy('utxo:read', 'none'),
  [Message.MessageAction.GET_ALL_LOCKED_UTXO_SATSNET]: policy('utxo:read', 'none'),
  [Message.MessageAction.GET_UTXOS_WITH_ASSET]: policy('utxo:read', 'none'),
  [Message.MessageAction.GET_UTXOS_WITH_ASSET_SATSNET]: policy('utxo:read', 'none'),
  [Message.MessageAction.GET_UTXOS_WITH_ASSET_V2]: policy('utxo:read', 'none'),
  [Message.MessageAction.GET_UTXOS_WITH_ASSET_V2_SATSNET]: policy('utxo:read', 'none'),
  [Message.MessageAction.GET_ASSET_AMOUNT]: policy('balance:read', 'none'),
  [Message.MessageAction.GET_ASSET_AMOUNT_SATSNET]: policy('balance:read', 'none'),
  [Message.MessageAction.LOCK_UTXO]: policy('utxo:lock', 'component-then-direct'),
  [Message.MessageAction.LOCK_UTXO_SATSNET]: policy('utxo:lock', 'component-then-direct'),
  [Message.MessageAction.UNLOCK_UTXO]: policy('utxo:unlock', 'component-then-direct'),
  [Message.MessageAction.UNLOCK_UTXO_SATSNET]: policy('utxo:unlock', 'component-then-direct'),
  [Message.MessageAction.SIGN_MESSAGE]: policy('transaction:sign', 'component'),
  [Message.MessageAction.SIGN_DATA]: policy('transaction:sign', 'fail-closed'),
  [Message.MessageAction.SIGN_PSBT]: policy('transaction:sign', 'component'),
  [Message.MessageAction.SIGN_PSBTS]: policy('transaction:sign', 'component'),
  [Message.MessageAction.PUSH_TX]: policy('transaction:broadcast', 'component-then-direct'),
  [Message.MessageAction.PUSH_PSBT]: policy('transaction:broadcast', 'component-then-direct'),
  [Message.MessageAction.SEND_BITCOIN]: policy('asset:send', 'fail-closed'),
  [Message.MessageAction.SEND_INSCRIPTION]: policy('asset:send', 'fail-closed'),
  [Message.MessageAction.SEND_ASSETS_SATSNET]: policy('asset:send', 'component'),
  [Message.MessageAction.BATCH_SEND_ASSETS_SATSNET]: policy('asset:send', 'component'),
  [Message.MessageAction.BATCH_SEND_ASSETS_V2_SATSNET]: policy('asset:send', 'component'),
  [Message.MessageAction.SPLIT_ASSET]: policy('asset:send', 'component'),
  [Message.MessageAction.BUILD_BATCH_SELL_ORDER]: policy('transaction:prepare', 'none'),
  [Message.MessageAction.SPLIT_BATCH_SIGNED_PSBT_SATSNET]: policy('transaction:prepare', 'none'),
  [Message.MessageAction.FINALIZE_SELL_ORDER]: policy('transaction:prepare', 'none'),
  [Message.MessageAction.MERGE_BATCH_SIGNED_PSBT]: policy('transaction:prepare', 'none'),
  [Message.MessageAction.ADD_INPUTS_TO_PSBT]: policy('transaction:prepare', 'none'),
  [Message.MessageAction.ADD_OUTPUTS_TO_PSBT]: policy('transaction:prepare', 'none'),
  [Message.MessageAction.EXTRACT_TX_FROM_PSBT]: policy('transaction:prepare', 'none'),
  [Message.MessageAction.EXTRACT_TX_FROM_PSBT_SATSNET]: policy('transaction:prepare', 'none'),
  [Message.MessageAction.GET_FEE_FOR_DEPLOY_CONTRACT]: policy('contract:read', 'none'),
  [Message.MessageAction.GET_FEE_FOR_INVOKE_CONTRACT]: policy('contract:read', 'none'),
  [Message.MessageAction.QUERY_PARAM_FOR_INVOKE_CONTRACT]: policy('contract:read', 'none'),
  [Message.MessageAction.DEPLOY_CONTRACT_REMOTE]: policy('contract:write', 'component'),
  [Message.MessageAction.INVOKE_CONTRACT_SATSNET]: policy('contract:write', 'component'),
  [Message.MessageAction.INVOKE_UNIFIED_CONTRACT]: policy('contract:write', 'component'),
  [Message.MessageAction.INVOKE_CONTRACT_V2]: policy('contract:write', 'component'),
  [Message.MessageAction.INVOKE_CONTRACT_V2_SATSNET]: policy('contract:write', 'component'),
  [Message.MessageAction.GET_CURRENT_NAME]: policy('identity:read', 'none'),
  [Message.MessageAction.REGISTER_AS_REFERRER]: policy('identity:write', 'component'),
  [Message.MessageAction.BIND_REFERRER_FOR_SERVER]: policy('identity:write', 'fail-closed'),
  getSupportedContracts: policy('contract:read', 'none'),
  getDeployedContractStatus: policy('contract:read', 'none'),
}

export const getDappActionPolicy = (action: string) => DAPP_ACTION_POLICY[action] ?? null
export const hasApprovalRenderer = (action: string) => {
  const actionPolicy = getDappActionPolicy(action)
  return actionPolicy?.approval === 'component' || actionPolicy?.approval === 'component-then-direct'
}
