# syntax=docker/dockerfile:1
# Vue / React / Svelte / Angular 等前端：构建后交给 nginx 托管。
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

FROM ${BASE_REGISTRY}library/nginx:1.27-alpine AS runtime
ARG APK_MIRROR=
ARG TZ=Asia/Shanghai
RUN if [ -n "$APK_MIRROR" ]; then sed -i "s#dl-cdn.alpinelinux.org#$APK_MIRROR#g" /etc/apk/repositories; fi \
    && apk add --no-cache ca-certificates tzdata curl
ENV TZ=${TZ}
COPY --from=build /app/dist /usr/share/nginx/html
EXPOSE 80
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s \
    CMD wget -q -O /dev/null http://127.0.0.1/ || exit 1
STOPSIGNAL SIGQUIT
