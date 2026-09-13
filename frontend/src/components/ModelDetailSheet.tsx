import { useEffect, useMemo, useState } from "react"
import { Activity, Check, Clock3, Database, Gauge, Server, WifiOff, XCircle } from "lucide-react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { fetchStatusTrend } from "@/lib/api"
import { cn } from "@/lib/utils"
import type { ChannelStatus, LatencyPoint, LatencyTrend, StatusCode, TargetStatus, TrendMetric, TrendWindow } from "@/types"

export interface ModelDetailRecord {
  provider: string
  channel: ChannelStatus
  target: TargetStatus
}

const windows: TrendWindow[] = ["24h", "7d", "90d"]
const metrics: Array<{ key: TrendMetric; label: string }> = [
  { key: "p95_latency_ms", label: "P95" },
  { key: "p50_latency_ms", label: "P50" },
  { key: "p99_latency_ms", label: "P99" },
  { key: "avg_latency_ms", label: "平均" },
]

function statusMeta(status?: StatusCode) {
  if (status === 1) return { label: "正常", tone: "success" as const, icon: Check, color: "var(--success)" }
  if (status === 2) return { label: "降级", tone: "warning" as const, icon: Clock3, color: "var(--warning)" }
  if (status === 0) return { label: "故障", tone: "danger" as const, icon: XCircle, color: "var(--destructive)" }
  return { label: "无数据", tone: "secondary" as const, icon: WifiOff, color: "var(--muted-foreground)" }
}

function formatDateTime(ts?: number) {
  if (!ts) return "--"
  const date = new Date(ts * 1000)
  const pad = (value: number) => String(value).padStart(2, "0")
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`
}

function formatLatency(ms?: number | null) {
  if (ms === undefined || ms === null) return "--"
  return `${Math.round(ms)} ms`
}

function formatWindow(window: TrendWindow) {
  return window === "24h" ? "24 小时" : window === "7d" ? "7 天" : "90 天"
}

function statusFor(target: TargetStatus) {
  return target.current_status ?? target.status
}

function vendorLabel(model: string) {
  const name = model.toLowerCase()
  if (name.includes("claude")) return "Anthropic"
  if (name.includes("gpt") || name.startsWith("o1") || name.startsWith("o3") || name.startsWith("o4")) return "OpenAI"
  if (name.includes("gemini")) return "Google"
  if (name.includes("deepseek")) return "DeepSeek"
  if (name.includes("qwen") || name.includes("通义")) return "阿里云"
  return model ? "自定义" : "通道自检"
}

function TrendChart({ trend, metric, model }: { trend: LatencyTrend; metric: TrendMetric; model: string }) {
  const valid = useMemo(() => trend.points.filter((point) => point[metric] !== null && point[metric] !== undefined), [metric, trend.points])
  const max = valid.length ? Math.max(...valid.map((point) => point[metric] as number)) : 0
  const min = valid.length ? Math.min(...valid.map((point) => point[metric] as number)) : 0
  const chartWidth = 700
  const chartHeight = 220
  const padding = { top: 18, right: 14, bottom: 28, left: 48 }
  const plotWidth = chartWidth - padding.left - padding.right
  const plotHeight = chartHeight - padding.top - padding.bottom
  const floor = Math.max(0, min - Math.max(20, (max - min) * 0.15))
  const ceiling = Math.max(floor + 1, max + Math.max(20, (max - min) * 0.15))
  const x = (index: number) => padding.left + (index / Math.max(1, trend.points.length - 1)) * plotWidth
  const y = (value: number) => padding.top + (1 - (value - floor) / (ceiling - floor)) * plotHeight

  const paths: string[] = []
  let current: string[] = []
  trend.points.forEach((point, index) => {
    const value = point[metric]
    if (value === null || value === undefined) {
      if (current.length) paths.push(current.join(" "))
      current = []
      return
    }
    current.push(`${current.length ? "L" : "M"}${x(index).toFixed(2)},${y(value).toFixed(2)}`)
  })
  if (current.length) paths.push(current.join(" "))

  const latest = valid.at(-1)
  const summary = valid.length
    ? `${model} ${formatWindow(trend.window)} ${metrics.find((item) => item.key === metric)?.label} 趋势，共 ${valid.length} 个有效分桶，当前 ${formatLatency(latest?.[metric])}，范围 ${formatLatency(min)} 至 ${formatLatency(max)}。`
    : `${model} ${formatWindow(trend.window)}暂无有效响应时间数据。`

  if (!valid.length) return <div className="flex min-h-[220px] items-center justify-center border border-dashed border-border/80 bg-surface/35 px-6 text-center text-sm text-muted-foreground">暂无有效响应时间数据</div>

  return (
    <div className="border border-border/80 bg-surface/35 px-2 py-3 sm:px-3">
      <p className="sr-only" role="status">{summary}</p>
      <svg viewBox={`0 0 ${chartWidth} ${chartHeight}`} className="h-auto w-full" role="img" aria-label={summary} focusable="true">
        {[0, 0.5, 1].map((ratio) => {
          const lineY = padding.top + ratio * plotHeight
          const label = Math.round(ceiling - ratio * (ceiling - floor))
          return <g key={ratio}><line x1={padding.left} x2={chartWidth - padding.right} y1={lineY} y2={lineY} stroke="hsl(var(--border) / .8)" strokeDasharray="3 5" /><text x={padding.left - 9} y={lineY + 4} textAnchor="end" fill="hsl(var(--muted-foreground))" fontSize="11">{label}ms</text></g>
        })}
        {paths.map((path, index) => <path key={index} d={path} fill="none" stroke="hsl(var(--primary))" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round" vectorEffect="non-scaling-stroke" />)}
        {latest ? <circle cx={x(trend.points.indexOf(latest))} cy={y(latest[metric] as number)} r="4" fill="hsl(var(--primary))" stroke="hsl(var(--card))" strokeWidth="2" /> : null}
        <text x={padding.left} y={chartHeight - 7} fill="hsl(var(--muted-foreground))" fontSize="11">{formatDateTime(trend.points[0]?.ts).slice(11)}</text>
        <text x={chartWidth - padding.right} y={chartHeight - 7} textAnchor="end" fill="hsl(var(--muted-foreground))" fontSize="11">{formatDateTime(trend.points.at(-1)?.ts).slice(11)}</text>
      </svg>
    </div>
  )
}

export function ModelDetailSheet({ open, onOpenChange, record }: { open: boolean; onOpenChange: (open: boolean) => void; record: ModelDetailRecord | null }) {
  const [window, setWindow] = useState<TrendWindow>("24h")
  const [metric, setMetric] = useState<TrendMetric>("p95_latency_ms")
  const [trend, setTrend] = useState<LatencyTrend | null>(null)
  const [trendLoading, setTrendLoading] = useState(false)
  const [trendError, setTrendError] = useState("")

  useEffect(() => {
    if (!open || !record) return
    setWindow("24h")
    setMetric("p95_latency_ms")
  }, [open, record?.channel.id, record?.target.model])

  useEffect(() => {
    if (!open || !record) return
    let active = true
    setTrendLoading(true)
    setTrendError("")
    setTrend(null)
    fetchStatusTrend(record.channel.id, record.target.model, window)
      .then((result) => { if (active) setTrend(result) })
      .catch((cause) => { if (active) setTrendError(cause instanceof Error ? cause.message : "趋势加载失败") })
      .finally(() => { if (active) setTrendLoading(false) })
    return () => { active = false }
  }, [open, record?.channel.id, record?.target.model, window])

  const target = record?.target
  const current = target ? statusMeta(statusFor(target)) : statusMeta(undefined)
  const CurrentIcon = current.icon
  const raw = target ? statusMeta(target.status) : statusMeta(undefined)
  const model = target?.model || "服务自检"
  const streak = target?.streak

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent side="right" className="overflow-y-auto p-0">
        <SheetHeader>
          <div className="flex items-center gap-2 pr-4"><Activity className="h-4 w-4 text-primary" /><SheetTitle className="truncate font-mono text-base">{model}</SheetTitle></div>
          <SheetDescription>{record ? `${record.provider} / ${record.channel.name}` : "模型详情"}</SheetDescription>
        </SheetHeader>
        {record && target ? <div className="space-y-5 p-5">
          <section className="grid gap-3 sm:grid-cols-2" aria-label="模型状态摘要">
            <div className="border border-border/80 bg-surface/40 p-3"><p className="text-xs text-muted-foreground">当前状态</p><div className="mt-2 flex items-center gap-2"><span className={cn("probe-status-dot", statusFor(target) === 1 ? "probe-dot-up" : statusFor(target) === 2 ? "probe-dot-slow" : statusFor(target) === 0 ? "probe-dot-down" : "probe-dot-empty")} /><Badge variant={current.tone}><CurrentIcon className="h-3.5 w-3.5" />{current.label}</Badge></div><p className="mt-2 text-xs text-muted-foreground">原始探测：{raw.label}</p></div>
            <div className="border border-border/80 bg-surface/40 p-3"><p className="text-xs text-muted-foreground">最近探测</p><p className="mt-2 font-mono text-sm font-semibold">{formatDateTime(target.checked_at)}</p><p className="mt-1 text-xs text-muted-foreground">{target.latency_ms !== undefined ? formatLatency(target.latency_ms) : "暂无延迟"}{target.http_code ? ` · HTTP ${target.http_code}` : ""}</p></div>
          </section>

          <section className="border border-border/80 bg-surface/40 p-4" aria-label="连续状态">
            <div className="flex items-center gap-2"><Gauge className="h-4 w-4 text-primary" /><h3 className="text-sm font-semibold">连续状态</h3></div>
            <div className="mt-4 grid grid-cols-2 gap-x-4 gap-y-3 text-sm sm:grid-cols-4"><div><p className="text-xs text-muted-foreground">连续成功</p><p className="mt-1 font-mono font-semibold text-success">{streak?.consecutive_successes ?? 0} 次</p></div><div><p className="text-xs text-muted-foreground">连续降级</p><p className="mt-1 font-mono font-semibold text-warning">{streak?.consecutive_degraded ?? 0} 次</p></div><div><p className="text-xs text-muted-foreground">连续故障</p><p className="mt-1 font-mono font-semibold text-destructive">{streak?.consecutive_failures ?? 0} 次</p></div><div><p className="text-xs text-muted-foreground">连续异常</p><p className="mt-1 font-mono font-semibold text-warning">{streak?.consecutive_anomalies ?? 0} / {streak?.failure_threshold ?? "--"}</p></div></div>
            <p className="mt-4 border-t border-border/70 pt-3 text-xs text-muted-foreground">恢复阈值 {streak?.recovery_threshold ?? "--"} 次连续成功 · 服务商 {vendorLabel(model)}</p>
          </section>

          <section aria-labelledby="latency-trend-title">
            <div className="flex flex-wrap items-end justify-between gap-3"><div><div className="flex items-center gap-2"><Database className="h-4 w-4 text-primary" /><h3 id="latency-trend-title" className="text-sm font-semibold">响应时间趋势</h3></div><p className="mt-1 text-xs text-muted-foreground">有效延迟参与统计，空桶保持为空。</p></div><div className="flex rounded-md border border-border/80 bg-surface/60 p-1" role="group" aria-label="趋势窗口">{windows.map((value) => <Button key={value} type="button" size="sm" variant={window === value ? "secondary" : "ghost"} className="h-8 px-2.5 text-xs" aria-pressed={window === value} onClick={() => setWindow(value)}>{formatWindow(value)}</Button>)}</div></div>
            <div className="mt-3 flex flex-wrap gap-1" role="group" aria-label="响应时间指标">{metrics.map((item) => <Button key={item.key} type="button" size="sm" variant={metric === item.key ? "default" : "outline"} className="h-8 px-3 text-xs" aria-pressed={metric === item.key} onClick={() => setMetric(item.key)}>{item.label}</Button>)}</div>
            <div className="mt-3" aria-live="polite">{trendLoading ? <div className="flex min-h-[220px] items-center justify-center border border-dashed border-border/80 text-sm text-muted-foreground"><Clock3 className="mr-2 h-4 w-4 animate-pulse" />加载趋势...</div> : trendError ? <div role="alert" className="border border-warning/40 bg-warning/10 px-3 py-4 text-sm text-warning-foreground">{trendError}</div> : trend ? <TrendChart trend={trend} metric={metric} model={model} /> : null}</div>
          </section>

          <div className="flex items-center gap-2 border-t border-border/80 pt-4 text-xs text-muted-foreground"><Server className="h-3.5 w-3.5" />探测服务：{record.channel.template || "未分类"} · {record.channel.hidden ? "隐藏渠道" : "公开渠道"}</div>
        </div> : <div className="p-6 text-sm text-muted-foreground">未选择模型</div>}
      </SheetContent>
    </Sheet>
  )
}
