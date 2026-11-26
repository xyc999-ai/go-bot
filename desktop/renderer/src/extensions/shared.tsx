import { useCallback, useEffect, useRef, useState, useSyncExternalStore, type ReactNode } from 'react'
import { AlertCircle, LoaderCircle, RefreshCw } from 'lucide-react'
import { APIError, settingsApplyFailed } from '../api/controller'
import { useLocale } from '../locale'
import type { TranscriptCache } from '../chat/transcriptCache'
import './extensions.css'
import { extensionOperations } from './operations'
import { useExtensionConfirm } from './Dialogs'

const states: Record<string, string> = { installed: 'Installed', active: 'Active', running: 'Running', connected: 'Connected', ready: 'Ready', installing: 'Installing', 'installation-failed': 'Installation failed', loading: 'Starting', connecting: 'Connecting', pending: 'Pending', failed: 'Failed', error: 'Failed', unavailable: 'Unavailable', stopped: 'Stopped', disabled: 'Disabled', stopping: 'Stopping', disconnected: 'Disconnected', 'waiting-credentials': 'Needs credentials', 'waiting-authorization': 'Authorization needed' }
export function ExtensionState({ state }: { state: string }) {
  const { t } = useLocale()
  const tone = ['active', 'running', 'connected', 'ready'].includes(state) ? 'good' : ['failed', 'error', 'installation-failed'].includes(state) ? 'bad' : 'quiet'
  return <span className={'extension-state extension-state-' + tone}>{['installing', 'loading'].includes(state) ? <LoaderCircle size={12} className="extension-state-spinner" aria-hidden="true" /> : <i aria-hidden="true" />}{t(states[state] ?? state)}</span>
}

export function extensionFailure(error: unknown) {
  if (settingsApplyFailed(error)) return 'Settings were saved, but the Host could not apply them. Refresh to check the actual state.'
  if (error instanceof APIError && error.message === 'host_not_running') return 'Start your Host before managing extensions.'
  if (error instanceof APIError && error.message === 'oauth_requires_https_or_loopback_http') return 'OAuth requires HTTPS or localhost HTTP. Open the Controller through one of these addresses.'
  if (error instanceof APIError && [400, 422].includes(error.status) && /\s/.test(error.message)) return error.message.slice(0, 1024)
  return 'Could not complete this operation. Refresh the actual state before trying again.'
}

export function useManagedView<T>(user: string, load: (user: string, signal?: AbortSignal, refresh?: boolean) => Promise<T>, cache: TranscriptCache) {
  const [view, setView] = useState<T>()
  const operations = extensionOperations(cache)
  const { busy, error, notice, revision } = useSyncExternalStore(operations.subscribe, operations.snapshot)
  const [loading, setLoading] = useState(true)
  const [version, setVersion] = useState(0)
  const force = useRef(false)
  const reload = useCallback(() => setVersion(value => value + 1), [])
  const refresh = useCallback(() => { force.current = true; reload() }, [reload])
  useEffect(() => cache.subscribe('@extensions', reload), [cache, reload])
  useEffect(() => {
    const request = new AbortController()
    setLoading(true)
    const requestedRefresh = force.current; force.current = false
    void load(user, request.signal, requestedRefresh).then(value => { if (!request.signal.aborted) { setView(value); if (operations.snapshot().error === 'Could not load extensions. Try again.') operations.setError('') } }, () => {
      if (!request.signal.aborted) operations.setError('Could not load extensions. Try again.')
    }).finally(() => { if (!request.signal.aborted) setLoading(false) })
    return () => request.abort()
  }, [user, load, version, revision, operations])
  useEffect(() => { window.addEventListener('focus', reload); return () => window.removeEventListener('focus', reload) }, [reload])
  const run = (label: string, action: () => Promise<unknown>, success = 'Configuration saved. Review the runtime state below.') =>
    operations.run(label, action, extensionFailure, success)
  return { view, error, notice, busy, loading, reload, refresh, run, setError: operations.setError, setNotice: operations.setNotice }
}

export function useUnsavedChanges(dirty: boolean) {
  const confirm = useExtensionConfirm()
  useEffect(() => {
    if (!dirty) return
    const leave = (event: Event) => {
      if (event.defaultPrevented) return
      event.preventDefault()
      const resume = (event as CustomEvent<{ resume: () => void }>).detail?.resume
      if (resume) void confirm('Discard unsaved changes?').then(accepted => { if (accepted) resume() })
    }
    const unload = (event: BeforeUnloadEvent) => { event.preventDefault(); event.returnValue = '' }
    window.addEventListener('go-bot:settings-navigation', leave)
    window.addEventListener('beforeunload', unload)
    return () => { window.removeEventListener('go-bot:settings-navigation', leave); window.removeEventListener('beforeunload', unload) }
  }, [dirty, confirm])
}

export function useReturnFocus() {
  const [opener] = useState(() => document.activeElement)
  useEffect(() => () => { requestAnimationFrame(() => { if (opener instanceof HTMLElement && opener.isConnected) opener.focus() }) }, [opener])
}

export function ExtensionFeedback({ error, notice }: { error: string; notice: string }) {
  const { t } = useLocale()
  return <>{error && <p className="extension-feedback extension-error" role="alert"><AlertCircle size={17} />{t(error)}</p>}
    {notice && <p className="extension-feedback" role="status">{t(notice)}</p>}</>
}
export function ExtensionHeader({ title, description, busy, onRefresh, children }: { title: string; description: string; busy: boolean; onRefresh: () => void; children?: ReactNode }) {
  const { t } = useLocale()
  return <header className="extension-header"><div>{title && <h2 className="settings-section-title">{t(title)}</h2>}<p className="model-muted">{t(description)}</p></div>
    <div className="extension-header-actions"><button type="button" className="extension-icon" disabled={busy} onClick={onRefresh} aria-label={t('Refresh extensions')}><RefreshCw size={17} /></button>{children}</div></header>
}
export function ExtensionLoading() { return <div className="extension-skeleton" aria-busy="true"><span /><span /><span /></div> }

export function parseObject(value: string): Record<string, unknown> {
  const parsed: unknown = JSON.parse(value)
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) throw new Error('Enter a JSON object.')
  return parsed as Record<string, unknown>
}
