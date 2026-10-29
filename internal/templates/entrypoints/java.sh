#!/bin/sh
set -e

if [ -n "${COMMAND:-}" ]; then
    exec sh -c "${COMMAND}"
fi

exec java ${JAVA_OPTS:-} -jar /app/app.jar "$@"
