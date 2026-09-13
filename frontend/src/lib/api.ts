import type { AdminChannel, AdminChannelsPayload, AdminSettings, ChannelWriteInput, LatencyTrend, ProbeResult, ProxyTestResult, ProxyWriteInput, StatusPayload, TrendWindow } from "@/types"

export class ApiError extends Error {
  readonly status: number

  constructor(message: string, status: number) {
    super(message)
    this.name = "ApiError"
    this.status = status
  }
}

async function request<T>(input: RequestInfo | URL, init?: RequestInit): Promise<T> {
  const response = await fetch(input, {
    credentials: "same-origin",
    ...init,
  })
  if (!response.ok) {
    let message = `请求失败（${response.status}）`
    try {
      const payload = (await response.json()) as { error?: string; message?: string }
      message = payload.error || payload.message || message
    } catch {
      // 非 JSON 错误响应使用默认消息。
    }
    throw new ApiError(message, response.status)
  }
  return response.json() as Promise<T>
}

export function fetchStatus() {
  return request<StatusPayload>("/api/status")
}

export function fetchStatusTrend(channelId: string, model: string, window: TrendWindow) {
  const params = new URLSearchParams({ channel_id: channelId, model, window })
  return request<LatencyTrend>(`/api/status/trend?${params.toString()}`)
}

export function login(username: string, password: string) {
  return request<{ ok: boolean }>("/api/login", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ username, password }),
  })
}

export function logout() {
  return request<{ ok: boolean }>("/api/logout", { method: "POST" })
}

export function fetchAdminChannels() {
  return request<AdminChannelsPayload>("/api/admin/channels")
}

export function createChannel(input: ChannelWriteInput) {
  return request<AdminChannel>("/api/admin/channels", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  })
}

export function updateChannel(id: string, input: ChannelWriteInput) {
  return request<AdminChannel>(`/api/admin/channels/${encodeURIComponent(id)}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  })
}

export function deleteChannel(id: string, revision: number) {
  return request<{ ok: boolean; archived_file: string }>(`/api/admin/channels/${encodeURIComponent(id)}`, {
    method: "DELETE",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ revision }),
  })
}

export function probeChannel(id: string, model: string) {
  return request<ProbeResult>(`/api/admin/channels/${encodeURIComponent(id)}/probe`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ model }),
  })
}

export function resetChannel(id: string, revision: number) {
  return request<{ ok: boolean; deleted_records: number }>(`/api/admin/channels/${encodeURIComponent(id)}/reset`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ revision }),
  })
}

export function createProxy(input: ProxyWriteInput) {
  return request<import("@/types").AdminProxy>("/api/admin/proxies", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  })
}

export function updateProxy(id: string, input: ProxyWriteInput) {
  return request<import("@/types").AdminProxy>(`/api/admin/proxies/${encodeURIComponent(id)}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  })
}

export function deleteProxy(id: string, revision: number) {
  return request<{ ok: boolean; archived_file: string }>(`/api/admin/proxies/${encodeURIComponent(id)}`, {
    method: "DELETE",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ revision }),
  })
}

export function testProxy(id: string) {
  return request<ProxyTestResult>(`/api/admin/proxies/${encodeURIComponent(id)}/test`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: "{}",
  })
}

export function fetchAdminSettings() {
  return request<AdminSettings>("/api/admin/settings")
}

export function updateAdminSettings(input: AdminSettings) {
  return request<AdminSettings>("/api/admin/settings", {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  })
}
