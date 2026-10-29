# Webhook 配置（GitLab / GitHub / Gitee）

服务启动后，在任一平台的项目中添加 Webhook：

- URL：`http://<构建机IP或域名>:8080/webhook/<platform>`
- Secret：与构建机 `WEBHOOK_SECRET` 完全相同
- 事件：只勾选 **Push**

也可以统一使用 `http://<构建机IP>:8080/webhook`，服务会根据请求头自动识别平台。

## GitLab

1. 项目 → **Settings → Webhooks**
2. URL：`http://<构建机IP>:8080/webhook/gitlab`
3. **Secret token**：`WEBHOOK_SECRET`
4. Trigger 勾选 **Push events**
5. 分支过滤留空，由服务端 `code.branches` 决定

私有仓库：

1. 构建机生成密钥：`ssh-keygen -t ed25519 -f ./secrets/deploy_key -N ""`
2. 项目 → **Settings → Repository → Deploy Keys**，粘贴公钥，勾选 **Read-only**
3. 启动服务时设置 `DEPLOY_KEY_PATH=/run/secrets/deploy_key`

## GitHub

1. 仓库 → **Settings → Webhooks → Add webhook**
2. **Payload URL**：`http://<构建机IP>:8080/webhook/github`
3. **Content type**：`application/json`
4. **Secret**：`WEBHOOK_SECRET`
5. **Which events**：选择 **Just the push event**
6. 点击 **Add webhook**

服务使用 `X-Hub-Signature-256` 对原始 body 做 HMAC-SHA256 校验。

私有仓库二选一：

- 部署公钥：仓库 → **Settings → Deploy keys**，粘贴 `deploy_key.pub`，勾选只读；服务设置 `DEPLOY_KEY_PATH`
- HTTPS token：设置 `GITHUB_TOKEN`，并在 `code.providers.github.token` 引用 `${GITHUB_TOKEN:-}`

## Gitee

1. 仓库 → **管理 → WebHooks → 添加 WebHook**
2. **URL**：`http://<构建机IP>:8080/webhook/gitee`
3. **密码 / Secret**：`WEBHOOK_SECRET`
4. 选择 **Push** 事件
5. 添加

Gitee 支持两种模式：

- **密码模式**：Webhook 密码与 `WEBHOOK_SECRET` 相同，Gitee 通过 `X-Gitee-Token` 或请求体 `password` 携带密码。
- **签名模式**：`X-Gitee-Token` = `urlEncode(base64(HMAC-SHA256(timestamp + "\n" + secret)))`，时间戳在 `X-Gitee-Timestamp` 头中。服务会同时兼容两种模式。

私有仓库二选一：

- 部署公钥：仓库 → **管理 → 部署公钥管理**，添加只读公钥；服务设置 `DEPLOY_KEY_PATH`
- HTTPS token：设置 `GITEE_TOKEN`，并在 `code.providers.gitee.token` 引用 `${GITEE_TOKEN:-}`

## 准入规则

```yaml
code:
  webhook_secret: "${WEBHOOK_SECRET}"
  branches: ["main", "master", "release/*"]
  providers:
    gitlab:
      enabled: true
      domains: ["code.gitlab.com"]
    github:
      enabled: true
      domains: ["github.com"]
    gitee:
      enabled: true
      domains: ["gitee.com"]
```

- 仓库地址域名必须属于对应平台的 `domains`，否则返回 `202 host_not_allowed`
- 分支必须命中 `branches`，支持精确名称和 `path.Match` 通配符（`*`、`?`、`[...]`）
- 同一仓库同一 Commit 在排队/构建期间只入队一次，重复投递返回 `duplicate`
- 三个平台共用 `WEBHOOK_SECRET`；平台未配置或 `enabled: false` 时访问对应入口返回 404

## 验证

```bash
# 查看构建列表
curl http://<构建机IP>:8080/api/builds

# 查看构建日志
curl http://<构建机IP>:8080/api/builds/<build_id>/log
```

平台 Webhook 的最近投递记录中应看到 202；`ping` / 测试事件返回 200 `pong`。

## 常见问题

| 现象 | 处理 |
|---|---|
| 401 | Secret 与 `WEBHOOK_SECRET` 不一致；GitHub 检查是否填了 Secret |
| 404 `provider_not_enabled` | `code.providers.<platform>` 未配置或 `enabled: false` |
| 202 `host_not_allowed` | 平台 `domains` 未包含仓库域名；GitHub Enterprise 填企业域名 |
| 202 `branch_filtered` | 分支不在 `code.branches` |
| 202 `ignored` | 平台发送的不是 Push 事件，或删除了分支 |
| clone 失败 `Permission denied (publickey)` | Deploy Key 未添加到项目，或 `DEPLOY_KEY_PATH` 不可读 |
| clone 失败但仓库是私有的 | 配置平台 token，或添加只读 Deploy Key |
| 修改配置后不生效 | `docker compose up -d --build` 重建容器 |
