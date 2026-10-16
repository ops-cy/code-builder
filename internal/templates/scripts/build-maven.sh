#!/bin/bash
# Maven 构建脚本
# 用法: build-maven.sh <java-version>

set -e
set -o pipefail

JAVA_VERSION="${1}"

echo "[INFO] 请求 Java 版本: ${JAVA_VERSION}"

# 初始化 SDKMAN
export SDKMAN_DIR="${SDKMAN_DIR:-/root/.sdkman}"
if [ -s "${SDKMAN_DIR}/bin/sdkman-init.sh" ]; then
    source "${SDKMAN_DIR}/bin/sdkman-init.sh"
else
    echo "[WARN] SDKMAN 初始化脚本未找到 (SDKMAN_DIR: ${SDKMAN_DIR})"
fi

# 检查 sdk 命令是否存在
if command -v sdk > /dev/null || type sdk > /dev/null; then
    # 1. Java 版本管理
    echo "[INFO] 准备 Java 环境..."
    INSTALLED_JAVA=$(sdk list java 2>/dev/null | grep -E "installed|local" | grep -E "${JAVA_VERSION}" | head -1 | awk '{print $NF}' || true)
    if [ -n "${INSTALLED_JAVA}" ]; then
        sdk use java "${INSTALLED_JAVA}"
    else
        AVAILABLE_JAVA=$(sdk list java 2>/dev/null | grep -E "${JAVA_VERSION}\.[0-9.]+-tem" | head -1 | awk '{print $NF}' || true)
        [ -z "${AVAILABLE_JAVA}" ] && AVAILABLE_JAVA=$(sdk list java 2>/dev/null | grep -E "${JAVA_VERSION}\.[0-9.]+" | head -1 | awk '{print $NF}' || true)
        if [ -n "${AVAILABLE_JAVA}" ]; then
            sdk install java "${AVAILABLE_JAVA}" && sdk use java "${AVAILABLE_JAVA}"
        fi
    fi

    # 2. Maven 版本管理 (默认安装最新稳定版)
    echo "[INFO] 准备 Maven 环境..."
    if ! command -v mvn &> /dev/null; then
        echo "[INFO] 未检测到 mvn，通过 SDKMAN 安装 Maven..."
        sdk install maven
    fi
    sdk use maven > /dev/null 2>&1 || true
else
    echo "[WARN] sdk 命令不可用，跳过版本管理，使用系统默认工具"
fi

java -version || { echo "[ERROR] 系统未安装 Java"; exit 1; }
mvn -version || { echo "[ERROR] 系统未安装 Maven"; exit 1; }

echo "[INFO] 执行 Maven 编译"
mvn clean package -DskipTests

# 复制产物（优先主 jar，跳过 sources/javadoc/plain 附属包）
JAR="$(ls target/*.jar 2>/dev/null | grep -vE '(-sources|-javadoc|-plain)\.jar$' | head -1 || true)"
if [ -z "${JAR}" ]; then
    JAR="$(ls target/*.jar 2>/dev/null | head -1 || true)"
fi
if [ -n "${JAR}" ]; then
    cp -f "${JAR}" app.jar
    echo "[INFO] Maven 构建完成，产物: app.jar"
else
    echo "[ERROR] 未找到 JAR 文件"
    exit 1
fi
