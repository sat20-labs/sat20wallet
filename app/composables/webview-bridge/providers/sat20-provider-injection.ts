import { BrowserManager } from "../utils/browser-manager";
import { INAPP_BROWSER_CONFIG, PROVIDER_NOTIFICATION_TYPES, LOG_PREFIXES } from "../constants";

export class Sat20ProviderInjection {
  constructor(private browserManager: BrowserManager) {}

  /**
   * 注入 SAT20 Provider 脚本
   */
  async injectProvider(): Promise<void> {
    if (!this.browserManager.inAppBrowserRef.value) return;

    try {
      console.log(`${LOG_PREFIXES.INJECT_PROVIDER} Injecting SAT20 Provider...`);

      // 先初始化回调存储
      const initResult = this.browserManager.executeScript({
        code: "window.sat20Callbacks = {};",
      });
      // 处理可能不返回 Promise 的情况
      if (initResult && typeof initResult.then === "function") {
        await initResult;
      }

      // 注入简化版的SAT20对象
      const scriptToInject = this.generateProviderScript();

      // 注入脚本
      const injectResult = this.browserManager.executeScript({
        code: scriptToInject,
      });

      let result;
      // 处理可能不返回 Promise 的情况
      if (injectResult && typeof injectResult.then === "function") {
        result = await injectResult;
      } else {
        result = injectResult;
      }

      console.log("✅ SAT20 Provider injected successfully", result);
    } catch (error) {
      console.error(`${LOG_PREFIXES.ERROR} Failed to inject provider:`, error);
      throw error;
    }
  }

  /**
   * 生成 SAT20 Provider 脚本
   */
  private generateProviderScript(): string {
    return `
      (function() {
        if (typeof window.sat20 !== 'undefined') {
          console.log('SAT20 Provider already exists.');
          // 通知原生应用已存在
          try {
            if (window.webkit && window.webkit.messageHandlers && window.webkit.messageHandlers.cordova_iab) {
              window.webkit.messageHandlers.cordova_iab.postMessage(JSON.stringify({
                type: '${PROVIDER_NOTIFICATION_TYPES.ALREADY_EXISTS}',
                methods: Object.keys(window.sat20),
                platform: 'inappbrowser'
              }));
            }
          } catch (e) {
            console.warn('Failed to notify existing provider:', e);
          }
          return;
        }

        console.log('🚀 Injecting SAT20 Provider...');

        // 初始化回调存储
        if (!window.sat20Callbacks) {
          window.sat20Callbacks = {};
        }

        // 初始化事件监听器存储
        if (!window.sat20EventListeners) {
          window.sat20EventListeners = {};
        }

        // 生成回调ID
        function generateCallbackId() {
          return 'iab_sat20_' + Date.now() + '_' + Math.random().toString(36).substr(2, 9);
        }

        // 发送消息到原生应用
        function sendMessage(type, data) {
          const callbackId = generateCallbackId();

          return new Promise(function(resolve, reject) {
            // 存储回调
            window.sat20Callbacks[callbackId] = { resolve, reject, type, timestamp: Date.now() };

            const message = {
              type: type,
              callbackId: callbackId,
              data: data,
              timestamp: Date.now(),
              origin: window.location.origin,
              url: window.location.href
            };

            console.log('📤 Sending message:', { type, callbackId });

            try {
              // iOS和Android的通信方式
              if (window.webkit && window.webkit.messageHandlers && window.webkit.messageHandlers.cordova_iab) {
                window.webkit.messageHandlers.cordova_iab.postMessage(JSON.stringify(message));
              } else {
                console.warn('⚠️ Message handler not found');
                reject(new Error('Message handler not available'));
              }

              // 设置超时
              setTimeout(function() {
                if (window.sat20Callbacks[callbackId]) {
                  delete window.sat20Callbacks[callbackId];
                  reject(new Error('Request timeout: ' + type));
                }
              }, ${INAPP_BROWSER_CONFIG.timeoutDuration});

            } catch (error) {
              delete window.sat20Callbacks[callbackId];
              reject(new Error('Failed to send message: ' + error.message));
            }
          });
        }

        // 创建SAT20对象
        window.sat20 = ${this.generateSat20Methods()};

        // 设置全局标志
        window.sat20Ready = true;
        window.sat20ProviderReady = true;
        window.sat20InAppBrowser = true;

        console.log('✅ SAT20 Provider injected successfully');
        console.log('📋 Available methods:', Object.keys(window.sat20));
        console.log('🚀 Platform: InAppBrowser');
        console.log('⏰ Injection time:', new Date(window.sat20._injectionTime).toISOString());

        // 通知原生应用注入成功
        setTimeout(function() {
          try {
            if (window.webkit && window.webkit.messageHandlers && window.webkit.messageHandlers.cordova_iab) {
              window.webkit.messageHandlers.cordova_iab.postMessage(JSON.stringify({
                type: '${PROVIDER_NOTIFICATION_TYPES.INJECTION_SUCCESS}',
                injectionTime: window.sat20._injectionTime,
                methods: Object.keys(window.sat20),
                version: window.sat20._version,
                platform: window.sat20._platform,
                url: window.location.href,
                origin: window.location.origin
              }));
            }
            console.log('✅ InAppBrowser notified of successful injection');
          } catch (e) {
            console.warn('Failed to notify InAppBrowser of injection success:', e);
          }
        }, 100);

      })();
    `;
  }

  /**
   * 生成 SAT20 对象方法
   */
  private generateSat20Methods(): string {
    return `{
      _injectionTime: Date.now(),
      _version: '${INAPP_BROWSER_CONFIG.version}',
      _platform: '${INAPP_BROWSER_CONFIG.platform}',

      requestAccounts: function() {
        return sendMessage('REQUEST_ACCOUNTS');
      },

      getAccounts: function() {
        return sendMessage('GET_ACCOUNTS');
      },

      getNetwork: function() {
        return sendMessage('GET_NETWORK');
      },

      switchNetwork: function(network) {
        return sendMessage('SWITCH_NETWORK', { network: network });
      },

      getPublicKey: function() {
        return sendMessage('GET_PUBLIC_KEY');
      },

      getBalance: function() {
        return sendMessage('GET_BALANCE');
      },

      sendBitcoin: function(address, amount, options) {
        return sendMessage('SEND_BITCOIN', { address: address, amount: amount, options: options });
      },

      signMessage: function(message, type) {
        return sendMessage('SIGN_MESSAGE', { message: message, type: type });
      },

      signPsbt: function(psbtHex, options) {
        return sendMessage('SIGN_PSBT', { psbtHex: psbtHex, options: options });
      },

      signPsbts: function(psbtHexs, options) {
        return sendMessage('SIGN_PSBTS', { psbtHexs: psbtHexs, options: options });
      },

      pushTx: function(rawtx, options) {
        return sendMessage('PUSH_TX', { rawtx: rawtx, options: options });
      },

      pushPsbt: function(psbtHex, options) {
        return sendMessage('PUSH_PSBT', { psbtHex: psbtHex, options: options });
      },

      getUtxos: function(options) {
        return sendMessage('GET_UTXOS', options);
      },

      getCurrentName: function() {
        return sendMessage('GET_CURRENT_NAME');
      },

      invokeContract: function(contract, method, params, options) {
        return sendMessage('INVOKE_CONTRACT_V2', { contract: contract, method: method, params: params, options: options });
      },

      deployContract: function(bytecode, options) {
        return sendMessage('DEPLOY_CONTRACT_REMOTE', { bytecode: bytecode, options: options });
      },

      getFeeForInvokeContract: function(url, invoke) {
        return sendMessage('GET_FEE_FOR_INVOKE_CONTRACT', { url: url, invoke: invoke });
      },

      getFeeForDeployContract: function(templateName, content, feeRate) {
        return sendMessage('GET_FEE_FOR_DEPLOY_CONTRACT', { templateName: templateName, content: content, feeRate: feeRate });
      },

    
      batchSendAssetsSatsNet: function(assets, options) {
        return sendMessage('BATCH_SEND_ASSETS_SATSNET', { assets: assets, options: options });
      },

      batchSendAssetsV2SatsNet: function(destAddr, assetName, amtList, options) {
        return sendMessage('BATCH_SEND_ASSETS_V2_SATSNET', { destAddr: destAddr, assetName: assetName, amtList: amtList, options: options });
      },

      sendAssetsSatsNet: function(address, assetName, amt, memo, options) {
        return sendMessage('SEND_ASSETS_SATSNET', { address: address, assetName: assetName, amt: amt, memo: memo, options: options });
      },

      splitAsset: function(assetKey, amount, options) {
        return sendMessage('SPLIT_ASSET', { asset_key: assetKey, amount: amount, options: options });
      },

      
      
      
      
      
      deployContract_Remote: function(templateName, content, feeRate, bol, options) {
        return sendMessage('DEPLOY_CONTRACT_REMOTE', { templateName: templateName, content: content, feeRate: feeRate, bol: bol, options: options });
      },

      invokeContractSatsNet: function(url, invoke, feeRate, options) {
        return sendMessage('INVOKE_CONTRACT_SATSNET', { url: url, invoke: invoke, feeRate: feeRate, options: options });
      },

      invokeContract_SatsNet: function(url, invoke, feeRate) {
        return sendMessage('INVOKE_CONTRACT_SATSNET', { url: url, invoke: invoke, feeRate: feeRate });
      },

      invokeContractV2_SatsNet: function(url, invoke, assetName, amt, feeRate, metadata, options) {
        return sendMessage('INVOKE_CONTRACT_V2_SATSNET', { url: url, invoke: invoke, assetName: assetName, amt: amt, feeRate: feeRate, metadata: metadata, options: options });
      },

      registerAsReferrer: function(name, feeRate, options) {
        return sendMessage('REGISTER_AS_REFERRER', { name: name, feeRate: feeRate, options: options });
      },

      bindReferrerForServer: function(referrerName, serverPubKey, options) {
        return sendMessage('BIND_REFERRER_FOR_SERVER', { referrerName: referrerName, serverPubKey: serverPubKey, options: options });
      },

      
      // 事件监听方法
      on: function(event, listener) {
        if (!window.sat20EventListeners[event]) {
          window.sat20EventListeners[event] = [];
        }
        window.sat20EventListeners[event].push(listener);
        console.log('${LOG_PREFIXES.EVENT_LISTENER} Event listener added:', event);
      },

      removeListener: function(event, listener) {
        if (window.sat20EventListeners[event]) {
          const index = window.sat20EventListeners[event].indexOf(listener);
          if (index > -1) {
            window.sat20EventListeners[event].splice(index, 1);
            console.log('${LOG_PREFIXES.EVENT_REMOVE} Event listener removed:', event);
          }
        }
      },

      // 移除所有监听器
      removeAllListeners: function(event) {
        if (event) {
          // 移除特定事件的所有监听器
          if (window.sat20EventListeners[event]) {
            window.sat20EventListeners[event] = [];
            console.log('${LOG_PREFIXES.EVENT_REMOVE} All listeners removed for event:', event);
          }
        } else {
          // 移除所有事件的监听器
          window.sat20EventListeners = {};
          console.log('${LOG_PREFIXES.EVENT_REMOVE} All event listeners removed');
        }
      },

      // 触发事件
      emit: function(event, ...args) {
        if (window.sat20EventListeners[event]) {
          console.log('${LOG_PREFIXES.EVENT_EMIT} Emitting event:', event, args);
          window.sat20EventListeners[event].forEach(function(listener) {
            try {
              listener.apply(null, args);
            } catch (error) {
              console.error('❌ Error in event listener:', error);
            }
          });
        }
      }
    }`;
  }

  /**
   * 验证 Provider 注入是否成功
   */
  async verifyInjection(): Promise<boolean> {
    if (!this.browserManager.inAppBrowserRef.value) return false;

    return new Promise((resolve) => {
      try {
        const verificationCode = `
          (function() {
            try {
              const data = {
                hasSat20: !!window.sat20,
                hasSat20Ready: !!window.sat20Ready,
                hasSat20ProviderReady: !!window.sat20ProviderReady,
                isInAppBrowser: !!window.sat20InAppBrowser,
                methods: window.sat20 ? Object.keys(window.sat20) : [],
                injectionTime: window.sat20 ? window.sat20._injectionTime : null,
                version: window.sat20 ? window.sat20._version : null,
                platform: window.sat20 ? window.sat20._platform : null,
                origin: window.location.origin,
                url: window.location.href,
                userAgent: navigator.userAgent
              };
              return JSON.stringify(data);
            } catch (error) {
              return JSON.stringify({
                error: error.message,
                hasSat20: !!window.sat20,
                hasSat20Ready: !!window.sat20Ready
              });
            }
          })()
        `;

        // 使用回调方式处理 Cordova executeScript
        this.browserManager.executeScript({ code: verificationCode });

        // 设置超时
        setTimeout(() => {
          console.warn("⚠️ Verification timeout, assuming injection failed");
          resolve(false);
        }, 5000);

      } catch (error) {
        console.error("❌ Failed to verify injection:", error);
        resolve(false);
      }
    });
  }
}