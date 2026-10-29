# syntax=docker/dockerfile:1
# Go：调用内置 build-go.sh（go mod download + go build -o app）。
# 运行阶段使用带 shell 的 Alpine，方便 docker exec 排查；不使用 scratch。
FROM golang:1.25-alpine AS build
ARG GOPROXY=https://goproxy.cn,direct
ENV GOPROXY=${GOPROXY}
RUN apk add --no-cache bash git ca-certificates
WORKDIR /src
COPY . .
RUN bash .code-builder/build.sh 0

FROM alpine:3.22 AS runtime
ARG TZ=Asia/Shanghai
RUN apk add --no-cache bash ca-certificates tzdata curl \
    && addgroup -S app && adduser -S -G app -u 10001 app
ENV TZ=${TZ}
WORKDIR /app
COPY --from=build /src/app /app/app
USER app
EXPOSE 8080
STOPSIGNAL SIGTERM
ENTRYPOINT ["/app/app"]
