# syntax=docker/dockerfile:1
# Go：多阶段构建，运行镜像保留 shell，便于 docker exec 排查。
# COMMAND 环境变量可覆盖默认启动命令，实现一个镜像启动多个服务。
ARG BASE_REGISTRY=
ARG GO_VERSION=1.25
FROM ${BASE_REGISTRY}library/golang:${GO_VERSION}-alpine AS build
ARG GOPROXY=https://proxy.golang.org,direct
ARG GOSUMDB=sum.golang.org
ARG APK_MIRROR=
ARG CGO_ENABLED=0
ARG APP_NAME=app
ENV GOPROXY=${GOPROXY} GOSUMDB=${GOSUMDB} GOTOOLCHAIN=auto APP_NAME=${APP_NAME}
RUN if [ -n "$APK_MIRROR" ]; then sed -i "s#dl-cdn.alpinelinux.org#$APK_MIRROR#g" /etc/apk/repositories; fi \
    && apk add --no-cache bash git ca-certificates build-base
WORKDIR /src
COPY . .
RUN bash .code-builder/build.sh ${CGO_ENABLED}

FROM ${BASE_REGISTRY}library/alpine:3.22 AS runtime
ARG APK_MIRROR=
ARG TZ=Asia/Shanghai
RUN if [ -n "$APK_MIRROR" ]; then sed -i "s#dl-cdn.alpinelinux.org#$APK_MIRROR#g" /etc/apk/repositories; fi \
    && apk add --no-cache bash ca-certificates tzdata curl \
    && addgroup -S app && adduser -S -G app -u 10001 app
ENV TZ=${TZ} APPNAME=app
WORKDIR /app
COPY --from=build /src/app /app/app
COPY .code-builder/entrypoint.sh /app/entrypoint.sh
RUN chmod +x /app/entrypoint.sh
USER app
EXPOSE 8080
STOPSIGNAL SIGTERM
ENTRYPOINT ["/app/entrypoint.sh"]
