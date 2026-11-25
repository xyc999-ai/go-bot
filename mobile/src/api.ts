import { mobileClientIdentity } from './clientIdentity'
import { clientIdentityHeader, encodeClientIdentity, type ClientDevice } from '../../frontend/src/clients/identity'
import type { PermissionMode, SessionPermission } from '../../frontend/src/chat/permissions'
import { fetch } from 'expo/fetch'
import { File } from 'expo-file-system'
import { createParser } from 'eventsource-parser'
import type { ModelEndpoint, ModelSelection, ModelSettings, ProviderPage, ProviderSettings, SessionEvent, SessionModel, SessionRow, TranscriptPage } from '../../frontend/src/api/controller'
import type { Answer, FileRef } from '../../frontend/src/chat/transcript'
import type { LibraryEntry } from './library'
import type { ProactiveStatus } from '../../frontend/src/settings/proactiveState'

export type { ModelEndpoint, ModelSelection, ModelSettings, ProviderPage, ProviderSettings, SessionEvent, SessionRow }
export type Profile = { server: string; account: string }
export type HostFile = { name: string; path: string; size: number; isDir: boolean }
export type HostItems = { rootPath: string; path: string; files: HostFile[]; nextOffset?: number }
export type ScheduleState = 'active' | 'paused' | 'ended'
export type ScheduleKind = 'after' | 'at' | 'every' | 'daily' | 'weekly' | 'cron' | 'rrule'
export type ScheduleTarget = { kind: 'session' | 'new' | 'new_once'; sessionId?: string; create?: { cwd?: string; agentPreset?: string } }
export type ScheduleReceipt = { scheduledAt: string; deliveredAt: string; messageId: string; targetSessionId?: string; prompt?: string }
export type Schedule = {
  record: { id: string; title: string; prompt: string; kind: ScheduleKind; scheduledAt: string; time?: string; timeZone?: string; weekdays?: number[]; expression?: string; afterSeconds?: number; everySeconds?: number; rrule?: string }
  status: ScheduleState
  target?: ScheduleTarget
  sessionId?: string
  updatedAt?: string
  lastDelivery?: ScheduleReceipt
  executionActive?: boolean
  deliveryFloorEndsAt?: string
}
export type ScheduleTiming =
  | { kind: 'after'; after_seconds: number }
  | { kind: 'at'; at: string | { date: string; time: string; time_zone: string } }
  | { kind: 'every'; every_seconds: number }
  | { kind: 'daily'; daily: { time: string; time_zone: string } }
  | { kind: 'weekly'; weekly: { time: string; time_zone: string; weekdays: number[] } }
  | { kind: 'cron'; cron: { expression: string; time_zone: string } }
  | { kind: 'rrule'; rrule: string }
export type SchedulePatch = { title?: string; prompt?: string; timing?: ScheduleTiming; target?: ScheduleTarget; active?: boolean }
export type ScheduleCreate = Required<Omit<SchedulePatch, 'active'>>
export class ApiError extends Error {
  constructor(readonly status: number, readonly code: string, readonly detail?: string) {
    super(code)
  }
}

export function normalizeProfile(profile: Profile): Profile {
  const account = profile.account.trim()
  if (!/^[a-z0-9][a-z0-9_-]{0,63}$/.test(account)) throw new Error('账号使用 1–64 位小写字母、数字、下划线或连字符，以字母或数字开头。')
  const raw = profile.server.trim()
  if (!/^https?:\/\//i.test(raw) || /[\s\\]/.test(raw)) throw new Error('请输入完整的 HTTP 或 HTTPS 地址。')
  let url: URL
  try { url = new URL(raw) } catch { throw new Error('服务器地址格式有误。') }
  if (!url.hostname || url.username || url.password || url.search || url.hash) throw new Error('服务器地址格式有误。')
  return { account, server: url.href.replace(/\/+$/, '') }
}

export function problem(error: unknown, fallback = '操作未完成，请重试。') {
  if (error instanceof ApiError) {
    if (error.code === 'invalid_model_protocol') return '请选择支持的接口协议。'
    if (error.code === 'model_base_url_required') return '请填写基础地址，不包含 /messages、/responses 或 /chat/completions。'
    if (error.code === 'api_key_required_for_new_origin') return '新地址需要重新填写 API key。'
    if (error.code === 'model_default_required') return '请先在「我的 → 模型」设置默认模型。'
    if (error.code === 'model_unavailable' || error.code === 'model_unconfigured') return '请为当前会话选择可用模型。'
    if (error.code === 'settings_saved_host_apply_failed') return '设置已保存，但 Host 暂未应用。可重试或重新连接。'
    if (error.code === 'session_has_schedule') return '此会话还有关联任务。'
    if (error.code === 'session_busy') return '会话正在执行中。'
    if (error.status === 413) return '附件超过上传限制。'
    if (error.status === 401 || error.status === 403) return '入口拒绝了请求。请检查服务器地址或代理设置。'
  }
  return fallback
}

const json = (method: string, body: unknown) => ({ method, headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) })
const part = encodeURIComponent
const session = (id: string) => '/v1/sessions/' + part(id)
const schedulePath = '/v1/host/schedules'
const scheduleURL = (id: string) => schedulePath + '/' + part(id)

export class Controller {
  mainSessionId = ''
  constructor(readonly profile: Profile) {}

  get headers() { return { 'X-User-ID': this.profile.account, 'X-Go-Bot-Controller-Origin': new URL(this.profile.server).origin } }

  async response(path: string, init?: RequestInit, timeout?: number) {
    const headers = new Headers(init?.headers)
    headers.set('X-User-ID', this.profile.account)
    headers.set('X-Go-Bot-Controller-Origin', new URL(this.profile.server).origin)
    const deadline = timeout && !init?.signal ? new AbortController() : undefined
    const timer = deadline ? setTimeout(() => deadline.abort(), timeout) : undefined
    const response = await fetch(this.profile.server + path, {
      ...init, headers, redirect: 'error', signal: init?.signal ?? deadline?.signal,
    }).finally(() => clearTimeout(timer))
    if (!response.ok) {
      const body = await response.json().catch(() => null)
      throw new ApiError(response.status, typeof body?.error === 'string' ? body.error : '', typeof body?.detail === 'string' ? body.detail : undefined)
    }
    return response
  }

  async request<T>(path: string, init?: RequestInit): Promise<T> {
    const response = await this.response(path, init, 20000)
    return response.status === 204 ? undefined as T : await response.json() as T
  }

  async connect(create = false, welcomeLocale: 'en' | 'zh-CN' = 'en') {
    await this.response('/readyz', undefined, 8000)
    if (create) {
      try { await this.request('/v1/accounts', json('POST', { userId: this.profile.account })) }
      catch (error) {
        if (error instanceof ApiError && error.status === 409) throw new Error('账号已存在。请直接连接。')
        throw error
      }
    }
    try {
      const models = await this.modelSettings()
      if (typeof models?.defaultEndpointId !== 'string' || typeof models?.endpoints !== 'object' || !models.endpoints) {
        throw new Error('这个地址未返回 Go Bot 账号数据。')
      }
    } catch (error) {
      if (error instanceof ApiError && error.status === 404) throw new Error('找不到这个账号。请检查账号名，或用下方入口创建。')
      throw error
    }
    const started = await this.response('/v1/host/start', json('POST', { welcomeLocale }), 320000)
    this.mainSessionId = (await started.json()).mainSessionId
    if (!this.mainSessionId) throw new Error('主会话未能准备完成。')
  }

  async preparePlugins() {
    const result = await this.response('/v1/host/plugins/prepare', { method: 'POST' }, 900000)
    return result.json()
  }
  clientIdentity() { return mobileClientIdentity() }
  clients(signal?: AbortSignal) { return this.request<{clients: ClientDevice[]}>('/v1/clients', { signal }) }
  stopHost() { return this.response('/v1/host/stop', { method: 'POST' }, 320000) }
  sessions(cursor = '') {
    return this.request<{ sessions: SessionRow[]; nextCursor?: string }>('/v1/sessions?limit=30' + (cursor ? '&cursor=' + part(cursor) : ''))
  }
  async latestSession(signal: AbortSignal) {
    type Page = { sessions: SessionRow[]; nextCursor?: string }
    let page = await this.request<Page>('/v1/sessions?latest=true', { signal })
    let latest: SessionRow | undefined
    // Older Hosts ignore latest and return the regular UUID-ordered pages.
    for (;;) {
      for (const row of page.sessions) {
        const created = Date.parse(row.createdAt)
        const previous = latest ? Date.parse(latest.createdAt) : -Infinity
        if (!latest || created > previous || created === previous && row.sessionId < latest.sessionId) latest = row
      }
      if (!page.nextCursor) return latest?.sessionId ?? ''
      page = await this.request<Page>('/v1/sessions?limit=100&cursor=' + part(page.nextCursor), { signal })
    }
  }
  createSession(workspaceSessionId?: string) { return this.request<{ sessionId: string }>('/v1/sessions', workspaceSessionId
    ? json('POST', { workspaceSessionId }) : { method: 'POST' }) }
  rename(id: string, title: string) { return this.request<{title: string}>(session(id) + '/title', json('PUT', {title})) }
  archive(id: string, stopActivity = false) { return this.request(session(id) + (stopActivity ? '?stopActivity=true' : ''), { method: 'DELETE' }) }
  transcript(id: string, before: number | null, signal?: AbortSignal) {
    return this.request<TranscriptPage>(session(id) + '/transcript?limit=20' + (before === null ? '' : '&before=' + before), { signal })
  }
  async hostStream(cursors: Record<string, number | null>, signal: AbortSignal, onOpen: () => void, onEvent: (type: string, data: unknown) => void) {
    const response = await this.response('/v1/host/stream?cursors=' + part(JSON.stringify(cursors)),
      { headers: { Accept: 'text/event-stream', [clientIdentityHeader]: encodeClientIdentity(await mobileClientIdentity()) }, signal })
    if (!response.body) throw new Error('服务器没有返回事件流。')
    const reader = response.body.getReader()
    const decoder = new TextDecoder()
    const parser = createParser({
      onEvent: event => {
        if (event.event) onEvent(event.event, JSON.parse(event.data))
      },
    })
    try {
      onOpen()
      for (;;) {
        const { value, done } = await reader.read()
        if (done) break
        parser.feed(decoder.decode(value, { stream: true }))
      }
    } finally {
      await reader.cancel().catch(() => {})
      reader.releaseLock()
    }
  }

  send(id: string, content: string, attachments: LibraryEntry[]) {
    if (!attachments.length) return this.request<{ messageId: string }>(session(id) + '/messages', json('POST', { content }))
    const body = new FormData()
    body.append('content', content)
    for (const attachment of attachments) {
      body.append(attachment.kind === 'image' ? 'images' : 'files', new File(attachment.uri), attachment.name)
    }
    return this.request<{ messageId: string }>(session(id) + '/messages', { method: 'POST', body })
  }
  updateQueue(id: string, messageId: string, steer: boolean) {
    return this.request(session(id) + '/queue/' + part(messageId) + (steer ? '/steer' : ''), { method: steer ? 'POST' : 'DELETE' })
  }
  stopTurn(id: string, turn: number) { return this.request(session(id) + '/turns/' + turn + '/cancel', { method: 'POST' }) }
  answer(id: string, callId: string, answers: Answer[]) {
    return this.request(session(id) + '/questions/' + part(callId) + '/answer', json('POST', { answers }))
  }
  cancelQuestion(id: string, callId: string) {
    return this.request(session(id) + '/questions/' + part(callId) + '/cancel', { method: 'POST' })
  }
  sessionPermission(id: string) { return this.request<SessionPermission>(session(id) + '/permissions') }
  selectPermission(id: string, mode: PermissionMode) { return this.request<SessionPermission>(session(id) + '/permissions', json('POST', { mode })) }
  modelSettings() { return this.request<ModelSettings>('/v1/settings/models') }
  proactiveSettings(signal?: AbortSignal) { return this.request<ProactiveStatus>('/v1/settings/proactive', { signal }) }
  saveProactiveSettings(enabled: boolean) { return this.request<ProactiveStatus>('/v1/settings/proactive', json('PUT', { enabled })) }
  sessionModel(id: string) { return this.request<SessionModel>(session(id) + '/model') }
  selectModel(id: string, choice: ModelSelection) { return this.request<SessionModel>(session(id) + '/model', json('PUT', choice)) }
  saveEndpoint(id: string | undefined, endpoint: Omit<ModelEndpoint, 'hasApiKey'> & { apiKey?: string }) {
    return this.request('/v1/settings/models/endpoints' + (id ? '/' + part(id) : ''), json(id ? 'PATCH' : 'POST', endpoint))
  }
  removeEndpoint(id: string) { return this.request('/v1/settings/models/endpoints/' + part(id), { method: 'DELETE' }) }
  setDefaultEndpoint(endpointId: string) { return this.request('/v1/settings/models/default', json('PUT', { endpointId })) }
  providers(page: ProviderPage) { return this.request<ProviderSettings>('/v1/settings/' + page) }
  saveProvider(page: ProviderPage, provider: { provider: string; apiKey?: string; model?: string; activate: boolean }) {
    return this.request<ProviderSettings>('/v1/settings/' + page, json('PUT', provider))
  }
  removeProvider(page: ProviderPage, provider: string) {
    return this.request<ProviderSettings>('/v1/settings/' + page + '?provider=' + part(provider), { method: 'DELETE' })
  }
  hostFiles(path: string, offset = 0, signal?: AbortSignal) {
    return this.request<HostItems>('/v1/host/files?path=' + part(path) + '&offset=' + offset, { signal })
  }
  schedules() { return this.request<{ tasks: Schedule[] }>(schedulePath) }
  createSchedule(input: ScheduleCreate) { return this.request<Schedule>(schedulePath, json('POST', input)) }
  editSchedule(id: string, expectedUpdatedAt: string | null, patch: SchedulePatch) {
    return this.request<Schedule>(scheduleURL(id), json('PATCH', { action: 'update', expectedUpdatedAt, patch }))
  }
  deleteSchedule(id: string, expectedUpdatedAt: string | null) {
    return this.request(scheduleURL(id), json('PATCH', { action: 'delete', expectedUpdatedAt }))
  }
  deleteEndedSchedules() { return this.request<{ deleted: number }>(schedulePath + '/ended', { method: 'DELETE' }) }
  runSchedule(id: string) { return this.request<Schedule>(scheduleURL(id) + '/run', { method: 'POST' }) }

  fileURL(id: string, ref: FileRef, name?: string) {
    return this.profile.server + session(id) + (ref.source === 'presented'
      ? '/files/content?path=' + encodeURIComponent(ref.path)
      : '/attachments/' + part(ref.attachmentId) + (name ? '?name=' + part(name) : ''))
  }
}
