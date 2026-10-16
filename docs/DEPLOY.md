# 部署文档

## 前置要求

- 构建机已安装 Docker 20+ 与 Docker Compose v2
- 构建机可访问 GitLab 实例（SSH 端口与 Web 端口）
- GitLab 侧已为目标仓库添加 Deploy Key（见 [GITLAB.md](GITLAB.md)）
- 每个被构建仓库的根目录有 `Dockerfile`，或至少含可识别的语言标记（`go.mod` / `pom.xml` / `build.gradle(.kts)` / `package.json`）；两者都没有时服务会明确报错（不猜测）

## 目录结构

```
gitlab-builder/
├── docker-compose.yml        # 服务编排
├── Dockerfile                # 服务镜像（内含 git / openssh-client / docker-cli）
├── config/
│   └── config.yaml           # 服务配置（只读挂载进容器，仅 server + code 两段）
├── secrets/                  # SSH Deploy Key 私钥目录（只读挂载，需自建）
│   └── gitlab_deploy_key     # 一把全局只读 key，添加到所有被构建项目
|—— data/                      # clone 工作区（构建后自动清理）
└── logs      → /app/logs        # 构建日志 <build_id>.log
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
| `LOG_LEVEL` | 否 | `debug` / `info`（默认）/ `warn` / `error` |

### 3. 编辑配置

编辑 `config/config.yaml`（字段均有中文注释），配置只有两段：

- `server`：`listen` / `workspace` / `logs_dir` / `queue_size`（默认 10，超出返回 503）/ `concurrency`（默认 2）
- `code.webhook_secret`：保持 `${GITLAB_WEBHOOK_SECRET}` 占位符
- `code.domain` **必填**：仓库所在的 GitLab 域名（如 `code.zmatrix.cn`），事件仓库地址域名必须与它**精确相等**才准入
- `code.branches`：触发分支白名单（精确匹配，默认 `["main", "master"]`）

配置文件**不包含**仓库列表、模板、分支正则等字段——任何授权域名下的项目挂上 Webhook 即接入。

### 4. 启动

```bash
export GITLAB_WEBHOOK_SECRET="$(openssl rand -hex 32)"
export GITLAB_DEPLOY_KEY_PATH="/run/secrets/gitlab_deploy_key"   # 可选
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
| 日志脱敏 | URL 中的用户凭据在日志与错误信息中一律替换为 `***` |

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
| 构建失败：`no Dockerfile at repository root and no built-in template matched` | 该仓库根目录既没有 `Dockerfile` 也没有可识别的语言标记（Go/Node/Vue/Java/Gradle）；请在仓库根目录提供自己的 `Dockerfile`，或确保语言标记可被识别 |
| 构建失败：套用了内置模板但入口/产物不符 | 模板约定：Go 构建**根包**产物 `app`、Node 入口 `dist/index.js`、Maven 产物 `app.jar`；不符时在仓库根目录提供自己的 `Dockerfile` 覆盖模板 |
| 构建失败：找不到 `.gitlab-builder/build.sh` | 仓库的 `.dockerignore` 排除了该目录；请勿排除模板写入的构建脚本，或在仓库根目录提供自己的 `Dockerfile` |
| 构建失败：`docker daemon not reachable` | 容器内未挂载 docker.sock 或宿主机无 Docker；检查 `docker-compose.yml` 卷挂载 |
| 构建失败：超时 | clone 超过 15 分钟、docker build 超过 25 分钟或单任务超过 30 分钟会自动终止并标记失败，查看日志定位慢点 |
