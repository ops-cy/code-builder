# syntax=docker/dockerfile:1
# Java (Maven)：调用内置 build-maven.sh（mvn clean package -DskipTests → app.jar），
# 在 JRE 21 上运行。
FROM maven:3.9-eclipse-temurin-21 AS build
WORKDIR /src
COPY . .
RUN bash .gitlab-builder/build.sh 21

FROM eclipse-temurin:21-jre AS runtime
WORKDIR /app
COPY --from=build /src/app.jar app.jar
EXPOSE 8080
ENTRYPOINT ["java", "-jar", "/app/app.jar"]
