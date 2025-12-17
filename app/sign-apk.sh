#!/bin/bash

# SAT20 Wallet APK 签名脚本
# 自动构建并签名已签名的发布版 APK

set -e  # 遇到错误立即退出

# 颜色定义
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# 项目路径
PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ANDROID_DIR="$PROJECT_DIR/android"
KEYSTORE_FILE="$PROJECT_DIR/sat20wallet-release.jks"
BUILD_DIR="$PROJECT_DIR/builds"
APK_DIR="$ANDROID_DIR/app/build/outputs/apk/release"

echo -e "${BLUE}🚀 SAT20 Wallet APK 签名构建脚本${NC}"
echo -e "${BLUE}======================================${NC}"
echo ""

# 检查必要工具
echo -e "${YELLOW}🔍 检查构建环境...${NC}"

if ! command -v keytool &> /dev/null; then
    echo -e "${RED}❌ keytool 未找到，请确保 Java JDK 已安装并配置在 PATH 中${NC}"
    exit 1
fi

if ! command -v node &> /dev/null; then
    echo -e "${RED}❌ Node.js 未找到，请确保 Node.js 已安装${NC}"
    exit 1
fi

if ! command -v bun &> /dev/null; then
    echo -e "${RED}❌ Bun 未找到，请确保 Bun 包管理器已安装${NC}"
    exit 1
fi

echo -e "${GREEN}✅ 构建环境检查通过${NC}"
echo ""

# 检查密钥库
if [ ! -f "$KEYSTORE_FILE" ]; then
    echo -e "${YELLOW}🔐 未找到密钥库文件，正在生成...${NC}"
    echo -e "${YELLOW}请按照提示输入密钥库信息：${NC}"

    # 运行密钥库生成脚本
    if [ -f "$PROJECT_DIR/generate-keystore.sh" ]; then
        bash "$PROJECT_DIR/generate-keystore.sh"
    else
        keytool -genkey -v \
            -keystore "$KEYSTORE_FILE" \
            -keyalg RSA \
            -keysize 2048 \
            -validity 10000 \
            -alias sat20wallet
    fi

    if [ $? -ne 0 ]; then
        echo -e "${RED}❌ 密钥库生成失败${NC}"
        exit 1
    fi
else
    echo -e "${GREEN}✅ 密钥库文件已存在：$KEYSTORE_FILE${NC}"
fi

# 检查 gradle.properties 配置
GRADLE_PROPERTIES="$ANDROID_DIR/gradle.properties"
if grep -q "your_keystore_password_here" "$GRADLE_PROPERTIES"; then
    echo -e "${YELLOW}⚠️  请先配置 $GRADLE_PROPERTIES 中的密码信息${NC}"
    echo -e "${YELLOW}请编辑该文件，替换以下占位符：${NC}"
    echo -e "${YELLOW}  - MYAPP_UPLOAD_STORE_PASSWORD${NC}"
    echo -e "${YELLOW}  - MYAPP_UPLOAD_KEY_PASSWORD${NC}"
    echo ""
    read -p "是否现在编辑该文件？(y/N): " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        ${EDITOR:-nano} "$GRADLE_PROPERTIES"
        echo -e "${YELLOW}请保存文件后按任意键继续...${NC}"
        read -n 1
    else
        echo -e "${RED}❌ 请先配置密码后再运行此脚本${NC}"
        exit 1
    fi
fi

# 创建构建目录
mkdir -p "$BUILD_DIR"

echo -e "${YELLOW}🏗️  开始构建应用...${NC}"

# 1. 构建前端
echo -e "${BLUE}步骤 1/4: 构建前端应用${NC}"
cd "$PROJECT_DIR"
bun run build

if [ $? -ne 0 ]; then
    echo -e "${RED}❌ 前端构建失败${NC}"
    exit 1
fi

echo -e "${GREEN}✅ 前端构建完成${NC}"

# 2. 同步到 Capacitor
echo -e "${BLUE}步骤 2/4: 同步到 Capacitor${NC}"
bun run sync

if [ $? -ne 0 ]; then
    echo -e "${RED}❌ Capacitor 同步失败${NC}"
    exit 1
fi

echo -e "${GREEN}✅ Capacitor 同步完成${NC}"

# 3. 构建 Android APK
echo -e "${BLUE}步骤 3/4: 构建 Android APK${NC}"
cd "$ANDROID_DIR"
./gradlew assembleRelease

if [ $? -ne 0 ]; then
    echo -e "${RED}❌ Android APK 构建失败${NC}"
    exit 1
fi

echo -e "${GREEN}✅ Android APK 构建完成${NC}"

# 4. 复制已签名的 APK
echo -e "${BLUE}步骤 4/4: 复制已签名的 APK${NC}"

SIGNED_APK="$APK_DIR/app-release.apk"
TIMESTAMP=$(date +"%Y%m%d_%H%M%S")
OUTPUT_APK="$BUILD_DIR/SAT20Wallet_$TIMESTAMP.apk"

if [ -f "$SIGNED_APK" ]; then
    cp "$SIGNED_APK" "$OUTPUT_APK"
    echo -e "${GREEN}✅ 已签名的 APK 已复制到：${NC}"
    echo -e "${GREEN}📁 $OUTPUT_APK${NC}"
    echo ""

    # 显示 APK 信息
    APK_SIZE=$(du -h "$OUTPUT_APK" | cut -f1)
    echo -e "${BLUE}📊 APK 信息：${NC}"
    echo -e "${BLUE}   文件大小：$APK_SIZE${NC}"
    echo -e "${BLUE}   签名状态：已签名${NC}"

    # 验证签名
    if command -v jarsigner &> /dev/null; then
        if jarsigner -verify -certs "$OUTPUT_APK" > /dev/null 2>&1; then
            echo -e "${GREEN}   签名验证：✅ 通过${NC}"
        else
            echo -e "${YELLOW}   签名验证：⚠️  验证失败，请检查签名配置${NC}"
        fi
    fi

else
    echo -e "${RED}❌ 未找到已签名的 APK 文件：$SIGNED_APK${NC}"
    exit 1
fi

echo ""
echo -e "${GREEN}🎉 APK 签名构建完成！${NC}"
echo -e "${GREEN}======================================${NC}"
echo -e "${BLUE}📱 安装方法：${NC}"
echo -e "${BLUE}   adb install $OUTPUT_APK${NC}"
echo ""
echo -e "${YELLOW}💡 提示：${NC}"
echo -e "${YELLOW}   - 首次安装可能需要卸载调试版本${NC}"
echo -e "${YELLOW}   - 请确保目标设备已开启开发者选项和 USB 调试${NC}"
echo -e "${YELLOW}   - 签名密钥库文件请妥善保管，丢失后无法更新应用${NC}"