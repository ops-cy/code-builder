# 部署文档

## 前置要求

- 构建机已安装 Docker 20+ 与 Docker Compose v2
- 构建机可访问 GitLab 实例（SSH 端口与 Web 端口）
- GitLab 侧已为目标仓库添加 Deploy Key（见 [GITLAB.md](GITLAB.md)）

## 目录结构

```
gitlab-builder/
├── docker-compose.yml        # 服务编排
├── Dockerfile                # 服务镜像（内含 git / openssh-client / docker-cli）
├── config/
│   └── config.yaml           # 服务配置（只读挂载进容器）
├── secrets/                  # SSH Deploy Key 私钥目录（只读挂载，需自建）
│   ├── backend-api_deploy_key
│   └── ex-user_deploy_key
|—— data/                      # clone 工作区（构建后自动清理）
└── logs      → /app/logs        # 构建日志 <build_id>.log
```

## 部署步骤

### 1. 生成 Deploy Key

每个私有仓库建议使用**独立**的只读 Deploy Key，便于轮换与泄漏隔离：

```bash
mkdir -p secrets && chmod 700 secrets
ssh-keygen -t ed25519 -C "gitlab-builder@build-host" \
  -f ./secrets/backend-api_deploy_key -N ""
chmod 600 ./secrets/*_deploy_key
```

将对应的 `.pub` 公钥添加到 GitLab 项目：**Settings → Repository → Deploy Keys**，勾选 **Read-only**，然后在 `config.yaml` 中为该仓库填写：

```yaml
deploy_key_path: "/run/secrets/backend-api_deploy_key"
```

> 未配置 `deploy_key_path` 时，git 使用容器内默认 ssh 身份，仅适用于免密已就绪的场景。

### 2. 编辑配置

编辑 `config/config.yaml`（字段均有中文注释），要点：

- `gitlab.webhook_secret` 保持 `${GITLAB_WEBHOOK_SECRET}` 占位符
- 每个仓库的 `url`、`branches`、`build.image` 必填
- `build.dockerfile`：内置模板名或仓库内路径（默认 `Dockerfile`）

### 3. 启动

```bash
export GITLAB_WEBHOOK_SECRET="$(openssl rand -hex 32)"
docker compose up -d
docker compose logs -f gitlab-builder
```

看到 `server starting` 即启动成功。

### 4. 升级

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
| Deploy Key | 只读授权、一仓一钥、`chmod 600`；泄漏时在 GitLab 立即 Remove 并重新生成 |
| 配置文件 | `config.yaml` 与 `secrets/` 均以 `:ro` 只读挂载 |

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
| webhook 返回 401 | `X-Gitlab-Token` 与 `GITLAB_WEBHOOK_SECRET` 不一致；核对 GitLab Webhook 与环境变量后重发 |
| 返回 202 `unmatched` | 事件中的仓库 URL 与任何 `repositories[].url` 不匹配；检查配置中的 URL（含端口）是否与 GitLab 项目地址一致 |
| 返回 202 `branch_filtered` | 分支未通过 `branch_regex` 或不在 `branches` 白名单；调整配置后重发 |
| 返回 503 `queue_full` | 待处理任务超过 `queue_size`；等待积压消化或调大该值 |
| 构建失败：git 相关 | 看 `/api/builds/:id/log`：`Permission denied (publickey)` 说明 Deploy Key 未配置或未添加到 GitLab；`Host key verification failed` 正常不会出现（默认跳过严格校验） |
| 构建失败：`docker daemon not reachable` | 容器内未挂载 docker.sock 或宿主机无 Docker；检查 `docker-compose.yml` 卷挂载 |
| 构建失败：模板构建报错 | 内置模板是通用默认值；若项目结构特殊（如 Go 入口不在 `./cmd/server`），改用仓库自有 `Dockerfile` 并把 `build.dockerfile` 设为 `Dockerfile` |
