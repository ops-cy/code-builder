# code-builder

多平台代码托管 Webhook 触发的**通用 Docker 镜像自动构建服务**。GitLab、GitHub、Gitee 任意仓库把 Webhook 指向本服务，Push 即自动构建本地镜像（可选推送 Registry、可选自动部署）。

## 特性

- **三平台统一入口**：GitLab / GitHub / Gitee，自动识别 payload；也可使用显式路径 `/webhook/gitlab`、`/webhook/github`、`/webhook/gitee`
- **共用 Webhook Secret**：三个平台填同一个 Secret；GitLab 用 `X-Gitlab-Token`，GitHub 用 HMAC-SHA256 签名，Gitee 支持签名 / Token / 密码
- **无需登记仓库**：只配置平台域名和分支白名单，不维护仓库列表
- **分支白名单支持 glob**：例如 `release/*`
- **仓库自带 Dockerfile 优先**：有 `Dockerfile` 就用，绝不覆盖；没有时套用内置模板
- **内置模板**：Go、Rust、Java（Maven）、Java（Gradle）、Node、Vue/React/Svelte 等前端
- **运行镜像保留 shell**：不使用 `scratch` / distroless，构建出的容器可直接 `docker exec -it <container> sh`
- **默认仅本地构建**：配置 `registry.url` 后自动打远程 tag 并 push
- **本机自动部署**：标准目录 `/data/services/<repo_name>/docker-compose.yml` 零配置部署；也支持显式 targets
- **部署更稳**：`--no-build --pull never --wait --remove-orphans`，失败时自动收集 `compose ps` 与最近日志
- **构建记录持久化**：重启后 `/api/builds` 仍可看到历史记录
- **流水线通知（可选）**：飞书卡片；部署失败只影响卡片标题，不影响构建成功状态

## 支持矩阵

| 平台 | Webhook 地址 | 校验方式 | 私有仓库 clone |
|---|---|---|---|
| GitLab | `/webhook/gitlab` 或 `/webhook` | `X-Gitlab-Token` | SSH Deploy Key 或 `gitlab.token` |
| GitHub | `/webhook/github` 或 `/webhook` | `X-Hub-Signature-256`（兼容 SHA1） | SSH Deploy Key 或 `github.token` |
| Gitee | `/webhook/gitee` 或 `/webhook` | `X-Gitee-Token`（密码或签名）/ body `password` | SSH Deploy Key 或 `gitee.token` |

## 快速开始

```bash
# 1. 准备全局 SSH Deploy Key（可选，私有仓库推荐）
mkdir -p secrets && chmod 700 secrets
ssh-keygen -t ed25519 -f ./secrets/deploy_key -N ""
# 公钥添加到各项目：GitLab Deploy Keys / GitHub Deploy keys / Gitee 部署公钥，勾选只读

# 2. 配置
#    编辑 config/config.yaml，至少填写 code.providers 中平台的 domains
export WEBHOOK_SECRET="$(openssl rand -hex 32)"
export DEPLOY_KEY_PATH="/run/secrets/deploy_key"   # 可选

# 3. 启动（compose 内含 build，可直接构建服务镜像）
docker compose up -d --build

# 4. 在代码平台添加 Webhook
#    地址：http://<构建机IP>:8080/webhook/gitlab | /github | /gitee
#    Secret / Token / 密码：与 WEBHOOK_SECRET 相同
#    事件：Push
```

## 配置示例

完整字段见 [`config/config.yaml`](config/config.yaml)。

```yaml
code:
  webhook_secret: "${WEBHOOK_SECRET}"
  branches: ["main", "master", "release/*"]
  providers:
    gitlab:
      enabled: true
      domains: ["code.gitlab.com"]
      token: "${GITLAB_TOKEN:-}"
    github:
      enabled: true
      domains: ["github.com"]        # GitHub Enterprise 填企业域名
      token: "${GITHUB_TOKEN:-}"
    gitee:
      enabled: true
      domains: ["gitee.com"]
      token: "${GITEE_TOKEN:-}"
```

旧配置仍可用：没有 `code.providers` 时，`code.domain` + `code.webhook_secret` 等价于只启用 GitLab。

## 内置模板

| 模板 | 触发文件 | 运行镜像 |
|---|---|---|
| Go | `go.mod` | `alpine:3.22`（带 shell） |
| Rust | `Cargo.toml` | `debian:bookworm-slim`（带 shell） |
| Java Maven | `pom.xml` | `eclipse-temurin:21-jre` |
| Java Gradle | `build.gradle` / `build.gradle.kts` | `eclipse-temurin:21-jre` |
| Node | `package.json` | `node:22-alpine` |
| Vue/React/Svelte/Angular 前端 | `package.json` 含对应框架或 `vite` | `nginx:1.27-alpine` |

- 仓库根目录有 `Dockerfile` 时始终优先使用它，不写任何构建脚本。
- 使用内置模板时，构建器会写入 `.code-builder/build.sh`；仓库没有 `.dockerignore` 时自动生成一份默认规则。
- 若仓库 `.dockerignore` 排除了 `.code-builder`，构建会提前报错；请删除该规则或自带 Dockerfile。
- 入口/产物约定见 [`docs/TEMPLATES.md`](docs/TEMPLATES.md)。

## 自动部署

推荐标准布局，无需在配置中登记：

```
/data/services/<repo_name>/docker-compose.yml
```

```yaml
deploy:
  enabled: true
  root: "/data/services"
  wait: true
  wait_timeout: "120s"
  pull: "never"
  remove_orphans: true
```

构建成功后执行：

```bash
docker compose -f <compose> up -d \
  --no-build --pull never --remove-orphans \
  --wait --wait-timeout 120s [service]
```

- `--no-build`：部署阶段绝不重新构建。
- `--pull never`：只使用刚构建出的本地镜像。
- `--wait`：等待容器 running/healthy。
- 失败时自动收集 `docker compose ps` 与 `docker compose logs --tail=100`，写入构建日志和通知。
- compose 可直接引用注入变量：`image: ${BUILDER_IMAGE:-app:latest}`。
- 也支持显式 `deploy.targets`，key 可以是 `repo_name` 或 `owner/repo`，优先于自动发现。

## 镜像与标签

- 镜像名 = 仓库 URL 最后一段（转小写、合法化），不同仓库撞名时追加稳定哈希后缀。
- 默认标签 `latest`；可用 `build.tag` / `build.branch_tags` 覆盖。
- `build.sha_tag: true` 时额外产出 `sha-<短SHA>`。
- 配置 `registry.url` 后，构建成功会推送 `<url>[/<project>]/<image>:<tag>`。

## API

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/webhook` | 自动识别平台 |
| POST | `/webhook/:provider` | 显式平台：`gitlab` / `github` / `gitee` |
| GET | `/api/builds` | 构建列表（最新在前） |
| GET | `/api/builds/:id` | 构建详情 |
| GET | `/api/builds/:id/log` | 构建日志（text/plain） |
| GET | `/healthz` | 健康检查 |

Webhook 响应：

| 状态码 | 含义 |
|---|---|
| 202 `queued` | 已入队 |
| 200 `pong` | 平台 ping / 测试事件 |
| 202 `ignored` | 非 Push / 删除 ref / 非分支 ref / SHA 非法 |
| 202 `host_not_allowed` | 仓库域名不属于该平台白名单 |
| 202 `branch_filtered` | 分支不在白名单 |
| 202 `duplicate` | 同仓库同 Commit 已在构建 |
| 202 `missing_repo_url` / `invalid_repo_name` | payload 缺少地址 / 无法得到合法镜像名 |
| 401 | 签名 / Token / 密码不匹配 |
| 400 | JSON 非法、body 过大、未知平台 |
| 404 | 平台未启用 |
| 503 `queue_full` | 队列已满 |

## 环境变量

| 变量 | 必填 | 说明 |
|---|---|---|
| `WEBHOOK_SECRET` | 是 | 三个平台共用的 Webhook Secret |
| `DEPLOY_KEY_PATH` | 否 | 全局 SSH Deploy Key 私钥路径；旧名 `GITLAB_DEPLOY_KEY_PATH` 仍兼容 |
| `GITLAB_TOKEN` / `GITHUB_TOKEN` / `GITEE_TOKEN` | 否 | 平台 HTTPS clone token（私有仓库） |
| `REGISTRY_PASSWORD` | 否 | `registry.password` 引用 |
| `LARK_WEBHOOK_URL` / `LARK_SECRET` | 否 | 飞书机器人 |
| `LOG_LEVEL` | 否 | `debug` / `info`（默认）/ `warn` / `error` |
| `TZ` | 否 | 默认 `Asia/Shanghai` |

## 文档

- [`docs/WEBHOOKS.md`](docs/WEBHOOKS.md) — GitLab / GitHub / Gitee Webhook 配置
- [`docs/DEPLOY.md`](docs/DEPLOY.md) — 部署、目录、安全与排障
- [`docs/TEMPLATES.md`](docs/TEMPLATES.md) — 内置模板约定与覆盖方式
