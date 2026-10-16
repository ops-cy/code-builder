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
6. 在 `config/config.yaml` 的全局构建段填写一次：
   ```yaml
   build:
     deploy_key_path: "/run/secrets/gitlab_deploy_key"
   ```

## 2. 创建 Webhook

1. 打开 GitLab 项目 → **Settings → Webhooks**
2. 填写：
   - **URL**：`http://<构建机IP或域名>:8080/webhook`
   - **Secret token**：填写与构建机 `GITLAB_WEBHOOK_SECRET` **完全相同**的值
3. **Trigger** 勾选 **Push events**
   - 分支过滤**不要**在 GitLab 侧配置（留空即可）：是否构建由服务端
     `branches` 白名单与 `branch_regex` 决定，两端重复配置容易产生歧义
4. 点击 **Add webhook**

## 3. 验证连通性

1. 在 Webhook 列表中点击该 Webhook → **Test → Push events**
2. 期望返回 **200/202**。常见结果含义：

   | 返回 | 含义 |
   |---|---|
   | 202 `queued` | 成功入队，记下响应中的 `build_id` |
   | 202 `ignored` | 测试事件不是真实分支 Push（正常，说明链路已通） |
   | 202 `host_not_allowed` | 事件仓库地址不在 `gitlab.host` 域名下，检查 `config.yaml` 的 `gitlab.host` |
   | 202 `branch_filtered` | 当前分支不在触发白名单 |
   | 401 | Secret token 不一致 |

3. 查询构建状态：
   ```bash
   curl http://<构建机IP>:8080/api/builds
   ```

## 4. 正式触发

向配置的触发分支 Push 一次提交：

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
`<image>:<分支>-<短SHA>` 镜像（仅本地，不推送）。

## 5. 分支触发策略速查

服务端 `config.yaml`（全局生效，对所有被触发仓库一致）：

```yaml
gitlab:
  host: "gitlab.example.com"   # 只接受该域名下的项目事件

build:
  branch_regex: ".*"           # 门槛：先过正则（默认匹配所有分支）
  branches:                    # 白名单：再精确命中
    - "main"
    - "release/*"              # 支持通配
  dockerfile: "auto"           # 默认：仓库自带 Dockerfile 优先，否则自动选模板
```

- `feature/*` 的 Push：正则通过，但不在白名单 → **不构建**
- `release/1.2` 的 Push：正则通过 + 通配命中 → **构建**
- 想用正则完全接管匹配：白名单写全需要的分支名；两道过滤是 **AND** 关系
- 任意新项目无需改配置：挂上 Webhook 即按上述规则构建

## 6. 常见问题

- **Push 了但没有任何记录**：先确认 Webhook 最近交付记录（Webhooks 页面底部 **Recent Deliveries**）是否有 202；若无交付，检查 URL 可达性与网络策略
- **同一仓库连推多次**：任务按仓库串行排队，属预期行为，逐个按序构建
- **修改了 config.yaml**：需 `docker compose restart gitlab-builder` 生效
