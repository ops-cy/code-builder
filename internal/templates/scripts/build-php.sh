#!/bin/bash
# PHP 构建脚本
# 用法: build-php.sh
# 与 Jenkins 共享库 PhpBuilder 语义一致：composer install（生产依赖 + 优化自动加载）。

set -e
set -o pipefail

if [ -f composer.json ]; then
    echo "[INFO] composer install --no-dev --optimize-autoloader"
    composer install --no-dev --optimize-autoloader
else
    echo "[WARN] 未找到 composer.json"
fi

echo "[INFO] PHP 构建完成"
