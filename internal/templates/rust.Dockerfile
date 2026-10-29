# syntax=docker/dockerfile:1
# Rust：调用内置 build-rust.sh（cargo build --release），
# 运行阶段使用带 shell 的 Debian slim，便于 docker exec 排查。
FROM rust:1.83-slim AS build
RUN apt-get update \
    && apt-get install -y --no-install-recommends bash git ca-certificates pkg-config libssl-dev \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY . .
RUN bash .code-builder/build.sh

FROM debian:bookworm-slim AS runtime
ARG TZ=Asia/Shanghai
RUN apt-get update \
    && apt-get install -y --no-install-recommends bash ca-certificates tzdata curl libssl3 \
    && groupadd -r app && useradd -r -g app -u 10001 app \
    && rm -rf /var/lib/apt/lists/*
ENV TZ=${TZ}
WORKDIR /app
COPY --from=build --chown=app:app /src/app /app/app
USER app
EXPOSE 8080
STOPSIGNAL SIGTERM
ENTRYPOINT ["/app/app"]
