FROM golang:1.24-alpine AS build

# 更换国内源下载
ENV GOPROXY=https://goproxy.cn,direct
ENV GOSUMDB=sum.golang.org

# 更换国内源下载
RUN sed -i 's#dl-cdn.alpinelinux.org#mirrors.ustc.edu.cn#g' /etc/apk/repositories \
    && apk add --no-cache git openssh-client docker-cli ca-certificates tzdata

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/gitlab-builder ./cmd/server

FROM alpine:3.21

# 国内源
RUN sed -i 's#dl-cdn.alpinelinux.org#mirrors.ustc.edu.cn#g' /etc/apk/repositories \
    && apk add --no-cache git openssh-client docker-cli docker-cli-compose ca-certificates tzdata

COPY --from=build /out/gitlab-builder /usr/local/bin/gitlab-builder

WORKDIR /app

VOLUME ["/app/workspace", "/app/logs"]

EXPOSE 8080

ENTRYPOINT ["gitlab-builder"]
