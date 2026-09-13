# AGENTS.md

## 项目概览

KunCodeRelayPulse 是一个 Go 后端与 React 前端组成的 API/模型状态监控服务。后端入口位于 `cmd/pulse`，前端位于 `frontend/`，前端构建产物会嵌入 Go 二进制或 Docker 镜像。

## 开发约定

- 后端修改后运行 `go test -count=1 -p 1 ./...` 和 `go vet -p 1 ./...`。
- 前端修改后在 `frontend/` 运行 `npm run build`。
- 优先复用现有 Go、React、Tailwind CSS 和 shadcn/ui 模式，避免无关重构。
- 不要提交真实配置、API Key、代理凭据、数据库、日志或本机构建产物。

## 敏感文件

以下内容只允许保留在本地：

- `config.yaml`
- 含真实 Key 的渠道配置，例如 `channels.d/newapi--wcnmb--luna.yaml`
- 含真实凭据的代理配置
- `data/` 下的 SQLite 数据库和运行日志
- 本地生成的 `pulse`、`pulse.exe` 和前端构建缓存

需要示例时使用 `config.example.yaml`、`channels.d/example.yaml`、`proxies.d/example.yaml` 和模板文件。

## 版本规则

版本使用 `vMAJOR.MINOR.PATCH`，预发布版本在补丁号后增加预发布标识：

| 类型 | Git Tag | Docker 镜像 Tag |
| --- | --- | --- |
| 稳定版 | `v1.1.0` | `v1.1.0`、`latest` |
| 最新稳定版别名 | `latest` | 指向最近一次成功发布的稳定版 |
| Beta 测试版 | `v1.1.0-beta` | `v1.1.0-beta` |
| Alpha 开发版 | `v1.1.0-alpha` | `v1.1.0-alpha` |
| Release Candidate | `v1.1.0-rc.1` | `v1.1.0-rc.1` |

规则如下：

- 稳定版 Tag 必须是完整的 `vMAJOR.MINOR.PATCH`，例如 `v1.2.0`。
- Alpha、Beta 和 RC 是预发布版本，只推送对应的完整 Docker Tag，不得覆盖 `latest`。
- `latest` 始终指向最近一次成功发布的稳定版。
- 不生成 `1.2.0`、`1.2` 等缩短版本 Tag。
- RC 使用递增序号，例如 `v1.2.0-rc.1`、`v1.2.0-rc.2`。

## 发布流程

发布前确认测试通过、敏感文件未被暂存，然后创建并推送 Tag：

```bash
git tag -a v1.1.0 -m "Release v1.1.0"
git push origin v1.1.0
```

预发布版本使用对应的预发布 Tag：

```bash
git tag -a v1.1.0-beta -m "Release v1.1.0-beta"
git push origin v1.1.0-beta
```

Docker Hub 发布 Workflow 只由 `v*` Tag 推送触发，负责构建并推送 `linux/amd64` 和 `linux/arm64` 镜像。分支 Push 和 Pull Request 不发布镜像。

镜像仓库使用 Docker 规范的小写路径：

```text
${DOCKERHUB_USERNAME}/kuncoderelaypulse
```

## 变更边界

修改 CI/CD 或文档时，不要顺带修改探测器、渠道管理、代理管理和前端业务功能。涉及发布规则的变更必须同时检查 `.github/workflows/docker-publish.yml`、`README.md` 和本文件。
