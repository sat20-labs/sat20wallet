import { Message } from "../../../types/message";
import { ApprovalHandler } from "../utils/approval-handler";
import { ResponseHandler } from "../utils/response-handler";
import { getCurrentDappScope } from "../../../lib/authorized-origins";
import { buildWalletMessagePayload, walletMessageDigest } from "../../../lib/wallet-message-domain";

export class TransactionHandlers {
  constructor(
    private approvalHandler: ApprovalHandler,
    private responseHandler: ResponseHandler,
    private currentUrl: () => string
  ) {}

  /**
   * 处理 SIGN_MESSAGE - 需要用户授权
   */
  async handleSignMessage(callbackId: string, data: any): Promise<void> {
    try {
	  const context = data?.__dappContext;
	  const scope = await getCurrentDappScope();
	  const domainPayload = buildWalletMessagePayload({
		network: scope.network,
		origin: String(context?.origin ?? ''),
		timestamp: String(context?.timestamp ?? ''),
		nonce: String(context?.nonce ?? ''),
		message: String(data?.message ?? ''),
	  });
      const result = await this.approvalHandler.handleWalletApproval(
        Message.MessageAction.SIGN_MESSAGE,
		{
		  message: String(data?.message ?? ''),
		  domainPayload,
		  digest: await walletMessageDigest(domainPayload),
		  origin: context.origin,
		  network: scope.network,
		  timestamp: context.timestamp,
		  nonce: context.nonce,
		},
        callbackId,
        this.currentUrl()
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ SIGN_MESSAGE error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }

  /**
   * 处理 SIGN_DATA - 签原始协议数据，需要用户授权
   */
  async handleSignData(callbackId: string, data: any): Promise<void> {
	this.responseHandler.sendResponse(
	  callbackId,
	  null,
	  new Error("Raw DApp signData is disabled; use signMessage with the SAT20 Wallet Message domain"),
	);
  }

  /**
   * 处理 SIGN_PSBT - 需要用户授权
   */
  async handleSignPsbt(callbackId: string, data: any): Promise<void> {
    try {
		console.log("📝 Handling SIGN_PSBT", { callbackId });
      const result = await this.approvalHandler.handleWalletApproval(
        Message.MessageAction.SIGN_PSBT,
        data,
        callbackId,
        this.currentUrl()
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ SIGN_PSBT error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }

  /**
   * 处理 SIGN_PSBTS - 需要用户授权
   */
  async handleSignPsbts(callbackId: string, data: any): Promise<void> {
    try {
		console.log("📝 Handling SIGN_PSBTS", { callbackId });
      const result = await this.approvalHandler.handleWalletApproval(
        Message.MessageAction.SIGN_PSBTS,
        data,
        callbackId,
        this.currentUrl()
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ SIGN_PSBTS error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }

  /**
   * 处理 SEND_BITCOIN - 需要用户授权
   */
  async handleSendBitcoin(callbackId: string, data: any): Promise<void> {
    try {
      console.log("💸 Handling SEND_BITCOIN", { callbackId });
      const result = await this.approvalHandler.handleWalletApproval(
        Message.MessageAction.SEND_BITCOIN,
        data,
        callbackId,
        this.currentUrl()
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ SEND_BITCOIN error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }

  /**
   * 处理 SEND_INSCRIPTION - 需要用户授权
   */
  async handleSendInscription(callbackId: string, data: any): Promise<void> {
    try {
      console.log("📤 Handling SEND_INSCRIPTION", { callbackId });
      const result = await this.approvalHandler.handleWalletApproval(
        Message.MessageAction.SEND_INSCRIPTION,
        data,
        callbackId,
        this.currentUrl()
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ SEND_INSCRIPTION error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }

  /** 处理 PUSH_TX - 广播前逐次授权 */
  async handlePushTx(callbackId: string, data: any): Promise<void> {
    try {
      console.log("📤 Handling PUSH_TX", { callbackId });
      const result = await this.approvalHandler.handleApprovedDirectRequest(
        Message.MessageAction.PUSH_TX, data, callbackId, this.currentUrl()
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ PUSH_TX error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }

  /** 处理 PUSH_PSBT - 广播前逐次授权 */
  async handlePushPsbt(callbackId: string, data: any): Promise<void> {
    try {
		console.log("📤 Handling PUSH_PSBT", { callbackId });
      const result = await this.approvalHandler.handleApprovedDirectRequest(
        Message.MessageAction.PUSH_PSBT, data, callbackId, this.currentUrl()
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ PUSH_PSBT error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }

  /**
   * 处理 GET_INSCRIPTIONS - 直接请求类型
   */
  async handleGetInscriptions(callbackId: string, data: any): Promise<void> {
    try {
      console.log("📜 Handling GET_INSCRIPTIONS", { callbackId });
      const result = await this.approvalHandler.handleDirectRequest(
        Message.MessageAction.GET_INSCRIPTIONS,
        data
      );
      this.responseHandler.sendResponse(callbackId, result, null);
    } catch (error) {
      console.error("❌ GET_INSCRIPTIONS error:", error);
      this.responseHandler.sendResponse(callbackId, null, error as Error);
    }
  }
}
