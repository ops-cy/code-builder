#!/bin/bash
# Node.js 构建脚本
# 用法: build-node.sh <node-version> <package-manager>

set -e
set -o pipefail

NODE_VERSION="${1:-lts}"
PM="${2:-npm}"

echo "[INFO] 请求 Node 版本: ${NODE_VERSION}"
echo "[INFO] 包管理器: ${PM}"

# 检查 volta 命令是否存在
if command -v volta > /dev/null || type volta > /dev/null; then
    echo "[INFO] 使用 Volta 管理 Node 版本"
    volta install node@"${NODE_VERSION}"
else
    echo "[WARN] volta 命令不可用，跳过版本管理，使用系统默认 Node"
fi

node --version || { echo "[ERROR] 系统未安装 Node.js"; exit 1; }

# 安装依赖并构建
echo "[INFO] 开始执行项目构建 (${PM})"
case "${PM}" in
    pnpm)
        pnpm install --frozen-lockfile
        pnpm run build
        ;;
    yarn)
        yarn install --frozen-lockfile
        yarn build
        ;;
    npm)
        if [ -f "package-lock.json" ]; then
            npm ci
        else
            npm install
        fi
        npm run build
        ;;
    *)
        echo "[ERROR] 未知包管理器: ${PM}"
        exit 1
        ;;
esac

echo "[INFO] Node 构建完成"
