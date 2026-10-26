# syntax=docker/dockerfile:1
# Go：调用内置 build-go.sh（go mod tidy + go build -o app），静态产物进 scratch。
# 入口为仓库根包（go build .）。入口在 ./cmd/xxx 等子目录的项目，
# 请在仓库根目录提供自己的 Dockerfile 覆盖本模板。
FROM golang:1.27-alpine AS build
WORKDIR /src
RUN apk add --no-cache bash
COPY . .
RUN bash .gitlab-builder/build.sh 0

FROM scratch
WORKDIR /app
COPY --from=build /src/app ./app
EXPOSE 8080
ENTRYPOINT ["./app"]
