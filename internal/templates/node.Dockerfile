# syntax=docker/dockerfile:1
FROM node:22-alpine AS build

WORKDIR /src

RUN apk add --no-cache libc6-compat

COPY package*.json ./
RUN if [ -f yarn.lock ]; then yarn install --frozen-lockfile; \
    elif [ -f pnpm-lock.yaml ]; then corepack enable && pnpm install --frozen-lockfile; \
    else npm ci; fi

COPY . .

ARG NODE_ENV=production
RUN if [ -f yarn.lock ]; then yarn build; \
    elif [ -f pnpm-lock.yaml ]; then pnpm build; \
    else npm run build; fi

FROM node:22-alpine

WORKDIR /app

ENV NODE_ENV=production

RUN apk add --no-cache ca-certificates \
    && addgroup -g 1001 -S app \
    && adduser -S app -u 1001 -G app

COPY --from=build --chown=app:app /src/node_modules ./node_modules
COPY --from=build --chown=app:app /src/package.json ./package.json
COPY --from=build --chown=app:app /src/dist ./dist

USER app

ARG APP_ENTRY=server.js
ENV APP_ENTRY=${APP_ENTRY}

EXPOSE 8080

CMD ["sh", "-c", "exec node ${APP_ENTRY}"]
