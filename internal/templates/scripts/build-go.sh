#!/bin/bash
# Go 构建脚本
# 用法: build-go.sh <cgo-enabled>
# 可选环境变量: GO_BUILD_PACKAGE / GOOS / GOARCH / GO_LDFLAGS / GOPROXY / APP_NAME

set -euo pipefail

CGO_ENABLED="${1:-0}"
GOOS="${GOOS:-linux}"
GOARCH="${GOARCH:-amd64}"
GO_LDFLAGS="${GO_LDFLAGS:--s -w}"
APP_NAME="${APP_NAME:-}"

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
        # 约定优先级：cmd/<仓库名> -> cmd/server -> cmd/api -> cmd/app -> cmd/main。
        for candidate in \
            "cmd/${APP_NAME}" \
            "cmd/server" \
            "cmd/api" \
            "cmd/app" \
            "cmd/main" \
            "cmd/web"; do
            if [ -n "${candidate}" ] && [ -f "${candidate}/main.go" ]; then
                PKG="./${candidate}"
                break
            fi
        done
        if [ -z "${PKG}" ]; then
            shopt -s nullglob
            cmd_mains=(cmd/*/main.go)
            shopt -u nullglob
            if [ "${#cmd_mains[@]}" -eq 1 ]; then
                PKG="./$(dirname "${cmd_mains[0]}")"
            else
                mapfile -t mains < <(go list -f '{{if eq .Name "main"}}{{.ImportPath}}{{end}}' ./... 2>/dev/null | sed '/^$/d' | sort)
                if [ "${#mains[@]}" -ge 1 ]; then
                    PKG="${mains[0]}"
                    echo "[WARN] 检测到多个 main 包，按排序选择: ${PKG}"
                    printf '[WARN] 候选: %s\n' "${mains[@]}"
                else
                    echo "[ERROR] 无法确定 Go 入口包。请在仓库根目录放 main.go，"
                    echo "[ERROR] 或使用 cmd/<仓库名>/main.go、cmd/server/main.go，"
                    echo "[ERROR] 或设置 GO_BUILD_PACKAGE。"
                    exit 1
                fi
            fi
        fi
    fi
fi

echo "[INFO] 构建包: ${PKG}"
if ! GOOS="${GOOS}" GOARCH="${GOARCH}" CGO_ENABLED="${CGO_ENABLED}" \
    go build -buildvcs=false -trimpath -ldflags "${GO_LDFLAGS}" -o app "${PKG}"; then
    if [ "${CGO_ENABLED}" = "0" ]; then
        echo "[WARN] CGO_ENABLED=0 构建失败，尝试 CGO_ENABLED=1"
        GOOS="${GOOS}" GOARCH="${GOARCH}" CGO_ENABLED=1 \
            go build -buildvcs=false -trimpath -ldflags "${GO_LDFLAGS}" -o app "${PKG}"
    else
        exit 1
    fi
fi

echo "[INFO] Go 构建完成，产物: app"
