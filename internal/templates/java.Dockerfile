# syntax=docker/dockerfile:1
# Java (Maven)：按检测到的 Java 版本选择构建 JDK 与运行 JRE。
# COMMAND 环境变量可覆盖默认启动命令。
ARG BASE_REGISTRY=
ARG JAVA_VERSION=21
FROM ${BASE_REGISTRY}library/maven:3.9-eclipse-temurin-${JAVA_VERSION} AS build
ARG JAVA_VERSION=21
ARG APT_MIRROR=
RUN if [ -n "$APT_MIRROR" ]; then \
        for f in /etc/apt/sources.list /etc/apt/sources.list.d/*; do \
            [ -f "$f" ] || continue; \
            sed -i "s#deb.debian.org#$APT_MIRROR#g; s#security.debian.org#$APT_MIRROR#g; s#archive.ubuntu.com#$APT_MIRROR#g; s#security.ubuntu.com#$APT_MIRROR#g" "$f"; \
        done; \
    fi
WORKDIR /src
COPY . .
RUN bash .code-builder/build.sh ${JAVA_VERSION}

FROM ${BASE_REGISTRY}library/eclipse-temurin:${JAVA_VERSION}-jre AS runtime
ARG APT_MIRROR=
ARG TZ=Asia/Shanghai
RUN if [ -n "$APT_MIRROR" ]; then \
        for f in /etc/apt/sources.list /etc/apt/sources.list.d/*; do \
            [ -f "$f" ] || continue; \
            sed -i "s#deb.debian.org#$APT_MIRROR#g; s#security.debian.org#$APT_MIRROR#g; s#archive.ubuntu.com#$APT_MIRROR#g; s#security.ubuntu.com#$APT_MIRROR#g" "$f"; \
        done; \
    fi \
    && apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates tzdata curl \
    && groupadd -r app && useradd -r -g app -u 10001 app \
    && rm -rf /var/lib/apt/lists/*
ENV TZ=${TZ} \
    APPNAME=app \
    JAVA_OPTS="-XX:MaxRAMPercentage=75"
WORKDIR /app
COPY --from=build --chown=app:app /src/app.jar /app/app.jar
COPY .code-builder/entrypoint.sh /app/entrypoint.sh
RUN chmod +x /app/entrypoint.sh
USER app
EXPOSE 8080
STOPSIGNAL SIGTERM
ENTRYPOINT ["/app/entrypoint.sh"]
