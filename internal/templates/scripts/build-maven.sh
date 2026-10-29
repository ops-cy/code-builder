#!/bin/bash
# Maven 构建脚本
# 用法: build-maven.sh <java-version>

set -euo pipefail

JAVA_VERSION="${1:-21}"

echo "[INFO] 请求 Java 版本: ${JAVA_VERSION}（使用构建镜像自带 JDK）"
java -version 2>&1 | head -1 || true

MVN="mvn"
if [ -x ./mvnw ]; then
    MVN="./mvnw"
elif [ -f ./mvnw ]; then
    MVN="bash ./mvnw"
fi
echo "[INFO] 使用 Maven: ${MVN}"

${MVN} -B -ntp -DskipTests clean package

# 复制产物：优先主 jar，跳过 sources / javadoc / plain 附属包。
JAR="$(find target -maxdepth 1 -name '*.jar' \
    ! -name '*-sources.jar' ! -name '*-javadoc.jar' ! -name '*-plain.jar' \
    2>/dev/null | sort | head -1 || true)"
if [ -z "${JAR}" ]; then
    # 多模块项目：回退到任意子模块的 target/*.jar。
    JAR="$(find . -path '*/target/*.jar' \
        ! -name '*-sources.jar' ! -name '*-javadoc.jar' ! -name '*-plain.jar' \
        2>/dev/null | sort | head -1 || true)"
fi
if [ -z "${JAR}" ]; then
    JAR="$(find target -maxdepth 1 -name '*.jar' 2>/dev/null | sort | head -1 || true)"
fi
if [ -z "${JAR}" ]; then
    echo "[ERROR] Maven 构建完成但未找到 JAR 文件"
    exit 1
fi

cp -f "${JAR}" app.jar
echo "[INFO] Maven 构建完成，产物: app.jar (${JAR})"
