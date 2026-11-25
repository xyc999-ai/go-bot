import type { Controller } from '../api'
import type { PluginView, AuthorizationAPI, AuthorizationEntry, AuthorizationAttempt, MCPAuthorizationAPI, MCPOAuthAttempt, MCPServer } from '../../../frontend/src/api/extensions'
export type { PackageInfo, PackageSource, PluginInstance, PluginSettings, PluginView, CatalogEntry, Localized } from '../../../frontend/src/api/extensions'
const pluginPath = '/v1/settings/plugins'
const part = encodeURIComponent
async function change<T>(api: Controller, path: string, method: string, value?: unknown): Promise<T> {
  const response = await api.response(path, { method, ...(value === undefined ? {} : { headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(value) }) }, 15 * 60_000)
  return response.status === 204 ? undefined as T : response.json() as Promise<T>
}
export const loadPlugins = (api: Controller, signal?: AbortSignal, refresh = false) => api.request<PluginView>(pluginPath + (refresh ? "?refresh=true" : ""), { signal })
export const installPackage = (api: Controller, pluginId: string) => change<PluginView>(api, pluginPath + '/packages', 'POST', { pluginId })
export const updatePackage = (api: Controller, name: string, pluginId: string) => change<PluginView>(api, pluginPath + '/packages/' + part(name) + '/update', 'POST', { pluginId })
export const removePackage = (api: Controller, name: string) => change<PluginView>(api, pluginPath + '/packages/' + part(name), 'DELETE')
export const setPluginEnabled = (api: Controller, name: string, enabled: boolean) => change<PluginView>(api, pluginPath + '/packages/' + part(name), 'PUT', { enabled })
export const restartPlugins = (api: Controller) => change<PluginView>(api, '/v1/host/plugins/runtime/restart', 'POST')

const authorization = '/v1/host/extensions/authorization'
export const authorizationAPI = (api: Controller): AuthorizationAPI => ({
  list: signal => api.request<AuthorizationEntry[]>(authorization, { signal }),
  get: (id, signal) => api.request<AuthorizationAttempt>(authorization + '/' + part(id), { signal }),
  start: (key, method) => change<AuthorizationAttempt>(api, authorization, 'POST', { key, method }),
  answer: (id, promptId, answer, decline) => change<AuthorizationAttempt>(api, authorization + '/' + part(id) + '/answer', 'POST', { promptId, answer, decline }),
  cancel: id => change<AuthorizationAttempt>(api, authorization + '/' + part(id), 'DELETE'),
  cancelKey: key => change(api, authorization, 'DELETE', { key }),
  disconnect: key => change(api, authorization + '/disconnect', 'POST', {key}),
})

const mcp = '/v1/host/mcp'
export const mcpAuthorizationAPI = (api: Controller): MCPAuthorizationAPI => ({
 list: signal => api.request<{ status: { servers: MCPServer[]; error?: string } }>(mcp, { signal }),
 start: name => change<MCPOAuthAttempt>(api, mcp + '/servers/' + part(name) + '/authorize', 'POST', { origin: new URL(api.profile.server).origin }),
 cancel: (name, attemptId) => change(api, mcp + '/servers/' + part(name) + '/authorization?id=' + part(attemptId), 'DELETE'),
 signOut: name => change(api, mcp + '/servers/' + part(name) + '/grant', 'DELETE'),
})
