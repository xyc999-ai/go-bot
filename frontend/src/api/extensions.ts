import { request } from './controller'
export type PackageSource = { type: 'registry' | 'git' | 'directory' | 'tarball' | 'bundled'; spec: string; registry?: string; authRef?: string; username?: string }
export type PackageInfo = { name: string; version: string; description: string; license: string; source: PackageSource; api: string; title?: string; configSchema?: Record<string, unknown>; present: boolean; availableVersion?: string }
export type PluginInstance = { id: string; package: string; state: string; error?: string }
export type Localized = { en: string; 'zh-CN': string; [language: string]: string | undefined }
export type PluginTutorial = { language: string; title: string; video: string; poster?: string }
export type CatalogEntry = { pluginId: string; package: string; name: string; description: Localized; version: string; icon: string; maintainer: string; license: string; tutorials?: PluginTutorial[] }
export type PluginSettings = { packages: Record<string, { catalogId: string; version: string; enabled: boolean }> }
export type PluginView = { catalog: CatalogEntry[]; settings: PluginSettings; status: { runtime: string; packages: PackageInfo[]; instances: PluginInstance[]; error?: string } }
export const preparePlugins = (user: string) => request<PluginView>('/v1/host/plugins/prepare', { method: 'POST' }, user)
const plugins = '/v1/settings/plugins'
function body(method: string, value?: unknown): RequestInit { return { method, ...(value === undefined ? {} : { headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(value) }) } }
export const getPlugins = (user: string, signal?: AbortSignal, refresh = false) => request<PluginView>(plugins + (refresh ? "?refresh=true" : ""), { signal }, user)
export const installPackage = (user: string, pluginId: string) => request<PluginView>(plugins + '/packages', body('POST', { pluginId }), user)
export const updatePackage = (user: string, name: string, pluginId: string) => request<PluginView>(plugins + '/packages/' + encodeURIComponent(name) + '/update', body('POST', { pluginId }), user)
export const removePackage = (user: string, name: string) => request<PluginView>(plugins + '/packages/' + encodeURIComponent(name), body('DELETE'), user)
export const setPluginEnabled = (user: string, name: string, enabled: boolean) => request<PluginView>(plugins + '/packages/' + encodeURIComponent(name), body('PUT', { enabled }), user)
export const restartPlugins = (user: string) => request<PluginView>('/v1/host/plugins/runtime/restart', body('POST'), user)

export type AuthorizationEntry = { key: string; label: string; methods: { id: string; label: string }[]; inFlight: boolean; configured: boolean; canDisconnect?: boolean; accountLabel?: string; attemptId?: string; attemptStatus?: AuthorizationAttempt['status'] }
export type AuthorizationPrompt = { id: string; message: string } & ({ kind: 'text' | 'secret'; placeholder?: string; webInput?: 'json-file' } | { kind: 'select'; options: { id: string; label: string; description?: string }[] })
export type AuthorizationAttempt = { id: string; key: string; label: string; method: string; status: 'running' | 'authorized' | 'cancelled' | 'failed'; notices: { message: string; url?: string; code?: string }[]; prompts: AuthorizationPrompt[]; error?: string }
export type AuthorizationAPI = {
  list(signal?: AbortSignal): Promise<AuthorizationEntry[]>
  get(id: string, signal?: AbortSignal): Promise<AuthorizationAttempt>
  start(key: string, method: string): Promise<AuthorizationAttempt>
  answer(id: string, promptId: string, answer?: string, decline?: boolean): Promise<AuthorizationAttempt>
  cancel(id: string): Promise<AuthorizationAttempt>
  cancelKey(key: string): Promise<unknown>
  disconnect(key: string): Promise<unknown>
}
const authorization = '/v1/host/extensions/authorization'
export const authorizationAPI = (user: string): AuthorizationAPI => ({
  list: signal => request(authorization, { signal }, user),
  get: (id, signal) => request(authorization + '/' + encodeURIComponent(id), { signal }, user),
  start: (key, method) => request(authorization, body('POST', { key, method }), user),
  answer: (id, promptId, answer, decline) => request(authorization + '/' + encodeURIComponent(id) + '/answer', body('POST', { promptId, answer, decline }), user),
  cancel: id => request(authorization + '/' + encodeURIComponent(id), { ...body('DELETE'), keepalive: true }, user),
  cancelKey: key => request(authorization, body('DELETE', { key }), user),
  disconnect: key => request(authorization + '/disconnect', body('POST', {key}), user),
})

export type MCPOAuthAttempt = { id?: string; name: string; state: string; phase?: string; url?: string; expiresAt?: number; error?: string }
export type MCPServer = { name: string; source: string; owner?: string; serverName?: string; state: string; error?: string; oauthEnabled: boolean; authorization?: MCPOAuthAttempt }
export type MCPAuthorizationAPI = {
 list(signal?: AbortSignal): Promise<{ status: { servers: MCPServer[]; error?: string } }>
 start(name: string): Promise<MCPOAuthAttempt>
 cancel(name: string, attemptId: string): Promise<unknown>
 signOut(name: string): Promise<unknown>
}
const mcp = '/v1/host/mcp'
export const mcpAuthorizationAPI = (user: string): MCPAuthorizationAPI => ({
 list: signal => request(mcp, { signal }, user),
 start: name => request(mcp + '/servers/' + encodeURIComponent(name) + '/authorize', body('POST', { origin: window.location.origin }), user),
 cancel: (name, attemptId) => request(mcp + '/servers/' + encodeURIComponent(name) + '/authorization?id=' + encodeURIComponent(attemptId), body('DELETE'), user),
 signOut: name => request(mcp + '/servers/' + encodeURIComponent(name) + '/grant', body('DELETE'), user),
})
