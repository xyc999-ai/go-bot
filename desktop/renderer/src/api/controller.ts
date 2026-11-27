import { clientIdentityHeader, encodeClientIdentity } from '../clients/identity'
import { notifyHostEvent, controllerOrigin, desktopClientIdentity } from '../desktop/bridge'
import type { ModelProtocol } from "../settings/modelProtocol"
import type { PermissionMode, SessionPermission } from '../chat/permissions'
import { messageRequest } from '../chat/attachments'
import { EventSourceParserStream } from 'eventsource-parser/stream'
import type { Answer, FileRef, PendingQuestion, QueueItem } from '../chat/transcript'
import type { ProactiveStatus } from '../settings/proactiveState'

export type SessionRow = {
  isMain?: boolean
  sessionId: string
  createdAt: string
  title?: string
  cwd?: string
}

export type SessionEvent = {
  seq: number
  time?: number
  type: string
  data: unknown
}

export type TranscriptPage = {
  events: SessionEvent[]
  before: number | null
  throughSeq: number | null
  queue: QueueItem[]
  questions: PendingQuestion[] | null
  baseline?: { revision: number; activeAttempt?: unknown }
  permission?: SessionPermission
}

export class APIError extends Error {
  status: number
  details?: unknown

  constructor(status: number, message: string, details?: unknown) {
    super(message)
    this.status = status
    this.details = details
  }
}

export function settingsApplyFailed(error: unknown) {
  return error instanceof APIError && error.message === 'settings_saved_host_apply_failed'
}

async function requireSuccess(response: Response) {
  if (response.ok) return response
  const failure = await response.json().catch(() => null)
  throw new APIError(response.status, typeof failure?.error === 'string' ? failure.error : '', failure)
}

function controllerHeaders(input?: HeadersInit, userId?: string) {
  const headers = new Headers(input)
  if (userId) headers.set('X-User-ID', userId)
  if (controllerOrigin()) headers.set('X-Go-Bot-Controller-Origin', controllerOrigin())
  return headers
}

export async function request<T>(path: string, options?: RequestInit, userId?: string): Promise<T> {
  const headers = controllerHeaders(options?.headers, userId)
  const response = await requireSuccess(await fetch(path, { ...options, headers }))
  if (response.status === 204) return undefined as T
  return response.json() as Promise<T>
}

export type ModelSelection = { endpointId: string; modelId: string }
export type ModelEndpoint = { protocol?: ModelProtocol; name: string; baseUrl: string; hasApiKey: boolean; models: string[] }
export type ModelSettings = { defaultEndpointId: string; endpoints: Record<string, ModelEndpoint> }
export type SessionModel = { selection: ModelSelection | null; status: 'ready' | 'unconfigured' | 'unavailable' }

function write(method: string, body: unknown): RequestInit {
  return { method, headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) }
}

export function getProactiveSettings(userId: string, signal?: AbortSignal) {
  return request<ProactiveStatus>('/v1/settings/proactive', { signal }, userId)
}

export function saveProactiveSettings(userId: string, enabled: boolean) {
  return request<ProactiveStatus>('/v1/settings/proactive', write('PUT', { enabled }), userId)
}

export function registerAccount(userId: string) {
  return request<{ userId: string }>('/v1/accounts', write('POST', { userId }))
}

const modelSettingsPath = '/v1/settings/models'

export function getModelSettings(userId: string, signal?: AbortSignal) {
  return request<ModelSettings>(modelSettingsPath, { signal }, userId)
}

export type ProviderPage = 'web-search' | 'image-generation'
export type Provider = { id: string; name: string; defaultModel?: string }
export type ProviderSettings = {
  provider: string
  defaultProvider: string
  providers: Provider[]
  entries: Record<string, { hasApiKey: boolean; model?: string }>
}

export function getProviderSettings(userId: string, page: ProviderPage, signal?: AbortSignal) {
  return request<ProviderSettings>('/v1/settings/' + page, { signal }, userId)
}

export function saveProviderSettings(userId: string, page: ProviderPage, settings: { provider: string; model?: string; apiKey?: string; activate?: boolean }) {
  return request<ProviderSettings>('/v1/settings/' + page, write('PUT', settings), userId)
}

export function removeProviderKey(userId: string, page: ProviderPage, provider: string) {
  return request<ProviderSettings>('/v1/settings/' + page + '?' + new URLSearchParams({ provider }), { method: 'DELETE' }, userId)
}

export function saveEndpoint(userId: string, endpointId: string | undefined, endpoint: Omit<ModelEndpoint, 'hasApiKey'> & { apiKey?: string }) {
  const path = modelSettingsPath + '/endpoints' + (endpointId ? '/' + encodeURIComponent(endpointId) : '')
  return request<{ endpointId: string; status: string }>(path, write(endpointId ? 'PATCH' : 'POST', endpoint), userId)
}

export function deleteEndpoint(userId: string, endpointId: string) {
  return request(modelSettingsPath + '/endpoints/' + encodeURIComponent(endpointId), { method: 'DELETE' }, userId)
}

export function saveDefaultEndpoint(userId: string, endpointId: string) {
  return request(modelSettingsPath + '/default', write('PUT', { endpointId }), userId)
}

export function getSessionModel(userId: string, id: string, signal?: AbortSignal) {
  return request<SessionModel>(sessionPath(id) + '/model', { signal }, userId)
}

export function saveSessionModel(userId: string, id: string, model: ModelSelection) {
  return request<SessionModel>(sessionPath(id) + '/model', write('PUT', model), userId)
}

export function openAccount(userId: string, welcomeLocale: string = 'en') {
  return request<{ mainSessionId: string }>('/v1/host/start', write('POST', { welcomeLocale }), userId)
}

export function stopHost(userId: string) {
  return request<void>('/v1/host/stop', { method: 'POST' }, userId)
}

export type HostFile = { name: string; path: string; isDir: boolean; size: number }
export type HostFilesPage = {
  rootPath: string
  path: string
  files: HostFile[]
  nextOffset?: number
}

export async function listHostFiles(userId: string, path: string, offset: number, signal: AbortSignal) {
  const query = new URLSearchParams({ path, offset: String(offset) })
  const response = await requireSuccess(await fetch('/v1/host/files?' + query, {
    headers: { 'X-User-ID': userId }, cache: 'no-store', signal,
  }))
  const page: HostFilesPage = await response.json()
  return {
    ...page,
    storage: response.headers.get('X-Harness-Storage'),
    storageSource: response.headers.get('X-Harness-Storage-Source'),
  }
}

function sessionPath(id: string) {
  return '/v1/sessions/' + encodeURIComponent(id)
}

export function listSessions(userId: string, cursor = '', signal?: AbortSignal) {
  const query = new URLSearchParams({ limit: '30' })
  if (cursor) query.set('cursor', cursor)
  return request<{ sessions: SessionRow[]; nextCursor?: string }>('/v1/sessions?' + query, { signal }, userId)
}

export function createSession(userId: string) {
  return request<{ sessionId: string }>('/v1/sessions', { method: 'POST' }, userId)
}

export function archiveSession(userId: string, id: string, stopActivity = false) {
  return request<void>(sessionPath(id) + (stopActivity ? '?stopActivity=true' : ''), { method: 'DELETE' }, userId)
}

export function sendMessage(userId: string, id: string, content: string, files: File[] = []) {
  return request<{ messageId: string }>(sessionPath(id) + '/messages', messageRequest(content, files), userId)
}

export function readTranscript(userId: string, id: string, before: number | null, signal?: AbortSignal) {
  const query = new URLSearchParams({ limit: '20' })
  if (before !== null) query.set('before', String(before))
  return request<TranscriptPage>(sessionPath(id) + '/transcript?' + query, { signal }, userId)
}

export function updateQueue(userId: string, id: string, messageId: string, action: 'steer' | 'delete') {
  const path = sessionPath(id) + '/queue/' + encodeURIComponent(messageId)
  return request<void>(action === 'steer' ? path + '/steer' : path, { method: action === 'steer' ? 'POST' : 'DELETE' }, userId)
}

export function getSessionPermission(userId: string, id: string, signal?: AbortSignal) {
  return request<SessionPermission>(sessionPath(id) + '/permissions', { signal }, userId)
}

export function saveSessionPermission(userId: string, id: string, mode: PermissionMode) {
  return request<SessionPermission>(sessionPath(id) + '/permissions', write('POST', { mode }), userId)
}

export function answerQuestion(userId: string, id: string, callId: string, answers: Answer[]) {
  return request<void>(sessionPath(id) + '/questions/' + encodeURIComponent(callId) + '/answer', write('POST', { answers }), userId)
}

export function stopTurn(userId: string, id: string, turn: number) {
  return request(sessionPath(id) + '/turns/' + turn + '/cancel', { method: 'POST' }, userId)
}

export async function fetchChatFile(userId: string, id: string, file: FileRef, name?: string, signal?: AbortSignal) {
  const path = file.source === 'presented'
    ? '/files/content?path=' + encodeURIComponent(file.path)
    : '/attachments/' + encodeURIComponent(file.attachmentId) + (name ? '?' + new URLSearchParams({ name }) : '')
  const response = await requireSuccess(await fetch(sessionPath(id) + path, {
    headers: { 'X-User-ID': userId }, signal,
  }))
  return response.blob()
}

export async function readHostStream(
  userId: string,
  cursors: Record<string, number | null>,
  signal: AbortSignal,
  onOpen: () => void,
  onEvent: (event: string, data: unknown) => void,
) {
  const query = new URLSearchParams({ cursors: JSON.stringify(cursors) })
  const response = await fetch('/v1/host/stream?' + query, {
    headers: controllerHeaders({ Accept: 'text/event-stream', [clientIdentityHeader]: encodeClientIdentity(desktopClientIdentity()) }, userId),
    signal,
  })
  await requireSuccess(response)
  if (!response.body) throw new Error('The event stream has no body')
  const reader = response.body
    .pipeThrough(new TextDecoderStream())
    .pipeThrough(new EventSourceParserStream())
    .getReader()
  try {
    onOpen()
    for (;;) {
      const { done, value } = await reader.read()
      if (done) return
      if (!value.event) continue
      const data = JSON.parse(value.data)
      onEvent(value.event, data)
      notifyHostEvent(value.event, data)
    }
  } finally {
    await reader.cancel().catch(() => {})
    reader.releaseLock()
  }
}

export function renameSession(userId: string, id: string, title: string) {
  return request<{ title: string }>(`/v1/sessions/${encodeURIComponent(id)}/title`, write('PUT', { title }), userId)
}

// Streaming URL for native media elements; account routing is limited to this read endpoint.
export function chatVideoURL(userId: string, id: string, path: string) {
  return sessionPath(id) + '/files/content?' + new URLSearchParams({ path, userId, inline: '1' })
}
