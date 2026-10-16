# syntax=docker/dockerfile:1
# Java (Gradle) + JDK 21：优先 bootJar（Spring Boot），否则 build；产物在 JRE 21 上运行。
FROM gradle:8-jdk21 AS build
WORKDIR /src
COPY . .
RUN gradle --no-daemon bootJar -x test || gradle --no-daemon build -x test

FROM eclipse-temurin:21-jre AS runtime
WORKDIR /app
COPY --from=build /src/build/libs/*.jar app.jar
EXPOSE 8080
ENTRYPOINT ["java", "-jar", "/app/app.jar"]
