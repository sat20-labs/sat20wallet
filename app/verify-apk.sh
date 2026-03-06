#!/bin/bash

# SAT20 Wallet APK 签名验证脚本
# 使用方法：./verify-apk.sh [version]

VERSION=${1:-"0.1.13"}
APK_FILE="release/SAT20-Wallet-v${VERSION}-release-signed.apk"
EXPECTED_SHA256="5F:8B:92:27:0F:85:14:C4:94:9B:17:88:91:54:D4:F4:ED:C4:C1:01:F5:E7:62:7D:4C:AA:1B:D0:72:2C:C2:03"

echo "🔍 验证 SAT20 Wallet v${VERSION} APK 签名..."
echo ""

# 检查文件是否存在
if [ ! -f "$APK_FILE" ]; then
    echo "❌ 错误：APK 文件不存在：$APK_FILE"
    exit 1
fi

echo "✅ APK 文件存在：$APK_FILE"
echo ""

# 获取 APK 大小
APK_SIZE=$(ls -lh "$APK_FILE" | awk '{print $5}')
echo "📦 APK 大小：$APK_SIZE"
echo ""

# 验证证书
echo "📋 证书信息:"
keytool -printcert -jarfile "$APK_FILE" 2>/dev/null | grep -E "(Owner|Issuer|SHA256|Valid)"
echo ""

# 验证 SHA256 指纹
ACTUAL_SHA256=$(keytool -printcert -jarfile "$APK_FILE" 2>/dev/null | grep "SHA256:" | awk -F': ' '{print $2}')

if [ "$ACTUAL_SHA256" = "$EXPECTED_SHA256" ]; then
    echo "✅ 签名验证通过！证书指纹匹配。"
    echo ""
    echo "🎉 APK 已成功签名，可以发布！"
else
    echo "❌ 签名验证失败！证书指纹不匹配。"
    echo "   期望：$EXPECTED_SHA256"
    echo "   实际：$ACTUAL_SHA256"
    exit 1
fi

echo ""
echo "📱 安装命令："
echo "   adb install $APK_FILE"
echo ""
