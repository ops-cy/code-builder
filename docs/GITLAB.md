# GitLab 配置步骤

以下步骤在 GitLab 网页端完成，假设构建服务已按 [DEPLOY.md](DEPLOY.md) 启动。

## 1. 添加 Deploy Key（私有仓库必做）

1. 在构建机上生成一把**全局**只读密钥（如部署文档所述）：
   ```bash
   ssh-keygen -t ed25519 -f ./secrets/gitlab_deploy_key -N ""
   ```
2. 复制公钥内容：`cat ./secrets/gitlab_deploy_key.pub`
3. 打开**每个需要构建的项目** → **Settings → Repository** → 展开 **Deploy Keys**
4. **Title** 填标识（如 `gitlab-builder`），**Key** 粘贴同一把公钥
5. 勾选 **Read-only** → **Add key**
6. 通过环境变量告知服务（不写进 config.yaml）：
   ```bash
   export GITLAB_DEPLOY_KEY_PATH="/run/secrets/gitlab_deploy_key"
   ```
   compose 部署时该值已在 `docker-compose.yml` 中透传（见 DEPLOY.md）。

## 2. 创建 Webhook

1. 打开 GitLab 项目 → **Settings → Webhooks**
2. 填写：
   - **URL**：`http://<构建机IP或域名>:8080/webhook`
   - **Secret token**：填写与构建机 `GITLAB_WEBHOOK_SECRET` **完全相同**的值
3. **Trigger** 勾选 **Push events**
   - 分支过滤**不要**在 GitLab 侧配置（留空即可）：是否构建只由服务端
     `code.branches` 白名单决定，两端重复配置容易产生歧义
4. 点击 **Add webhook**

## 3. 验证连通性

1. 在 Webhook 列表中点击该 Webhook → **Test → Push events**
2. 期望返回 **200/202**。常见结果含义：

   | 返回 | 含义 |
   |---|---|
   | 202 `queued` | 成功入队，记下响应中的 `build_id`（含 `image` 与 `tag`） |
   | 202 `ignored` | 测试事件不是真实分支 Push（正常，说明链路已通） |
   | 202 `host_not_allowed` | 事件仓库地址域名不等于 `code.domain`，检查 `config.yaml` 的 `code.domain` |
   | 202 `branch_filtered` | 当前分支不在 `code.branches` 白名单 |
   | 202 `duplicate` | 同仓库同 Commit 已在排队/构建中（正常，未重复入队） |
   | 401 | Secret token 不一致 |

3. 查询构建状态：
   ```bash
   curl http://<构建机IP>:8080/api/builds
   ```

## 4. 正式触发

向白名单分支 Push 一次提交：

```bash
git commit -m "trigger build" --allow-empty
git push origin main
```

然后：

```bash
# 列表应出现 status=queued/running/success 的记录
curl http://<构建机IP>:8080/api/builds

# 查看日志（把 <build_id> 换成实际值）
curl http://<构建机IP>:8080/api/builds/<build_id>/log
```

构建成功后在构建机执行 `docker images` 可看到
`<仓库名>:<标签>` 镜像（标签默认 = commit SHA 前 8 位，配置 `build.tag` /
`build.branch_tags` 后为固定标签，如 `latest`）。默认仅本地；若 `config.yaml` 配置了
`registry.url`，同一镜像还会以 `<url>[/<project>]/<仓库名>:<标签>` 推送到镜像仓库（日志中
`==> registry push enabled remote=...` 一行可见）。若配置了 `deploy.enabled` 且产出标签等于
`deploy.tag`，服务还会对该服务目录执行 `docker compose up -d`（日志中 `==> deploy command: ...` 一行可见）。

## 5. 准入与触发策略速查

服务端 `config.yaml`（全局生效，对所有被触发仓库一致）：

```yaml
server:
  queue_size: 10                  # 待处理任务上限，超过返回 503
  concurrency: 2                  # 全局并行数；同一仓库始终串行

code:
  domain: "gitlab.example.com"    # 只接受该域名下的仓库事件（域名精确相等，端口不影响）
  branches:                       # 触发分支白名单（精确匹配）
    - "main"
    - "master"

# 可选：配置标签策略；省略则标签取 Commit SHA 前 8 位
# build:
#   tag: "latest"                 # 全局默认标签
#   branch_tags: {main: "stable"} # 按分支覆盖（精确匹配）

# 可选：配置 url 后构建成功自动推送，省略则仅本地构建
# registry:
#   url: "registry.example.com"
#   project: "tenants"
#   username: "ci"
#   password: "${REGISTRY_PASSWORD}"

# 可选：产出标签等于 tag 时自动发布（docker compose up -d）
# deploy:
#   enabled: true
#   services_dir: "/data/services"
#   tag: "latest"
```

- `feature/x` 的 Push → 不在白名单 → **不构建**（`branch_filtered`）
- 事件仓库地址在其他域名 → **不构建**（`host_not_allowed`，即使 Secret 正确也拒绝）
- 任意新项目无需改配置：在授权域名的项目上挂 Webhook 即可接入
- 构建文件：仓库根目录有 `Dockerfile` 就用它；没有时按语言标记（Go / Node / Vue / Java / Gradle / Python / PHP）自动套用内置模板与构建脚本，两者都缺才报错

## 6. 常见问题

- **Push 了但没有任何记录**：先确认 Webhook 最近交付记录（Webhooks 页面底部 **Recent Deliveries**）是否有 202；若无交付，检查 URL 可达性与网络策略
- **同一仓库连推多次**：任务按仓库（归一化 URL）串行排队；同 Commit 去重返回 `duplicate`，不同 Commit 逐个按序构建
- **修改了 config.yaml 或环境变量**：需 `docker compose up -d`（重建容器）生效
