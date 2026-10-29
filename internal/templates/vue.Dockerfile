# syntax=docker/dockerfile:1
# Vue / React / Svelte / Angular 等前端：调用内置 build-node.sh 构建，
# 产物 dist/ 交给 nginx 托管。运行镜像保留 shell，便于 docker exec。
FROM node:22-alpine AS build
ARG NPM_REGISTRY=
ENV NPM_CONFIG_REGISTRY=${NPM_REGISTRY}
RUN apk add --no-cache bash git
WORKDIR /app
COPY . .
RUN bash .code-builder/build.sh lts auto

FROM nginx:1.27-alpine AS runtime
ARG TZ=Asia/Shanghai
RUN apk add --no-cache ca-certificates tzdata curl
ENV TZ=${TZ}
COPY --from=build /app/dist /usr/share/nginx/html
EXPOSE 80
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s \
    CMD wget -q -O /dev/null http://127.0.0.1/ || exit 1
STOPSIGNAL SIGQUIT
