#!/bin/sh
set -e

if [ -n "${COMMAND:-}" ]; then
    exec sh -c "${COMMAND}"
fi

if [ -f /app/dist/index.js ]; then
    exec node /app/dist/index.js "$@"
fi
if [ -f /app/server.js ]; then
    exec node /app/server.js "$@"
fi
if [ -f /app/index.js ]; then
    exec node /app/index.js "$@"
fi
if [ -f /app/.output/server/index.mjs ]; then
    exec node /app/.output/server/index.mjs "$@"
fi
if [ -f /app/.next/standalone/server.js ]; then
    exec node /app/.next/standalone/server.js "$@"
fi

exec npm start -- "$@"
