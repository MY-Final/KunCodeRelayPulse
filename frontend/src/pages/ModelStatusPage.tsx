import { useCallback, useEffect, useMemo, useState } from "react"
import {
  Activity,
  Check,
  ChevronDown,
  Clock3,
  LogIn,
  LogOut,
  Moon,
  RefreshCw,
  Search,
  Sun,
  WifiOff,
  XCircle,
} from "lucide-react"
import { StatusDot } from "@/components/status-dot"
import { AdminLoginDialog } from "@/components/admin-login-dialog"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip"
import { fetchStatus, logout } from "@/lib/api"
import { cn } from "@/lib/utils"
import type { ChannelStatus, ProbePoint, StatusCode, StatusPayload, TargetStatus } from "@/types"

type Theme = "light" | "dark"
type StatusFilter = "all" | "up" | "slow" | "down" | "unknown"
type WindowFilter = "90d" | "24h" | "7d"

interface ModelRecord {
  provider: string
  channel: ChannelStatus
  target: TargetStatus
}

const THEME_STORAGE_KEY = "pulse-theme-v2"
const ADMIN_PATH = window.location.pathname.startsWith("/static/") ? "/static/admin/channels" : "/admin/channels"

function getInitialTheme(): Theme {
  const stored = localStorage.getItem(THEME_STORAGE_KEY)
  return stored === "light" ? "light" : "dark"
}

function statusMeta(status?: StatusCode) {
  if (status === 1) return { label: "正常", tone: "success" as const, icon: Check }
  if (status === 2) return { label: "缓慢", tone: "warning" as const, icon: Clock3 }
  if (status === 0) return { label: "故障", tone: "danger" as const, icon: XCircle }
  return { label: "无数据", tone: "secondary" as const, icon: WifiOff }
}

function statusFilterKey(status?: StatusCode): Exclude<StatusFilter, "all"> {
  if (status === 1) return "up"
  if (status === 2) return "slow"
  if (status === 0) return "down"
  return "unknown"
}

function formatLatency(ms?: number) {
  if (ms === undefined || ms === null) return "--"
  if (ms < 1) return "<1 ms"
  if (ms < 10) return `${ms.toFixed(1)} ms`
  return `${Math.round(ms)} ms`
}

function formatRelative(ts?: number) {
  if (!ts) return "尚未探测"
  const seconds = Math.max(0, Math.floor(Date.now() / 1000 - ts))
  if (seconds < 60) return `${seconds} 秒前`
  if (seconds < 3600) return `${Math.floor(seconds / 60)} 分钟前`
  if (seconds < 86400) return `${Math.floor(seconds / 3600)} 小时前`
  return `${Math.floor(seconds / 86400)} 天前`
}

function formatPercent(up: number, yellow: number, total: number) {
  return total ? `${Math.round(((up + yellow) / total) * 100)}%` : "--"
}

function flattenModels(data: StatusPayload): ModelRecord[] {
  return (data.providers ?? []).flatMap((provider) =>
    (provider.channels ?? []).flatMap((channel) =>
      (channel.targets ?? []).map((target) => ({ provider: provider.name, channel, target })),
    ),
  )
}

function getWindow(target: TargetStatus, name: WindowFilter) {
  return (target.windows ?? []).find((window) => window.window === name)
}

function windowLabel(name: WindowFilter) {
  if (name === "24h") return "24 小时"
  if (name === "7d") return "7 天"
  return "90 天"
}

function serviceLabel(template?: string) {
  if (template === "selfcheck") return "自检"
  if (template === "anthropic-messages") return "Anthropic"
  if (template === "openai-chat") return "OpenAI"
  return template || "未分类"
}

function vendorLabel(model: string) {
  const name = model.toLowerCase()
  if (name.includes("claude")) return "Anthropic"
  if (name.includes("gpt") || name.startsWith("o1") || name.startsWith("o3") || name.startsWith("o4")) return "OpenAI"
  if (name.includes("gemini")) return "Google"
  if (name.includes("deepseek")) return "DeepSeek"
  if (name.includes("qwen") || name.includes("通义")) return "阿里云"
  if (name.includes("glm") || name.includes("chatglm")) return "智谱"
  return model ? "自定义" : "--"
}

function qualityMeta(target: TargetStatus) {
  if (target.status === undefined) return { label: "等待探测", detail: "尚无响应" }
  const http = target.http_code ? `HTTP ${target.http_code}` : "无 HTTP 状态"
  const labels: Record<string, string> = {
    ok: "响应通过",
    slow: "响应偏慢",
    content_mismatch: "内容不符",
    network_error: "网络错误",
    timeout: "请求超时",
    invalid_request: "请求失败",
    upstream_error: "上游错误",
    canceled: "已取消",
  }
  const label = labels[target.sub_status || ""] || (target.status === 1 ? "响应通过" : target.status === 2 ? "响应偏慢" : "探测失败")
  const detail = target.sub_status === "ok" ? `${http} · 内容校验` : http
  return { label, detail }
}

function formatDateTime(ts?: number) {
  return ts ? new Date(ts * 1000).toLocaleString([], { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" }) : "--"
}

function formatExactDateTime(ts?: number) {
  if (!ts) return "--"
  const date = new Date(ts * 1000)
  const pad = (value: number) => String(value).padStart(2, "0")
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`
}

function probeTone(status?: StatusCode) {
  if (status === 1) return "probe-cell-up"
  if (status === 2) return "probe-cell-slow"
  if (status === 0) return "probe-cell-down"
  return "probe-cell-empty"
}

function probeDotTone(status?: StatusCode) {
  if (status === 1) return "probe-dot-up"
  if (status === 2) return "probe-dot-slow"
  if (status === 0) return "probe-dot-down"
  return "probe-dot-empty"
}

function probeDetail(point?: ProbePoint) {
  if (!point) return "尚无探测记录"
  const labels: Record<string, string> = {
    ok: "内容校验通过",
    slow: "响应超过慢阈值",
    content_mismatch: "响应内容不匹配",
    network_error: "网络连接失败",
    timeout: "请求超时",
    invalid_request: "请求或鉴权失败",
    upstream_error: "上游服务错误",
    canceled: "探测已取消",
  }
  return labels[point.sub_status || ""] || "探测完成"
}

function ProbeTooltipCard({ model, service, point, index }: { model: string; service: string; point?: ProbePoint; index: number }) {
  const meta = statusMeta(point?.status)
  return (
    <div className="w-[252px] space-y-3 p-3.5">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <p className="truncate font-mono text-[13px] font-semibold text-slate-100">{model}</p>
          <p className="mt-1 text-[11px] text-slate-400">{service} · 第 {index} 次探测</p>
        </div>
        <span className="shrink-0 rounded border border-slate-700/80 bg-slate-900/80 px-1.5 py-0.5 text-[10px] font-medium text-slate-300">模型</span>
      </div>
      <div className="flex items-center gap-2 border-t border-slate-800/90 pt-3 text-[11px] text-slate-400">
        <Clock3 className="h-3.5 w-3.5 text-slate-500" />
        <span className="font-mono">{formatExactDateTime(point?.ts)}</span>
      </div>
      <div className="flex items-center justify-between rounded border border-slate-800 bg-slate-900/60 px-2.5 py-2">
        <div className="flex items-center gap-2">
          <span className={cn("probe-status-dot", probeDotTone(point?.status))} />
          <span className="text-xs font-semibold text-slate-100">{meta.label}</span>
        </div>
        <span className="font-mono text-[11px] text-slate-400">{probeDetail(point)}</span>
      </div>
      <div className="grid grid-cols-2 gap-2 border-t border-slate-800/90 pt-3">
        <div>
          <p className="text-[10px] uppercase tracking-[0.12em] text-slate-500">探测延迟</p>
          <p className="mt-1 font-mono text-sm font-semibold text-cyan-300">{formatLatency(point?.latency_ms)}</p>
        </div>
        <div className="text-right">
          <p className="text-[10px] uppercase tracking-[0.12em] text-slate-500">HTTP 响应</p>
          <p className="mt-1 font-mono text-sm font-semibold text-slate-100">{point?.http_code || "--"}</p>
        </div>
      </div>
    </div>
  )
}

const HEATMAP_SIZE = 48

function ProbeHeatmap({ target, model, service, compact = false }: { target: TargetStatus; model: string; service: string; compact?: boolean }) {
  const history = (target.history ?? []).slice(-HEATMAP_SIZE)
  const points: Array<ProbePoint | undefined> = [
    ...Array.from({ length: HEATMAP_SIZE - history.length }, () => undefined),
    ...history,
  ]

  return (
    <div className={cn("probe-heatmap", compact && "probe-heatmap-compact")} aria-label={`${model} 最近 ${history.length} 次探测状态`}>
      {points.map((point, index) => {
        const label = point
          ? `${model}，${formatExactDateTime(point.ts)}，${statusMeta(point.status).label}，${formatLatency(point.latency_ms)}，HTTP ${point.http_code || "未知"}`
          : `${model}，暂无第 ${index + 1} 次探测数据`
        return (
          <Tooltip key={point ? `${point.ts}-${index}` : `empty-${index}`}>
            <TooltipTrigger asChild>
              <button type="button" aria-label={label} className={cn("probe-cell", probeTone(point?.status))}>
                <span aria-hidden="true" />
              </button>
            </TooltipTrigger>
            <TooltipContent side="top" align="center" sideOffset={8}>
              <ProbeTooltipCard model={model} service={service} point={point} index={index + 1} />
            </TooltipContent>
          </Tooltip>
        )
      })}
    </div>
  )
}

function ModelTableRow({ record, range }: { record: ModelRecord; range: WindowFilter }) {
  const { channel, provider, target } = record
  const meta = statusMeta(target.status)
  const Icon = meta.icon
  const selectedWindow = getWindow(target, range)
  const modelName = target.model || "服务自检"
  const vendor = vendorLabel(target.model)
  const quality = qualityMeta(target)

  return (
    <TableRow className={cn("model-table-row", `model-row-${statusFilterKey(target.status)}`)}>
      <TableCell className="model-table-marker">
        <div className="flex items-center gap-2"><StatusDot status={target.status} /><span>{channel.hidden ? "隐藏" : "公开"}</span></div>
      </TableCell>
      <TableCell><span className="model-table-primary">{provider}</span></TableCell>
      <TableCell><span className="service-pill">{serviceLabel(channel.template)}</span></TableCell>
      <TableCell><span className="model-table-primary">{channel.name}</span></TableCell>
      <TableCell>
        <div className="min-w-0"><span className="model-table-primary block truncate">{modelName}</span><span className="model-table-secondary">{target.model ? "模型探测" : "通道探测"}</span></div>
      </TableCell>
      <TableCell><span className={cn("vendor-label", !target.model && "vendor-empty")}>{vendor}</span></TableCell>
      <TableCell><Badge className="text-xs" variant={meta.tone}><Icon className="h-3.5 w-3.5" />{meta.label}</Badge></TableCell>
      <TableCell>
        <div className={cn("quality-label", target.status === 0 && "quality-danger", target.status === 2 && "quality-warning")}>{quality.label}</div>
        <div className="model-table-secondary">{quality.detail}</div>
      </TableCell>
      <TableCell>
        <span className="font-mono text-sm font-semibold text-foreground">{formatLatency(target.latency_ms)}</span>
        <span className="model-table-secondary">均值 {selectedWindow?.avg_latency_ms ? formatLatency(selectedWindow.avg_latency_ms) : "--"}</span>
      </TableCell>
      <TableCell>
        <span className="font-mono text-base font-bold text-success">{formatPercent(selectedWindow?.up ?? 0, selectedWindow?.yellow ?? 0, selectedWindow?.total ?? 0)}</span>
        <span className="model-table-secondary">{selectedWindow?.total ? `${selectedWindow.total} 次探测` : "无样本"}</span>
      </TableCell>
      <TableCell>
        <span className="model-table-primary whitespace-nowrap">{formatRelative(target.checked_at)}</span>
        <span className="model-table-secondary">{formatDateTime(target.checked_at)}</span>
      </TableCell>
      <TableCell className="heatmap-table-cell">
        <ProbeHeatmap target={target} model={modelName} service={serviceLabel(channel.template)} />
      </TableCell>
    </TableRow>
  )
}

function ModelMobileCard({ record, range }: { record: ModelRecord; range: WindowFilter }) {
  const { channel, provider, target } = record
  const meta = statusMeta(target.status)
  const Icon = meta.icon
  const selectedWindow = getWindow(target, range)
  const quality = qualityMeta(target)
  const modelName = target.model || "服务自检"

  return (
    <div className={cn("model-mobile-row border-b border-border/70 px-4 py-5 last:border-b-0", `model-row-${statusFilterKey(target.status)}`)}>
      <div className="flex items-start gap-3">
        <StatusDot status={target.status} className="mt-1.5" />
        <div className="min-w-0 flex-1"><div className="truncate font-mono text-[15px] font-semibold">{modelName}</div><div className="mt-1.5 truncate text-[13px] text-muted-foreground">{provider} / {channel.name}</div></div>
      </div>
      <div className="mobile-model-meta"><span className="service-pill">{serviceLabel(channel.template)}</span><span className="vendor-label">{vendorLabel(target.model)}</span><span className="model-table-secondary">{target.model ? "模型探测" : "通道探测"}</span></div>
      <div className="mt-5 flex flex-wrap items-end justify-between gap-4"><div><Badge className="text-xs" variant={meta.tone}><Icon className="h-3.5 w-3.5" />{meta.label}</Badge><p className="mt-3 font-mono text-xl font-bold text-success">{formatPercent(selectedWindow?.up ?? 0, selectedWindow?.yellow ?? 0, selectedWindow?.total ?? 0)}</p><p className="mt-1 text-xs text-muted-foreground">{windowLabel(range)}可用率 · {selectedWindow?.total || 0} 次探测</p></div><div className="text-right"><p className="font-mono text-base font-semibold">{formatLatency(target.latency_ms)}</p><p className="mt-1 text-xs text-muted-foreground">{formatRelative(target.checked_at)}</p></div></div>
      <div className="mobile-quality"><span className={cn("quality-label", target.status === 0 && "quality-danger", target.status === 2 && "quality-warning")}>{quality.label}</span><span className="model-table-secondary">{quality.detail}</span></div>
      <div className="mt-4"><ProbeHeatmap target={target} model={modelName} service={serviceLabel(channel.template)} compact /></div>
    </div>
  )
}

export default function ModelStatusPage() {
  const [data, setData] = useState<StatusPayload | null>(null)
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [error, setError] = useState("")
  const [query, setQuery] = useState("")
  const [provider, setProvider] = useState("all")
  const [status, setStatus] = useState<StatusFilter>("all")
  const [range, setRange] = useState<WindowFilter>("90d")
  const [loginOpen, setLoginOpen] = useState(false)
  const [theme, setTheme] = useState<Theme>(getInitialTheme)

  const load = useCallback(async (initial = false) => {
    if (!initial) setRefreshing(true)
    try {
      setData(await fetchStatus())
      setError("")
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "状态加载失败")
    } finally {
      setLoading(false)
      setRefreshing(false)
    }
  }, [])

  useEffect(() => {
    void load(true)
    const timer = window.setInterval(() => void load(), 60_000)
    return () => window.clearInterval(timer)
  }, [load])

  useEffect(() => {
    document.documentElement.classList.toggle("dark", theme === "dark")
    document.documentElement.classList.toggle("light", theme === "light")
    localStorage.setItem(THEME_STORAGE_KEY, theme)
  }, [theme])

  const rows = useMemo(() => (data ? flattenModels(data) : []), [data])
  const providers = useMemo(() => [...new Set(rows.map((row) => row.provider))], [rows])
  const filteredRows = useMemo(() => rows.filter(({ provider: rowProvider, channel, target }) => {
    const haystack = `${rowProvider} ${channel.name} ${target.model}`.toLowerCase()
    return (!query || haystack.includes(query.toLowerCase())) && (provider === "all" || rowProvider === provider) && (status === "all" || statusFilterKey(target.status) === status)
  }), [provider, query, rows, status])
  const counts = useMemo(() => ({ up: rows.filter((row) => row.target.status === 1).length, slow: rows.filter((row) => row.target.status === 2).length, down: rows.filter((row) => row.target.status === 0).length, unknown: rows.filter((row) => row.target.status === undefined).length }), [rows])
  const admin = data?.view === "admin"

  async function handleLogout() {
    await logout()
    await load()
  }

  if (loading && !data) return <LoadingState />
  if (!data) return <ErrorState message={error} onRetry={() => void load()} />

  return (
    <div className="min-h-screen bg-background text-foreground">
      <a href="#model-status" className="sr-only focus:not-sr-only focus:absolute focus:left-4 focus:top-4 focus:z-50 focus:rounded-md focus:bg-primary focus:px-4 focus:py-2 focus:text-primary-foreground">跳到模型状态</a>
      <header className="border-b border-border/80 bg-background/95">
        <div className="mx-auto flex w-full max-w-[1500px] flex-wrap items-center justify-between gap-4 px-4 py-3 lg:px-7">
          <div className="flex min-w-0 flex-1 items-center gap-3"><div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-md bg-primary text-primary-foreground"><Activity className="h-4 w-4" /></div><div className="min-w-0"><div className="flex items-center gap-2"><h1 className="font-mono text-base font-semibold">KunCodeRelayPulse</h1><span className="hidden text-xs text-muted-foreground sm:inline">/ {data.site_title || "模型状态"}</span></div><p className="hidden font-mono text-[11px] uppercase tracking-[0.16em] text-muted-foreground sm:block">Model availability monitor</p></div></div>
          <div className="ml-auto flex shrink-0 items-center gap-1.5"><div className="header-counts mr-2 hidden items-center gap-2 md:flex"><Badge variant="success"><span className="h-1.5 w-1.5 rounded-full bg-success" />{counts.up} 正常</Badge><Badge variant="danger"><span className="h-1.5 w-1.5 rounded-full bg-destructive" />{counts.down} 故障</Badge>{counts.slow ? <Badge variant="warning"><span className="h-1.5 w-1.5 rounded-full bg-warning" />{counts.slow} 缓慢</Badge> : null}{counts.unknown ? <Badge variant="secondary"><span className="h-1.5 w-1.5 rounded-full bg-muted-foreground" />{counts.unknown} 无数据</Badge> : null}</div><Button variant="ghost" size="icon" onClick={() => setTheme(theme === "light" ? "dark" : "light")} aria-label={theme === "light" ? "切换深色主题" : "切换浅色主题"} title={theme === "light" ? "切换深色主题" : "切换浅色主题"}>{theme === "light" ? <Moon className="h-4 w-4" /> : <Sun className="h-4 w-4" />}</Button><Button variant="ghost" size="icon" onClick={() => void load()} disabled={refreshing} aria-label="刷新状态" title="刷新状态"><RefreshCw className={cn("h-4 w-4", refreshing && "animate-spin")} /></Button>{admin ? <><Button asChild variant="outline" size="sm"><a href={ADMIN_PATH}><Activity className="h-3.5 w-3.5" />渠道管理</a></Button><Button variant="outline" size="sm" onClick={() => void handleLogout()}><LogOut className="h-3.5 w-3.5" />退出</Button></> : <Button variant="outline" size="sm" onClick={() => setLoginOpen(true)}><LogIn className="h-3.5 w-3.5" />登录</Button>}</div>
        </div>
      </header>

      <main id="model-status" className="mx-auto w-full min-w-0 max-w-[1500px] px-4 pb-10 pt-4 lg:px-7">
        <div className="mb-4 flex flex-wrap items-end justify-between gap-3"><div><p className="font-mono text-[11px] uppercase tracking-[0.2em] text-primary">Live matrix</p><h2 className="mt-1 text-xl font-semibold">模型状态</h2></div><div className="font-mono text-sm text-muted-foreground">显示 {filteredRows.length} / {rows.length} 个模型</div></div>
        <section className="toolbar" aria-label="筛选模型">
          <div className="relative min-w-[260px] flex-1"><Search className="pointer-events-none absolute left-3.5 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" /><Input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="搜索模型、服务商或通道" className="h-10 border-0 bg-transparent pl-10 text-sm shadow-none focus-visible:ring-0" /></div>
          <div className="flex items-center gap-2"><span className="hidden text-xs text-muted-foreground sm:inline">服务商</span><div className="relative"><select value={provider} onChange={(event) => setProvider(event.target.value)} aria-label="筛选服务商" className="h-10 appearance-none rounded-md border border-border/80 bg-card px-3 pr-9 text-sm text-foreground outline-none focus:ring-2 focus:ring-ring"><option value="all">所有服务商</option>{providers.map((name) => <option key={name} value={name}>{name}</option>)}</select><ChevronDown className="pointer-events-none absolute right-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" /></div></div>
          <div className="flex items-center gap-0.5 rounded-md border border-border/80 bg-card p-1" role="group" aria-label="筛选状态">{(["all", "up", "slow", "down", "unknown"] as StatusFilter[]).map((value) => <Button key={value} type="button" size="sm" variant={status === value ? "secondary" : "ghost"} className="h-8 px-3 text-xs" aria-pressed={status === value} onClick={() => setStatus(value)}>{value === "all" ? "全部" : value === "up" ? "正常" : value === "slow" ? "缓慢" : value === "down" ? "故障" : "无数据"}</Button>)}</div>
          <div className="flex items-center gap-0.5 rounded-md border border-border/80 bg-card p-1" role="group" aria-label="选择统计窗口">{(["90d", "24h", "7d"] as WindowFilter[]).map((value) => <Button key={value} type="button" size="sm" variant={range === value ? "default" : "ghost"} className="h-8 px-3 text-xs" aria-pressed={range === value} onClick={() => setRange(value)}>{value === "90d" ? "近 90 天" : value === "24h" ? "近 24 小时" : "近 7 天"}</Button>)}</div>
        </section>
        {error ? <div role="alert" className="mt-3 border border-warning/40 bg-warning/10 px-3 py-2 text-xs text-warning-foreground">{error}</div> : null}

        <section className="status-shell mt-3" aria-label="模型状态列表">
          <TooltipProvider delayDuration={140} skipDelayDuration={80}>
            {filteredRows.length ? <>
              <div className="hidden lg:block">
                <Table className="model-table">
                  <TableHeader>
                    <TableRow className="model-table-header">
                      <TableHead>标注</TableHead>
                      <TableHead>服务商</TableHead>
                      <TableHead>服务</TableHead>
                      <TableHead>通道</TableHead>
                      <TableHead>模型</TableHead>
                      <TableHead>厂商</TableHead>
                      <TableHead>当前状态</TableHead>
                      <TableHead>探测质量</TableHead>
                      <TableHead>延迟</TableHead>
                      <TableHead>{windowLabel(range)}可用率</TableHead>
                      <TableHead>最后监测</TableHead>
                      <TableHead>探测热力图</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>{filteredRows.map((record) => <ModelTableRow key={`${record.channel.id}-${record.target.model}`} record={record} range={range} />)}</TableBody>
                </Table>
              </div>
              <div className="lg:hidden">{filteredRows.map((record) => <ModelMobileCard key={`${record.channel.id}-${record.target.model}`} record={record} range={range} />)}</div>
            </> : <div className="px-6 py-16 text-center text-sm text-muted-foreground">没有匹配的模型</div>}
          </TooltipProvider>
        </section>

        <footer className="mt-5 flex flex-wrap items-center justify-between gap-3 px-1 text-xs text-muted-foreground"><span>数据生成于 {new Date(data.generated_at * 1000).toLocaleString()}</span><span className="flex items-center gap-1.5"><Clock3 className="h-3.5 w-3.5" />每 60 秒自动刷新</span></footer>
      </main>
      <AdminLoginDialog open={loginOpen} onOpenChange={setLoginOpen} onSuccess={() => load()} />
    </div>
  )
}

function LoadingState() {
  return <div className="min-h-screen bg-background px-4 py-5 lg:px-7"><div className="mx-auto max-w-[1500px] animate-pulse"><div className="h-8 w-48 rounded bg-muted" /><div className="mt-8 h-12 rounded-lg bg-muted" /><div className="mt-3 h-[520px] rounded-lg bg-muted" /></div></div>
}

function ErrorState({ message, onRetry }: { message: string; onRetry: () => void }) {
  return <div className="flex min-h-screen items-center justify-center bg-background px-6 text-foreground"><div className="text-center"><WifiOff className="mx-auto h-8 w-8 text-destructive" /><h1 className="mt-4 text-lg font-semibold">无法加载模型状态</h1><p className="mt-2 text-sm text-muted-foreground">{message || "服务暂时没有返回数据。"}</p><Button className="mt-5" onClick={onRetry}><RefreshCw className="h-4 w-4" />重试</Button></div></div>
}
