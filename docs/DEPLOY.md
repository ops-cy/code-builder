# 部署与运维

## 前置要求

- 构建机已安装 Docker 20+ 与 Docker Compose v2.17+（部署使用 `--wait` / `--pull never`）
- 构建机可访问 GitLab / GitHub / Gitee
- 私有仓库已添加只读 Deploy Key，或配置平台 HTTPS token
- 被构建仓库根目录有 `Dockerfile`，或包含可识别语言标记（Go / Rust / Maven / Gradle / Node / Vue）

## 目录结构

```
code-builder/
├── docker-compose.yml        # 服务编排（build、docker.sock、配置、secrets、工作区、日志、服务目录）
├── Dockerfile                # 服务镜像（git / openssh-client / docker-cli / compose / tini）
├── config/
│   └── config.yaml           # 服务配置
├── secrets/                  # SSH Deploy Key 私钥目录（只读挂载，需自建）
│   └── deploy_key
├── data/                     # clone 工作区（构建后自动清理）
├── logs/                     # 构建日志 + builds.json（构建记录持久化）
└── /data/services/           # 宿主机服务目录（可选自动部署），同路径只读挂载
```

## 启动

```bash
export WEBHOOK_SECRET="$(openssl rand -hex 32)"
export DEPLOY_KEY_PATH="/run/secrets/deploy_key"   # 可选
export GITHUB_TOKEN="..."                          # 可选
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

## 环境变量

| 变量 | 必填 | 说明 |
|---|---|---|
| `WEBHOOK_SECRET` | 是 | 三个平台共用 Webhook Secret |
| `DEPLOY_KEY_PATH` | 否 | 全局 SSH Deploy Key；旧名 `GITLAB_DEPLOY_KEY_PATH` 兼容 |
| `GITLAB_TOKEN` / `GITHUB_TOKEN` / `GITEE_TOKEN` | 否 | 平台 HTTPS clone token |
| `REGISTRY_PASSWORD` | 否 | 镜像仓库密码 |
| `LARK_WEBHOOK_URL` / `LARK_SECRET` | 否 | 飞书机器人 |
| `LOG_LEVEL` | 否 | `debug` / `info` / `warn` / `error` |
| `TZ` | 否 | 默认 `Asia/Shanghai` |

## 自动部署

标准布局无需配置 targets：

```
/data/services/<repo_name>/docker-compose.yml
```

也支持：

```
/data/services/<namespace>/<repo_name>/docker-compose.yml
/data/services/<owner>/<repo>/docker-compose.yml
```

```yaml
deploy:
  enabled: true
  root: "/data/services"
  compose_files: ["docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"]
  wait: true
  wait_timeout: "120s"
  pull: "never"
  remove_orphans: true
```

执行命令：

```bash
docker compose -f <compose> up -d \
  --no-build \
  --pull never \
  --remove-orphans \
  --wait --wait-timeout 120s \
  [service]
```

- **不 pull、不 build**：只使用刚构建出的本地镜像。
- **--wait**：等待容器 running/healthy，避免“命令成功但服务没起来”。
- 失败时自动收集 `compose ps` 和 `compose logs --tail=100`，写入构建日志和飞书卡片。
- 同一目录的部署在进程内串行。
- compose 可直接引用 `${BUILDER_IMAGE}`、`${BUILDER_TAG}`、`${BUILDER_SHA}`、`${BUILDER_BRANCH}`、`${BUILDER_REPO}`、`${BUILDER_PROVIDER}`。

### 显式 targets

```yaml
deploy:
  enabled: true
  targets:
    acme/atlas-market:                 # 优先按 owner/repo 匹配
      dir: "/data/services/atlas-market"
      compose_file: ""                 # 留空自动探测
      service: "api"
      project_name: ""
      env:
        APP_ENV: "prod"
```

匹配顺序：`owner/repo` → `repo_name` → 自动发现。显式 target 优先。

### 部署失败

构建状态仍为 `success`，部署结果单独记录：

- `DeployStatus`: `deployed` / `skipped` / `failed`
- `DeployError`：失败原因
- `DeployOutput`：`compose ps` 与最近容器日志

飞书成功卡片标题会变为 `⚠️ 构建成功，部署失败`。

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
