#!/bin/bash
# Go 构建脚本
# 用法: build-go.sh <cgo-enabled>
# 可选环境变量: GO_BUILD_PACKAGE / GOOS / GOARCH / GO_LDFLAGS / GOPROXY

set -euo pipefail

CGO_ENABLED="${1:-0}"
GOOS="${GOOS:-linux}"
GOARCH="${GOARCH:-amd64}"
GO_LDFLAGS="${GO_LDFLAGS:--s -w}"

echo "[INFO] Go 构建: CGO_ENABLED=${CGO_ENABLED} GOOS=${GOOS} GOARCH=${GOARCH}"

if [ -f go.mod ]; then
    echo "[INFO] 下载 Go 依赖"
    go mod download
fi

PKG="${GO_BUILD_PACKAGE:-}"
if [ -z "${PKG}" ]; then
    if [ -f main.go ]; then
        PKG="."
    else
        shopt -s nullglob
        cmd_mains=(cmd/*/main.go)
        shopt -u nullglob
        if [ "${#cmd_mains[@]}" -eq 1 ]; then
            PKG="./$(dirname "${cmd_mains[0]}")"
        else
            mapfile -t mains < <(go list -f '{{if eq .Name "main"}}{{.ImportPath}}{{end}}' ./... 2>/dev/null | sed '/^$/d')
            if [ "${#mains[@]}" -eq 1 ]; then
                PKG="${mains[0]}"
            else
                echo "[ERROR] 无法确定 Go 入口包。请在仓库根目录放 main.go，"
                echo "[ERROR] 或只保留一个 cmd/<name>/main.go，或设置 GO_BUILD_PACKAGE。"
                exit 1
            fi
        fi
    fi
fi

echo "[INFO] 构建包: ${PKG}"
GOOS="${GOOS}" GOARCH="${GOARCH}" CGO_ENABLED="${CGO_ENABLED}" \
    go build -buildvcs=false -trimpath -ldflags "${GO_LDFLAGS}" -o app "${PKG}"

echo "[INFO] Go 构建完成，产物: app"
