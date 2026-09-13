# KunCodeRelayPulse

对 API 中转/模型服务做**真实业务请求探测**的单二进制状态监测（Go）。
核心思路来自 `status-monitor-design.md`：状态不能靠 ping 判断，必须发真实请求并校验响应内容。

> 仓库只提交示例配置。真实 Key、管理员配置和本机代理配置保存在本地，并被 `.gitignore` 排除。

## 功能

- 模板驱动探测：`templates/*.json` 定义 HTTP 形态 + `success_contains` 内容校验
- 二级状态：绿/黄/红 + `sub_status`（timeout / content_mismatch / invalid_request / upstream_error / network_error / slow）
- 多 Provider：`channels.d/` 每通道一个 yaml；`hidden: true` 的通道首页不展示，管理员登录后可见
- 多代理出口：`proxies.d/` 可维护多个 HTTP/HTTPS/SOCKS5/SOCKS5H 代理，渠道可单独选择
- 调度：错峰启动、全局并发上限、通道级 interval 覆盖
- 存储：SQLite（纯 Go 驱动），probe_log 只 append，90 天按天分桶，保留期自动清理
- 热更新：fsnotify 监听配置目录，fail-closed（坏配置保留旧配置），失败暴露到 `/ready`（503）
- 事件：连续 N 次失败判 down / 恢复判 up，写 event 表并通知（webhook / Telegram）
- 状态页：单页（90 天 uptime 条 + 当前状态 + 延迟），embed 进二进制

## 快速开始

### 本地开发（推荐）

开发时不需要先构建 `pulse.exe`，分别启动后端和 React 前端。

终端 1：

```bash
# 1. 创建本地配置
cp config.example.yaml config.yaml

# 2. 生成管理员密码哈希，填入 config.yaml 的 admin.password_hash
go run ./cmd/pulse hashpass 你的密码

# 3. 启动 Go 后端
go run ./cmd/pulse -config config.yaml
```

终端 2：

```bash
cd frontend
npm install
npm run dev
```

打开状态预览：<http://127.0.0.1:5173/static/>

管理员登录后，开发环境打开 <http://127.0.0.1:5173/static/admin/channels>，单文件服务打开 <http://127.0.0.1:18080/admin/channels> 进入渠道管理；也可以从状态页右上角的“渠道管理”入口进入。

后端接口地址：<http://127.0.0.1:18080/ready>

编辑 `channels.d/*.yaml` 后后端会自动热加载通道配置。

代理配置放在 `proxies.d/*.yaml`，修改后会自动热加载。管理页可以新增、编辑和归档代理；代理 URL 中的用户名/密码不会通过管理 API 明文返回。渠道编辑时选择代理后，该渠道的探测请求会使用对应出口；不选择代理时沿用系统环境代理。

### Docker Hub

Docker 镜像由 GitHub Actions 发布，支持 `linux/amd64` 和 `linux/arm64`。镜像仓库使用 Docker 规范的小写名称：

```text
${DOCKERHUB_USERNAME}/kuncoderelaypulse
```

容器需要挂载本地 `config.yaml`，并建议挂载渠道、代理和数据目录，以便管理后台写入配置并持久化 SQLite：

```bash
docker run -d \
  --name kuncode-relay-pulse \
  -p 18080:18080 \
  -v "$PWD/config.yaml:/app/config.yaml:ro" \
  -v "$PWD/channels.d:/app/channels.d" \
  -v "$PWD/proxies.d:/app/proxies.d" \
  -v "$PWD/data:/app/data" \
  ${DOCKERHUB_USERNAME}/kuncoderelaypulse:latest
```

镜像使用 UID `10001` 的非 root 用户运行。Linux 主机使用 bind mount 时，请确保 `data/`、`channels.d/` 和 `proxies.d/` 对该 UID 可写，例如：

```bash
sudo chown -R 10001:10001 data channels.d proxies.d
```

首次部署可从 `config.example.yaml` 创建配置，并按上面的本地开发步骤生成管理员密码哈希。容器内默认监听 `127.0.0.1:18080`；如需从容器外访问，请将 `config.yaml` 的 `listen` 改为 `0.0.0.0:18080`。

只有推送符合版本规则的 `v*` Git Tag 才会发布镜像。例如稳定版：

```bash
git tag -a v1.1.0 -m "Release v1.1.0"
git push origin v1.1.0
```

稳定版会生成以下两个 Tag：

```text
${DOCKERHUB_USERNAME}/kuncoderelaypulse:v1.1.0
${DOCKERHUB_USERNAME}/kuncoderelaypulse:latest
```

预发布版本也会触发构建，但只生成对应版本 Tag，不会覆盖 `latest`：

```bash
git tag -a v1.1.0-beta -m "Release v1.1.0-beta"
git push origin v1.1.0-beta
```

支持的版本形式包括 `v1.1.0-alpha`、`v1.1.0-beta` 和 `v1.1.0-rc.1`。不会生成 `1.1.0`、`1.1` 等缩短 Tag。完整规则见 [`AGENTS.md`](AGENTS.md)。

### 单文件发布

发布或部署时，才需要先把前端构建到 Go 的 embed 目录，再构建后端：

```bash
cd frontend
npm run build
cd ..
go build -o pulse ./cmd/pulse
./pulse -config config.yaml
```

Windows 下 Go 会生成 `pulse.exe`，运行：

```powershell
.\pulse.exe -config .\config.yaml
```

单文件状态页地址：<http://127.0.0.1:18080/>

部署到服务器（跨平台单文件，无 CGO）：

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o pulse ./cmd/pulse
```

systemd 示例：

```ini
[Unit]
Description=pulse status monitor
After=network-online.target

[Service]
ExecStart=/opt/pulse/pulse -config /opt/pulse/config.yaml
Environment=KUNCODE_API_KEY=sk-xxx
Restart=always
User=pulse

[Install]
WantedBy=multi-user.target
```

## 前端开发

React + TypeScript + Vite + shadcn/ui 脚手架位于 `frontend/`：

```bash
cd frontend
npm install
npm run dev       # http://127.0.0.1:5173/static/，API 代理到 Go 服务 18080
npm run build     # 构建到 internal/web/static，供 Go embed 使用
```

组件按 shadcn/ui 的本地源码模式维护在 `frontend/src/components/ui/`，后续可以继续用：

```bash
npx shadcn@latest add <component>
```

## 配置

四层：

| 层 | 位置 | 说明 |
|---|---|---|
| 全局 | `config.yaml` | 监听地址、存储、通知、管理员、间隔等 |
| 通道 | `channels.d/*.yaml` | 每条线路一个文件，`provider` 分组，`hidden` 控制公开可见性 |
| 代理 | `proxies.d/*.yaml` | 可复用的 HTTP/HTTPS/SOCKS5/SOCKS5H 出口配置 |
| 模板 | `templates/*.json` | 探针模板，占位符 `{{BASE_URL}}` `{{API_KEY}}` `{{MODEL}}` |

通道文件字段：

```yaml
provider: kuncode        # 首页按 provider 分组
name: 主力线路            # 展示名，随便改；历史挂在稳定 ID（id 字段）上
hidden: false            # true = 公开首页不展示，管理员登录后可见
disabled: false          # true = 不探测、仅展示
interval: 300s           # 覆盖全局间隔
template: anthropic-messages
base_url: https://...
api_key_env: KUNCODE_API_KEY   # 或直接 api_key: "sk-..."
models: [claude-sonnet-4-5]    # 留空 = 整通道单目标（适合 GET 健康检查）
# proxy: px_overseas              # 可选，引用 proxies.d/ 中的代理 ID
```

首次加载时若通道文件缺 `id`，程序会自动生成 `ch_<uuid>` 并写回文件——
这是历史数据的锚，改名不丢数据，不要手动改它。

代理文件示例：

```yaml
id: px_overseas
revision: 1
name: 海外出口
url: socks5://username:password@127.0.0.1:1080
```

代理支持 `http://`、`https://`、`socks5://` 和 `socks5h://`。代理 ID 缺失时会自动生成并写回文件；同一目录下代理名称和 ID 必须唯一。

`listen`、`sqlite_path`、`channels_dir`、`proxies_dir`、`templates_dir`、`max_concurrency` 修改后需要重启；
其余运行配置和通道/模板文件支持热加载。热加载失败时旧配置继续运行，`/ready` 返回 503。

本地真实配置不要提交：`config.yaml`、带真实 Key 的渠道文件和本机代理文件均已加入 `.gitignore`。新部署请从 `config.example.yaml` 开始；代理可直接在管理页维护，也可以复制 `proxies.d/example.yaml` 修改。

## API

- `GET /api/status` —— 聚合状态（管理员会话额外包含 hidden 通道）
- `GET /ready` —— 健康检查；热更新失败时返回 503 并带错误详情
- `POST /api/login` / `POST /api/logout`
- `GET /api/admin/channels` —— 管理员渠道列表（包含可用探测模板，API Key 只返回是否已配置）
- `POST /api/admin/channels` —— 新建渠道；一个渠道就是一个号池，`models` 是该号池下的模型列表
- `PUT /api/admin/channels/:id` —— 编辑渠道；必须提交当前 `revision`，成功后版本递增
- `DELETE /api/admin/channels/:id` —— 按 `revision` 归档渠道到 `channels.d/.archive/`
- `POST /api/admin/channels/:id/probe` —— 管理员立即探测指定模型，结果走正常落库/事件/通知流水线
- `POST /api/admin/channels/:id/reset` —— 按 `revision` 清空该渠道的探测记录、事件和当前检测状态
- `GET /api/admin/proxies` —— 管理员代理列表，只返回脱敏 URL 预览
- `POST /api/admin/proxies` / `PUT /api/admin/proxies/:id` —— 新建或编辑代理
- `POST /api/admin/proxies/:id/test` —— 通过该代理访问 Google `generate_204`，返回连通性、HTTP 状态码和延迟
- `DELETE /api/admin/proxies/:id` —— 按 `revision` 归档代理；仍被渠道引用时拒绝删除

## 后续

已实现：探测/调度/存储/热更新/通知/状态页/管理员视角/渠道管理 CRUD/代理管理/代理连通性测试/立即探测/渠道状态重置。

后续可继续完善：

- API key AES 加密落盘
- 公开页/内部页分离、Telegram 之外的更多通知渠道
