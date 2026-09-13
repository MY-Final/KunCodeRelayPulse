import { FormEvent, useEffect, useState } from "react"
import { Activity, Check, CircleAlert, Clock3, Gauge, LogIn, LogOut, Moon, RefreshCw, Save, Settings, Sun } from "lucide-react"
import { AdminLoginDialog } from "@/components/admin-login-dialog"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { ApiError, fetchAdminSettings, logout, updateAdminSettings } from "@/lib/api"
import { cn } from "@/lib/utils"
import type { AdminSettings } from "@/types"

type Theme = "light" | "dark"

const THEME_STORAGE_KEY = "pulse-theme-v2"
const STATUS_PATH = window.location.pathname.startsWith("/static/") ? "/static/" : "/"
const CHANNELS_PATH = window.location.pathname.startsWith("/static/") ? "/static/admin/channels" : "/admin/channels"

function initialTheme(): Theme {
  return localStorage.getItem(THEME_STORAGE_KEY) === "light" ? "light" : "dark"
}

export default function ProbeSettingsPage() {
  const [theme, setTheme] = useState<Theme>(initialTheme)
  const [settings, setSettings] = useState<AdminSettings | null>(null)
  const [draft, setDraft] = useState<AdminSettings>({ interval: "60s", timeout: "30s", slow_latency: "5s", failure_threshold: 3, recovery_threshold: 2 })
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [refreshing, setRefreshing] = useState(false)
  const [error, setError] = useState("")
  const [success, setSuccess] = useState("")
  const [loginOpen, setLoginOpen] = useState(false)

  async function load(initial = false) {
    if (!initial) setRefreshing(true)
    try {
      const next = await fetchAdminSettings()
      setSettings(next)
      setDraft(next)
      setError("")
      setLoginOpen(false)
    } catch (cause) {
      if (cause instanceof ApiError && cause.status === 401) setLoginOpen(true)
      setError(cause instanceof Error ? cause.message : "探测配置加载失败")
    } finally {
      setLoading(false)
      setRefreshing(false)
    }
  }

  useEffect(() => { void load(true) }, [])

  useEffect(() => {
    document.documentElement.classList.toggle("dark", theme === "dark")
    document.documentElement.classList.toggle("light", theme === "light")
    localStorage.setItem(THEME_STORAGE_KEY, theme)
  }, [theme])

  function update<K extends keyof AdminSettings>(key: K, value: AdminSettings[K]) {
    setDraft((current) => ({ ...current, [key]: value }))
    setSuccess("")
  }

  async function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setSaving(true)
    setError("")
    setSuccess("")
    try {
      const next = await updateAdminSettings(draft)
      setSettings(next)
      setDraft(next)
      setSuccess("已保存，新的探测配置已热加载生效。")
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "保存探测配置失败")
    } finally {
      setSaving(false)
    }
  }

  async function handleLogout() {
    await logout()
    setSettings(null)
    setLoginOpen(true)
  }

  return (
    <div className="min-h-screen bg-background text-foreground">
      <header className="border-b border-border/80 bg-background/95">
        <div className="mx-auto flex w-full max-w-[1100px] flex-wrap items-center justify-between gap-4 px-4 py-3 lg:px-7">
          <div className="flex min-w-0 items-center gap-3"><div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-md bg-primary text-primary-foreground"><Activity className="h-4 w-4" /></div><div className="min-w-0"><h1 className="font-mono text-base font-semibold">KunCodeRelayPulse</h1><p className="font-mono text-[11px] uppercase tracking-[0.16em] text-muted-foreground">Admin / Probe settings</p></div></div>
          <div className="flex shrink-0 items-center gap-1.5"><Button asChild variant="ghost" size="sm"><a href={STATUS_PATH}><Activity className="h-3.5 w-3.5" />状态预览</a></Button><Button asChild variant="ghost" size="sm"><a href={CHANNELS_PATH}><Settings className="h-3.5 w-3.5" />渠道管理</a></Button><Button variant="ghost" size="icon" onClick={() => setTheme(theme === "light" ? "dark" : "light")} aria-label={theme === "light" ? "切换深色主题" : "切换浅色主题"} title={theme === "light" ? "切换深色主题" : "切换浅色主题"}>{theme === "light" ? <Moon className="h-4 w-4" /> : <Sun className="h-4 w-4" />}</Button>{settings ? <Button variant="outline" size="sm" onClick={() => void handleLogout()}><LogOut className="h-3.5 w-3.5" />退出</Button> : <Button variant="outline" size="sm" onClick={() => setLoginOpen(true)}><LogIn className="h-3.5 w-3.5" />登录</Button>}</div>
        </div>
      </header>

      <main className="mx-auto w-full max-w-[1100px] px-4 pb-10 pt-6 lg:px-7">
        <div className="flex flex-wrap items-end justify-between gap-4"><div><p className="font-mono text-[11px] uppercase tracking-[0.2em] text-primary">Runtime policy</p><h2 className="mt-1 text-2xl font-semibold">探测配置</h2><p className="mt-1 text-sm text-muted-foreground">调整全局探测默认值；渠道级间隔和模板明确配置仍保持优先。</p></div><Button variant="outline" size="sm" onClick={() => void load()} disabled={refreshing}><RefreshCw className={cn("h-3.5 w-3.5", refreshing && "animate-spin")} />刷新</Button></div>
        {error ? <div role="alert" className="mt-5 flex items-start gap-2 border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive"><CircleAlert className="mt-0.5 h-4 w-4 shrink-0" />{error}</div> : null}
        {success ? <div role="status" className="mt-5 flex items-start gap-2 border border-success/40 bg-success/10 px-3 py-2 text-sm text-success"><Check className="mt-0.5 h-4 w-4 shrink-0" />{success}</div> : null}
        <section className="mt-5 border border-border bg-card shadow-sm" aria-label="探测配置表单">
          {loading ? <div className="space-y-4 p-6"><div className="h-10 animate-pulse rounded bg-muted" /><div className="h-10 animate-pulse rounded bg-muted" /><div className="h-10 animate-pulse rounded bg-muted" /></div> : settings ? <form className="p-5 sm:p-7" onSubmit={(event) => void save(event)}>
            <div className="grid gap-x-6 gap-y-6 sm:grid-cols-2">
              <div className="space-y-2"><Label htmlFor="probe-interval">探测间隔</Label><Input id="probe-interval" value={draft.interval} onChange={(event) => update("interval", event.target.value)} placeholder="60s" /><p className="text-xs text-muted-foreground">允许 5s 到 24h</p></div>
              <div className="space-y-2"><Label htmlFor="probe-timeout">超时时间</Label><Input id="probe-timeout" value={draft.timeout} onChange={(event) => update("timeout", event.target.value)} placeholder="30s" /><p className="text-xs text-muted-foreground">允许 1s 到 10m</p></div>
              <div className="space-y-2"><Label htmlFor="probe-slow">慢响应阈值</Label><Input id="probe-slow" value={draft.slow_latency} onChange={(event) => update("slow_latency", event.target.value)} placeholder="5s 或 0" /><p className="text-xs text-muted-foreground">设为 0 可关闭慢响应判定</p></div>
              <div className="space-y-2"><Label htmlFor="probe-failure">失败阈值</Label><Input id="probe-failure" type="number" min={1} max={100} value={draft.failure_threshold} onChange={(event) => update("failure_threshold", Number(event.target.value))} /><p className="text-xs text-muted-foreground">连续异常达到此次数后升级为故障</p></div>
              <div className="space-y-2"><Label htmlFor="probe-recovery">恢复阈值</Label><Input id="probe-recovery" type="number" min={1} max={100} value={draft.recovery_threshold} onChange={(event) => update("recovery_threshold", Number(event.target.value))} /><p className="text-xs text-muted-foreground">连续成功达到此次数后恢复正常</p></div>
            </div>
            <div className="mt-7 flex flex-wrap items-center justify-between gap-3 border-t border-border/80 pt-5"><div className="flex items-center gap-2 text-xs text-muted-foreground"><Gauge className="h-4 w-4 text-primary" />保存后通过热加载立即应用</div><Button type="submit" disabled={saving}>{saving ? <RefreshCw className="h-4 w-4 animate-spin" /> : <Save className="h-4 w-4" />}{saving ? "保存中" : "保存配置"}</Button></div>
          </form> : <div className="flex min-h-[180px] items-center justify-center px-6 text-center text-sm text-muted-foreground">需要管理员登录后查看和修改探测配置。</div>}
        </section>
        <p className="mt-4 flex items-center gap-2 px-1 text-xs text-muted-foreground"><Clock3 className="h-3.5 w-3.5" />当前配置只影响全局默认值，不会覆盖渠道和探针模板中的明确设置。</p>
      </main>
      <AdminLoginDialog open={loginOpen} onOpenChange={setLoginOpen} onSuccess={load} />
    </div>
  )
}
