#!/bin/bash
# Gradle 构建脚本
# 用法: build-gradle.sh
# 优先使用仓库自带的 Gradle Wrapper；优先 bootJar（Spring Boot），失败后回退 build。

set -euo pipefail

GRADLE="gradle"
if [ -x ./gradlew ]; then
    GRADLE="./gradlew"
elif [ -f ./gradlew ]; then
    GRADLE="bash ./gradlew"
fi

echo "[INFO] 使用 Gradle: ${GRADLE}"

if ! ${GRADLE} --no-daemon bootJar -x test; then
    echo "[WARN] bootJar 不可用，回退到 build"
    ${GRADLE} --no-daemon build -x test
fi

JAR="$(find build/libs -maxdepth 1 -name '*.jar' \
    ! -name '*-sources.jar' ! -name '*-javadoc.jar' ! -name '*-plain.jar' \
    2>/dev/null | sort | head -1 || true)"
if [ -z "${JAR}" ]; then
    # 多模块项目：回退到任意子模块的 build/libs/*.jar。
    JAR="$(find . -path '*/build/libs/*.jar' \
        ! -name '*-sources.jar' ! -name '*-javadoc.jar' ! -name '*-plain.jar' \
        2>/dev/null | sort | head -1 || true)"
fi
if [ -z "${JAR}" ]; then
    JAR="$(find build/libs -maxdepth 1 -name '*.jar' 2>/dev/null | sort | head -1 || true)"
fi
if [ -z "${JAR}" ]; then
    echo "[ERROR] Gradle 构建完成但未找到 JAR 文件"
    exit 1
fi

cp -f "${JAR}" app.jar
echo "[INFO] Gradle 构建完成，产物: app.jar (${JAR})"
