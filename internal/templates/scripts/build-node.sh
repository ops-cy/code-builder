#!/bin/bash
# Node.js 构建脚本
# 用法: build-node.sh <node-version> <package-manager|auto>
# 根据 lockfile 自动选择 npm / pnpm / yarn / bun，并在存在 build 脚本时执行。

set -euo pipefail

NODE_VERSION="${1:-lts}"
PM="${2:-auto}"

echo "[INFO] 请求 Node 版本: ${NODE_VERSION}（使用构建镜像自带 Node）"
node --version || { echo "[ERROR] 系统未安装 Node.js"; exit 1; }

if [ "${PM}" = "auto" ]; then
    if [ -f pnpm-lock.yaml ]; then
        PM="pnpm"
    elif [ -f yarn.lock ]; then
        PM="yarn"
    elif [ -f bun.lockb ] || [ -f bun.lock ]; then
        PM="bun"
    else
        PM="npm"
    fi
fi
echo "[INFO] 包管理器: ${PM}"

corepack enable >/dev/null 2>&1 || true

# 镜像不一定自带所有包管理器，缺失时用 npm 安装。
if [ "${PM}" = "pnpm" ] && ! command -v pnpm >/dev/null 2>&1; then
    npm install -g pnpm
fi
if [ "${PM}" = "yarn" ] && ! command -v yarn >/dev/null 2>&1; then
    npm install -g yarn
fi
if [ "${PM}" = "bun" ] && ! command -v bun >/dev/null 2>&1; then
    echo "[WARN] 未安装 bun，回退到 npm"
    PM="npm"
fi

has_build_script() {
    node -e "const p=require('./package.json'); process.exit(p.scripts && p.scripts.build ? 0 : 1)" 2>/dev/null
}

case "${PM}" in
    npm)
        if [ -f package-lock.json ]; then
            npm ci --no-audit --no-fund
        else
            npm install --no-audit --no-fund
        fi
        if has_build_script; then
            npm run build
        else
            echo "[WARN] package.json 没有 build 脚本，跳过编译"
        fi
        ;;
    pnpm)
        pnpm install --frozen-lockfile || pnpm install
        if has_build_script; then pnpm run build; else echo "[WARN] 没有 build 脚本"; fi
        ;;
    yarn)
        yarn install --frozen-lockfile || yarn install
        if has_build_script; then yarn run build; else echo "[WARN] 没有 build 脚本"; fi
        ;;
    bun)
        bun install --frozen-lockfile || bun install
        if has_build_script; then bun run build; else echo "[WARN] 没有 build 脚本"; fi
        ;;
    *)
        echo "[ERROR] 未知包管理器: ${PM}"
        exit 1
        ;;
esac

# 兼容 CRA 的 build/、Nuxt 的 .output/public、Angular 的 dist/<project>。
if [ ! -d dist ] && [ -d build ]; then
    cp -a build dist
fi
if [ ! -d dist ] && [ -d .output/public ]; then
    cp -a .output/public dist
fi
if [ -d dist ] && [ ! -f dist/index.html ]; then
    INDEX="$(find dist -mindepth 1 -maxdepth 2 -name index.html 2>/dev/null | head -1 || true)"
    if [ -n "${INDEX}" ]; then
        cp -a "$(dirname "${INDEX}")/." dist/
    fi
fi

echo "[INFO] Node 构建完成"
