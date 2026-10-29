#!/bin/bash
# Rust 构建脚本
# 用法: build-rust.sh
# 可选环境变量: CARGO_BIN（指定二进制名）

set -euo pipefail

echo "[INFO] cargo build --release"
if [ -f Cargo.lock ]; then
    cargo build --release --locked || cargo build --release
else
    cargo build --release
fi

NAME="${CARGO_BIN:-}"
if [ -z "${NAME}" ]; then
    NAME="$(awk -F'"' '/^[[:space:]]*name[[:space:]]*=/{print $2; exit}' Cargo.toml || true)"
fi

BIN=""
if [ -n "${NAME}" ] && [ -x "target/release/${NAME}" ]; then
    BIN="target/release/${NAME}"
else
    BIN="$(find target/release -maxdepth 1 -type f -perm -111 \
        ! -name '*.d' ! -name '*.so' ! -name '*.rlib' ! -name '*.rmeta' 2>/dev/null | sort | head -1 || true)"
fi
if [ -z "${BIN}" ]; then
    echo "[ERROR] 未找到 cargo release 产物；请检查 Cargo.toml 或设置 CARGO_BIN"
    exit 1
fi

cp -f "${BIN}" app
echo "[INFO] Rust 构建完成，产物: app (${BIN})"
