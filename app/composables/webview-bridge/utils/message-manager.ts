import { InAppBrowserEvent, HandlerFunction } from "../types";
import { ACTIONS_REQUIRING_ORIGIN_AUTH, PROVIDER_NOTIFICATION_TYPES, LOG_PREFIXES } from "../constants";
import { HandlerFactory } from "../handlers/handler-factory";
import { isOriginAuthorized } from "../../../lib/authorized-origins";
import { ResponseHandler } from "./response-handler";

export class MessageManager {
  private handlers: Record<string, HandlerFunction>;

  constructor(
    private handlerFactory: HandlerFactory,
    private currentUrl: () => string
  ) {
    this.handlers = this.handlerFactory.createHandlers();
  }

  /**
   * 处理来自 InAppBrowser 的消息
   */
  async handleMessage(event: InAppBrowserEvent): Promise<void> {
    try {
      console.log(`${LOG_PREFIXES.MESSAGE_RECEIVED} Received message from InAppBrowser:`, event);

      // 解析消息数据
      let messageData;
      if (event.data?.message) {
        messageData = JSON.parse(event.data.message);
      } else {
        messageData = event.data;
      }

      const { type, callbackId, data } = messageData;

      console.log("📋 Processing message:", { type, callbackId, data });

      // 检查 origin 授权
      const authorized = await this.checkOriginAuthorization(type);
      if (!authorized) {
        // 创建一个临时的 response handler 来发送错误响应
        const tempResponseHandler = new ResponseHandler(this.handlerFactory['browserManager']);
        tempResponseHandler.sendResponse(
          callbackId,
          null,
          new Error("未授权的来源，请先调用 REQUEST_ACCOUNTS 方法")
        );
        return;
      }

      // 查找对应的处理函数
      const handler = this.handlers[type];
      if (handler) {
        console.log(`🎯 Delegating to handler: ${type}`);
        await handler(callbackId, data);
      } else if (this.isProviderNotification(type)) {
        // 处理 provider 注入相关的通知消息，不需要响应
        console.log(`📝 Received provider notification: ${type}`);
        // 这些是通知消息，不需要回调响应，直接忽略
      } else {
        console.warn("⚠️ Unknown message type:", type);
        // 只有当 callbackId 存在时才发送错误响应
        if (callbackId) {
          const tempResponseHandler = new ResponseHandler(this.handlerFactory['browserManager']);
          tempResponseHandler.sendResponse(
            callbackId,
            null,
            new Error(`Unknown message type: ${type}`)
          );
        }
      }
    } catch (error) {
      console.error(`${LOG_PREFIXES.ERROR} Failed to handle InAppBrowser message:`, error);
      // 尝试发送错误响应
      try {
        const callbackId =
          event.data?.callbackId || event.data?.message?.callbackId;
        if (callbackId) {
          const tempResponseHandler = new ResponseHandler(this.handlerFactory['browserManager']);
          tempResponseHandler.sendResponse(callbackId, null, error as Error);
        }
      } catch (responseError) {
        console.error(`${LOG_PREFIXES.ERROR} Failed to send error response:`, responseError);
      }
    }
  }

  /**
   * 检查 origin 授权
   */
  private async checkOriginAuthorization(actionType: string): Promise<boolean> {
    // 如果不是需要授权的操作，直接通过
    if (!ACTIONS_REQUIRING_ORIGIN_AUTH.includes(actionType as any)) {
      return true;
    }

    let origin = "inappbrowser";
    if (this.currentUrl()) {
      try {
        origin = new URL(this.currentUrl()).origin;
      } catch (error) {
        console.warn(
          "⚠️ Failed to parse URL for origin:",
          this.currentUrl(),
          error
        );
        origin = "inappbrowser";
      }
    }

    // 验证 origin 授权
    return await isOriginAuthorized(origin);
  }

  /**
   * 检查是否为 Provider 通知消息
   */
  private isProviderNotification(type: string): boolean {
    return Object.values(PROVIDER_NOTIFICATION_TYPES).includes(type as any);
  }
}