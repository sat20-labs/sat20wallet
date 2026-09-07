import { ProviderConfig } from "./types";

// InAppBrowser 配置
export const INAPP_BROWSER_CONFIG: ProviderConfig = {
  inAppBrowserOptions: "location=yes,fullscreen=yes,clearcache=yes,hideurlbar=yes,clearsessioncache=yes,toolbar=no,enableviewportscale=yes,mediaPlaybackRequiresUserAction=no,allowInlineMediaPlayback=yes,keyboardDisplayRequiresUserAction=no,suppressesIncrementalRendering=no",
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
