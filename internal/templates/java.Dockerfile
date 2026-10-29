# syntax=docker/dockerfile:1
# Java (Maven)：调用内置 build-maven.sh（mvn clean package -DskipTests -> app.jar），
# 在带 shell 的 JRE 21 上运行，便于 docker exec 排查。
FROM maven:3.9-eclipse-temurin-21 AS build
WORKDIR /src
COPY . .
RUN bash .code-builder/build.sh 21

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
