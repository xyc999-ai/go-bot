import { useCallback, useEffect, useRef, useState } from 'react'
import type { AuthorizationAPI, AuthorizationAttempt, AuthorizationEntry } from '../api/extensions'
import type { TranscriptCache } from '../chat/transcriptCache'

// Both self-owned clients consume the same upstream interaction vocabulary.
// No credential, answer or attempt handle is persisted in client storage.
export function useAuthorization(api: AuthorizationAPI, cache: TranscriptCache) {
  const [entries, setEntries] = useState<AuthorizationEntry[]>([])
  const [attempt, setAttempt] = useState<AuthorizationAttempt>()
  const [error, setError] = useState(''), [busy, setBusy] = useState(false)
  const [version, setVersion] = useState(0)
  const alive = useRef(true), locked = useRef(false), current = useRef<AuthorizationAttempt | undefined>(undefined)
  const reload = useCallback(() => setVersion(value => value + 1), [])
  const adopt = useCallback((value?: AuthorizationAttempt) => { current.current = value; setAttempt(value) }, [])
  useEffect(() => { alive.current = true; return () => { alive.current = false } }, [api])
  useEffect(() => cache.subscribe('@extensions', reload), [cache, reload])
  useEffect(() => {
    const request = new AbortController()
    void api.list(request.signal).then(next => { if (!request.signal.aborted) { setEntries(next); setError(previous => previous === 'Could not load account authorizations. Refresh to try again.' ? '' : previous) } }, () => {
      if (!request.signal.aborted) setError('Could not load account authorizations. Refresh to try again.')
    })
    return () => request.abort()
  }, [api, version])
  const id = attempt?.id
  useEffect(() => {
    if (!id) return
    const request = new AbortController()
    void api.get(id, request.signal).then(next => { if (!request.signal.aborted) { adopt(next); setError(previous => previous === 'Authorization is unavailable. Close this panel and start again if the Host restarted.' ? '' : previous) } }, () => {
      if (!request.signal.aborted) setError('Authorization is unavailable. Close this panel and start again if the Host restarted.')
    })
    return () => request.abort()
  }, [api, id, version, adopt])
  async function run(work: () => Promise<AuthorizationAttempt>, message: string, closeMissing = false) {
    if (locked.current) return false
    locked.current = true; setBusy(true); setError('')
    try {
      const next = await work()
      if (alive.current) { adopt(next); reload() }
      return true

    } catch (error) {
      if (alive.current) {
        if (closeMissing && typeof error === 'object' && error && 'status' in error && [404, 409, 503].includes(Number(error.status))) { adopt(undefined); setError('') }
        else setError(message)
        reload()
      }
      return false
    }
    finally { locked.current = false; if (alive.current) setBusy(false) }
  }
  const start = (key: string, method: string) => run(() => api.start(key, method), 'Could not start authorization. Refresh the account state and try again.')
  const answer = (promptId: string, value?: string, decline?: boolean) => {
    const attempt = current.current
    return attempt && run(() => api.answer(attempt.id, promptId, value, decline), 'Could not submit this answer. Check the current question before trying again.')
  }
  async function cancelKey(key: string) {
    if (locked.current) return
    locked.current = true; setBusy(true); setError('')
    try { await api.cancelKey(key) }
    catch { if (alive.current) setError('Could not cancel authorization. Refresh to check the current state.') }
    finally { locked.current = false; if (alive.current) { setBusy(false); reload() } }
  }
  async function disconnect(key: string) {
    if (locked.current) return
    locked.current = true; setBusy(true); setError('')
    try { await api.disconnect(key) }
    catch { if (alive.current) setError('Could not disconnect this account. Refresh to check the current state.') }
    finally { locked.current = false; if (alive.current) { setBusy(false); reload() } }
  }
  function close() {
    if (locked.current) return
    adopt(undefined); setError(''); reload()
  }
  const open = (id: string) => run(() => api.get(id), 'Authorization is unavailable. Close this panel and start again if the Host restarted.')
  const cancel = () => {
    const value = current.current
    return value && run(() => api.cancel(value.id), 'Could not cancel authorization. Refresh to check the current state.', true)
  }
  const restart = () => {
    const value = current.current
    return value && run(async () => { await api.cancelKey(value.key); return api.start(value.key, value.method) }, 'Could not start authorization. Refresh the account state and try again.')
  }
  return { entries, attempt, error, busy, reload, start, answer, close, open, cancel, cancelKey, disconnect, restart }

}

export function authorizationURL(value?: string) {
  try { const url = new URL(value ?? ''); return ['https:', 'http:'].includes(url.protocol) && !url.username && !url.password ? url.href : undefined }
  catch { return undefined }
}
export const authorizationLabel = { running: 'Authorization in progress', authorized: 'Authorization complete', cancelled: 'Authorization cancelled', failed: 'Authorization failed' }
