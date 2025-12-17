#!/bin/bash

# Android 密钥库生成脚本
# 请确保你的 Java SDK 已正确配置

echo "🔐 正在生成 SAT20 Wallet 的签名密钥库..."
echo ""

# 密钥库信息
KEYSTORE_NAME="sat20wallet-release.jks"
ALIAS="sat20wallet"
VALIDITY=10000  # 10,000天 = 约27年

# 检查是否已存在密钥库文件
if [ -f "$KEYSTORE_NAME" ]; then
    echo "⚠️  警告：密钥库文件 $KEYSTORE_NAME 已存在！"
    echo "是否要覆盖现有文件？(y/N)"
    read -r response
    if [[ ! "$response" =~ ^[Yy]$ ]]; then
        echo "操作已取消。"
        exit 1
    fi
    rm "$KEYSTORE_NAME"
fi

# 生成密钥库
echo "请按照提示输入以下信息："
echo "1. 设置密钥库密码（至少6位字符）"
echo "2. 设置密钥密码（可以与密钥库密码相同）"
echo "3. 输入您的组织信息"
echo ""

keytool -genkey -v \
    -keystore "$KEYSTORE_NAME" \
    -keyalg RSA \
    -keysize 2048 \
    -validity "$VALIDITY" \
    -alias "$ALIAS"

if [ $? -eq 0 ]; then
    echo ""
    echo "✅ 密钥库生成成功！"
    echo "📁 文件位置：$KEYSTORE_NAME"
    echo "🔑 别名：$ALIAS"
    echo "⏰ 有效期：$VALIDITY 天"
    echo ""
    echo "⚠️  重要提醒："
    echo "   - 请妥善保管此密钥库文件和密码"
    echo "   - 丢失密钥库将无法更新应用"
    echo "   - 建议备份到多个安全位置"
    echo ""
    echo "📝 下一步：请编辑 android/app/build.gradle 添加签名配置"
else
    echo "❌ 密钥库生成失败，请检查输入信息"
    exit 1
fi