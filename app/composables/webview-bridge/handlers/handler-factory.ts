import { Message } from "../../../types/message";
import { ApprovalHandler } from "../utils/approval-handler";
import { ResponseHandler } from "../utils/response-handler";
import { AccountHandlers } from "./account-handlers";
import { TransactionHandlers } from "./transaction-handlers";
import { AssetHandlers } from "./asset-handlers";
import { ContractHandlers } from "./contract-handlers";
import { UtilityHandlers } from "./utility-handlers";
import { MessageHandlerMap } from "../types";

export class HandlerFactory {
  private accountHandlers: AccountHandlers;
  private transactionHandlers: TransactionHandlers;
  private assetHandlers: AssetHandlers;
  private contractHandlers: ContractHandlers;
  private utilityHandlers: UtilityHandlers;

  constructor(
    private approvalHandler: ApprovalHandler,
    private responseHandler: ResponseHandler,
    private currentUrl: () => string,
    public browserManager: any // 公开 browserManager 供 MessageManager 使用
  ) {
    this.accountHandlers = new AccountHandlers(approvalHandler, responseHandler, currentUrl);
    this.transactionHandlers = new TransactionHandlers(approvalHandler, responseHandler, currentUrl);
    this.assetHandlers = new AssetHandlers(approvalHandler, responseHandler, currentUrl);
    this.contractHandlers = new ContractHandlers(approvalHandler, responseHandler, currentUrl);
    this.utilityHandlers = new UtilityHandlers(approvalHandler, responseHandler, currentUrl);
  }

  /**
   * 创建所有消息处理器的映射
   */
  createHandlers(): MessageHandlerMap {
    return {
      // 账户相关处理器
      REQUEST_ACCOUNTS: (callbackId, data) => this.accountHandlers.handleRequestAccounts(callbackId, data),
      GET_ACCOUNTS: (callbackId, data) => this.accountHandlers.handleGetAccounts(callbackId, data),
      GET_PUBLIC_KEY: (callbackId, data) => this.accountHandlers.handleGetPublicKey(callbackId, data),
      GET_BALANCE: (callbackId, data) => this.accountHandlers.handleGetBalance(callbackId, data),
      SWITCH_NETWORK: (callbackId, data) => this.accountHandlers.handleSwitchNetwork(callbackId, data),
      GET_NETWORK: (callbackId, data) => this.accountHandlers.handleGetNetwork(callbackId, data),

      // 交易相关处理器
      SIGN_MESSAGE: (callbackId, data) => this.transactionHandlers.handleSignMessage(callbackId, data),
      SIGN_PSBT: (callbackId, data) => this.transactionHandlers.handleSignPsbt(callbackId, data),
      SIGN_PSBTS: (callbackId, data) => this.transactionHandlers.handleSignPsbts(callbackId, data),
      SEND_BITCOIN: (callbackId, data) => this.transactionHandlers.handleSendBitcoin(callbackId, data),
      SEND_INSCRIPTION: (callbackId, data) => this.transactionHandlers.handleSendInscription(callbackId, data),
      PUSH_TX: (callbackId, data) => this.transactionHandlers.handlePushTx(callbackId, data),
      PUSH_PSBT: (callbackId, data) => this.transactionHandlers.handlePushPsbt(callbackId, data),
      GET_INSCRIPTIONS: (callbackId, data) => this.transactionHandlers.handleGetInscriptions(callbackId, data),

      // 资产相关处理器
      BATCH_SEND_ASSETS_SATSNET: (callbackId, data) => this.assetHandlers.handleBatchSendAssetsSatsNet(callbackId, data),
      BATCH_SEND_ASSETS_V2_SATSNET: (callbackId, data) => this.assetHandlers.handleBatchSendAssetsV2SatsNet(callbackId, data),
      SEND_ASSETS_SATSNET: (callbackId, data) => this.assetHandlers.handleSendAssetsSatsNet(callbackId, data),
      SPLIT_ASSET: (callbackId, data) => this.assetHandlers.handleSplitAsset(callbackId, data),
      GET_UTXOS_WITH_ASSET: (callbackId, data) => this.assetHandlers.handleGetUtxosWithAsset(callbackId, data),
      GET_UTXOS_WITH_ASSET_SATSNET: (callbackId, data) => this.assetHandlers.handleGetUtxosWithAssetSatsNet(callbackId, data),
      GET_UTXOS_WITH_ASSET_V2: (callbackId, data) => this.assetHandlers.handleGetUtxosWithAssetV2(callbackId, data),
      GET_UTXOS_WITH_ASSET_V2_SATSNET: (callbackId, data) => this.assetHandlers.handleGetUtxosWithAssetV2SatsNet(callbackId, data),
      GET_ASSET_AMOUNT: (callbackId, data) => this.assetHandlers.handleGetAssetAmount(callbackId, data),
      GET_ASSET_AMOUNT_SATSNET: (callbackId, data) => this.assetHandlers.handleGetAssetAmountSatsNet(callbackId, data),
      LOCK_UTXO: (callbackId, data) => this.assetHandlers.handleLockUtxo(callbackId, data),
      UNLOCK_UTXO: (callbackId, data) => this.assetHandlers.handleUnlockUtxo(callbackId, data),
      GET_ALL_LOCKED_UTXO: (callbackId, data) => this.assetHandlers.handleGetAllLockedUtxo(callbackId, data),

      // 合约相关处理器
      DEPLOY_CONTRACT_REMOTE: (callbackId, data) => this.contractHandlers.handleDeployContractRemote(callbackId, data),
      INVOKE_CONTRACT_V2: (callbackId, data) => this.contractHandlers.handleInvokeContractV2(callbackId, data),
      INVOKE_CONTRACT_SATSNET: (callbackId, data) => this.contractHandlers.handleInvokeContractSatsNet(callbackId, data),
      INVOKE_CONTRACT_V2_SATSNET: (callbackId, data) => this.contractHandlers.handleInvokeContractV2SatsNet(callbackId, data),
      GET_FEE_FOR_DEPLOY_CONTRACT: (callbackId, data) => this.contractHandlers.handleGetFeeForDeployContract(callbackId, data),
      GET_FEE_FOR_INVOKE_CONTRACT: (callbackId, data) => this.contractHandlers.handleGetFeeForInvokeContract(callbackId, data),
      QUERY_PARAM_FOR_INVOKE_CONTRACT: (callbackId, data) => this.contractHandlers.handleQueryParamForInvokeContract(callbackId, data),

      // 工具相关处理器
      REGISTER_AS_REFERRER: (callbackId, data) => this.utilityHandlers.handleRegisterAsReferrer(callbackId, data),
      BIND_REFERRER_FOR_SERVER: (callbackId, data) => this.utilityHandlers.handleBindReferrerForServer(callbackId, data),
      BUILD_BATCH_SELL_ORDER: (callbackId, data) => this.utilityHandlers.handleBuildBatchSellOrder(callbackId, data),
      SPLIT_BATCH_SIGNED_PSBT_SATSNET: (callbackId, data) => this.utilityHandlers.handleSplitBatchSignedPsbtSatsNet(callbackId, data),
      FINALIZE_SELL_ORDER: (callbackId, data) => this.utilityHandlers.handleFinalizeSellOrder(callbackId, data),
      MERGE_BATCH_SIGNED_PSBT: (callbackId, data) => this.utilityHandlers.handleMergeBatchSignedPsbt(callbackId, data),
      ADD_INPUTS_TO_PSBT: (callbackId, data) => this.utilityHandlers.handleAddInputsToPsbt(callbackId, data),
      ADD_OUTPUTS_TO_PSBT: (callbackId, data) => this.utilityHandlers.handleAddOutputsToPsbt(callbackId, data),
      EXTRACT_TX_FROM_PSBT: (callbackId, data) => this.utilityHandlers.handleExtractTxFromPsbt(callbackId, data),
      EXTRACT_TX_FROM_PSBT_SATSNET: (callbackId, data) => this.utilityHandlers.handleExtractTxFromPsbtSatsNet(callbackId, data),
      GET_CURRENT_NAME: (callbackId, data) => this.utilityHandlers.handleGetCurrentName(callbackId, data),
      GET_UTXOS: (callbackId, data) => this.utilityHandlers.handleGetUtxos(callbackId, data),
      GET_UTXOS_SATSNET: (callbackId, data) => this.utilityHandlers.handleGetUtxosSatsNet(callbackId, data),
    };
  }
}