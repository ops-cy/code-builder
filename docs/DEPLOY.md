# 部署与运维

## 前置要求

- 构建机已安装 Docker 20+ 与 Docker Compose v2（如需 `wait: true`，建议 Compose 2.17+）
- 构建机可访问 GitLab / GitHub / Gitee
- 私有仓库已添加只读 Deploy Key，或配置平台 HTTPS token
- 被构建仓库根目录有 `Dockerfile`，或包含可识别语言标记（Go / Rust / Maven / Gradle / Node / Vue）

## 目录结构

```
code-builder/
├── docker-compose.yml        # 服务编排（build、docker.sock、配置、secrets、工作区、日志、服务目录）
├── Dockerfile                # 服务镜像（git / openssh-client / docker-cli / compose / tini）
├── config/
│   └── config.yaml           # 全部非敏感配置
├── .env                      # 敏感变量（从 .env.example 复制，不提交）
├── .env.example              # 敏感变量示例
├── secrets/                  # SSH Deploy Key 私钥目录（只读挂载，需自建）
│   └── deploy_key
├── data/                     # clone 工作区（构建后自动清理）
├── logs/                     # 构建日志 + builds.json（构建记录持久化）
└── /data/services/           # 宿主机服务目录（可选自动部署），同路径只读挂载
```

## 启动

```bash
cp .env.example .env
# 编辑 .env，至少设置 WEBHOOK_SECRET
# 编辑 config/config.yaml，填写 domains / branches / deploy 等
# 使用 SSH 时在 config.yaml 设置 code.deploy_key_path: "/run/secrets/deploy_key"

docker compose up -d --build
docker compose logs -f code-builder
```

看到 `starting code-builder` 即启动成功。

## 升级

```bash
git pull
docker compose up -d --build
```

构建记录已持久化到 `logs/builds.json`；队列仍在内存中，升级会中断未完成任务，重新 Push 即可。

## 环境变量（.env）

`docker-compose.yml` 使用 `env_file: .env`，以下变量只放 `.env`：

| 变量 | 必填 | 说明 |
|---|---|---|
| `WEBHOOK_SECRET` | 是 | 三个平台共用 Webhook Secret |
| `GITLAB_TOKEN` / `GITHUB_TOKEN` / `GITEE_TOKEN` | 否 | 平台 HTTPS clone token |
| `REGISTRY_PASSWORD` | 否 | 镜像仓库密码 |
| `LARK_WEBHOOK_URL` / `LARK_SECRET` | 否 | 飞书机器人 |
| `LOG_LEVEL` | 否 | 临时覆盖 `server.log_level` |
| `TZ` | 否 | 默认 `Asia/Shanghai` |

其余配置均在 `config/config.yaml`：监听、日志级别、分支、平台域名、下载源、标签、语言版本、部署 targets、通知开关等。

## 自动部署

当前部署方式：

```text
/data/services/{服务别名}/docker-compose.yaml
```

服务别名不等于仓库名时，用 `deploy.targets` 做映射：

```yaml
deploy:
  enabled: true
  compose_files: ["docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"]
  wait: false
  wait_timeout: "120s"
  pull: "never"
  remove_orphans: false
  targets:
    atlas-market:                         # 仓库名或 owner/repo
      dir: "/data/services/order-service" # 服务别名目录
      compose_file: "docker-compose.yaml"
      service: ""                         # 留空启动整个 compose
      project_name: ""
      env:
        APP_ENV: "prod"
```

执行命令：

```bash
docker compose -f <compose> up -d --no-build --pull never [service]
```

- **不 pull、不 build**：只使用刚构建出的本地镜像。
- `wait` / `remove_orphans` 默认关闭，需要时显式开启。
- 失败时自动收集 `compose ps` 和 `compose logs --tail=100`，写入构建日志和飞书卡片。
- 同一目录的部署在进程内串行。
- compose 可直接引用 `${BUILDER_IMAGE}`、`${BUILDER_TAG}`、`${BUILDER_SHA}`、`${BUILDER_BRANCH}`、`${BUILDER_REPO}`、`${BUILDER_PROVIDER}`。

### 一个镜像启动多个服务

运行镜像的 entrypoint 支持 `COMMAND`，也支持 compose `command` 参数：

```yaml
services:
  api:
    image: ${BUILDER_IMAGE}
    command: ["--role=api"]

  worker:
    image: ${BUILDER_IMAGE}
    command: ["--role=worker"]

  job:
    image: ${BUILDER_IMAGE}
    environment:
      COMMAND: "/app/app --role=job"
```

这样构建一次镜像，部署阶段启动多个不同命令的容器。

### 匹配顺序

`owner/repo` → `repo_name` → `deploy.root` 自动发现。显式 target 优先。

### 部署失败

构建状态仍为 `success`，部署结果单独记录：

- `DeployStatus`: `deployed` / `skipped` / `failed`
- `DeployError`：失败原因
- `DeployOutput`：`compose ps` 与最近容器日志

飞书成功卡片标题会变为 `⚠️ 构建成功，部署失败`。

## 下载源

```yaml
network:
  region: auto
  probe_timeout: "3s"
  cache_ttl: "10m"
  # 留空使用区域默认值
  goproxy: ""
  npm_registry: ""
  maven_mirror: ""
  gradle_mirror: ""
  cargo_registry: ""
  apk_mirror: ""
  apt_mirror: ""
```

- `auto` 自动探测国内 / 国外。
- `cn` / `global` 可强制指定。
- 单项非空时覆盖自动结果。
- Docker 基础镜像加速由宿主机 `/etc/docker/daemon.json` 维护，本服务不配置。

## 安全

| 风险 | 建议 |
|---|---|
| Docker Socket | 挂载 `/var/run/docker.sock` 等同宿主机 root，仅在受信任内网使用 |
| Webhook Secret | 使用 `openssl rand -hex 32`，三个平台填同一个强随机值 |
| Deploy Key | 只读授权、`chmod 600`、不写入 config.yaml |
| SSH 主机密钥 | `StrictHostKeyChecking=accept-new`：首次 TOFU，之后拒绝变更 |
| 域名准入 | 仓库地址域名必须属于平台白名单，防止 Secret 泄露后被指向其他主机 |
| 配置文件 | `config.yaml` 与 `secrets/` 均只读挂载 |
| 日志脱敏 | URL 凭据替换为 `***`；registry 密码通过 stdin 传递 |

## 排障

| 现象 | 处理 |
|---|---|
| 启动即退出：`unset environment variables` | 未设置 `WEBHOOK_SECRET` 等引用变量 |
| 启动即退出：`DEPLOY_KEY_PATH ... not accessible` | 检查 secrets 挂载与文件权限 |
| 404 `provider_not_enabled` | 平台未在 `code.providers` 启用 |
| 401 | Webhook Secret 不一致；GitHub 必须填写 Secret |
| 202 `host_not_allowed` | 平台 `domains` 不包含仓库域名 |
| 202 `branch_filtered` | 分支不在白名单 |
| 构建失败 `no Dockerfile ... no built-in template matched` | 仓库没有 Dockerfile 且无 Go/Rust/Java/Gradle/Node/Vue 标记 |
| 构建失败：模板入口不符 | 根目录放自己的 Dockerfile 覆盖模板，见 TEMPLATES.md |
| 构建失败：`docker daemon not reachable` | 未挂载 docker.sock |
| 部署失败：`service dir ... does not exist` | `deploy.root` / `targets[].dir` 路径错误或未同路径挂载 |
| 部署失败：`no compose file` | 目录内没有支持的 compose 文件名 |
| 部署后仍是旧镜像 | compose 未引用本地镜像，或 `image:` 写死错误；使用 `${BUILDER_IMAGE}` |
| 构建成功但无远程镜像 | 未配置 `registry.url`，或推送失败（看日志） |
| 重启后构建列表为空 | 检查 `server.state_file` 与 `logs/` 挂载 |
