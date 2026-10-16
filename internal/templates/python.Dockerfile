# syntax=docker/dockerfile:1
# Python：调用内置 build-python.sh（requirements.txt / Pipfile / pyproject.toml），
# 依赖装入运行镜像，默认入口为仓库根目录的 main.py。
# 入口不是根目录 main.py 的项目，请在仓库根目录提供自己的 Dockerfile 覆盖本模板。
FROM python:3.12-slim AS build
WORKDIR /app
ENV PIP_NO_CACHE_DIR=1
COPY . .
RUN bash .gitlab-builder/build.sh

FROM python:3.12-slim AS runtime
WORKDIR /app
ENV PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1
COPY --from=build /app /app
CMD ["python", "main.py"]
