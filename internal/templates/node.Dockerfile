# syntax=docker/dockerfile:1
# Node.js / SSR：按检测到的 Node 版本构建，运行阶段保留 shell。
# COMMAND 环境变量可覆盖默认启动命令。
ARG BASE_REGISTRY=
ARG NODE_VERSION=22
FROM ${BASE_REGISTRY}library/node:${NODE_VERSION}-alpine AS build
ARG NODE_VERSION=22
ARG NPM_REGISTRY=
ARG APK_MIRROR=
ARG PACKAGE_MANAGER=auto
RUN if [ -n "$APK_MIRROR" ]; then sed -i "s#dl-cdn.alpinelinux.org#$APK_MIRROR#g" /etc/apk/repositories; fi \
    && apk add --no-cache bash git
RUN if [ -n "$NPM_REGISTRY" ]; then npm config set registry "$NPM_REGISTRY"; fi
WORKDIR /app
COPY . .
RUN bash .code-builder/build.sh "${NODE_VERSION}" "${PACKAGE_MANAGER}"

FROM ${BASE_REGISTRY}library/node:${NODE_VERSION}-alpine AS runtime
ARG APK_MIRROR=
ARG TZ=Asia/Shanghai
RUN if [ -n "$APK_MIRROR" ]; then sed -i "s#dl-cdn.alpinelinux.org#$APK_MIRROR#g" /etc/apk/repositories; fi \
    && apk add --no-cache bash ca-certificates tzdata curl
ENV TZ=${TZ} NODE_ENV=production
WORKDIR /app
COPY --from=build --chown=node:node /app /app
COPY .code-builder/entrypoint.sh /app/entrypoint.sh
RUN chmod +x /app/entrypoint.sh
USER node
EXPOSE 3000
STOPSIGNAL SIGTERM
ENTRYPOINT ["/app/entrypoint.sh"]
