import { Message } from "../../types/message";
import { ProviderConfig } from "./types";

// 需要origin授权的操作列表
export const ACTIONS_REQUIRING_ORIGIN_AUTH = [
  Message.MessageAction.GET_ACCOUNTS,
  Message.MessageAction.GET_PUBLIC_KEY,
  Message.MessageAction.GET_BALANCE,
  Message.MessageAction.GET_NETWORK,
  Message.MessageAction.GET_UTXOS,
  Message.MessageAction.GET_UTXOS_SATSNET,
  Message.MessageAction.GET_ALL_LOCKED_UTXO,
  Message.MessageAction.GET_ALL_LOCKED_UTXO_SATSNET,
  Message.MessageAction.GET_CURRENT_NAME,
  Message.MessageAction.GET_ASSET_AMOUNT,
  Message.MessageAction.GET_ASSET_AMOUNT_SATSNET,
  Message.MessageAction.GET_FEE_FOR_DEPLOY_CONTRACT,
  Message.MessageAction.GET_FEE_FOR_INVOKE_CONTRACT,
  Message.MessageAction.GET_UTXOS_WITH_ASSET,
  Message.MessageAction.GET_UTXOS_WITH_ASSET_SATSNET,
  Message.MessageAction.GET_UTXOS_WITH_ASSET_V2,
  Message.MessageAction.GET_UTXOS_WITH_ASSET_V2_SATSNET,
  Message.MessageAction.BUILD_BATCH_SELL_ORDER,
  Message.MessageAction.SPLIT_BATCH_SIGNED_PSBT_SATSNET,
  Message.MessageAction.FINALIZE_SELL_ORDER,
  Message.MessageAction.MERGE_BATCH_SIGNED_PSBT,
  Message.MessageAction.ADD_INPUTS_TO_PSBT,
  Message.MessageAction.ADD_OUTPUTS_TO_PSBT,
  Message.MessageAction.EXTRACT_TX_FROM_PSBT,
  Message.MessageAction.EXTRACT_TX_FROM_PSBT_SATSNET,
  Message.MessageAction.PUSH_TX,
  Message.MessageAction.PUSH_PSBT,
  Message.MessageAction.GET_INSCRIPTIONS,
  Message.MessageAction.QUERY_PARAM_FOR_INVOKE_CONTRACT,
  Message.MessageAction.BIND_REFERRER_FOR_SERVER,
] as const;

// InAppBrowser 配置
export const INAPP_BROWSER_CONFIG: ProviderConfig = {
  inAppBrowserOptions: "location=yes,fullscreen=yes,clearcache=yes,hideurlbar=yes,clearsessioncache=yes,toolbar=yes,enableviewportscale=yes,mediaPlaybackRequiresUserAction=no,allowInlineMediaPlayback=yes,keyboardDisplayRequiresUserAction=no,suppressesIncrementalRendering=no",
  timeoutDuration: 60000,
  version: "2.0.0-inappbrowser",
  platform: "inappbrowser",
};

// 等待遮罩样式
export const WAITING_OVERLAY_STYLES = {
  overlay: 'position:fixed;top:0;left:0;width:100%;height:100%;background:rgba(0,0,0,0.8);color:white;display:flex;flex-direction:column;justify-content:center;align-items:center;z-index:999999;font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif;',
  content: 'text-align:center;',
  icon: 'font-size:48px;margin-bottom:20px;',
  title: 'margin:0 0 10px 0;font-size:20px;',
  message: 'margin:0;opacity:0.8;font-size:14px;',
  hint: 'margin:20px 0 0 0;opacity:0.6;font-size:12px;',
};

// 等待遮罩文本
export const WAITING_OVERLAY_TEXT = {
  icon: '⏳',
  title: '钱包授权需要',
  message: '请切换到钱包应用完成授权',
  hint: '完成后将自动返回',
};

// Provider 通知类型
export const PROVIDER_NOTIFICATION_TYPES = {
  ALREADY_EXISTS: 'SAT20_ALREADY_EXISTS',
  INJECTION_SUCCESS: 'SAT20_INAPPBROWSER_INJECTION_SUCCESS',
} as const;

// 日志前缀
export const LOG_PREFIXES = {
  HIDE_BROWSER: '🙈',
  SHOW_BROWSER: '👁️',
  WALLET_APPROVAL: '🔐',
  DIRECT_REQUEST: '🔍',
  OPEN_DAPP: '🚀',
  INJECT_PROVIDER: '💉',
  MESSAGE_RECEIVED: '📥',
  MESSAGE_SEND: '📤',
  RESPONSE_SENT: '✅',
  ERROR: '❌',
  WARNING: '⚠️',
  SUCCESS: '✅',
  VERIFICATION: '🔍',
  EVENT_LISTENER: '📝',
  EVENT_REMOVE: '🗑️',
  EVENT_EMIT: '📢',
} as const;