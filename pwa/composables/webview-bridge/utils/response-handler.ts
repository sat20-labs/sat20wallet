import { BrowserManager } from "./browser-manager";
import { LOG_PREFIXES } from "../constants";

export class ResponseHandler {
  constructor(private browserManager: BrowserManager) {}

  /**
   * 发送响应到 InAppBrowser
   */
  sendResponse(callbackId: string, result: any, error: Error | null): void {
    console.log(`${LOG_PREFIXES.RESPONSE_SENT} sendResponse called with:`, {
      callbackId, result, error, hasBrowserRef: !!this.browserManager.inAppBrowserRef.value
    });

    if (!this.browserManager.inAppBrowserRef.value) {
      console.warn("⚠️ No inAppBrowser reference available for response");
      return;
    }

    // 检查 callbackId 是否有效
    if (!callbackId) {
      console.warn("⚠️ No callbackId provided, skipping response");
      return;
    }

    try {
      // 转义 callbackId 以防止 JavaScript 注入
      const escapedCallbackId = callbackId
        .replace(/'/g, "\\'")
        .replace(/"/g, '\\"');
      console.log("🔧 Escaped callbackId:", escapedCallbackId);

      let responseScript;
      if (error) {
        console.log(`${LOG_PREFIXES.ERROR} Preparing error response:`, { error: error.message });
        responseScript = this.buildErrorResponse(escapedCallbackId, error.message);
      } else {
        console.log(`${LOG_PREFIXES.SUCCESS} Preparing success response:`, { result });
        responseScript = this.buildSuccessResponse(escapedCallbackId, result);
      }

      console.log(`${LOG_PREFIXES.MESSAGE_SEND} Executing script in InAppBrowser for callbackId: ${callbackId}`, {
        hasError: !!error,
        scriptLength: responseScript.length
      });

      // 使用回调方式处理 Cordova executeScript
      this.browserManager.executeScript({ code: responseScript });

      console.log(`${LOG_PREFIXES.SUCCESS} Response sent successfully for callbackId: ${callbackId}`);
    } catch (error) {
      console.error(`${LOG_PREFIXES.ERROR} Error preparing response script:`, error);
      console.error(`${LOG_PREFIXES.ERROR} Error details:`, {
        message: (error as Error).message,
        stack: (error as Error).stack,
        callbackId,
        result,
        hasError: !!error
      });
    }
  }

  /**
   * 构建错误响应脚本
   */
  private buildErrorResponse(callbackId: string, errorMessage: string): string {
    return `
      console.log("🔍 Looking for callback in webview: ${callbackId}");
      console.log("🔍 Available callbacks:", Object.keys(window.sat20Callbacks || {}));
      if (window.sat20Callbacks[${JSON.stringify(callbackId)}]) {
        console.log("✅ Found callback, executing reject");
        const callback = window.sat20Callbacks[${JSON.stringify(callbackId)}];
        callback.reject(new Error(${JSON.stringify(errorMessage)}));
        delete window.sat20Callbacks[${JSON.stringify(callbackId)}];
        console.log("✅ Callback rejected and deleted");
      } else {
        console.error("❌ Callback not found in webview:", ${JSON.stringify(callbackId)});
      }
    `;
  }

  /**
   * 构建成功响应脚本
   */
  private buildSuccessResponse(callbackId: string, result: any): string {
    return `
      console.log("🔍 Looking for callback in webview: ${callbackId}");
      console.log("🔍 Available callbacks:", Object.keys(window.sat20Callbacks || {}));
      if (window.sat20Callbacks[${JSON.stringify(callbackId)}]) {
        console.log("✅ Found callback, executing resolve");
        const callback = window.sat20Callbacks[${JSON.stringify(callbackId)}];
        const resultData = ${JSON.stringify(result)};
        console.log("🔍 Resolving with result:", resultData);
        callback.resolve(resultData);
        delete window.sat20Callbacks[${JSON.stringify(callbackId)}];
        console.log("✅ Callback resolved and deleted");
      } else {
        console.error("❌ Callback not found in webview:", ${JSON.stringify(callbackId)});
      }
    `;
  }
}