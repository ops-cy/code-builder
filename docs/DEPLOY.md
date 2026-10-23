# 部署文档

## 前置要求

- 构建机已安装 Docker 20+ 与 Docker Compose v2
- 构建机可访问 GitLab 实例（SSH 端口与 Web 端口）
- GitLab 侧已为目标仓库添加 Deploy Key（见 [GITLAB.md](GITLAB.md)）
- 每个被构建仓库的根目录有 `Dockerfile`，或至少含可识别的语言标记（`go.mod` / `pom.xml` / `build.gradle(.kts)` / `package.json` / `requirements.txt`|`Pipfile`|`pyproject.toml` / `composer.json`）；两者都没有时服务会明确报错（不猜测）

## 目录结构

```
gitlab-builder/
├── docker-compose.yml        # 服务编排（挂载 docker.sock 与 /data/services）
├── Dockerfile                # 服务镜像（内含 git / openssh-client / docker-cli / docker-cli-compose）
├── config/
│   └── config.yaml           # 服务配置（只读挂载进容器，server + code，可选 build/registry/deploy）
├── secrets/                  # SSH Deploy Key 私钥目录（只读挂载，需自建）
│   └── gitlab_deploy_key     # 一把全局只读 key，添加到所有被构建项目
|—— data/                      # clone 工作区（构建后自动清理）
└── logs      → /app/logs        # 构建日志 <build_id>.log
```

自动发布（可选）时，宿主机另需服务目录，且必须以**相同路径**挂载进容器：

```
/data/services/                 # deploy.services_dir（宿主机与容器内路径一致）
├── atlas-market/
│   └── docker-compose.yaml     # 服务名 = 目录名 = 镜像名；compose 内写 image: atlas-market:latest
├── ex-backend/
│   └── docker-compose.yaml
└── ...
```

## 部署步骤

### 1. 生成 Deploy Key

一把**全局只读** Deploy Key 即可（同一实例所有被构建项目共用）：

```bash
mkdir -p secrets && chmod 700 secrets
ssh-keygen -t ed25519 -C "gitlab-builder@build-host" \
  -f ./secrets/gitlab_deploy_key -N ""
chmod 600 ./secrets/*_deploy_key
```

将 `.pub` 公钥添加到每个需要构建的 GitLab 项目：**Settings → Repository → Deploy Keys**，勾选 **Read-only**，然后通过环境变量告知服务（**不写进 config.yaml**）：

```bash
export GITLAB_DEPLOY_KEY_PATH="/run/secrets/gitlab_deploy_key"
```

> 未设置 `GITLAB_DEPLOY_KEY_PATH` 时，git 使用容器内默认 ssh 身份，仅适用于免密已就绪的场景；设置但文件不可读则启动时直接失败（fail-fast）。

### 2. 环境变量

| 变量 | 必填 | 说明 |
|---|---|---|
| `GITLAB_WEBHOOK_SECRET` | 是 | Webhook Secret；未设置时启动失败（config.yaml 中 `${...}` 无法展开） |
| `GITLAB_DEPLOY_KEY_PATH` | 否 | 全局 Deploy Key 私钥路径；设置但不可访问则启动失败 |
| `REGISTRY_PASSWORD` | 否 | 供 `registry.password` 以 `${REGISTRY_PASSWORD}` 引用；仅启用推送时需要 |
| `LOG_LEVEL` | 否 | `debug` / `info`（默认）/ `warn` / `error` |

### 3. 编辑配置

编辑 `config/config.yaml`（字段均有中文注释）：

- `server`：`listen` / `workspace` / `logs_dir` / `queue_size`（默认 10，超出返回 503）/ `concurrency`（默认 2）
- `code.webhook_secret`：保持 `${GITLAB_WEBHOOK_SECRET}` 占位符
- `code.domain` **必填**：仓库所在的 GitLab 域名（如 `code.zmatrix.cn`），事件仓库地址域名必须与它**精确相等**才准入
- `code.branches`：触发分支白名单（精确匹配，默认 `["main", "master"]`）
- `build`（**可选**）：省略时标签取 Commit SHA 前 8 位；`build.tag` 设全局固定标签，`build.branch_tags`（精确匹配分支）覆盖之，例 `{main: stable, pre: latest}`
- `registry`（**可选**）：不写或 `url` 留空 = **仅本地构建**（默认）；填 `url` 才启用推送，`project` 为可选镜像名前缀，`username`/`password` 成对出现（都填则推送前 `docker login`，建议密码用 `${REGISTRY_PASSWORD}` 注入）
- `deploy`（**可选**）：`enabled: true` 开启自动发布；仅当构建产出的标签等于 `deploy.tag`（默认 `latest`）时才执行 `docker compose up -d`。`services_dir`（默认 `/data/services`，需同路径挂载）、`compose_file`（默认 `docker-compose.yaml`）、`project_name`（留空=服务目录名）、`wait`/`wait_timeout`（默认 `false`/`120s`）
- `notify`（**可选**）：`enabled: true` 且填写 `lark.webhook_url` 后推送飞书卡片；`on_started`/`on_success`/`on_failure` 默认全开，`lark.secret` 仅在机器人开启签名校验时填写，`lark.timeout` 默认 `10s`

配置文件**不包含**仓库列表、模板、分支正则等字段——任何授权域名下的项目挂上 Webhook 即接入。

### 自动发布（可选）

约定：`<services_dir>/<服务名>/<compose 文件>`，其中**目录名 = 镜像名 = compose 服务名**，compose 内以 `image: <服务名>:<tag>` 引用刚构建出的本地镜像（不要用 `build:`）。当仓库 `Backend-API`（镜像名 `backend-api`）产出 `latest` 标签时，服务执行：

```bash
docker compose -f /data/services/backend-api/docker-compose.yaml -p backend-api up -d backend-api
```

- 工作目录设为该服务目录，因此 compose 的 `.env` / `env_file` / 相对 bind mount 都能正确解析——**前提是宿主机 `/data/services` 以相同路径挂载进容器**（见 `docker-compose.yml`）。
- compose 文件名默认 `docker-compose.yaml`；若不存在会自动尝试 `docker-compose.yml` → `compose.yaml` → `compose.yml`，备份文件（如 `ow-bak-docker-compose.yaml`）**永不**被使用。
- 发布**不 pull**：直接使用本次构建出的本地 `:latest`，因此镜像名/标签必须与 compose 内 `image:` 一致。
- 发布失败**不影响**构建结果：构建仍为成功，错误写入 `deploy_err`，可在 `/api/builds/:id` 查看；构建日志中亦有 `==> deploy command: ...` 一行。

### 流水线通知（可选）

卡片格式与 Jenkins 共享库一致，通过飞书自定义机器人 Webhook 推送：

```yaml
notify:
  enabled: true
  on_started: true    # 🚀 流水线开始
  on_success: true    # ✅ 流水线成功（部署失败时追加“⚠️ 部署失败”）
  on_failure: true    # ❌ 流水线失败
  lark:
    webhook_url: "https://open.feishu.cn/open-apis/bot/v2/hook/xxxxxxxx"
    secret: ""        # 机器人开启“签名校验”时填写
    timeout: "10s"
```

卡片字段：

| 字段 | 取值 |
|---|---|
| 标题 | `{🚀/✅/❌} 流水线{开始/成功/失败} · <镜像名>`，头部颜色 blue/green/red |
| 项目名称 | 镜像名（仓库名） |
| 构建发起 | Webhook 事件的 `user_name`（缺省回退 commit 作者） |
| 当前版本 | `<分支>-<commit 短 SHA>`，如 `develop-d8f8dddf` |
| 持续时间 | 构建耗时，如 `3 min 46 sec` |
| 💬 提交摘要 | 推送 commit 的首行信息 |
| 错误摘要 | 仅失败时出现（折叠面板），最多 2000 字，超出显示“…（已截断）” |
| 推送时间 | 发送时刻（Asia/Shanghai） |

- 通知**不阻塞**构建、**不影响**构建结果：投递失败只记录 `NotifyError` 与 WARN 日志，构建状态不变。
- 投递失败会自动重试（网络错误 / 5xx / 429，最多 3 次）；4xx 不重试。
- Webhook 地址含 token，属敏感信息：错误与日志中会被脱敏为 `.../hook/****`。
- `notify.enabled: true` 时 `lark.webhook_url` 必填且必须是 `https://`；三个事件开关不能全部为 `false`（启动时校验）。

### 4. 启动

```bash
export GITLAB_WEBHOOK_SECRET="$(openssl rand -hex 32)"
export GITLAB_DEPLOY_KEY_PATH="/run/secrets/gitlab_deploy_key"   # 可选
export REGISTRY_PASSWORD="..."                                   # 可选，仅启用推送时
docker compose up -d
docker compose logs -f gitlab-builder
```

看到 `server starting` 即启动成功。

### 5. 升级

```bash
git pull
docker compose up -d --build
```

构建队列在内存中，升级会丢失未完成任务；Push 一次或在 GitLab 中手动 Trigger 即可重发。

## 安全注意事项

| 风险 | 说明与建议 |
|---|---|
| Docker Socket | 挂载 `/var/run/docker.sock` 等同宿主机 root 权限。**仅在受信任的内网构建机部署**，用防火墙限制 8080 端口来源 |
| Webhook Secret | 使用 `openssl rand -hex 32` 级别的强随机值，勿复用其他系统 secret |
| Deploy Key | 只读授权、`chmod 600`、经环境变量注入（不落配置文件）；泄漏时在 GitLab 立即 Remove 并重新生成 |
| SSH 主机密钥 | `StrictHostKeyChecking=accept-new`：首次连接 TOFU 记录到 `{workspace}/.ssh/known_hosts`，此后主机密钥变更会被拒绝；**不是**关闭校验 |
| 域名准入 | 泄露的 webhook Secret 也无法把部署密钥指向其他 SSH 主机：事件仓库地址域名不等于 `code.domain` 即拒绝（`host_not_allowed`） |
| 配置文件 | `config.yaml` 与 `secrets/` 均以 `:ro` 只读挂载 |
| 日志脱敏 | URL 中的用户凭据在日志与错误信息中一律替换为 `***`；`registry.password` 通过 stdin 传给 `docker login`，从不写入日志 |
| 镜像推送凭据 | `registry.password` 建议用 `${REGISTRY_PASSWORD}` 环境变量注入而非明文写进 `config.yaml`；`username`/`password` 要么都填、要么都留空，否则启动失败 |

## 日常运维

```bash
# 查看服务日志
docker compose logs -f gitlab-builder

# 查看某次构建日志（build_id 来自 /api/builds 或 webhook 响应）
curl http://localhost:8080/api/builds/<build_id>/log

# 查看已产出的本地镜像
docker images | head -20

# 清理无用镜像（谨慎）
docker image prune
```

工作区 `/app/workspace` 中的临时 clone 在每次构建结束后自动删除，无需手工清理。

配置了 `registry.url` 后，成功构建的镜像除本地 tag 外还会被打上远程 tag 并推送到镜像仓库；可用 `docker manifest inspect <url>/<project>/<image>:<sha8>` 或在仓库界面核对。

## 排障

| 现象 | 原因与处理 |
|---|---|
| 启动即退出：`unset environment variables` | `config.yaml` 引用了未设置的环境变量；导出 `GITLAB_WEBHOOK_SECRET` 后重启 |
| 启动即退出：`GITLAB_DEPLOY_KEY_PATH is set but not accessible` | 环境变量指向的文件不存在或不可读；核对 secrets 挂载路径 |
| webhook 返回 401 | `X-Gitlab-Token` 与 `GITLAB_WEBHOOK_SECRET` 不一致；核对 GitLab Webhook 与环境变量后重发 |
| 返回 202 `host_not_allowed` | 事件中的仓库地址域名不等于 `code.domain`；检查 `code.domain` 是否与 GitLab 访问域名一致（端口不影响匹配） |
| 返回 202 `branch_filtered` | 分支不在 `code.branches` 白名单（精确匹配，无通配）；调整配置后重发 |
| 返回 202 `duplicate` | 同仓库同 Commit 仍在排队/构建中，属预期行为 |
| 返回 503 `queue_full` | 待处理任务超过 `queue_size`；等待积压消化或调大该值 |
| 构建失败：git 相关 | 看 `/api/builds/:id/log`：`Permission denied (publickey)` 说明 Deploy Key 未配置或未添加到 GitLab；`HOST IDENTIFICATION HAS CHANGED` 说明 SSH 主机密钥被变更（TOFU 后拒绝），确认目标主机后删除 `{workspace}/.ssh/known_hosts` 对应行 |
| 构建失败：`no Dockerfile at repository root and no built-in template matched` | 该仓库根目录既没有 `Dockerfile` 也没有可识别的语言标记（Go/Node/Vue/Java/Gradle/Python/PHP）；请在仓库根目录提供自己的 `Dockerfile`，或确保语言标记可被识别 |
| 构建失败：套用了内置模板但入口/产物不符 | 模板约定：Go 构建**根包**产物 `app`、Node 入口 `dist/index.js`、Maven 产物 `app.jar`、Python 入口根目录 `main.py`、PHP 入口 `public/`；不符时在仓库根目录提供自己的 `Dockerfile` 覆盖模板 |
| 构建失败：找不到 `.gitlab-builder/build.sh` | 仓库的 `.dockerignore` 排除了该目录；请勿排除模板写入的构建脚本，或在仓库根目录提供自己的 `Dockerfile` |
| 构建失败：`docker login ... failed` | `registry.username`/`password` 与仓库不符，或仓库不支持该登录方式；核对凭据与 `registry.url`（不改 `url` 不影响本地构建） |
| 构建失败：`docker push ... failed` | 网络不通、仓库未创建对应命名空间/项目、或凭据无推送权限；本地镜像仍保留（`docker images` 可见），修复后重发即可 |
| 构建成功但仓库里没有镜像 | 未配置 `registry.url`（此时按设计只本地构建）；或推送地址与预期命名空间不符，检查日志中 `==> registry push enabled remote=...` 一行 |
| 构建失败：`docker daemon not reachable` | 容器内未挂载 docker.sock 或宿主机无 Docker；检查 `docker-compose.yml` 卷挂载 |
| 构建成功但 `/api/builds/:id` 有 `deploy_err` | 构建仍成功、仅发布失败：`no compose file` 说明服务目录/文件名不符约定或只存在备份文件；`escapes services_dir` 说明 `deploy.services[].dir` 越出服务根；`docker compose up -d failed` 见日志末尾 docker 输出；`invalid wait_timeout` 见配置 |
| 发布后容器仍是旧镜像 | compose 内用的是 `image: <服务名>:latest` 吗？若写死旧 tag 或用了 `build:` 则不会用新镜像；发布不 pull，镜像名/标签须与本次构建一致 |
| 发布找不到服务目录 | `deploy.services_dir` 是否以**相同路径**挂载进容器（宿主机 `/data/services` → 容器 `/data/services`）；相对路径挂载会导致 compose 相对路径解析错误 |
| 构建失败：`docker compose` 命令不存在 | 服务镜像缺少 compose 插件；确认 `Dockerfile` 已安装 `docker-cli-compose` 并重建镜像（`docker compose up -d --build`） |
| 没收到飞书通知 | 看 `/api/builds/:id` 的 `NotifyError`：`webhook_url must be an https URL` 见配置；`lark rejected message` 多为 Webhook 失效/签名密钥不符；`lark returned 4xx` 为地址或权限问题；WARN 日志含脱敏后的地址 |
| 飞书报签名错误 | 机器人开启“签名校验”后必须在 `notify.lark.secret` 填同一个密钥；未开启则留空 |
| 构建失败：超时 | clone 超过 15 分钟、docker build 超过 25 分钟、单任务超过 30 分钟或单次发布超过 5 分钟会自动终止并标记（发布超时仅记 `deploy_err`），查看日志定位慢点 |
