import { useCallback, useEffect, useMemo, useState } from "react"
import {
	Activity,
	ArrowLeft,
  Check,
  CircleAlert,
  Eye,
  EyeOff,
  KeyRound,
  LoaderCircle,
  LogIn,
  LogOut,
  Moon,
  Pencil,
  Power,
  PowerOff,
  Plus,
  RefreshCw,
  RotateCcw,
  Server,
  Settings,
  Sun,
	Trash2,
	Wifi,
	Zap,
} from "lucide-react"
import { AdminLoginDialog } from "@/components/admin-login-dialog"
import { ConfirmDialog } from "@/components/confirm-dialog"
import { StatusDot } from "@/components/status-dot"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip"
import { ApiError, createChannel, createProxy, deleteChannel, deleteProxy, fetchAdminChannels, logout, probeChannel, resetChannel, testProxy, updateChannel, updateProxy } from "@/lib/api"
import { cn } from "@/lib/utils"
import type { AdminChannel, AdminChannelsPayload, AdminProxy, ChannelWriteInput, ProbeResult, ProxyTestResult, ProxyWriteInput, StatusCode } from "@/types"

type Theme = "light" | "dark"

interface ChannelDraft {
  provider: string
  name: string
  hidden: boolean
  disabled: boolean
  interval: string
  template: string
  baseUrl: string
  proxy: string
  apiKeyEnv: string
  apiKey: string
  clearApiKey: boolean
  modelsText: string
}

interface ConfirmConfig {
	title: string
	description: string
	action: string
	fallback: string
	onConfirm: () => Promise<void>
	destructive?: boolean
}

const THEME_STORAGE_KEY = "pulse-theme-v2"
const STATUS_PATH = window.location.pathname.startsWith("/static/") ? "/static/" : "/"
const SETTINGS_PATH = window.location.pathname.startsWith("/static/") ? "/static/admin/settings" : "/admin/settings"

function initialDraft(templates: string[]): ChannelDraft {
  return {
    provider: "kuncode",
    name: "",
    hidden: false,
    disabled: false,
    interval: "15s",
    template: templates[0] || "openai-chat",
    baseUrl: "",
    proxy: "",
    apiKeyEnv: "",
    apiKey: "",
    clearApiKey: false,
    modelsText: "",
  }
}

function draftFromChannel(channel: AdminChannel): ChannelDraft {
  return {
    provider: channel.provider,
    name: channel.name,
    hidden: channel.hidden,
    disabled: channel.disabled,
    interval: channel.interval === "0s" ? "0" : channel.interval,
    template: channel.template,
    baseUrl: channel.base_url,
    proxy: channel.proxy || "",
    apiKeyEnv: channel.api_key_env || "",
    apiKey: "",
    clearApiKey: false,
    modelsText: channel.models.join("\n"),
  }
}

function parseModels(value: string) {
  return [...new Set(value.split(/[\r\n,]+/).map((model) => model.trim()).filter(Boolean))]
}

function buildChannelInput(draft: ChannelDraft, revision?: number): ChannelWriteInput {
  const input: ChannelWriteInput = {
    provider: draft.provider.trim(),
    name: draft.name.trim(),
    hidden: draft.hidden,
    disabled: draft.disabled,
    interval: draft.interval.trim(),
    template: draft.template,
    base_url: draft.baseUrl.trim(),
    proxy: draft.proxy,
    api_key_env: draft.apiKeyEnv.trim(),
    models: parseModels(draft.modelsText),
  }
  if (draft.apiKey.trim()) input.api_key = draft.apiKey.trim()
  if (draft.clearApiKey) input.clear_api_key = true
  if (revision !== undefined) input.revision = revision
  return input
}

function statusLabel(status: StatusCode) {
  if (status === 1) return { label: "可用", variant: "success" as const }
  if (status === 2) return { label: "偏慢", variant: "warning" as const }
  return { label: "故障", variant: "danger" as const }
}

function formatProbeTime(ts: number) {
  return ts ? new Date(ts * 1000).toLocaleString() : "--"
}

function ProxyEditorDialog({
  open,
  onOpenChange,
  proxy,
  onSaved,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  proxy: AdminProxy | null
  onSaved: () => Promise<void>
}) {
  const [name, setName] = useState("")
  const [url, setUrl] = useState("")
  const [pending, setPending] = useState(false)
  const [error, setError] = useState("")

  useEffect(() => {
    if (open) {
      setName(proxy?.name || "")
      setUrl("")
      setError("")
    }
  }, [open, proxy])

  async function submit() {
    setPending(true)
    setError("")
    try {
      const input: ProxyWriteInput = { name: name.trim() }
      if (url.trim()) input.url = url.trim()
      if (proxy) input.revision = proxy.revision
      if (proxy) await updateProxy(proxy.id, input)
      else await createProxy(input)
      await onSaved()
      onOpenChange(false)
    } catch (cause) {
      if (cause instanceof ApiError && cause.status === 409) setError("版本冲突或代理名称已存在，请刷新后重试。")
      else setError(cause instanceof Error ? cause.message : "保存代理失败")
    } finally {
      setPending(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2"><Server className="h-4 w-4 text-primary" />{proxy ? "编辑代理" : "添加代理"}</DialogTitle>
          <DialogDescription>支持 HTTP、HTTPS、SOCKS5 和 SOCKS5H。代理 URL 可包含用户名和密码。</DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-2"><Label htmlFor="proxy-name">代理名称</Label><Input id="proxy-name" value={name} onChange={(event) => setName(event.target.value)} placeholder="香港出口" /></div>
          <div className="space-y-2"><Label htmlFor="proxy-url">代理 URL</Label><Input id="proxy-url" type="text" value={url} onChange={(event) => setUrl(event.target.value)} placeholder={proxy?.url_set ? "已配置，留空保持不变" : "socks5://127.0.0.1:1080"} autoComplete="off" spellCheck={false} />{proxy?.url_preview ? <p className="break-all font-mono text-xs text-muted-foreground">当前：{proxy.url_preview}</p> : null}</div>
        </div>
        {error ? <p role="alert" className="text-sm text-destructive">{error}</p> : null}
        <DialogFooter><Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={pending}>取消</Button><Button type="button" onClick={() => void submit()} disabled={pending}>{pending ? <LoaderCircle className="h-4 w-4 animate-spin" /> : <Check className="h-4 w-4" />}{pending ? "保存中" : "保存代理"}</Button></DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function ChannelEditorDialog({
  open,
  onOpenChange,
  channel,
  templates,
  proxies,
  onSaved,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  channel: AdminChannel | null
  templates: string[]
  proxies: AdminProxy[]
  onSaved: () => Promise<void>
}) {
  const [draft, setDraft] = useState<ChannelDraft>(() => initialDraft(templates))
  const [pending, setPending] = useState(false)
  const [error, setError] = useState("")

  useEffect(() => {
    if (open) {
      setDraft(channel ? draftFromChannel(channel) : initialDraft(templates))
      setError("")
    }
  }, [channel, open, templates])

  function updateDraft<K extends keyof ChannelDraft>(key: K, value: ChannelDraft[K]) {
    setDraft((current) => ({ ...current, [key]: value }))
  }

  async function submit() {
    setPending(true)
    setError("")
    try {
      const input = buildChannelInput(draft, channel?.revision)
      if (channel) await updateChannel(channel.id, input)
      else await createChannel(input)
      await onSaved()
      onOpenChange(false)
    } catch (cause) {
      if (cause instanceof ApiError && cause.status === 409) {
        setError("版本已变化，请刷新列表后再保存。")
      } else {
        setError(cause instanceof Error ? cause.message : "保存渠道失败")
      }
    } finally {
      setPending(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[92vh] max-w-2xl overflow-y-auto">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2"><Server className="h-4 w-4 text-primary" />{channel ? "编辑渠道" : "新建渠道"}</DialogTitle>
          <DialogDescription>{channel ? `${channel.provider} / ${channel.name} · revision ${channel.revision}` : "创建一个号池，并为它配置需要探测的模型。"}</DialogDescription>
        </DialogHeader>
        <div className="grid gap-4 sm:grid-cols-2">
          <div className="space-y-2"><Label htmlFor="channel-provider">服务商 / Provider</Label><Input id="channel-provider" value={draft.provider} onChange={(event) => updateDraft("provider", event.target.value)} placeholder="kuncode" /></div>
          <div className="space-y-2"><Label htmlFor="channel-name">号池名称</Label><Input id="channel-name" value={draft.name} onChange={(event) => updateDraft("name", event.target.value)} placeholder="主力线路" /></div>
          <div className="space-y-2 sm:col-span-2"><Label htmlFor="channel-base-url">Base URL</Label><Input id="channel-base-url" value={draft.baseUrl} onChange={(event) => updateDraft("baseUrl", event.target.value)} placeholder="https://your-endpoint.example" /></div>
          <div className="space-y-2"><Label htmlFor="channel-template">探测模板</Label><select id="channel-template" value={draft.template} onChange={(event) => updateDraft("template", event.target.value)} className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm text-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring">{templates.length ? templates.map((template) => <option key={template} value={template}>{template}</option>) : <option value="">暂无模板</option>}</select></div>
          <div className="space-y-2"><Label htmlFor="channel-interval">探测间隔</Label><Input id="channel-interval" value={draft.interval} onChange={(event) => updateDraft("interval", event.target.value)} placeholder="15s，0 表示使用全局间隔" /></div>
          <div className="space-y-2"><Label htmlFor="channel-proxy">出口代理</Label><select id="channel-proxy" value={draft.proxy} onChange={(event) => updateDraft("proxy", event.target.value)} className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 text-sm text-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring"><option value="">直连（遵循系统代理）</option>{proxies.map((proxy) => <option key={proxy.id} value={proxy.id}>{proxy.name}</option>)}</select></div>
          <div className="space-y-2 sm:col-span-2"><Label htmlFor="channel-models">模型列表</Label><textarea id="channel-models" value={draft.modelsText} onChange={(event) => updateDraft("modelsText", event.target.value)} placeholder="每行一个模型，例如：\ngpt-5.6-luna\ngpt-5.6" rows={5} className="flex min-h-24 w-full resize-y rounded-md border border-input bg-background px-3 py-2 font-mono text-sm text-foreground outline-none placeholder:text-muted-foreground focus-visible:ring-2 focus-visible:ring-ring" /></div>
          <div className="space-y-2 sm:col-span-2"><Label htmlFor="channel-api-key-env">API Key 环境变量</Label><Input id="channel-api-key-env" value={draft.apiKeyEnv} onChange={(event) => updateDraft("apiKeyEnv", event.target.value)} placeholder="例如 KUNCODE_API_KEY" /></div>
          <div className="space-y-2 sm:col-span-2"><Label htmlFor="channel-api-key">API Key</Label><Input id="channel-api-key" type="password" value={draft.apiKey} onChange={(event) => updateDraft("apiKey", event.target.value)} placeholder={channel?.api_key_set ? "已配置，留空保持不变" : "只在保存时写入，不会回显"} autoComplete="new-password" /></div>
        </div>
        <div className="grid gap-3 sm:grid-cols-3">
          <label className="flex cursor-pointer items-start gap-3 rounded-md border border-border/80 bg-surface/60 p-3"><input type="checkbox" checked={!draft.hidden} onChange={(event) => updateDraft("hidden", !event.target.checked)} className="mt-0.5 h-4 w-4 accent-primary" /><span><span className="block text-sm font-medium">公开状态页展示</span><span className="mt-1 block text-xs text-muted-foreground">关闭后仅管理员可见</span></span></label>
          <label className="flex cursor-pointer items-start gap-3 rounded-md border border-border/80 bg-surface/60 p-3"><input type="checkbox" checked={!draft.disabled} onChange={(event) => updateDraft("disabled", !event.target.checked)} className="mt-0.5 h-4 w-4 accent-primary" /><span><span className="block text-sm font-medium">启用自动探测</span><span className="mt-1 block text-xs text-muted-foreground">关闭后暂停调度</span></span></label>
          {channel?.api_key_set ? <label className="flex cursor-pointer items-start gap-3 rounded-md border border-destructive/40 bg-destructive/5 p-3"><input type="checkbox" checked={draft.clearApiKey} onChange={(event) => updateDraft("clearApiKey", event.target.checked)} className="mt-0.5 h-4 w-4 accent-destructive" /><span><span className="block text-sm font-medium text-destructive">清除 API Key</span><span className="mt-1 block text-xs text-muted-foreground">优先于留空保留</span></span></label> : <div className="hidden sm:block" />}
        </div>
        {error ? <p role="alert" className="text-sm text-destructive">{error}</p> : null}
        <DialogFooter><Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={pending}>取消</Button><Button type="button" onClick={() => void submit()} disabled={pending || !templates.length}>{pending ? <LoaderCircle className="h-4 w-4 animate-spin" /> : <Check className="h-4 w-4" />}{pending ? "保存中" : "保存渠道"}</Button></DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function ProbeDialog({
  channel,
  model,
  onModelChange,
  result,
  error,
  pending,
  onProbe,
  open,
  onOpenChange,
}: {
  channel: AdminChannel | null
  model: string
  onModelChange: (model: string) => void
  result: ProbeResult | null
  error: string
  pending: boolean
  onProbe: () => Promise<void>
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const meta = result ? statusLabel(result.status) : null
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2"><Zap className="h-4 w-4 text-warning" />立即探测</DialogTitle>
          <DialogDescription>{channel ? `${channel.provider} / ${channel.name}` : "选择一个渠道"}</DialogDescription>
        </DialogHeader>
        {channel ? <div className="space-y-4">
          <div className="space-y-2"><Label htmlFor="probe-model">模型</Label>{channel.models.length > 1 ? <select id="probe-model" value={model} onChange={(event) => onModelChange(event.target.value)} className="flex h-10 w-full rounded-md border border-input bg-background px-3 py-2 font-mono text-sm text-foreground outline-none focus-visible:ring-2 focus-visible:ring-ring">{channel.models.map((candidate) => <option key={candidate} value={candidate}>{candidate}</option>)}</select> : <div className="flex h-10 items-center rounded-md border border-border/80 bg-surface px-3 font-mono text-sm text-foreground">{channel.models[0] || "通道级健康检查"}</div>}</div>
          {result ? <div className="rounded-md border border-border/80 bg-surface/70 p-4"><div className="flex flex-wrap items-center justify-between gap-3"><Badge variant={meta?.variant}><StatusDot status={result.status} />{meta?.label}</Badge><span className="font-mono text-xs text-muted-foreground">{formatProbeTime(result.ts)}</span></div><div className="mt-4 grid grid-cols-2 gap-3 text-sm"><div><span className="block text-xs text-muted-foreground">延迟</span><span className="font-mono font-semibold">{result.latency_ms} ms</span></div><div><span className="block text-xs text-muted-foreground">HTTP</span><span className="font-mono font-semibold">{result.http_code || "--"}</span></div><div className="col-span-2"><span className="block text-xs text-muted-foreground">结果</span><span className="font-mono text-xs">{result.sub_status || "--"}</span></div></div>{result.error ? <p className="mt-3 break-all text-xs text-destructive">{result.error}</p> : null}</div> : <div className="rounded-md border border-dashed border-border/80 p-4 text-sm text-muted-foreground">点击探测后，这里会显示本次请求的状态、延迟和 HTTP 响应。</div>}
          {error ? <p role="alert" className="text-sm text-destructive">{error}</p> : null}
        </div> : null}
        <DialogFooter><Button type="button" variant="outline" onClick={() => onOpenChange(false)}>关闭</Button><Button type="button" onClick={() => void onProbe()} disabled={pending || !channel}>{pending ? <LoaderCircle className="h-4 w-4 animate-spin" /> : <Zap className="h-4 w-4" />}{pending ? "探测中" : "开始探测"}</Button></DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

export default function ChannelManagementPage() {
  const [data, setData] = useState<AdminChannelsPayload | null>(null)
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [error, setError] = useState("")
  const [loginOpen, setLoginOpen] = useState(false)
  const [editorOpen, setEditorOpen] = useState(false)
  const [editingChannel, setEditingChannel] = useState<AdminChannel | null>(null)
  const [proxyEditorOpen, setProxyEditorOpen] = useState(false)
  const [editingProxy, setEditingProxy] = useState<AdminProxy | null>(null)
  const [probeOpen, setProbeOpen] = useState(false)
  const [probeTarget, setProbeTarget] = useState<AdminChannel | null>(null)
  const [probeModel, setProbeModel] = useState("")
  const [probePending, setProbePending] = useState(false)
  const [probeError, setProbeError] = useState("")
  const [probeResult, setProbeResult] = useState<ProbeResult | null>(null)
  const [proxyTestPending, setProxyTestPending] = useState<string | null>(null)
  const [proxyTestResults, setProxyTestResults] = useState<Record<string, ProxyTestResult>>({})
  const [confirmConfig, setConfirmConfig] = useState<ConfirmConfig | null>(null)
  const [confirmPending, setConfirmPending] = useState(false)
  const [confirmError, setConfirmError] = useState("")
  const [theme, setTheme] = useState<Theme>(() => localStorage.getItem(THEME_STORAGE_KEY) === "light" ? "light" : "dark")

  const load = useCallback(async () => {
    setRefreshing(true)
    try {
      const next = await fetchAdminChannels()
      setData(next)
      setError("")
    } catch (cause) {
      if (cause instanceof ApiError && cause.status === 401) {
        setData(null)
        setLoginOpen(true)
        setError("")
      } else {
        setError(cause instanceof Error ? cause.message : "渠道列表加载失败")
      }
    } finally {
      setLoading(false)
      setRefreshing(false)
    }
  }, [])

  useEffect(() => { void load() }, [load])

  useEffect(() => {
    document.documentElement.classList.toggle("dark", theme === "dark")
    document.documentElement.classList.toggle("light", theme === "light")
    localStorage.setItem(THEME_STORAGE_KEY, theme)
  }, [theme])

  const enabledCount = useMemo(() => data?.channels.filter((channel) => !channel.disabled).length || 0, [data])
  const modelCount = useMemo(() => data?.channels.reduce((total, channel) => total + channel.models.length, 0) || 0, [data])
  const isAdmin = data !== null

  function openCreate() {
    setEditingChannel(null)
    setEditorOpen(true)
  }

  function openEdit(channel: AdminChannel) {
    setEditingChannel(channel)
    setEditorOpen(true)
  }

  function openProbe(channel: AdminChannel) {
    setProbeTarget(channel)
    setProbeModel(channel.models[0] || "")
    setProbeResult(null)
    setProbeError("")
    setProbeOpen(true)
  }

  function openProxyCreate() {
    setEditingProxy(null)
    setProxyEditorOpen(true)
  }

  function openProxyEdit(proxy: AdminProxy) {
    setEditingProxy(proxy)
    setProxyEditorOpen(true)
  }

  function requestConfirm(config: ConfirmConfig) {
    setConfirmConfig(config)
    setConfirmError("")
  }

  async function runConfirmedAction() {
    if (!confirmConfig) return
    setConfirmPending(true)
    setConfirmError("")
    try {
      await confirmConfig.onConfirm()
      setConfirmConfig(null)
    } catch (cause) {
      setConfirmError(cause instanceof Error ? cause.message : confirmConfig.fallback)
    } finally {
      setConfirmPending(false)
    }
  }

  async function runProbe() {
    if (!probeTarget) return
    setProbePending(true)
    setProbeError("")
    try {
      setProbeResult(await probeChannel(probeTarget.id, probeModel))
      await load()
    } catch (cause) {
      setProbeError(cause instanceof Error ? cause.message : "探测失败")
    } finally {
      setProbePending(false)
    }
  }

  function archive(channel: AdminChannel) {
    requestConfirm({
      title: "归档渠道",
      description: `确认归档渠道“${channel.name}”？归档后会停止探测并从列表移除。`,
      action: "确认归档",
      fallback: "归档渠道失败",
      destructive: true,
      onConfirm: async () => {
        await deleteChannel(channel.id, channel.revision)
        await load()
      },
    })
  }

  function resetChannelStats(channel: AdminChannel) {
    requestConfirm({
      title: "重置渠道状态",
      description: `确认重置“${channel.name}”的全部统计状态？历史探测、事件和当前状态都会清空。`,
      action: "确认重置",
      fallback: "重置渠道状态失败",
      destructive: true,
      onConfirm: async () => {
        await resetChannel(channel.id, channel.revision)
        await load()
      },
    })
  }

  async function toggleChannel(channel: AdminChannel) {
    try {
      await updateChannel(channel.id, {
        provider: channel.provider,
        name: channel.name,
        hidden: channel.hidden,
        disabled: !channel.disabled,
        interval: channel.interval,
        template: channel.template,
        base_url: channel.base_url,
        proxy: channel.proxy || "",
        api_key_env: channel.api_key_env || "",
        models: channel.models,
        revision: channel.revision,
      })
      await load()
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "更新渠道状态失败")
    }
  }

  function archiveProxy(proxy: AdminProxy) {
    requestConfirm({
      title: "归档代理",
      description: `确认归档代理“${proxy.name}”？引用它的渠道需要先切换到其他代理。`,
      action: "确认归档",
      fallback: "归档代理失败",
      destructive: true,
      onConfirm: async () => {
        await deleteProxy(proxy.id, proxy.revision)
        await load()
      },
    })
  }

  async function testProxyConnection(proxy: AdminProxy) {
    setProxyTestPending(proxy.id)
    setProxyTestResults((current) => {
      const next = { ...current }
      delete next[proxy.id]
      return next
    })
    try {
      const result = await testProxy(proxy.id)
      setProxyTestResults((current) => ({ ...current, [proxy.id]: result }))
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "测试代理失败")
    } finally {
      setProxyTestPending(null)
    }
  }

  async function handleLogout() {
    await logout()
    setData(null)
    setLoginOpen(true)
  }

  return (
    <div className="min-h-screen bg-background text-foreground">
      <header className="border-b border-border/80 bg-background/95">
        <div className="mx-auto flex w-full max-w-[1280px] flex-wrap items-center justify-between gap-4 px-4 py-3 lg:px-7">
          <div className="flex min-w-0 items-center gap-3"><div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-md bg-primary text-primary-foreground"><Activity className="h-4 w-4" /></div><div className="min-w-0"><h1 className="font-mono text-base font-semibold">KunCodeRelayPulse</h1><p className="font-mono text-[11px] uppercase tracking-[0.16em] text-muted-foreground">Channel control plane</p></div></div>
          <div className="flex shrink-0 items-center gap-1.5"><Button asChild variant="ghost" size="sm"><a href={STATUS_PATH}><ArrowLeft className="h-3.5 w-3.5" />状态预览</a></Button>{isAdmin ? <Button asChild variant="ghost" size="sm"><a href={SETTINGS_PATH}><Settings className="h-3.5 w-3.5" />探测配置</a></Button> : null}<Button variant="ghost" size="icon" onClick={() => setTheme(theme === "light" ? "dark" : "light")} aria-label={theme === "light" ? "切换深色主题" : "切换浅色主题"} title={theme === "light" ? "切换深色主题" : "切换浅色主题"}>{theme === "light" ? <Moon className="h-4 w-4" /> : <Sun className="h-4 w-4" />}</Button>{isAdmin ? <Button variant="outline" size="sm" onClick={() => void handleLogout()}><LogOut className="h-3.5 w-3.5" />退出</Button> : <Button variant="outline" size="sm" onClick={() => setLoginOpen(true)}><LogIn className="h-3.5 w-3.5" />登录</Button>}</div>
        </div>
      </header>

      <main className="mx-auto w-full max-w-[1280px] px-4 pb-10 pt-6 lg:px-7">
        <div className="flex flex-wrap items-end justify-between gap-4"><div><p className="font-mono text-[11px] uppercase tracking-[0.2em] text-primary">Admin / Channels</p><h2 className="mt-1 text-2xl font-semibold">渠道管理</h2><p className="mt-1 text-sm text-muted-foreground">一个渠道对应一个号池，模型列表会展开为独立探测目标。</p></div><div className="flex items-center gap-2"><Button variant="outline" size="sm" onClick={() => void load()} disabled={refreshing} aria-label="刷新渠道列表" title="刷新渠道列表"><RefreshCw className={cn("h-3.5 w-3.5", refreshing && "animate-spin")} />刷新</Button><Button size="sm" onClick={openCreate}><Plus className="h-3.5 w-3.5" />新建渠道</Button></div></div>
        <div className="mt-6 grid gap-3 sm:grid-cols-3"><div className="border-l-2 border-primary bg-surface/45 px-4 py-3"><p className="text-xs text-muted-foreground">渠道 / 号池</p><p className="mt-1 font-mono text-2xl font-semibold">{data?.channels.length || 0}</p></div><div className="border-l-2 border-success bg-surface/45 px-4 py-3"><p className="text-xs text-muted-foreground">自动探测中</p><p className="mt-1 font-mono text-2xl font-semibold">{enabledCount}</p></div><div className="border-l-2 border-warning bg-surface/45 px-4 py-3"><p className="text-xs text-muted-foreground">模型探测目标</p><p className="mt-1 font-mono text-2xl font-semibold">{modelCount}</p></div></div>
        {error ? <div role="alert" className="mt-4 flex items-start gap-2 border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive"><CircleAlert className="mt-0.5 h-4 w-4 shrink-0" />{error}</div> : null}
        <section className="mt-5 overflow-hidden rounded-lg border border-border bg-card shadow-sm" aria-label="渠道列表">
          {loading && !data ? <div className="space-y-3 p-6"><div className="h-10 animate-pulse rounded bg-muted" /><div className="h-16 animate-pulse rounded bg-muted" /><div className="h-16 animate-pulse rounded bg-muted" /></div> : data?.channels.length ? <TooltipProvider delayDuration={180}><div className="hidden overflow-x-auto lg:block"><Table className="min-w-[980px]"><TableHeader><TableRow className="bg-surface hover:bg-surface"><TableHead>渠道 / 号池</TableHead><TableHead>模型</TableHead><TableHead>探测路由</TableHead><TableHead>间隔</TableHead><TableHead>状态</TableHead><TableHead>密钥</TableHead><TableHead>版本</TableHead><TableHead className="text-right">操作</TableHead></TableRow></TableHeader><TableBody>{data.channels.map((channel) => <TableRow key={channel.id} className="h-[82px]"><TableCell><div className="flex min-w-[170px] items-start gap-3"><StatusDot status={channel.disabled ? undefined : 1} className={cn("mt-1.5", channel.disabled && "bg-muted-foreground/50")} /><div className="min-w-0"><div className="flex items-center gap-2"><span className="font-semibold">{channel.name}</span>{channel.hidden ? <EyeOff className="h-3.5 w-3.5 text-warning" /> : <Eye className="h-3.5 w-3.5 text-muted-foreground" />}</div><p className="mt-1 truncate font-mono text-xs text-muted-foreground">{channel.provider}</p></div></div></TableCell><TableCell><div className="max-w-[180px] space-y-1">{channel.models.length ? channel.models.slice(0, 3).map((model) => <div key={model} className="truncate font-mono text-xs text-foreground">{model}</div>) : <span className="text-xs text-muted-foreground">通道级检查</span>}{channel.models.length > 3 ? <span className="text-xs text-muted-foreground">+{channel.models.length - 3} 个</span> : null}</div></TableCell><TableCell><div className="max-w-[230px]"><Badge variant="outline" className="font-mono text-[11px]">{channel.template}</Badge><p className="mt-1 truncate font-mono text-xs text-muted-foreground" title={channel.base_url}>{channel.base_url}</p>{channel.proxy_name || channel.proxy ? <p className="mt-1 truncate font-mono text-[11px] text-primary">代理 · {channel.proxy_name || channel.proxy}</p> : <p className="mt-1 text-[11px] text-muted-foreground">直连</p>}</div></TableCell><TableCell><span className="font-mono text-sm">{channel.interval}</span></TableCell><TableCell>{channel.disabled ? <Badge variant="secondary">已停用</Badge> : <Badge variant="success"><span className="h-1.5 w-1.5 rounded-full bg-success" />监测中</Badge>}{channel.hidden ? <span className="mt-1 block text-xs text-warning">公开页隐藏</span> : null}</TableCell><TableCell>{channel.api_key_set ? <Badge variant="success"><KeyRound className="h-3 w-3" />已配置</Badge> : <Badge variant="secondary">未配置</Badge>}{channel.api_key_env ? <p className="mt-1 max-w-[120px] truncate font-mono text-[11px] text-muted-foreground">{channel.api_key_env}</p> : null}</TableCell><TableCell><span className="font-mono text-xs text-muted-foreground">r{channel.revision}</span></TableCell><TableCell><div className="flex justify-end gap-1"><Button variant="outline" size="sm" onClick={() => openEdit(channel)}><Pencil className="h-3.5 w-3.5" />编辑</Button><Tooltip><TooltipTrigger asChild><Button variant="ghost" size="icon" onClick={() => void toggleChannel(channel)} aria-label={channel.disabled ? `启用 ${channel.name}` : `停用 ${channel.name}`} title={channel.disabled ? "启用自动探测" : "停用自动探测"}>{channel.disabled ? <Power className="h-4 w-4 text-success" /> : <PowerOff className="h-4 w-4 text-warning" />}</Button></TooltipTrigger><TooltipContent>{channel.disabled ? "启用自动探测" : "停用自动探测"}</TooltipContent></Tooltip><Tooltip><TooltipTrigger asChild><Button variant="ghost" size="icon" onClick={() => openProbe(channel)} aria-label={`立即探测 ${channel.name}`} title="立即探测"><Zap className="h-4 w-4 text-warning" /></Button></TooltipTrigger><TooltipContent>立即探测</TooltipContent></Tooltip><Tooltip><TooltipTrigger asChild><Button variant="ghost" size="icon" onClick={() => void resetChannelStats(channel)} aria-label={`重置状态 ${channel.name}`} title="重置状态"><RotateCcw className="h-4 w-4 text-warning" /></Button></TooltipTrigger><TooltipContent>重置统计状态</TooltipContent></Tooltip><Tooltip><TooltipTrigger asChild><Button variant="ghost" size="icon" onClick={() => void archive(channel)} aria-label={`归档 ${channel.name}`} title="归档"><Trash2 className="h-4 w-4 text-destructive" /></Button></TooltipTrigger><TooltipContent>归档渠道</TooltipContent></Tooltip></div></TableCell></TableRow>)}</TableBody></Table></div><div className="divide-y divide-border lg:hidden">{data.channels.map((channel) => <div key={channel.id} className="space-y-4 p-4"><div className="flex items-start justify-between gap-3"><div className="flex min-w-0 items-start gap-3"><StatusDot status={channel.disabled ? undefined : 1} className={cn("mt-1.5", channel.disabled && "bg-muted-foreground/50")} /><div className="min-w-0"><div className="flex items-center gap-2"><span className="truncate font-semibold">{channel.name}</span>{channel.hidden ? <EyeOff className="h-3.5 w-3.5 shrink-0 text-warning" /> : null}</div><p className="mt-1 font-mono text-xs text-muted-foreground">{channel.provider} / {channel.template}</p></div></div>{channel.disabled ? <Badge variant="secondary">已停用</Badge> : <Badge variant="success">监测中</Badge>}</div><div className="grid gap-3 text-sm sm:grid-cols-2"><div><p className="text-xs text-muted-foreground">模型</p><p className="mt-1 break-all font-mono text-xs">{channel.models.length ? channel.models.join(" · ") : "通道级检查"}</p></div><div><p className="text-xs text-muted-foreground">间隔 / 版本</p><p className="mt-1 font-mono text-xs">{channel.interval} / r{channel.revision}</p></div><div><p className="sm:col-span-2 text-xs text-muted-foreground">探测路由</p><p className="mt-1 break-all font-mono text-xs">{channel.proxy_name || channel.proxy ? `代理 · ${channel.proxy_name || channel.proxy}` : "直连"} · {channel.base_url}</p></div></div><div className="flex flex-wrap items-center justify-end gap-2"><Button variant="outline" size="sm" onClick={() => openEdit(channel)}><Pencil className="h-3.5 w-3.5" />编辑</Button><Button variant="secondary" size="sm" onClick={() => void toggleChannel(channel)}>{channel.disabled ? <Power className="h-3.5 w-3.5 text-success" /> : <PowerOff className="h-3.5 w-3.5 text-warning" />}{channel.disabled ? "启用" : "停用"}</Button><Button variant="secondary" size="sm" onClick={() => openProbe(channel)}><Zap className="h-3.5 w-3.5 text-warning" />立即探测</Button><Button variant="secondary" size="sm" onClick={() => void resetChannelStats(channel)}><RotateCcw className="h-3.5 w-3.5 text-warning" />重置状态</Button><Button variant="ghost" size="sm" onClick={() => void archive(channel)}><Trash2 className="h-3.5 w-3.5 text-destructive" />归档</Button></div></div>)}</div></TooltipProvider> : <div className="px-6 py-16 text-center"><Server className="mx-auto h-8 w-8 text-muted-foreground" /><p className="mt-3 text-sm text-muted-foreground">暂无渠道</p><Button className="mt-5" onClick={openCreate}><Plus className="h-4 w-4" />新建第一个渠道</Button></div>}
        </section>
        <section className="mt-5 overflow-hidden rounded-lg border border-border bg-card shadow-sm" aria-label="代理设置">
          <div className="flex flex-wrap items-center justify-between gap-3 border-b border-border/80 bg-surface/60 px-4 py-4 sm:px-5"><div><h3 className="flex items-center gap-2 text-base font-semibold"><Server className="h-4 w-4 text-primary" />代理设置</h3><p className="mt-1 text-xs text-muted-foreground">代理可复用给多个渠道；渠道编辑时选择出口。</p></div><Button size="sm" onClick={openProxyCreate}><Plus className="h-3.5 w-3.5" />添加代理</Button></div>
          {data?.proxies.length ? <div className="divide-y divide-border">{data.proxies.map((proxy) => <div key={proxy.id} className="flex flex-wrap items-center justify-between gap-4 px-4 py-4 sm:px-5"><div className="flex min-w-0 items-start gap-3"><div className="mt-0.5 flex h-8 w-8 shrink-0 items-center justify-center rounded-md border border-primary/30 bg-primary/10 text-primary"><Server className="h-4 w-4" /></div><div className="min-w-0"><div className="flex flex-wrap items-center gap-2"><span className="font-semibold">{proxy.name}</span>{proxy.has_auth ? <Badge variant="warning"><KeyRound className="h-3 w-3" />含认证</Badge> : null}</div><p className="mt-1 max-w-[640px] break-all font-mono text-xs text-muted-foreground">{proxy.url_preview || "已配置"}</p><p className="mt-1 text-xs text-muted-foreground">{data.channels.filter((channel) => channel.proxy === proxy.id).length} 个渠道使用 · r{proxy.revision}</p>{proxyTestResults[proxy.id] ? <p role="status" className={cn("mt-2 break-all text-xs", proxyTestResults[proxy.id].ok ? "text-success" : "text-destructive")}><span className="font-semibold">{proxyTestResults[proxy.id].ok ? "Google 可用" : "Google 不通"}</span><span> · {proxyTestResults[proxy.id].latency_ms ?? "--"} ms</span>{proxyTestResults[proxy.id].http_code ? <span> · HTTP {proxyTestResults[proxy.id].http_code}</span> : null}{proxyTestResults[proxy.id].error ? <span> · {proxyTestResults[proxy.id].error}</span> : null}</p> : null}</div></div><div className="flex shrink-0 items-center gap-2"><Button variant="outline" size="sm" onClick={() => void testProxyConnection(proxy)} disabled={proxyTestPending === proxy.id}>{proxyTestPending === proxy.id ? <LoaderCircle className="h-3.5 w-3.5 animate-spin" /> : <Wifi className="h-3.5 w-3.5" />}测试</Button><Button variant="outline" size="sm" onClick={() => openProxyEdit(proxy)}><Pencil className="h-3.5 w-3.5" />编辑</Button><Button variant="ghost" size="sm" onClick={() => void archiveProxy(proxy)}><Trash2 className="h-3.5 w-3.5 text-destructive" />归档</Button></div></div>)}</div> : <div className="px-5 py-10 text-center"><p className="text-sm text-muted-foreground">还没有代理配置</p><Button className="mt-4" variant="outline" onClick={openProxyCreate}><Plus className="h-4 w-4" />添加第一个代理</Button></div>}
        </section>
        <p className="mt-4 flex items-center gap-2 px-1 text-xs text-muted-foreground"><KeyRound className="h-3.5 w-3.5" />API Key 只显示是否已配置；编辑时留空会保留原值。</p>
      </main>

      <ChannelEditorDialog open={editorOpen} onOpenChange={setEditorOpen} channel={editingChannel} templates={data?.templates || []} proxies={data?.proxies || []} onSaved={load} />
      <ProxyEditorDialog open={proxyEditorOpen} onOpenChange={setProxyEditorOpen} proxy={editingProxy} onSaved={load} />
      <ProbeDialog open={probeOpen} onOpenChange={setProbeOpen} channel={probeTarget} model={probeModel} onModelChange={setProbeModel} result={probeResult} error={probeError} pending={probePending} onProbe={runProbe} />
      <ConfirmDialog
        open={confirmConfig !== null}
        onOpenChange={(open) => {
          if (!open) {
            setConfirmConfig(null)
            setConfirmError("")
          }
        }}
        title={confirmConfig?.title || "确认操作"}
        description={confirmConfig?.description || ""}
        action={confirmConfig?.action || "确认"}
        onConfirm={() => void runConfirmedAction()}
        pending={confirmPending}
        error={confirmError}
        destructive={confirmConfig?.destructive}
      />
      <AdminLoginDialog open={loginOpen} onOpenChange={setLoginOpen} onSuccess={load} />
    </div>
  )
}
