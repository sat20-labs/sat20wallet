import { ref } from "vue";
import { InAppBrowserEvent } from "../types";
import { INAPP_BROWSER_CONFIG, WAITING_OVERLAY_STYLES, WAITING_OVERLAY_TEXT, LOG_PREFIXES } from "../constants";

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

export class BrowserManager {
  public inAppBrowserRef = ref<any>(null);
  public isBrowserVisible = ref(true);

  /**
   * 隐藏 InAppBrowser 以便显示原生弹窗
   */
  hideBrowser(): void {
    if (this.inAppBrowserRef.value && this.isBrowserVisible.value) {
      console.log(`${LOG_PREFIXES.HIDE_BROWSER} Hiding InAppBrowser for modal display`);
      try {
        // 在Android上可以使用hide方法
        if (this.inAppBrowserRef.value.hide) {
          this.inAppBrowserRef.value.hide();
        } else {
          // iOS或其他平台的备选方案：显示等待遮罩
          this.injectWaitingOverlay();
        }
        this.isBrowserVisible.value = false;
      } catch (error) {
        console.warn("Failed to hide InAppBrowser:", error);
      }
    }
  }

  /**
   * 显示 InAppBrowser
   */
  showBrowser(): void {
    if (this.inAppBrowserRef.value && !this.isBrowserVisible.value) {
      console.log(`${LOG_PREFIXES.SHOW_BROWSER} Showing InAppBrowser after modal interaction`);
      try {
        // 移除等待遮罩
        this.removeWaitingOverlay();

        // 恢复显示
        if (this.inAppBrowserRef.value.show) {
          this.inAppBrowserRef.value.show();
        }
        this.isBrowserVisible.value = true;
      } catch (error) {
        console.warn("Failed to show InAppBrowser:", error);
      }
    }
  }

  /**
   * 注入等待遮罩
   */
  private injectWaitingOverlay(): void {
    if (!this.inAppBrowserRef.value) return;

    this.inAppBrowserRef.value.executeScript({
      code: `
        if (!document.getElementById('sat20-waiting-overlay')) {
          const overlay = document.createElement('div');
          overlay.id = 'sat20-waiting-overlay';
          overlay.style.cssText = '${WAITING_OVERLAY_STYLES.overlay}';

          const content = document.createElement('div');
          content.style.cssText = '${WAITING_OVERLAY_STYLES.content}';

          const icon = document.createElement('div');
          icon.textContent = '${WAITING_OVERLAY_TEXT.icon}';
          icon.style.cssText = '${WAITING_OVERLAY_STYLES.icon}';

          const title = document.createElement('h2');
          title.textContent = '${WAITING_OVERLAY_TEXT.title}';
          title.style.cssText = '${WAITING_OVERLAY_STYLES.title}';

          const message = document.createElement('p');
          message.textContent = '${WAITING_OVERLAY_TEXT.message}';
          message.style.cssText = '${WAITING_OVERLAY_STYLES.message}';

          const hint = document.createElement('p');
          hint.textContent = '${WAITING_OVERLAY_TEXT.hint}';
          hint.style.cssText = '${WAITING_OVERLAY_STYLES.hint}';

          content.appendChild(icon);
          content.appendChild(title);
          content.appendChild(message);
          content.appendChild(hint);
          overlay.appendChild(content);

          document.body.appendChild(overlay);
        }
      `,
    });
  }

  /**
   * 移除等待遮罩
   */
  private removeWaitingOverlay(): void {
    if (!this.inAppBrowserRef.value) return;

    const hideOverlayResult = this.inAppBrowserRef.value.executeScript({
      code: `
        const overlay = document.getElementById('sat20-waiting-overlay');
        if (overlay) {
          overlay.remove();
        }
      `,
    });

    // 处理可能不返回 Promise 的情况
    if (hideOverlayResult && typeof hideOverlayResult.then === "function") {
      hideOverlayResult
        .then(() => {
          console.log("✅ Overlay removed successfully");
        })
        .catch((err: any) => {
          console.warn("Failed to remove overlay:", err);
        });
    }
  }

  /**
   * 打开 InAppBrowser
   */
  openDApp(url: string): boolean {
    try {
      console.log(`${LOG_PREFIXES.OPEN_DAPP} Opening DApp in InAppBrowser:`, url);

      // 确保Cordova和InAppBrowser插件可用
      if (!window.cordova || !window.cordova.InAppBrowser) {
        throw new Error("Cordova InAppBrowser plugin not available");
      }

      // 打开 InAppBrowser - 使用原生cordova API
      this.inAppBrowserRef.value = window.cordova.InAppBrowser.open(
        url,
        "_blank",
        INAPP_BROWSER_CONFIG.inAppBrowserOptions
      );

      if (!this.inAppBrowserRef.value) {
        throw new Error("Failed to open InAppBrowser");
      }

      console.log("✅ InAppBrowser opened successfully");
      return true;
    } catch (error) {
      console.error(`${LOG_PREFIXES.ERROR} Failed to open DApp in InAppBrowser:`, error);
      return false;
    }
  }

  /**
   * 关闭 InAppBrowser
   */
  close(): void {
    if (this.inAppBrowserRef.value) {
      this.inAppBrowserRef.value.close();
      this.inAppBrowserRef.value = null;
    }
  }

  /**
   * 执行脚本
   */
  executeScript(script: { code: string }): any {
    if (!this.inAppBrowserRef.value) {
      throw new Error("InAppBrowser reference not available");
    }
    return this.inAppBrowserRef.value.executeScript(script);
  }

  /**
   * 设置事件监听器
   */
  setEventListener(event: string, handler: (event: any) => void): void {
    if (this.inAppBrowserRef.value) {
      this.inAppBrowserRef.value.addEventListener(event, handler);
    }
  }
}