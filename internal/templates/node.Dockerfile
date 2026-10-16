# syntax=docker/dockerfile:1
# Node.js 服务：调用内置 build-node.sh（默认 npm）安装依赖并构建，
# 运行阶段仅装生产依赖、非 root 运行，默认入口 dist/index.js。
# 其他包管理器/入口请在仓库根目录提供自己的 Dockerfile 覆盖本模板。
FROM node:22-alpine AS build
WORKDIR /app
RUN apk add --no-cache bash
COPY . .
RUN bash .gitlab-builder/build.sh lts npm

FROM node:22-alpine AS runtime
WORKDIR /app
ENV NODE_ENV=production
COPY package*.json ./
RUN npm ci --omit=dev && npm cache clean --force
COPY --from=build /app/dist ./dist
USER node
EXPOSE 3000
CMD ["node", "dist/index.js"]
