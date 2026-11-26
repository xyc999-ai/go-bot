import { useCallback, useEffect, useRef, useState } from 'react'
import type { MCPAuthorizationAPI, MCPServer } from '../api/extensions'
import type { TranscriptCache } from '../chat/transcriptCache'
import { connectionFailure, mcpAuthorizationRunning, pluginOAuthConnections } from './connections'

// A view adapter only: OAuth discovery, callback state, PKCE, token exchange
// and persistence remain in the existing Go MCP/authorization services.
export function useMCPAuthorization(api: MCPAuthorizationAPI, cache: TranscriptCache, instanceIds: string[]) {
 const [all, setAll] = useState<MCPServer[]>([]), [version, setVersion] = useState(0)
 const [opened, setOpened] = useState<string>()
 const [busy, setBusy] = useState(false), [error, setError] = useState('')
 const alive = useRef(true), locked = useRef(false)
 const reload = useCallback(() => setVersion(value => value + 1), [])
 useEffect(() => { alive.current = true; return () => { alive.current = false } }, [api])
 useEffect(() => cache.subscribe('@extensions', reload), [cache, reload])
 useEffect(() => {
  const call = new AbortController()
  void api.list(call.signal).then(view => {
   if (call.signal.aborted) return
   setAll(view.status.servers); setError(view.status.error ? 'Could not load service connections. Refresh to try again.' : '')
  }, () => { if (!call.signal.aborted) setError('Could not load service connections. Refresh to try again.') })
  return () => call.abort()
 }, [api, version])
 const servers = pluginOAuthConnections(all, instanceIds)
 const selected = opened ? servers.find(server => server.name === opened) : undefined
 const attempt = selected?.authorization
 async function run(work: () => Promise<void>) {
  if (locked.current) return
  locked.current = true; setBusy(true); setError('')
  try { await work() }
  catch (error) { if (alive.current) setError(connectionFailure(error)) }
  finally { locked.current = false; if (alive.current) { setBusy(false); reload() } }
 }
 const open = (server: MCPServer) => run(async () => {
  const current = server.authorization
  if (!mcpAuthorizationRunning(current)) {
   await api.start(server.name)
   const view = await api.list()
   if (alive.current) setAll(view.status.servers)
  }
  if (alive.current) setOpened(server.name)
 })
 function resume(server: MCPServer) {
  if (server.authorization?.id) { setOpened(server.name); setError(''); reload() }
 }
 const cancel = () => run(async () => {
  if (opened && attempt?.id) await api.cancel(opened, attempt.id)
 })
 const signOut = (server: MCPServer) => run(async () => {
  await api.signOut(server.name)
  if (alive.current && opened === server.name) setOpened(undefined)
 })
 const close = () => { if (!locked.current) { setOpened(undefined); setError(''); reload() } }
 return { servers, selected, attempt, busy, error, reload, open, resume, cancel, signOut, close }
}
