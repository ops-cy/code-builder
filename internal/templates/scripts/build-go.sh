#!/bin/bash
# Go 构建脚本
# 用法: build-go.sh <cgo-enabled>

set -e
set -o pipefail

CGO_ENABLED="${1:-0}"

echo "[INFO] 执行 Go 编译, CGO_ENABLED=${CGO_ENABLED}"

go mod tidy

GOOS=linux GOARCH=amd64 CGO_ENABLED="${CGO_ENABLED}" go build -buildvcs=false -o app -trimpath .

echo "[INFO] Go 构建完成，产物: app"
