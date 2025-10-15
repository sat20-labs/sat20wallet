import { ref, readonly } from "vue";
import { InjectionStatus, WebViewBridgeState } from "./webview-bridge/types";
import { BrowserManager } from "./webview-bridge/utils/browser-manager";
import { ResponseHandler } from "./webview-bridge/utils/response-handler";
import { ApprovalHandler } from "./webview-bridge/utils/approval-handler";
import { MessageManager } from "./webview-bridge/utils/message-manager";
import { Sat20ProviderInjection } from "./webview-bridge/providers/sat20-provider-injection";
import { HandlerFactory } from "./webview-bridge/handlers/handler-factory";
import { LOG_PREFIXES } from "./webview-bridge/constants";

// 使用原生cordova接口
declare global {
  interface Window {
    cordova: {
      InAppBrowser: {
        open: (url: string, target: string, options: string) => any;
      };
    };
  }
}

export function useWebViewBridge() {
  // 状态管理
  const isReady = ref(false);
  const injectionStatus = ref<InjectionStatus>("idle");
  const lastError = ref<string | null>(null);
  const currentUrl = ref<string>("");

  // 存储回调函数
  const sat20Callbacks = ref<Record<string, Function>>({});

  // 创建核心管理器实例
  const browserManager = new BrowserManager();
  const responseHandler = new ResponseHandler(browserManager);
  const approvalHandler = new ApprovalHandler(browserManager);
  const handlerFactory = new HandlerFactory(
    approvalHandler,
    responseHandler,
    () => currentUrl.value,
    browserManager
  );
  const messageManager = new MessageManager(handlerFactory, () => currentUrl.value);
  const providerInjection = new Sat20ProviderInjection(browserManager);

  /**
   * 设置 InAppBrowser 事件监听器
   */
  function setupEventListeners(): void {
    if (!browserManager.inAppBrowserRef.value) return;

    // 监听页面加载完成事件
    browserManager.setEventListener("loadstop", (event: any) => {
      console.log("📄 Page loaded:", event.url);
      injectProvider();
    });

    // 监听来自 InAppBrowser 的消息
    browserManager.setEventListener("message", (event: any) => {
      messageManager.handleMessage(event);
    });

    // 监听错误事件
    browserManager.setEventListener("loaderror", (event: any) => {
      console.error(`${LOG_PREFIXES.ERROR} Page load error:`, event);
      lastError.value = `Failed to load page: ${event.url}`;
      injectionStatus.value = "failed";
    });

    // 监听关闭事件
    browserManager.setEventListener("exit", () => {
      console.log("🚪 InAppBrowser closed");
      cleanup();
    });
  }

  /**
   * 打开 InAppBrowser 并注入 SAT20 Provider
   */
  async function openDApp(url: string): Promise<boolean> {
    try {
      console.log(`${LOG_PREFIXES.OPEN_DAPP} Opening DApp in InAppBrowser:`, url);

      currentUrl.value = url;
      injectionStatus.value = "injecting";
      lastError.value = null;

      // 打开 InAppBrowser
      const success = browserManager.openDApp(url);
      if (!success) {
        throw new Error("Failed to open InAppBrowser");
      }

      // 设置事件监听器
      setupEventListeners();

      console.log("✅ InAppBrowser opened successfully");
      return true;
    } catch (error) {
      injectionStatus.value = "failed";
      lastError.value = error instanceof Error ? error.message : String(error);
      console.error(`${LOG_PREFIXES.ERROR} Failed to open DApp in InAppBrowser:`, error);
      return false;
    }
  }

  /**
   * 注入 SAT20 Provider
   */
  async function injectProvider(): Promise<void> {
    try {
      await providerInjection.injectProvider();

      // 等待一下让注入完成
      setTimeout(async () => {
        const success = await verifyInjection();
        if (success) {
          injectionStatus.value = "success";
          isReady.value = true;
          console.log("🎉 Provider injection verified successfully");
        } else {
          injectionStatus.value = "failed";
          lastError.value = "注入验证失败";
          console.error("❌ Provider injection verification failed");
        }
      }, 1000);
    } catch (error) {
      injectionStatus.value = "failed";
      lastError.value = error instanceof Error ? error.message : String(error);
      console.error(`${LOG_PREFIXES.ERROR} Failed to inject provider:`, error);
    }
  }

  /**
   * 验证 Provider 注入是否成功
   */
  async function verifyInjection(): Promise<boolean> {
    return await providerInjection.verifyInjection();
  }

  /**
   * 关闭 InAppBrowser
   */
  function close(): void {
    browserManager.close();
    cleanup();
  }

  /**
   * 清理资源
   */
  function cleanup(): void {
    isReady.value = false;
    injectionStatus.value = "idle";
    lastError.value = null;
    currentUrl.value = "";

    // 清理回调
    for (const key in sat20Callbacks.value) {
      delete sat20Callbacks.value[key];
    }
  }

  // 兼容性：为了保持与原有代码的接口一致，提供一些额外的内部方法
  const internalAPI = {
    // 允许外部访问内部管理器（用于测试或高级用法）
    _browserManager: browserManager,
    _responseHandler: responseHandler,
    _approvalHandler: approvalHandler,
    _messageManager: messageManager,
    _providerInjection: providerInjection,
    _handlerFactory: handlerFactory,

    // 内部状态访问
    _state: {
      sat20Callbacks: readonly(sat20Callbacks),
      isBrowserVisible: readonly(browserManager.isBrowserVisible),
    },
  };

  return {
    // 状态
    isReady: readonly(isReady),
    injectionStatus: readonly(injectionStatus),
    lastError: readonly(lastError),
    currentUrl: readonly(currentUrl),

    // 方法
    openDApp,
    close,
    cleanup,
    verifyInjection,

    // 内部 API（用于高级用法和测试）
    ...internalAPI,
  };
}

// 导出类型和常量，保持向后兼容
export type {
  WebViewBridgeState,
  InjectionStatus,
  InAppBrowserEvent,
  ApprovalMetadata,
  MessageHandlerMap,
} from "./webview-bridge/types";

export {
  ACTIONS_REQUIRING_ORIGIN_AUTH,
  INAPP_BROWSER_CONFIG,
  PROVIDER_NOTIFICATION_TYPES,
  LOG_PREFIXES,
} from "./webview-bridge/constants";

// 导出管理器类，供高级用户使用
export {
  BrowserManager,
  ResponseHandler,
  ApprovalHandler,
  MessageManager,
  Sat20ProviderInjection,
  HandlerFactory,
} from "./webview-bridge";