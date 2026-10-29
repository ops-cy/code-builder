# 内置模板与构建约定

## 选择顺序

1. 仓库根目录存在 `Dockerfile`：直接使用，绝不覆盖，也不写入任何脚本。
2. 没有 Dockerfile：按根目录语言标记选择内置模板，并写入 `.code-builder/build.sh`。
3. 两者都没有：构建明确失败（不猜测）。

使用内置模板时：

- 仓库没有 `.dockerignore` 会自动生成默认规则；`.code-builder` 不会被排除。
- 仓库已有 `.dockerignore` 且排除了 `.code-builder` 时，构建会提前报错。
- 所有运行镜像都保留 `sh` / `bash`，可直接 `docker exec -it <container> sh`。
- 运行阶段尽量使用非 root 用户。

## 模板约定

| 模板 | 触发文件 | 构建方式 | 运行入口 / 产物 |
|---|---|---|---|
| Go | `go.mod` | `build-go.sh`：`go mod download` + `go build -o app` | `/app/app` |
| Rust | `Cargo.toml` | `build-rust.sh`：`cargo build --release` | `/app/app` |
| Java Maven | `pom.xml` | `build-maven.sh`：`mvn clean package -DskipTests` | `app.jar` |
| Java Gradle | `build.gradle` / `.kts` | `build-gradle.sh`：优先 `bootJar`，回退 `build` | `app.jar` |
| Node | `package.json` | `build-node.sh`：自动选 npm/pnpm/yarn/bun | `dist/index.js` → `server.js` → `index.js` → `.output/server/index.mjs` → `.next/standalone/server.js` → `npm start` |
| Vue/前端 | `package.json` 含 Vue/React/Svelte/Angular/Vite 等 | `build-node.sh` | `dist/` 交给 nginx |

### Go 入口

脚本按以下顺序确定入口包：

1. 环境变量 `GO_BUILD_PACKAGE`
2. 根目录 `main.go` → `.`
3. 只有一个 `cmd/<name>/main.go` → `./cmd/<name>`
4. `go list` 找到唯一 `main` 包
5. 仍无法确定时失败，并提示自带 Dockerfile 或设置 `GO_BUILD_PACKAGE`

### Node / 前端

- 包管理器根据 `pnpm-lock.yaml`、`yarn.lock`、`bun.lockb`、`package-lock.json` 自动选择。
- 只有 `package.json` 存在 `build` 脚本时才执行构建。
- 产物兼容 `dist/`、CRA 的 `build/`、Nuxt 的 `.output/public/`、Angular 的 `dist/<project>/`。
- Node 模板运行阶段保留完整 `node_modules`，SSR 项目可直接 `npm start`。

### Java

- Maven 优先使用仓库自带 `./mvnw`，否则用镜像内 `mvn`。
- Gradle 优先使用 `./gradlew`，否则用镜像内 `gradle --no-daemon`。
- 自动跳过 `-sources`、`-javadoc`、`-plain` 附属 jar，复制主 jar 为 `app.jar`。
- 运行阶段支持 `JAVA_OPTS` 环境变量。

## 覆盖模板

入口、端口或产物不符合约定时，在仓库根目录提供自己的 `Dockerfile` 即可。服务不会修改或覆盖它。

## 构建参数与标签

构建器会向镜像注入：

```text
BUILD_SHA / BUILD_BRANCH / BUILD_TAG / BUILD_DATE / BUILD_VERSION
```

以及 OCI labels：

```text
org.opencontainers.image.revision
org.opencontainers.image.created
org.opencontainers.image.version
org.opencontainers.image.source
```

可通过 `build.args`、`build.labels` 追加，`build.platform` 指定平台，`build.cache_from` 指定缓存源。
