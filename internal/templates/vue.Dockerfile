# syntax=docker/dockerfile:1
# Vue / Vite 前端：调用内置 build-node.sh（默认 npm）构建，产物交给 nginx 托管。
FROM node:22-alpine AS build
WORKDIR /app
RUN apk add --no-cache bash
COPY . .
RUN bash .gitlab-builder/build.sh lts npm

FROM nginx:1.27-alpine AS runtime
COPY --from=build /app/dist /usr/share/nginx/html
EXPOSE 80
CMD ["nginx", "-g", "daemon off;"]
