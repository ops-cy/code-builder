# syntax=docker/dockerfile:1
# Java (Gradle)：调用内置 build-gradle.sh，优先 bootJar（Spring Boot），
# 否则执行 build；产物在带 shell 的 JRE 21 上运行。
FROM gradle:8-jdk21 AS build
# 官方 Gradle 镜像默认以 gradle 用户运行；构建阶段显式用 root，避免 /src 写入权限问题。
USER root
WORKDIR /src
COPY . .
RUN bash .code-builder/build.sh

FROM eclipse-temurin:21-jre AS runtime
ARG TZ=Asia/Shanghai
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates tzdata curl \
    && groupadd -r app && useradd -r -g app -u 10001 app \
    && rm -rf /var/lib/apt/lists/*
ENV TZ=${TZ} \
    JAVA_OPTS="-XX:MaxRAMPercentage=75"
WORKDIR /app
COPY --from=build --chown=app:app /src/app.jar /app/app.jar
USER app
EXPOSE 8080
STOPSIGNAL SIGTERM
ENTRYPOINT ["sh", "-c", "exec java $JAVA_OPTS -jar /app/app.jar"]
