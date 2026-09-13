# 自建「API 服务状态监测」项目设计参考

> 基于 relay-pulse 的源码阅读总结。目标不是复刻它，而是拆出「状态监测」这件事的本质，
> 说清哪些设计值得抄、哪些是为它的特定场景（公开运营 + 反作弊）服务的、个人项目可以砍掉，
> 最后给一个从零做最小可行版的路线。

---

## 1. 这类项目的本质

一个状态监测（status monitor）项目，核心闭环只有四步：

```
定时探测 → 记录结果 → 聚合计算 → 展示/通知
```

relay-pulse 监测的对象是 LLM API 中转站（中转/代理 Claude、GPT、Gemini 等），它的关键洞察是：

**「状态」不能靠 ping 或 TCP 连通性判断，必须发真实业务请求。**
中转站可能：TCP 通但上游挂了、HTTP 200 但返回 mock/回显作弊内容、200 但思考模型把
token 预算吃光导致正文为空。所以它的探测 = 一次真实的 chat completion 请求 + 响应内容校验。

这是整个项目最值得学的判断：**探测方式和被测对象的真实健康强相关**。

---

## 2. 领域模型：四层结构 + 两个稳定 ID

```
Provider（服务商，如 KunCode）
  └─ Service（服务类型：cc=Claude / cx=GPT 类 / gm=Gemini）
      └─ Channel（通道，即一条具体的中转线路，如 pro_0.25_astra）
          └─ Model（通道内的具体模型，一个通道可挂多个模型）
```

- **PSC**（provider/service/channel）标识一个通道；**PSCM**（+model）标识一个探测目标。
- 两个系统生成的稳定 ID，与「展示名」彻底分离：
  - `channel_id`（`ch_<uuid>`）：通道锚，文件级不可变
  - `model_id`（`md_<uuid>`）：探测目标锚，数据库历史记录按它重键

**为什么这是重点**：通道可以改名（展示名是运营信息），改名后历史数据不能丢。
事实表里存的是稳定 ID，展示名 join 出来。任何监测项目都该在第一天做这个分离，
事后补的代价极大（这个仓库里有一堆列迁移和回填 CLI 就是在还这个债）。

---

## 3. 探测系统（项目的核心）

### 3.1 模板驱动

每个通道不直接写 HTTP 细节，而是引用一个**探针模板**（`templates/*.json`）：

```json
{
  "name": "cc-haiku-arith",
  "url": "{{BASE_URL}}/v1/messages",
  "method": "POST",
  "headers": { "x-api-key": "{{API_KEY}}", "anthropic-version": "..." },
  "body": "{ ...随机算术题... }",
  "success_contains": "42",
  "model": "claude-haiku-...",
  "timeout": "30s",
  "slow_latency": "5s",
  "retry": 1
}
```

通道行只需填：`template` + `base_url` + `api_key`（+ 可选覆盖 model/interval）。
模板里用 `{{BASE_URL}}`/`{{API_KEY}}` 占位，运行时注入。

**好处**：
- 同一种 API 形态（Anthropic / OpenAI / Gemini）写一个模板，所有通道复用；
- 请求体、成功判定、超时阈值集中管理，改一处全局生效；
- 新增监测目标 = 选模板 + 填三个字段，后台表单可以做得极简。

### 3.2 内容校验与反作弊

- `success_contains`：响应必须包含预期内容，HTTP 200 但内容不对 = 红
  （`content_mismatch` 子状态）。这一条就能抓掉「永 200 假绿」的中转站。
- **随机题面**：模板里放随机算术题模板，每次探测生成不同的题，
  响应里校验计算结果。固定题面会被中转站缓存回显作弊，随机题面抓不到。
- 附加约束：思考型模型要放大 `max_tokens`（否则思考吃光预算、正文为空 → 假红），
  关掉 fallback（否则上游挂了会静默回退到别的模型 → 假绿）。

### 3.3 结果模型：二级状态

```
status:      0=红  1=绿  2=黄
sub_status:  network_error / invalid_request / content_mismatch /
             timeout / slow / canceled / concurrency_limited / ...
+ http_code, latency(ms), timestamp, error_detail
```

单一「up/down」不够用：黄（慢但可用）和红（彻底挂）对用户意义完全不同。
`sub_status` 让红有原因可查，排障时不用点进去翻日志。

### 3.4 探测执行要点

- 超时（timeout）与慢阈值（slow_latency）分离：超过 slow → 黄，超过 timeout → 红；
- 失败重试：基础延迟 → 指数退避 → 抖动，避免惊群；
- 探测响应可捕获为脱敏 curl（管理后台「测一下」按钮直接复用同一套探测逻辑，
  **字段级一致**——测试通过但定时任务失败这类灵异问题就没了）；
- 面向公开用户的测试入口要有 SSRF 防护（禁内网地址）。

---

## 4. 调度器

- 全局默认 interval，通道可覆盖（热门线路 5m，普通 10m）；
- **错峰**：启动和重建任务时按固定间隔把首次探测摊开，避免所有通道同一秒打出去；
- 全局并发上限（`max_concurrency`），单通道超载时排队（`concurrency_limited`）；
- disabled / cold 板通道不创建探测任务（省配额），只保留展示；
- 热更新时**整堆重建**：用 generation 计数让旧任务全部失效，防止新旧任务并存重复探测；
- 单 goroutine 调度循环 + 定时器 + 任务堆，量级在几百通道时完全够用，
  不需要引入任何调度框架（不需要 cron 库，`time.Timer` + 堆就够了）。

---

## 5. 配置体系与热更新（最值得抄的部分之一）

### 5.1 三层配置

| 层 | 内容 | 特点 |
|---|---|---|
| `config.yaml` | 全局配置（interval、存储、admin、功能开关）+ 可选内联监测行 | 人手编辑 |
| `monitors.d/*.yaml` | 每通道一个文件（含 metadata：revision/channel_id） | 程序写（后台 CRUD） |
| `proxies.d/*.yaml` | 可复用代理出口（HTTP/HTTPS/SOCKS5/SOCKS5H） | 程序写（后台 CRUD） |
| `templates/*.json` | 探针模板 | 版本管理 |

**每通道一个文件**而不是一个大数组，是个好决定：
- 后台创建/编辑/删除 = 单文件原子写，不用重写整份配置；
- 文件名即 PSC key（`provider--service--channel.yaml`），天然冲突检测；
- 删除走归档（移进 `.archive/`）而不是真删，可恢复；
- YAML 里留 `revision` 做乐观锁，后台并发编辑不会互相覆盖。

### 5.2 热更新 fail-closed

fsnotify 监听配置目录 + `monitors.d/` + `templates/`，200ms 防抖后重载。关键规则：

**新配置加载/校验失败 → 保留旧配置继续服务，绝不带着坏配置运行。**

配套可观测性：把「热更新失败次数/时间」暴露到 `/ready` 端点。否则会出现最阴险的
故障模式：后台保存返回 200，热更新其实失败了，运行态一直是旧配置，没人发现。

### 5.3 写入安全

- 所有配置写盘走 **原子写**（临时文件 + rename），崩溃不会留下半个文件；
- 校验前置：写盘前跑完整性校验（如 ID 唯一性），拒绝落盘会产出「加载不了」的坏文件。

---

## 6. 存储

- 默认 SQLite，可选 PostgreSQL（同一套接口两个实现）；
- 核心事实表就一张：

```sql
CREATE TABLE probe_history (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  model_id TEXT NOT NULL,        -- 稳定 ID，join 锚
  provider/service/channel/model TEXT,  -- 展示快照（排障方便）
  status INTEGER,                -- 0/1/2
  sub_status TEXT,
  http_code INTEGER,
  latency INTEGER,               -- ms
  timestamp INTEGER,             -- unix 秒
  error_detail TEXT
);
```

- 只 append 不 update；按 `(model_id, timestamp)` 建复合索引；
- 保留期清理（默认 N 天）+ PG 可选归档到文件；
- 状态表（service/channel_states）存事件检测的状态机，事件表（status_events）存 down/up 事件流。

**教训**：这个仓库的表结构是「先按展示名设计 → 后来补 model_id 列 + 全量回填」。
从零做的话第一天就把稳定 ID 作为主键的一部分。

---

## 7. 状态计算与展示

- **时间线分桶**：90 天/7 天/24 小时按策略分 bucket，每桶算可用率 + 平均延迟，
  前端渲染成 uptime 条（statuspage 风格）；
- **事件检测**：连续 N 次失败判 down（避免单次抖动触发事件），恢复同理判 up；
- **板位**：hot / secondary / cold 三档运营分类，可按历史质量自动移板
  （阈值：冷板命中率），sticky 状态持久化避免重启抖动；
- 列表页「活化」：列表接口顺带返回每个通道最近一次探测快照，打开页面即是新数据。

---

## 8. 管理后台与安全

- 登录：bcrypt 密码哈希 + session secret + TTL；
- 通道 CRUD 直接写 `monitors.d/`（乐观锁 revision），成功后 fsnotify 自动热加载；
- API Key 加密落盘（AES，密钥来自环境变量），读取接口只返回掩码 + 末四位；
- 复制通道时密文只在服务端文件间流转，不进浏览器；
- 独立于主服务的探测测试入口（inline prober），与定时探测共用同一套解析逻辑。

---

## 9. 我的评价：值得抄 vs 不值得抄

### 值得抄（普适设计）

1. **真实请求 + 内容校验**的探测方式，以及 status + sub_status 二级状态
2. **模板驱动**的探针组织方式
3. **稳定 ID 与展示名分离**（第一天就做）
4. **每通道一文件 + 原子写 + 乐观锁**的配置管理
5. **fail-closed 热更新**（坏配置保留旧配置）+ 把热更新失败暴露到健康端点
6. 测试入口与定时探测**共用同一套代码**（字段级一致）
7. 时间线分桶聚合 + 事件检测（连续 N 次才判定状态翻转）

### 不值得抄（为它的特定场景服务，个人项目是负担）

1. **多租户自助收录 / 变更申请 / 提交审核**——它是公开运营平台才需要的服务
2. **反作弊体系**（随机题面、整包 attestation、计费头指纹）——只防「中转商造假」
3. **model_vendor 正交轴、四语言 i18n、赞助商/板位运营**——产品化功能
4. 自动移板、rpdiag 质量信号等运营自动化
5. 大量防御性不变量（子行一对一合并、跨源 PSC 冲突检测……）——
   这些是为了「多人协作 + 公开部署 + admin 写盘」才必要的，单人手写配置根本不会遇到
6. 前端的批量快照注入、防抖中止等优化——通道数上百才需要

一句话：它的**监测内核**（模板、探测、调度、热更新、存储）质量不错，可以放心借鉴；
**产品外壳**（自助收录、变更流程、运营板位）和它绑定的反作弊约束不用带走。

---

## 10. 从零做一个最小可行版

### 建议架构（单二进制）

```
Go（或 Node）单二进制
├── scheduler   时间轮/最小堆 + 并发信号量 + 错峰
├── prober      模板渲染 → HTTP 请求 → 内容校验 → status/sub_status
├── store       SQLite：probe_log + channel + event
├── api         /api/status（时间线聚合） /api/admin/*（CRUD）
└── web         静态页（React/Vue 均可，build 后 embed 进二进制）
```

### 数据模型（第一天就定好）

```sql
CREATE TABLE channel (
  id TEXT PRIMARY KEY,          -- 稳定 ID，如 ch_<uuid>
  name TEXT,                    -- 展示名，随便改
  target_url TEXT, template_id TEXT, api_key_enc TEXT,
  interval_secs INTEGER, disabled INTEGER
);
CREATE TABLE template (
  id TEXT PRIMARY KEY, url TEXT, method TEXT,
  headers_json TEXT, body_template TEXT, success_contains TEXT,
  timeout_ms INTEGER, slow_ms INTEGER
);
CREATE TABLE probe_log (
  channel_id TEXT NOT NULL,
  status INTEGER NOT NULL,      -- 0/1/2
  sub_status TEXT, latency_ms INTEGER, http_code INTEGER,
  ts INTEGER NOT NULL
);
CREATE INDEX idx_probe ON probe_log(channel_id, ts DESC);
CREATE TABLE event (             -- 状态翻转事件
  channel_id TEXT, type TEXT,    -- down / up / slow
  ts INTEGER, detail TEXT
);
```

### 配置（学它，但更简单）

```yaml
interval: 60s
storage: { sqlite_path: ./data.db }
notify:  { telegram_bot_token: ..., chat_id: ... }
channels_dir: ./channels.d    # 每通道一个 yaml，后台写这里
templates_dir: ./templates
```

### 探测最小实现

1. 渲染模板（替换 `{{BASE_URL}}`/`{{API_KEY}}`，body 可用 Go text/template 支持
   随机数——需要反作弊的话）；
2. 带 timeout 发请求；对照 `success_contains` 判内容；
3. latency > slow_ms → 黄；失败/超时/内容不符 → 红 + sub_status；
4. 重试 1 次再定级（可配置），写 probe_log；
5. 事件检测：连续 N 次红 → down 事件 → 发通知（Telegram/邮件/webhook）。

### 里程碑

1. **M1（能看）**：手写 yaml 定义通道 → 定时探测 → SQLite → 单页状态板
   （90 天 uptime 条 + 当前状态 + 延迟）
2. **M2（能用）**：文件热更新（fail-closed）+ 简单 admin CRUD（写 channels.d，
   原子写 + revision）+ 事件通知
3. **M3（好用）**：多模板、每通道 interval、代理支持（探测走代理出口）、
   探测测试按钮、公开状态页/内部页分离。其中代理支持和探测测试按钮已在当前项目实现，公开状态页/内部页分离仍可继续完善。

### 易踩的坑（这个仓库用血泪验证过的）

- 只判 HTTP 200 会假绿，必须校验响应内容
- 展示名当主键，改名断历史
- 热更新失败只打日志 → 「保存成功但没生效」的静默故障，要暴露到健康端点
- 配置写盘不原子 → 崩溃后半个文件，服务起不来
- 后台写配置没有乐观锁 → 两个标签页互相覆盖
- 探测不与调度错峰 → 重启瞬间所有通道一起打上游
