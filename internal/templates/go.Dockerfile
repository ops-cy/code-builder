FROM golang:1.25-alpine AS build

WORKDIR /src

RUN apk add --no-cache git ca-certificates

COPY go.mod ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/app ./cmd/server

FROM alpine:3.21

RUN apk add --no-cache ca-certificates

ARG APP_NAME=app

COPY --from=build /out/app /app/${APP_NAME}

USER 65532:65532

EXPOSE 8080

CMD ["/bin/sh", "-c", "exec /app/${APP_NAME}"]