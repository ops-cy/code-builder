FROM golang:1.25-alpine AS build

# 国内源下载
ENV GOPROXY=https://goproxy.cn,direct
ENV GOSUMDB=sum.golang.org

RUN apk add --no-cache git ca-certificates

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/code-builder ./cmd/server

FROM alpine:3.22

# 国内源 + 运行所需工具（git / ssh / docker CLI / compose 插件）
RUN sed -i 's#dl-cdn.alpinelinux.org#mirrors.ustc.edu.cn#g' /etc/apk/repositories \
    && apk add --no-cache bash git openssh-client docker-cli docker-cli-compose \
       ca-certificates tzdata tini curl

COPY --from=build /out/code-builder /usr/local/bin/code-builder

ENV TZ=Asia/Shanghai

WORKDIR /app

VOLUME ["/app/workspace", "/app/logs"]

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s \
    CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1

ENTRYPOINT ["/sbin/tini", "--", "/usr/local/bin/code-builder"]
