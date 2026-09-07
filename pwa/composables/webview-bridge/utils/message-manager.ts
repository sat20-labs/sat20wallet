import { InAppBrowserEvent, HandlerFunction } from "../types";
import { PROVIDER_NOTIFICATION_TYPES, LOG_PREFIXES } from "../constants";
import { HandlerFactory } from "../handlers/handler-factory";
import { getCurrentDappScope, isDappCapabilityGranted } from "../../../lib/authorized-origins";
import { ResponseHandler } from "./response-handler";
import { getDappActionPolicy } from "../../../lib/dapp-policy";
import { Message } from "../../../types/message";

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
      console.log(`${LOG_PREFIXES.MESSAGE_RECEIVED} Received message from InAppBrowser`);

      // 解析消息数据
      let messageData;
      if (event.data?.message) {
        messageData = JSON.parse(event.data.message);
      } else {
        messageData = event.data;
      }

      const { type, callbackId, data } = messageData;

      console.log("📋 Processing message:", { type, callbackId });

      // 检查 origin 授权
      const authorization = await this.checkOriginAuthorization(type);
      if (!authorization.authorized) {
        // 创建一个临时的 response handler 来发送错误响应
        const tempResponseHandler = new ResponseHandler(this.handlerFactory['browserManager']);
        tempResponseHandler.sendResponse(
          callbackId,
          null,
          new Error(authorization.error ?? "DApp capability is not granted")
        );
        return;
      }

      // 查找对应的处理函数
      const handler = this.handlers[type];
      if (handler) {
        console.log(`🎯 Delegating to handler: ${type}`);
		let requestOrigin = "";
		try {
		  requestOrigin = new URL(this.currentUrl()).origin;
		} catch {
		  throw new Error("Invalid DApp request origin");
		}
		const scopedData = {
		  ...(data ?? {}),
		  ...(authorization.owner ? { __dappOwner: authorization.owner } : {}),
		  __dappContext: {
			origin: requestOrigin,
			timestamp: String(messageData.timestamp ?? ''),
			nonce: String(callbackId ?? ''),
		  },
		};
        await handler(callbackId, scopedData);
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
  private async checkOriginAuthorization(actionType: string): Promise<{
    authorized: boolean;
    error?: string;
    owner?: Record<string, unknown>;
  }> {
    const policy = getDappActionPolicy(actionType);
    if (!policy) return { authorized: false, error: `Unsupported DApp action: ${actionType}` };
    let origin = "";
    if (this.currentUrl()) {
      try {
        origin = new URL(this.currentUrl()).origin;
      } catch {
        return { authorized: false, error: "Invalid DApp origin" };
      }
    }
    if (!origin) return { authorized: false, error: "Missing DApp origin" };
    if (actionType === Message.MessageAction.REQUEST_ACCOUNTS) return { authorized: true };
    const scope = await getCurrentDappScope();
    if (!policy.capability || !await isDappCapabilityGranted(origin, scope, policy.capability)) {
      return { authorized: false, error: `DApp capability is not granted for ${actionType}` };
    }
    if ([
      Message.MessageAction.LOCK_UTXO,
      Message.MessageAction.LOCK_UTXO_SATSNET,
      Message.MessageAction.UNLOCK_UTXO,
      Message.MessageAction.UNLOCK_UTXO_SATSNET,
    ].includes(actionType as Message.MessageAction)) {
      return {
        authorized: true,
        owner: {
          origin,
          network: scope.network,
          wallet_fingerprint: scope.walletFingerprint,
          account_index: scope.accountIndex,
        },
      };
    }
    return { authorized: true };
  }

  /**
   * 检查是否为 Provider 通知消息
   */
  private isProviderNotification(type: string): boolean {
    return Object.values(PROVIDER_NOTIFICATION_TYPES).includes(type as any);
  }
}
