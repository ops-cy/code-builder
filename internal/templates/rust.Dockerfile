# syntax=docker/dockerfile:1
# Rust：按配置的 Rust 版本构建，运行阶段使用带 shell 的 Debian slim。
ARG BASE_REGISTRY=
ARG RUST_VERSION=1.83
FROM ${BASE_REGISTRY}library/rust:${RUST_VERSION}-slim AS build
ARG APT_MIRROR=
ARG CARGO_REGISTRY=
RUN if [ -n "$APT_MIRROR" ]; then \
        for f in /etc/apt/sources.list /etc/apt/sources.list.d/*; do \
            [ -f "$f" ] || continue; \
            sed -i "s#deb.debian.org#$APT_MIRROR#g; s#security.debian.org#$APT_MIRROR#g; s#archive.ubuntu.com#$APT_MIRROR#g; s#security.ubuntu.com#$APT_MIRROR#g" "$f"; \
        done; \
    fi \
    && apt-get update \
    && apt-get install -y --no-install-recommends bash git ca-certificates pkg-config libssl-dev \
    && rm -rf /var/lib/apt/lists/*
RUN if [ -n "$CARGO_REGISTRY" ]; then \
        mkdir -p /root/.cargo && \
        printf '[source.crates-io]\nreplace-with = "mirror"\n\n[source.mirror]\nregistry = "%s"\n' "$CARGO_REGISTRY" > /root/.cargo/config.toml; \
    fi
WORKDIR /src
COPY . .
RUN bash .code-builder/build.sh

FROM ${BASE_REGISTRY}library/debian:bookworm-slim AS runtime
ARG APT_MIRROR=
ARG TZ=Asia/Shanghai
RUN if [ -n "$APT_MIRROR" ]; then \
        for f in /etc/apt/sources.list /etc/apt/sources.list.d/*; do \
            [ -f "$f" ] || continue; \
            sed -i "s#deb.debian.org#$APT_MIRROR#g; s#security.debian.org#$APT_MIRROR#g; s#archive.ubuntu.com#$APT_MIRROR#g; s#security.ubuntu.com#$APT_MIRROR#g" "$f"; \
        done; \
    fi \
    && apt-get update \
    && apt-get install -y --no-install-recommends bash ca-certificates tzdata curl libssl3 \
    && groupadd -r app && useradd -r -g app -u 10001 app \
    && rm -rf /var/lib/apt/lists/*
ENV TZ=${TZ} APPNAME=app
WORKDIR /app
COPY --from=build --chown=app:app /src/app /app/app
COPY .code-builder/entrypoint.sh /app/entrypoint.sh
RUN chmod +x /app/entrypoint.sh
USER app
EXPOSE 8080
STOPSIGNAL SIGTERM
ENTRYPOINT ["/app/entrypoint.sh"]
