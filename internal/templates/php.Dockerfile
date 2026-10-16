# syntax=docker/dockerfile:1
# PHP：调用内置 build-php.sh（composer install --no-dev --optimize-autoloader），
# 运行阶段用 PHP 内置服务器托管 public/ 目录，监听 8000。
# 入口不是 public/ 的项目，请在仓库根目录提供自己的 Dockerfile 覆盖本模板。
FROM composer:2 AS composer

FROM php:8.2-cli AS build
WORKDIR /app
COPY --from=composer /usr/bin/composer /usr/local/bin/composer
COPY . .
RUN bash .gitlab-builder/build.sh

FROM php:8.2-cli AS runtime
WORKDIR /app
COPY --from=build /app /app
EXPOSE 8000
CMD ["php", "-S", "0.0.0.0:8000", "-t", "public"]
