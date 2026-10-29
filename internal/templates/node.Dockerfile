# syntax=docker/dockerfile:1
# Node.js / SSR：调用内置 build-node.sh 自动选择 npm / pnpm / yarn / bun，
# 运行阶段保留完整 node_modules 与 shell，方便 docker exec 排查。
# 入口优先级：dist/index.js -> server.js -> index.js -> npm start。
FROM node:22-alpine AS build
ARG NPM_REGISTRY=
ENV NPM_CONFIG_REGISTRY=${NPM_REGISTRY}
RUN apk add --no-cache bash git
WORKDIR /app
COPY . .
RUN bash .code-builder/build.sh lts auto

FROM node:22-alpine AS runtime
ARG TZ=Asia/Shanghai
RUN apk add --no-cache bash ca-certificates tzdata curl
ENV TZ=${TZ} NODE_ENV=production
WORKDIR /app
COPY --from=build --chown=node:node /app /app
RUN printf '%s\n' \
    '#!/bin/sh' \
    'set -e' \
    'if [ -f /app/dist/index.js ]; then exec node /app/dist/index.js; fi' \
    'if [ -f /app/server.js ]; then exec node /app/server.js; fi' \
    'if [ -f /app/index.js ]; then exec node /app/index.js; fi' \
    'if [ -f /app/.output/server/index.mjs ]; then exec node /app/.output/server/index.mjs; fi' \
    'if [ -f /app/.next/standalone/server.js ]; then exec node /app/.next/standalone/server.js; fi' \
    'exec npm start' \
    > /usr/local/bin/entrypoint.sh && chmod +x /usr/local/bin/entrypoint.sh
USER node
EXPOSE 3000
STOPSIGNAL SIGTERM
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
