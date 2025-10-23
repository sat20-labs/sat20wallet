import type { CapacitorConfig } from '@capacitor/cli';

const config: CapacitorConfig = {
  appId: 'com.sat20.wallet',
  appName: 'SAT20 Wallet',
  webDir: 'dist',
  server: {
    androidScheme: 'https',
    // 添加 cleartext 以支持本地网络调试
    cleartext: true
  },
  ios: {
    scheme: 'SAT20Wallet'
  },
  android: {
    // Android 性能优化配置
    webContentsDebuggingEnabled: false, // 生产环境关闭调试
    // 添加硬件加速配置
    allowMixedContent: false,
    overrideUserAgent: 'SAT20Wallet/1.0'
  },
  plugins: {
    SplashScreen: {
      launchShowDuration: 2000,
      launchAutoHide: true,
      backgroundColor: "#ffffff",
      androidSplashResourceName: "splash",
      androidScaleType: "CENTER_CROP",
      showSpinner: false,
      splashFullScreen: true,
      splashImmersive: true
    },
    BiometricAuth: {
      allowDeviceCredentials: true,
      iosUseCustomAuthUI: true,
      iosFallbackTitle: "使用密码",
      iosTitle: "生物识别验证",
      iosSubtitle: "请验证您的身份",
      androidTitle: "生物识别验证",
      androidSubtitle: "请验证您的身份",
      androidDescription: "请使用指纹或面容解锁钱包",
      androidNegativeButtonText: "取消"
    },
    App: {
      // 添加应用级别的性能优化
      disableBackButtonHandler: false
    }
  }
};

export default config;
