#!/bin/bash
# Python 构建脚本
# 用法: build-python.sh
# 与 Jenkins 共享库 PythonBuilder 语义一致：优先 requirements.txt，
# 其次 Pipfile（pipenv），再次 pyproject.toml。

set -e
set -o pipefail

if [ -f requirements.txt ]; then
    echo "[INFO] pip install -r requirements.txt"
    pip install -r requirements.txt
elif [ -f Pipfile ]; then
    echo "[INFO] pipenv install (Pipfile)"
    pip install pipenv
    pipenv install --system
elif [ -f pyproject.toml ]; then
    echo "[INFO] pip install . (pyproject.toml)"
    pip install .
else
    echo "[WARN] 未找到 Python 依赖文件 (requirements.txt/Pipfile/pyproject.toml)"
fi

echo "[INFO] Python 构建完成"
