import type { AuthorizationEntry, MCPServer, MCPOAuthAttempt } from '../api/extensions'

export function authorizationEntryLabels(entry: AuthorizationEntry) {
 const apiKey = entry.methods.length === 1 && entry.methods[0].id === 'api-key'
 return {
  status: entry.inFlight ? 'Authorization in progress' : entry.configured ? apiKey ? 'Configured' : 'Connected' : 'Not configured',
  start: entry.inFlight ? 'Continue authorization' : entry.configured ? apiKey ? 'Update API Key' : 'Authorize again' : entry.methods.length === 1 ? entry.methods[0].label : 'Authorize',
  disconnect: apiKey ? 'Remove API Key' : 'Disconnect account',
 }
}

export function pluginOAuthConnections(servers: MCPServer[], instances: string[]): MCPServer[] {
 return servers.filter(server => server.source === 'plugin' && server.oauthEnabled && instances.some(id => server.name.startsWith(id + '__')))
}
export function mcpAuthorizationRunning(attempt?: MCPOAuthAttempt): boolean {
 return !!attempt && ['starting', 'waiting-browser', 'exchanging', 'committing'].includes(attempt.phase ?? '')
}
export function mcpAuthorizationLabel(attempt: MCPOAuthAttempt): string {
 if (mcpAuthorizationRunning(attempt)) return 'Authorization in progress'
 if (attempt.phase === 'authorized') return 'Authorization complete'
 if (attempt.phase === 'cancelled') return 'Authorization cancelled'
 return 'Authorization failed'
}
export function connectionLabel(server: MCPServer): string {
 if (mcpAuthorizationRunning(server.authorization)) return 'Authorization in progress'
 if (server.state === 'connected') return 'Connected'
 if (server.authorization?.phase === 'authorized') return 'Credential saved'
 if (server.state === 'failed') return 'Connection needs attention'
 return 'Authorization needed'
}
export function connectionTitle(server: MCPServer): string {
 return server.serverName || server.name.slice(server.name.indexOf('__') + 2)
}
export function connectionFailure(error: unknown): string {
 const code = error && typeof error === 'object' ? ('code' in error ? error.code : 'message' in error ? error.message : undefined) : undefined
 if (code === 'oauth_requires_https_or_loopback_http') return 'OAuth requires HTTPS or localhost HTTP. Open the Controller through one of these addresses.'
 return 'Could not complete service authorization. Refresh to check the current state.'
}
