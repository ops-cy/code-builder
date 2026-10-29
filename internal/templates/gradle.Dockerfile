# syntax=docker/dockerfile:1
# Java (Gradle)：按检测到的 Java 版本选择构建 JDK 与运行 JRE。
ARG BASE_REGISTRY=
ARG JAVA_VERSION=21
FROM ${BASE_REGISTRY}library/gradle:8-jdk${JAVA_VERSION} AS build
# 官方 Gradle 镜像默认可能以 gradle 用户运行；先切 root，避免 apt / /root 写入失败。
USER root
ARG APT_MIRROR=
ARG GRADLE_MIRROR=
RUN if [ -n "$APT_MIRROR" ]; then \
        for f in /etc/apt/sources.list /etc/apt/sources.list.d/*; do \
            [ -f "$f" ] || continue; \
            sed -i "s#deb.debian.org#$APT_MIRROR#g; s#security.debian.org#$APT_MIRROR#g; s#archive.ubuntu.com#$APT_MIRROR#g; s#security.ubuntu.com#$APT_MIRROR#g" "$f"; \
        done; \
    fi \
    && if [ -n "$GRADLE_MIRROR" ]; then \
        mkdir -p /root/.gradle && \
        printf 'allprojects { repositories { maven { url "%s" } } }\nsettingsEvaluated { settings -> settings.pluginManagement.repositories { maven { url "%s" } } }\n' \
            "$GRADLE_MIRROR" "$GRADLE_MIRROR" > /root/.gradle/init.gradle; \
    fi
WORKDIR /src
COPY . .
RUN bash .code-builder/build.sh

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
