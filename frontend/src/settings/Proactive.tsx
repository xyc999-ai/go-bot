import { useEffect, useRef, useState } from 'react'
import { Clock3, RefreshCw, Sparkles } from 'lucide-react'
import { APIError, getProactiveSettings, saveProactiveSettings, settingsApplyFailed } from '../api/controller'
import { ModelRequiredDialog } from '../chat/ModelRequiredDialog'
import { useLocale } from '../locale'
import { proactiveEnableProblem, proactivePhase, proactiveProblem, type ProactiveStatus } from './proactiveState'

export function Proactive({ userId, onModels }: { userId: string; onModels: () => void }) {
  const { t, language } = useLocale()
  const [status, setStatus] = useState<ProactiveStatus>()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [retry, setRetry] = useState(false)
  const [modelIssue, setModelIssue] = useState('')
  const generation = useRef(0)
  const saving = useRef(false)
  const date = (at: number) => new Date(at).toLocaleString(language === 'zh-CN' ? 'zh-CN' : 'en-US', { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })

  async function refresh(signal?: AbortSignal) {
    if (saving.current) return
    const version = generation.current
    try {
      const next = await getProactiveSettings(userId, signal)
      if (!signal?.aborted && version === generation.current) { setStatus(next); setError(current => current === 'Could not load proactive settings.' ? '' : current) }
    } catch { if (!signal?.aborted && version === generation.current) setError('Could not load proactive settings.') }
  }
  useEffect(() => {
    const controller = new AbortController()
    void refresh(controller.signal)
    const timer = window.setInterval(() => { if (!document.hidden) void refresh(controller.signal) }, 15000)
    return () => { controller.abort(); window.clearInterval(timer); generation.current++ }
  }, [userId])

  async function save(enabled: boolean) {
    generation.current++
    saving.current = true
    setBusy(true); setError(''); setRetry(false)
    try { setStatus(await saveProactiveSettings(userId, enabled)) }
    catch (issue) {
      const modelProblem = issue instanceof APIError && enabled ? proactiveEnableProblem(issue.message) : ''
      if (modelProblem) {
        setModelIssue(modelProblem)
      } else if (settingsApplyFailed(issue)) {
        setStatus(await getProactiveSettings(userId).catch(() => undefined))
        setError('Settings were saved, but the Host could not apply them. Retry or reopen the account.')
        setRetry(true)
      } else setError('Could not save proactive settings.')
    } finally { saving.current = false; setBusy(false) }
  }

  const issue = status && proactiveProblem(status)
  return <section className="proactive-settings">
    <div className="provider-settings-intro">
      <h2 className="settings-section-title">{t('Proactive assistance')}</h2>
      <p className="model-muted">{t('Keeps up with your recent conversations and memory, investigates useful opportunities, and shares relevant findings in your main chat.')}</p>
    </div>
    {error && <p className="account-error" role="alert">{t(error)}</p>}
    {!status ? !error && <p role="status">{t('Loading proactive settings…')}</p> : <>
      <div className="model-card proactive-control">
        <Sparkles size={21} strokeWidth={1.6} aria-hidden="true" />
        <div className="proactive-control-copy"><h3>{t('Enable proactive assistance')}</h3></div>
        <label className="proactive-switch"><input type="checkbox" role="switch" aria-label={t('Enable proactive assistance')} checked={status.enabled} disabled={busy} onChange={event => void save(event.target.checked)} /><span aria-hidden="true" /></label>
      </div>
      <div className="proactive-status" role="status">
        <div className="proactive-status-heading"><span className={'proactive-dot ' + (status.enabled ? status.phase : 'disabled')} /><span>{t(proactivePhase(status))}</span></div>
        <dl>
          <div><dt><Clock3 size={16} aria-hidden="true" />{t('Next check')}</dt><dd>{status.enabled ? status.nextCheckAt ? date(status.nextCheckAt) : t('Waiting for the first check') : t('No checks while turned off')}{status.enabled && status.nextCheckReason && <p className="proactive-reason">{status.nextCheckReason}</p>}</dd></div>
          {status.lastCompletedAt && <div><dt>{t('Last completed')}</dt><dd>{date(status.lastCompletedAt)}</dd></div>}
        </dl>
        {issue && <p className="model-muted">{t(issue)}</p>}
        {!!status.error?.startsWith('model_') && status.enabled && <button type="button" className="text-button" onClick={onModels}>{t('Open model settings')}</button>}
      </div>
    </>}
    <div className="proactive-footer"><p className="model-muted">{t('Uses the main chat’s model and connected services while your Host is running. Model and service usage may incur charges.')}</p><button type="button" className="text-button" disabled={busy} onClick={() => { setError(''); void (retry && status ? save(status.enabled) : refresh()) }}><RefreshCw size={14} />{t(retry ? 'Retry applying' : 'Refresh status')}</button></div>
    {modelIssue && <ModelRequiredDialog message={modelIssue} keepDraft={false} onClose={() => setModelIssue('')} onSettings={() => { setModelIssue(''); onModels() }} />}
  </section>
}
