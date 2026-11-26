import { useEffect, useId, useRef, useState } from 'react'
import { useLocale } from '../locale'
import { Check, ChevronDown, Shield, ShieldAlert } from 'lucide-react'
import { answerQuestion, getSessionPermission, saveSessionPermission } from '../api/controller'
import { permissionLabel, permissionOptions, type PermissionMode, type SessionPermission } from './permissions'
import type { PendingQuestion } from './transcript'

export function PermissionPicker({ userId, sessionId, streamState, disabled, onSaving }: {
  userId: string; sessionId: string; streamState?: SessionPermission; disabled: boolean; onSaving: (value: boolean) => void
}) {
  const { t } = useLocale()
  const [mode, setMode] = useState<PermissionMode>()
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const root = useRef<HTMLDivElement>(null)
  const menuID = useId()

  useEffect(() => {
    const abort = new AbortController()
    setMode(undefined)
    setError('')
    getSessionPermission(userId, sessionId, abort.signal).then(state => setMode(state.mode)).catch(() => {
      if (!abort.signal.aborted) setError('Could not read chat permission.')
    })
    return () => abort.abort()
  }, [sessionId, userId])
  useEffect(() => { if (streamState) setMode(streamState.mode) }, [streamState])
  useEffect(() => {
    if (!open) return
    function dismiss(event: PointerEvent) {
      if (event.target instanceof Node && !root.current?.contains(event.target)) setOpen(false)
    }
    function escape(event: KeyboardEvent) { if (event.key === 'Escape') setOpen(false) }
    document.addEventListener('pointerdown', dismiss)
    document.addEventListener('keydown', escape)
    return () => {
      document.removeEventListener('pointerdown', dismiss)
      document.removeEventListener('keydown', escape)
    }
  }, [open])

  async function choose(next: PermissionMode) {
    setOpen(false)
    if (next === mode || disabled || busy) return
    setBusy(true)
    onSaving(true)
    setError('')
    try { setMode((await saveSessionPermission(userId, sessionId, next)).mode) }
    catch { setError('Could not change chat permission.') }
    finally { setBusy(false); onSaving(false) }
  }

  return <div className="model-picker permission-picker" ref={root}>
    <button className="model-picker-trigger permission-trigger" type="button" aria-label={t('Chat permission')}
      aria-expanded={open} aria-controls={menuID} disabled={busy || disabled || !mode} onClick={() => setOpen(value => !value)}>
      <Shield size={16} /><span>{t(permissionLabel(mode) ?? 'Permissions')}</span><ChevronDown size={13} />
    </button>
    {open && <div className="model-picker-menu permission-menu" id={menuID}>
      <p>{t('Permissions for this chat')}</p>
      <div className="model-picker-options">
        {permissionOptions.map(option => <button key={option.mode} type="button" aria-pressed={mode === option.mode}
          disabled={disabled || busy} onClick={() => void choose(option.mode)}>
          <span><strong>{t(option.label)}</strong><small>{t(option.detail)}</small></span>
          {mode === option.mode && <Check size={16} />}
        </button>)}
      </div>
    </div>}
    {error && <small role="alert">{t(error)}</small>}
  </div>
}

export function ApprovalPanel({ item, userId, sessionId }: { item: PendingQuestion; userId: string; sessionId: string }) {
  const { t } = useLocale()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const info = item.approval
  if (!info) return null
  async function decide(decision: 'Allow once' | 'Deny') {
    if (busy) return
    setBusy(true)
    setError('')
    try { await answerQuestion(userId, sessionId, item.callId, [{ id: 'approval', selected: [decision] }]) }
    catch { setError('The decision could not be sent. Try again.'); setBusy(false) }
  }

  return <section className="question-panel permission-approval" role="dialog" aria-label={t('Tool approval')}>
    <header><span className="permission-icon"><ShieldAlert size={18} /></span>
      <div><strong>{t('Approval required')}</strong><small>{info.tool}{info.target && ' · ' + t(permissionLabel(info.target) ?? 'Permissions')}</small></div>
    </header>
    <p>{info.reason}</p>
    {info.detail && <pre>{info.detail}</pre>}
    <small className="permission-note">{t('Allow once applies to this operation only.')}</small>
    {error && <p role="alert" className="feedback">{t(error)}</p>}
    <div className="permission-decisions">
      <button type="button" onClick={() => void decide('Deny')} disabled={busy}>{t('Deny')}</button>
      <button type="button" onClick={() => void decide('Allow once')} disabled={busy}>{t('Allow once')}</button>
    </div>
  </section>
}
