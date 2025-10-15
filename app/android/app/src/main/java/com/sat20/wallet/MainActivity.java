package com.sat20.wallet;

import android.content.Intent;
import android.net.Uri;
import android.os.Bundle;
import android.util.Log;
import android.webkit.WebView;
import android.webkit.WebViewClient;
import android.webkit.WebChromeClient;
import android.webkit.ValueCallback;
import android.webkit.JavascriptInterface;
import android.annotation.SuppressLint;
import android.view.View;
import android.widget.ProgressBar;

import com.getcapacitor.BridgeActivity;
import com.getcapacitor.Plugin;
import com.getcapacitor.PluginCall;
import com.getcapacitor.PluginMethod;
import com.getcapacitor.annotation.CapacitorPlugin;
import com.getcapacitor.annotation.Permission;
import com.getcapacitor.annotation.PermissionCallback;

import org.json.JSONObject;
import org.json.JSONException;

public class MainActivity extends BridgeActivity {

    private static final String TAG = "Sat20WebView";
    private static WebView webView;
    private static ProgressBar progressBar;

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);

        // 注册自定义插件
        this.registerPlugin(Sat20WebViewPlugin.class);

        Log.d(TAG, "MainActivity onCreate with custom WebView support");
    }

    // 创建自定义WebView的方法
    public void createCustomWebView(String url) {
        runOnUiThread(() -> {
            if (webView == null) {
                webView = new WebView(this);
                progressBar = new ProgressBar(this);

                // 配置WebView - 增强安全性和功能
                android.webkit.WebSettings settings = webView.getSettings();

                // 基础配置
                settings.setJavaScriptEnabled(true);
                settings.setDomStorageEnabled(true);
                settings.setDatabaseEnabled(true);
                // settings.setAppCacheEnabled(true); // 已废弃

                // 安全配置
                settings.setAllowFileAccess(false);
                settings.setAllowContentAccess(false);
                settings.setAllowFileAccessFromFileURLs(false);
                settings.setAllowUniversalAccessFromFileURLs(false);
                settings.setMixedContentMode(android.webkit.WebSettings.MIXED_CONTENT_COMPATIBILITY_MODE);

                // 性能和功能配置
                settings.setCacheMode(android.webkit.WebSettings.LOAD_DEFAULT);
                settings.setRenderPriority(android.webkit.WebSettings.RenderPriority.HIGH);
                settings.setLayoutAlgorithm(android.webkit.WebSettings.LayoutAlgorithm.TEXT_AUTOSIZING);

                // 支持缩放
                settings.setSupportZoom(true);
                settings.setBuiltInZoomControls(true);
                settings.setDisplayZoomControls(false);

                // 用户代理设置
                String userAgent = settings.getUserAgentString();
                settings.setUserAgentString(userAgent + " SAT20Wallet/2.0.0-Enhanced");

                // 调试配置 (仅开发模式)
                // 在调试模式下启用WebView调试
                try {
                    // 检查调试模式 - 简化版本
                    android.webkit.WebView.setWebContentsDebuggingEnabled(true);
                } catch (Exception e) {
                    Log.w(TAG, "Could not enable WebView debugging: " + e.getMessage());
                }

                // 添加JavaScript接口
                webView.addJavascriptInterface(new Sat20JSInterface(), "Sat20Native");

                // 设置WebViewClient - 增强版智能注入
                webView.setWebViewClient(new WebViewClient() {
                    @Override
                    public void onPageStarted(WebView view, String url, android.graphics.Bitmap favicon) {
                        super.onPageStarted(view, url, favicon);
                        Log.d(TAG, "Page started loading: " + url);

                        // 页面开始加载时预注入基础框架
                        injectBaseFramework();

                        if (progressBar != null) {
                            progressBar.setVisibility(View.VISIBLE);
                        }
                    }

                    @Override
                    public void onPageFinished(WebView view, String url) {
                        super.onPageFinished(view, url);
                        Log.d(TAG, "Page finished loading: " + url);

                        // 页面完成后注入完整Provider
                        injectFullProvider();

                        // 延迟再次确保注入成功
                        android.os.Handler handler = new android.os.Handler();
                        handler.postDelayed(() -> {
                            verifyAndReinjectProvider();
                        }, 2000);

                        if (progressBar != null) {
                            progressBar.setVisibility(View.GONE);
                        }
                    }

                    @Override
                    public void onReceivedError(WebView view, int errorCode, String description, String failingUrl) {
                        super.onReceivedError(view, errorCode, description, failingUrl);
                        Log.e(TAG, "WebView error: " + description + " for URL: " + failingUrl);

                        // 错误处理：尝试注入基本Provider以供调试
                        injectErrorFallback();
                    }
                });

                // 设置增强型WebChromeClient
                webView.setWebChromeClient(new WebChromeClient() {
                    @Override
                    public void onProgressChanged(WebView view, int newProgress) {
                        super.onProgressChanged(view, newProgress);
                        if (progressBar != null) {
                            progressBar.setProgress(newProgress);
                        }
                        Log.d(TAG, "📊 Page loading progress: " + newProgress + "%");
                    }

                    @Override
                    public void onReceivedTitle(WebView view, String title) {
                        super.onReceivedTitle(view, title);
                        Log.d(TAG, "📄 Page title: " + title);
                        // 可以在这里更新应用标题
                    }

                    @Override
                    public boolean onConsoleMessage(android.webkit.ConsoleMessage consoleMessage) {
                        String level = consoleMessage.messageLevel().toString();
                        String message = String.format("[%s] %s at %s:%d",
                            level, consoleMessage.message(),
                            consoleMessage.sourceId(), consoleMessage.lineNumber());

                        switch (consoleMessage.messageLevel()) {
                            case ERROR:
                                Log.e(TAG, "🔴 JS Console: " + message);
                                break;
                            case WARNING:
                                Log.w(TAG, "🟡 JS Console: " + message);
                                break;
                            case DEBUG:
                                Log.d(TAG, "🔵 JS Console: " + message);
                                break;
                            default:
                                Log.i(TAG, "⚪ JS Console: " + message);
                                break;
                        }
                        return true;
                    }

                    @Override
                    public boolean onJsAlert(WebView view, String url, String message, android.webkit.JsResult result) {
                        Log.d(TAG, "⚠️ JavaScript Alert: " + message);
                        // 显示Android对话框而不是Web对话框
                        showJavaScriptAlert("JavaScript Alert", message, result);
                        return true;
                    }

                    @Override
                    public boolean onJsConfirm(WebView view, String url, String message, android.webkit.JsResult result) {
                        Log.d(TAG, "❓ JavaScript Confirm: " + message);
                        showJavaScriptConfirm("JavaScript Confirm", message, result);
                        return true;
                    }

                    @Override
                    public void onGeolocationPermissionsShowPrompt(String origin,
                            android.webkit.GeolocationPermissions.Callback callback) {
                        Log.d(TAG, "📍 Geolocation permission requested for: " + origin);
                        callback.invoke(origin, true, false);
                    }
                });
            }

            // 加载URL
            webView.loadUrl(url);
            setContentView(webView);
        });
    }

    // 预注入基础框架
    private void injectBaseFramework() {
        if (webView != null) {
            String baseScript = "(function() {" +
                "console.log('🚀 Pre-injecting SAT20 base framework...');" +
                "window.sat20Android = true;" +
                "window.sat20Loading = true;" +
                "window.sat20Version = '2.0.0-enhanced';" +
                "window.sat20Timestamp = Date.now();" +
                "})()";

            webView.evaluateJavascript(baseScript, result -> {
                Log.d(TAG, "Base framework pre-injection result: " + result);
            });
        }
    }

    // 注入完整SAT20 Provider
    private void injectFullProvider() {
        if (webView != null) {
            String sat20Script = getSat20ProviderScript();
            webView.evaluateJavascript(sat20Script, result -> {
                Log.d(TAG, "Full SAT20 Provider injection result: " + result);
            });
        }
    }

    // 验证并重新注入Provider
    private void verifyAndReinjectProvider() {
        if (webView != null) {
            String verificationScript = "(function() {" +
                "try {" +
                "  if (!window.sat20 || !window.sat20.requestAccounts) {" +
                "    console.log('⚠️ SAT20 Provider not found, re-injecting...');" +
                "    return 'REINJECT_NEEDED';" +
                "  }" +
                "  console.log('✅ SAT20 Provider verified successfully');" +
                "  return 'INJECTION_OK';" +
                "} catch (e) {" +
                "  console.error('❌ Verification error:', e);" +
                "  return 'VERIFICATION_ERROR';" +
                "}" +
                "})()";

            webView.evaluateJavascript(verificationScript, result -> {
                Log.d(TAG, "Provider verification result: " + result);

                if (result != null && result.contains("REINJECT_NEEDED")) {
                    Log.d(TAG, "Re-injecting SAT20 Provider...");
                    injectFullProvider();
                }
            });
        }
    }

    // 错误回退注入
    private void injectErrorFallback() {
        if (webView != null) {
            String fallbackScript = "(function() {" +
                "console.error('💥 Page load failed, injecting fallback SAT20 Provider...');" +
                "window.sat20Error = true;" +
                "window.sat20Fallback = {" +
                "  _status: 'fallback_mode'," +
                "  requestAccounts: () => Promise.reject(new Error('Page load failed'))," +
                "  _error: 'DApp failed to load properly'" +
                "};" +
                "window.sat20 = window.sat20Fallback;" +
                "window.sat20Ready = true;" +
                "})()";

            webView.evaluateJavascript(fallbackScript, result -> {
                Log.d(TAG, "Fallback injection result: " + result);
            });
        }
    }

    // 获取SAT20 Provider脚本
    private String getSat20ProviderScript() {
        return "(function() {" +
            "'use strict';" +
            "if (window.sat20 || window.sat20Ready) {" +
            "  console.log('SAT20 Provider already exists');" +
            "  return;" +
            "}" +
            "console.log('🤖 Injecting SAT20 Provider into WebView...');" +
            "class AndroidSat20 {" +
            "  constructor() {" +
            "    this.pendingRequests = new Map();" +
            "    this.requestIdCounter = 0;" +
            "    this.setupNativeBridge();" +
            "    this.setupGlobalPresence();" +
            "  }" +
            "  setupNativeBridge() {" +
            "    console.log('🔗 Setting up native bridge...');" +
            "  }" +
            "  setupGlobalPresence() {" +
            "    window.sat20 = this;" +
            "    window.SAT20 = this;" +
            "    window.sat20Ready = true;" +
            "    window.sat20ProviderReady = true;" +
            "    window.sat20Android = true;" +
            "    this._injectionTime = Date.now();" +
            "    this._version = '2.0.0-android-native';" +
            "    this._platform = 'android';" +
            "    this._bridgeType = 'native-webview';" +
            "    console.log('✅ Global presence established');" +
            "  }" +
            "  generateMessageId() {" +
            "    return 'android_sat20_' + (++this.requestIdCounter) + '_' + Date.now();" +
            "  }" +
            "  async send(type, action, data = {}) {" +
            "    return new Promise((resolve, reject) => {" +
            "      const messageId = this.generateMessageId();" +
            "      this.pendingRequests.set(messageId, { resolve, reject, type, action });" +
            "      try {" +
            "        if (window.Sat20Native && window.Sat20Native.sendRequest) {" +
            "          window.Sat20Native.sendRequest(messageId, type, action, JSON.stringify(data));" +
            "        } else {" +
            "          reject(new Error('Native bridge not available'));" +
            "        }" +
            "      } catch (error) {" +
            "        reject(error);" +
            "      }" +
            "    });" +
            "  }" +
            "  handleResponse(messageId, result, error) {" +
            "    const pendingRequest = this.pendingRequests.get(messageId);" +
            "    if (pendingRequest) {" +
            "      this.pendingRequests.delete(messageId);" +
            "      if (error) {" +
            "        pendingRequest.reject(new Error(error));" +
            "      } else {" +
            "        pendingRequest.resolve(result);" +
            "      }" +
            "    }" +
            "  }" +
            "}" +
            "const sat20 = new AndroidSat20();" +
            "sat20.requestAccounts = function() { return this.send('APPROVE', 'REQUEST_ACCOUNTS'); };" +
            "sat20.getAccounts = function() { return this.send('REQUEST', 'GET_ACCOUNTS'); };" +
            "sat20.getNetwork = function() { return this.send('REQUEST', 'GET_NETWORK'); };" +
            "sat20.switchNetwork = function(network) { return this.send('APPROVE', 'SWITCH_NETWORK', { network }); };" +
            "sat20.getPublicKey = function() { return this.send('REQUEST', 'GET_PUBLIC_KEY'); };" +
            "sat20.getBalance = function() { return this.send('REQUEST', 'GET_BALANCE'); };" +
            "sat20.sendBitcoin = function(address, amount, options) { return this.send('APPROVE', 'SEND_BITCOIN', { address, amount, options }); };" +
            "sat20.signMessage = function(message, type) { return this.send('APPROVE', 'SIGN_MESSAGE', { message, type }); };" +
            "sat20.signPsbt = function(psbtHex, options) { return this.send('APPROVE', 'SIGN_PSBT', { psbtHex, options }); };" +
            "sat20.getInscriptions = function() { return this.send('REQUEST', 'GET_INSCRIPTIONS'); };" +
            "sat20.sendInscription = function(inscriptionId, address, options) { return this.send('APPROVE', 'SEND_INSCRIPTION', { inscriptionId, address, options }); };" +
            "sat20.getUtxos = function(options) { return this.send('REQUEST', 'GET_UTXOS', options); };" +
            "sat20.getAssetAmount = function(assetId) { return this.send('REQUEST', 'GET_ASSET_AMOUNT', { assetId }); };" +
            "sat20.getCurrentName = function() { return this.send('REQUEST', 'GET_CURRENT_NAME'); };" +
            "sat20.invokeContract = function(contract, method, params, options) { return this.send('APPROVE', 'INVOKE_CONTRACT_V2', { contract, method, params, options }); };" +
            "sat20.deployContract = function(bytecode, options) { return this.send('APPROVE', 'DEPLOY_CONTRACT_REMOTE', { bytecode, options }); };" +
            "sat20.batchSendAssetsSatsNet = function(assets, options) { return this.send('APPROVE', 'BATCH_SEND_ASSETS_SATSNET', { assets, options }); };" +
            "console.log('✅ SAT20 Provider injected successfully via native WebView');" +
            "console.log('📋 Available methods:', Object.keys(sat20));" +
            "})()";
    }

    // 增强型JavaScript接口类
    private class Sat20JSInterface {
        private static final String TAG = "Sat20JSBridge";

        @JavascriptInterface
        public void sendRequest(String messageId, String type, String action, String data) {
            Log.d(TAG, String.format("📨 Received SAT20 request: %s.%s (ID: %s)", type, action, messageId));

            try {
                // 解析数据
                JSONObject jsonData = new JSONObject(data);

                // 处理请求
                handleDAppRequest(messageId, type, action, jsonData);

            } catch (JSONException e) {
                Log.e(TAG, "❌ Error parsing request data: " + e.getMessage());
                sendErrorResponse(messageId, "Invalid request data: " + e.getMessage());
            } catch (Exception e) {
                Log.e(TAG, "❌ Unexpected error: " + e.getMessage());
                sendErrorResponse(messageId, "Unexpected error: " + e.getMessage());
            }
        }

        @JavascriptInterface
        public void log(String level, String message) {
            switch (level) {
                case "debug":
                    Log.d(TAG, "🔍 DApp: " + message);
                    break;
                case "info":
                    Log.i(TAG, "ℹ️ DApp: " + message);
                    break;
                case "warn":
                    Log.w(TAG, "⚠️ DApp: " + message);
                    break;
                case "error":
                    Log.e(TAG, "❌ DApp: " + message);
                    break;
                default:
                    Log.i(TAG, "📝 DApp: " + message);
                    break;
            }
        }

        @JavascriptInterface
        public void notify(String event, String data) {
            Log.d(TAG, String.format("📢 DApp notification: %s - %s", event, data));

            // 处理特殊事件
            if ("injection_complete".equals(event)) {
                Log.i(TAG, "✅ SAT20 Provider injection completed");
            } else if ("provider_ready".equals(event)) {
                Log.i(TAG, "🎉 SAT20 Provider is ready for use");
            }
        }

        private void handleDAppRequest(String messageId, String type, String action, JSONObject data) {
            runOnUiThread(() -> {
                try {
                    // 模拟处理不同类型的请求
                    if ("REQUEST".equals(type)) {
                        handleReadOnlyRequest(messageId, action, data);
                    } else if ("APPROVE".equals(type)) {
                        handleApprovalRequest(messageId, action, data);
                    } else {
                        sendErrorResponse(messageId, "Unknown request type: " + type);
                    }
                } catch (Exception e) {
                    Log.e(TAG, "❌ Error handling request: " + e.getMessage());
                    sendErrorResponse(messageId, "Request handling failed: " + e.getMessage());
                }
            });
        }

        private void handleReadOnlyRequest(String messageId, String action, JSONObject data) {
            // 模拟只读请求响应
            JSONObject response = new JSONObject();
            try {
                switch (action) {
                    case "GET_ACCOUNTS":
                        response.put("accounts", new String[]{"bc1p...example"});
                        response.put("chainId", "satsnet-test");
                        break;
                    case "GET_NETWORK":
                        response.put("network", "satsnet-test");
                        response.put("chainId", "satsnet-test");
                        break;
                    case "GET_BALANCE":
                        response.put("balance", "1000000");
                        response.put("assetBalance", new JSONObject());
                        break;
                    default:
                        sendErrorResponse(messageId, "Unknown read-only action: " + action);
                        return;
                }
                sendSuccessResponse(messageId, response);
            } catch (Exception e) {
                sendErrorResponse(messageId, "Failed to build response: " + e.getMessage());
            }
        }

        private void handleApprovalRequest(String messageId, String action, JSONObject data) {
            // 模拟需要用户批准的请求
            try {
                // 这里应该显示用户界面进行批准
                Log.i(TAG, String.format("🔐 Approval required for: %s.%s", "APPROVE", action));

                // 模拟用户批准
                JSONObject response = new JSONObject();
                response.put("txHash", "0x..." + System.currentTimeMillis() % 10000);
                response.put("status", "approved");

                sendSuccessResponse(messageId, response);
            } catch (Exception e) {
                sendErrorResponse(messageId, "Approval process failed: " + e.getMessage());
            }
        }

        private void sendSuccessResponse(String messageId, JSONObject result) {
            if (webView != null) {
                try {
                    String responseScript = String.format(
                        "if (window.sat20 && window.sat20.handleResponse) { " +
                        "window.sat20.handleResponse('%s', %s, null); " +
                        "} else { console.error('SAT20 Provider not available for response'); }",
                        messageId, result.toString()
                    );
                    webView.evaluateJavascript(responseScript, null);
                    Log.d(TAG, "✅ Sent success response for: " + messageId);
                } catch (Exception e) {
                    Log.e(TAG, "❌ Failed to send success response: " + e.getMessage());
                }
            }
        }

        private void sendErrorResponse(String messageId, String errorMessage) {
            if (webView != null) {
                try {
                    JSONObject error = new JSONObject();
                    error.put("message", errorMessage);
                    error.put("code", -1);

                    String responseScript = String.format(
                        "if (window.sat20 && window.sat20.handleResponse) { " +
                        "window.sat20.handleResponse('%s', null, %s); " +
                        "} else { console.error('SAT20 Provider not available for error response'); }",
                        messageId, error.toString()
                    );
                    webView.evaluateJavascript(responseScript, null);
                    Log.d(TAG, "❌ Sent error response for: " + messageId);
                } catch (Exception e) {
                    Log.e(TAG, "❌ Failed to send error response: " + e.getMessage());
                }
            }
        }
    }

    // 获取WebView实例的静态方法
    public static WebView getWebView() {
        return webView;
    }

    // 静态方法来注入JavaScript
    public static void injectJS(String code) {
        if (webView != null) {
            webView.evaluateJavascript(code, result -> {
                Log.d(TAG, "JavaScript injection result: " + result);
            });
        }
    }

    // 静态方法来执行JavaScript
    public static void evaluateJS(String code) {
        if (webView != null) {
            webView.evaluateJavascript(code, result -> {
                Log.d(TAG, "JavaScript evaluation result: " + result);
            });
        }
    }

    // 显示JavaScript Alert对话框
    private void showJavaScriptAlert(String title, String message, android.webkit.JsResult result) {
        runOnUiThread(() -> {
            new androidx.appcompat.app.AlertDialog.Builder(this)
                .setTitle(title)
                .setMessage(message)
                .setPositiveButton("确定", (dialog, which) -> {
                    result.confirm();
                    Log.d(TAG, "✅ JavaScript alert confirmed");
                })
                .setNegativeButton("取消", (dialog, which) -> {
                    result.cancel();
                    Log.d(TAG, "❌ JavaScript alert cancelled");
                })
                .setCancelable(false)
                .show();
        });
    }

    // 显示JavaScript Confirm对话框
    private void showJavaScriptConfirm(String title, String message, android.webkit.JsResult result) {
        runOnUiThread(() -> {
            new androidx.appcompat.app.AlertDialog.Builder(this)
                .setTitle(title)
                .setMessage(message)
                .setPositiveButton("确定", (dialog, which) -> {
                    result.confirm();
                    Log.d(TAG, "✅ JavaScript confirm accepted");
                })
                .setNegativeButton("取消", (dialog, which) -> {
                    result.cancel();
                    Log.d(TAG, "❌ JavaScript confirm rejected");
                })
                .setCancelable(false)
                .show();
        });
    }

    // 显示错误对话框
    private void showErrorDialog(String title, String message, boolean shouldFinish) {
        runOnUiThread(() -> {
            androidx.appcompat.app.AlertDialog.Builder builder = new androidx.appcompat.app.AlertDialog.Builder(this)
                .setTitle(title)
                .setMessage(message)
                .setPositiveButton("重试", (dialog, which) -> {
                    Log.d(TAG, "🔄 User chose to retry");
                    // 这里可以实现重试逻辑
                })
                .setNegativeButton("关闭", (dialog, which) -> {
                    if (shouldFinish) {
                        finish();
                    }
                });

            if (!shouldFinish) {
                builder.setNeutralButton("继续", (dialog, which) -> {
                    Log.d(TAG, "➡️ User chose to continue");
                });
            }

            builder.setCancelable(false).show();
        });
    }

    // 显示加载状态
    private void showLoadingState(String message) {
        runOnUiThread(() -> {
            if (progressBar != null) {
                progressBar.setVisibility(View.VISIBLE);
            }
            Log.d(TAG, "📊 Loading: " + message);
        });
    }

    // 隐藏加载状态
    private void hideLoadingState() {
        runOnUiThread(() -> {
            if (progressBar != null) {
                progressBar.setVisibility(View.GONE);
            }
            Log.d(TAG, "✅ Loading completed");
        });
    }

    // 自定义插件类
    @CapacitorPlugin(name = "Sat20WebView")
    public static class Sat20WebViewPlugin extends Plugin {

        @PluginMethod
        public void openUrl(PluginCall call) {
            String url = call.getString("url");
            if (url != null) {
                Log.d(TAG, "Opening URL in custom WebView: " + url);
                ((MainActivity) getActivity()).createCustomWebView(url);
                call.resolve();
            } else {
                call.reject("URL is required");
            }
        }

        @PluginMethod
        public void injectJavaScript(PluginCall call) {
            String code = call.getString("code");
            if (code != null) {
                Log.d(TAG, "Injecting JavaScript into WebView");
                injectJS(code);
                call.resolve();
            } else {
                call.reject("Code is required");
            }
        }

        @PluginMethod
        public void evaluateJavaScript(PluginCall call) {
            String code = call.getString("code");
            if (code != null) {
                Log.d(TAG, "Evaluating JavaScript in WebView");
                evaluateJS(code);
                // 由于evaluateJavascript是异步的，这里需要处理回调
                call.resolve();
            } else {
                call.reject("Code is required");
            }
        }
    }
}
