#!/bin/sh
set -e

# 与 Jenkins 共享库一致：COMMAND 可覆盖默认启动命令，
# 便于同一个镜像在 compose 中启动多个不同角色的服务。
if [ -n "${COMMAND:-}" ]; then
    exec sh -c "${COMMAND}"
fi

exec "/app/${APPNAME:-app}" "$@"
