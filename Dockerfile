# syntax=docker/dockerfile:1
FROM golang:1.24-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/gitlab-builder ./cmd/server

FROM alpine:3.21

RUN apk add --no-cache git openssh-client docker-cli ca-certificates tzdata

COPY --from=build /out/gitlab-builder /usr/local/bin/gitlab-builder

WORKDIR /app

VOLUME ["/app/workspace", "/app/logs"]

EXPOSE 8080

ENTRYPOINT ["gitlab-builder"]
