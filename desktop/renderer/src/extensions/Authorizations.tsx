import { useEffect, useMemo, useRef, useState } from 'react'
import { Check, ChevronDown, ExternalLink, KeyRound, LoaderCircle, Upload, X } from 'lucide-react'
import { authorizationAPI, mcpAuthorizationAPI, type AuthorizationEntry, type AuthorizationPrompt, type MCPServer } from '../api/extensions'
import type { TranscriptCache } from '../chat/transcriptCache'
import { useLocale } from '../locale'
import { authorizationLabel, authorizationURL, useAuthorization } from './useAuthorization'
import { useReturnFocus } from './shared'
import { readAuthorizationJSON } from './authorizationFile'
import { useMCPAuthorization } from './useMCPAuthorization'
import { authorizationEntryLabels, connectionLabel, connectionTitle, mcpAuthorizationLabel, mcpAuthorizationRunning } from './connections'

export function Authorizations({ user, cache, instanceIds }: { user: string; cache: TranscriptCache; instanceIds: string[] }) {
  const api = useMemo(() => authorizationAPI(user), [user]), mcpAPI = useMemo(() => mcpAuthorizationAPI(user), [user])
  const state = useAuthorization(api, cache), mcp = useMCPAuthorization(mcpAPI, cache, instanceIds), { t } = useLocale()
  const entries = state.entries.filter(entry => instanceIds.some(id => entry.key.startsWith(id + '/')))
  const attempt = state.attempt
  const reload = () => { state.reload(); mcp.reload() }
  useEffect(() => { const focus = () => { state.reload(); mcp.reload() }; window.addEventListener('focus', focus); return () => window.removeEventListener('focus', focus) }, [state.reload, mcp.reload])
  if (!entries.length && !mcp.servers.length && !state.error && !mcp.error) return null
  return <>
    <details className="extension-details extension-authorization-list"><summary><KeyRound size={17} />{t('Accounts and authorization')}<span>{entries.length + mcp.servers.length}</span><ChevronDown size={15} /></summary>
      <div className="extension-details-body"><p className="model-muted">{t('Connect accounts using the authorization methods provided by your plugins.')}</p>
        {entries.map(entry => <AuthorizationRow key={entry.key} entry={entry} onDisconnect={() => { void state.disconnect(entry.key) }} busy={state.busy || mcp.busy || !!attempt || !!mcp.attempt} onCancel={() => { void state.cancelKey(entry.key) }} onStart={method => { if (entry.inFlight && entry.attemptId) void state.open(entry.attemptId); else void state.start(entry.key, method) }} onView={() => { if (entry.attemptId) void state.open(entry.attemptId) }} />)}
        {mcp.servers.map(server => <MCPConnection key={server.name} server={server} busy={state.busy || mcp.busy || !!attempt || !!mcp.attempt} onOpen={() => { void mcp.open(server) }} onView={() => mcp.resume(server)} />)}
        {!entries.length && !mcp.servers.length && <p className="model-muted">{t('No account authorization methods are available.')}</p>}
        {!attempt && !mcp.attempt && (state.error || mcp.error) && <p role="alert" className="extension-diagnostic">{t(state.error || mcp.error)}</p>}
        <button className="extension-text" type="button" disabled={state.busy || mcp.busy} onClick={reload}>{t('Refresh')}</button>
      </div>
    </details>
    {attempt && <AuthorizationDialog state={state} />}
    {mcp.attempt && mcp.selected && <MCPAuthorizationDialog state={mcp} />}
  </>
}
function AuthorizationRow({ entry, busy, onStart, onCancel, onView, onDisconnect }: { entry: AuthorizationEntry; busy: boolean; onStart: (method: string) => void; onCancel: () => void; onView: () => void; onDisconnect: () => void }) {
  const { t } = useLocale(), [method, setMethod] = useState(entry.methods[0]?.id ?? '')
  const labels = authorizationEntryLabels(entry)
  return <div className="extension-account-row"><KeyRound size={18} strokeWidth={1.6} /><div className="extension-identity"><strong>{entry.label}</strong><small>{t(labels.status)}{entry.accountLabel && <> · {entry.accountLabel}</>}</small></div>
    {entry.methods.length > 1 && <select className="extension-auth-method" aria-label={t('Authorization method') + ': ' + entry.label} value={method} disabled={busy || entry.inFlight} onChange={event => setMethod(event.target.value)}>{entry.methods.map(method => <option key={method.id} value={method.id}>{t(method.label)}</option>)}</select>}
    <button className="model-secondary" type="button" disabled={busy} onClick={() => onStart(method)}>{t(labels.start)}</button>
    {entry.configured && entry.canDisconnect && !entry.inFlight && <button type="button" className="extension-text" disabled={busy} onClick={onDisconnect}>{t(labels.disconnect)}</button>}
    {entry.inFlight && <button className="extension-text" type="button" disabled={busy} onClick={onCancel}>{t('Cancel authorization')}</button>}
    {!entry.inFlight && entry.attemptId && <button className="extension-text" type="button" disabled={busy} onClick={onView}>{t('View last result')}</button>}
  </div>
}
function AuthorizationDialog({ state }: { state: ReturnType<typeof useAuthorization> }) {
  const { t } = useLocale(), dialog = useRef<HTMLDialogElement>(null)
  const attempt = state.attempt!
  useReturnFocus()
  useEffect(() => { dialog.current?.showModal(); dialog.current?.querySelector<HTMLInputElement>('input')?.focus() }, [])
  return <dialog ref={dialog} className="extension-auth-dialog" aria-labelledby="authorization-title" onCancel={event => { event.preventDefault(); void state.close() }} onClick={event => {
    if (event.target !== event.currentTarget) return
    const rect = event.currentTarget.getBoundingClientRect()
    if (event.clientX < rect.left || event.clientX > rect.right || event.clientY < rect.top || event.clientY > rect.bottom) void state.close()
  }}>
    <header className="extension-auth-heading"><KeyRound size={21} strokeWidth={1.6} /><div><h2 id="authorization-title">{attempt.label}</h2><p>{t(authorizationLabel[attempt.status])}</p></div><button type="button" className="extension-icon" aria-label={t('Close')} disabled={state.busy} onClick={() => { void state.close() }}><X size={19} /></button></header>
    <div className="extension-auth-content">
      {attempt.notices.map((notice, index) => <div className="extension-auth-notice" key={index}><p>{t(notice.message)}</p>{attempt.status === 'running' && notice.code && <code tabIndex={0}>{notice.code}</code>}{attempt.status === 'running' && authorizationURL(notice.url) && <a href={authorizationURL(notice.url)} target="_blank" rel="noopener noreferrer">{t('Continue in browser')}<ExternalLink size={14} /></a>}</div>)}
      {attempt.prompts.map(prompt => <AuthorizationQuestion key={prompt.id} prompt={prompt} busy={state.busy} onAnswer={(answer, decline) => { void state.answer(prompt.id, answer, decline) }} />)}
      {attempt.status === 'running' && !attempt.prompts.length && <p className="extension-auth-wait" role="status"><LoaderCircle size={17} className="extension-auth-spinner" />{t('Waiting for authorization to finish…')}</p>}
      {attempt.status === 'authorized' && <p className="extension-auth-complete" role="status"><Check size={19} />{t('Authorization is ready.')}</p>}
      {attempt.status === 'running' && <p className="model-muted">{t('Closing this panel keeps the authorization running. You can return to it from this plugin.')}</p>}
      {(state.error || attempt.error) && <p role="alert" className="extension-diagnostic">{state.error ? t(state.error) : attempt.error ? t(attempt.error) : undefined}</p>}
      <div className="extension-actions"><button className="model-secondary" type="button" disabled={state.busy} onClick={() => { if (attempt.status === 'running') void state.cancel(); else state.close() }}>{t(attempt.status === 'running' ? 'Cancel authorization' : 'Done')}</button><button type="button" className="extension-text" disabled={state.busy} onClick={state.reload}>{t('Refresh')}</button></div>
    </div>
  </dialog>
}
function AuthorizationQuestion({ prompt, busy, onAnswer }: { prompt: AuthorizationPrompt; busy: boolean; onAnswer: (answer?: string, decline?: boolean) => void }) {
  const { t } = useLocale(), [answer, setAnswer] = useState(''), fields = useRef<HTMLFieldSetElement>(null)
  const fileInput = useRef<HTMLInputElement>(null), [fileBusy, setFileBusy] = useState(false), [fileError, setFileError] = useState('')
  const jsonFile = prompt.kind !== 'select' && prompt.webInput === 'json-file'
  useEffect(() => { fields.current?.querySelector<HTMLElement>(jsonFile ? 'button' : 'input')?.focus() }, [])
  return <form className="extension-auth-question model-form" onSubmit={event => { event.preventDefault(); if (jsonFile) return; onAnswer(answer); setAnswer('') }}>
    <fieldset ref={fields} disabled={busy || fileBusy}><legend>{t(jsonFile ? 'Select a JSON configuration file.' : prompt.message)}</legend>
      {jsonFile ? <div className="extension-auth-file">
        <Upload size={23} strokeWidth={1.5} /><p>{t('Choose the client JSON downloaded from your provider. It will be parsed and submitted automatically.')}</p>
        <input ref={fileInput} className="extension-file-input" aria-hidden="true" tabIndex={-1} type="file" accept=".json,application/json" onChange={async event => {
          const input = event.currentTarget, file = input.files?.[0]
          if (!file) return
          setFileError(''); setFileBusy(true)
          try { const value = await readAuthorizationJSON(file); onAnswer(value) }
          catch (error) { setFileError(error instanceof Error ? error.message : 'Could not read this configuration file.') }
          finally { input.value = ''; setFileBusy(false) }
        }} />
        <button type="button" className="model-secondary" onClick={() => fileInput.current?.click()}>{fileBusy ? <LoaderCircle size={16} className="extension-auth-spinner" /> : null}{t('Choose JSON file')}</button>
        {fileError && <p role="alert" className="extension-diagnostic">{t(fileError)}</p>}
      </div> : prompt.kind === 'select' ? <div className="extension-auth-options">{prompt.options.map(option => <label key={option.id} className={answer === option.id ? 'is-selected' : undefined}><input type="radio" name={prompt.id} value={option.id} checked={answer === option.id} onChange={() => setAnswer(option.id)} /><span><strong>{t(option.label)}</strong>{option.description && <small>{t(option.description)}</small>}</span></label>)}</div>
        : <input aria-label={t(prompt.message)} autoFocus type={prompt.kind === 'secret' ? 'password' : 'text'} value={answer} maxLength={16384} placeholder={prompt.placeholder} autoComplete="off" onChange={event => setAnswer(event.target.value)} />}
      <div className="extension-actions">{!jsonFile && <button type="submit" className="account-submit" disabled={busy || (prompt.kind === 'select' && !answer)}>{t('Continue')}</button>}<button type="button" className="extension-text" onClick={() => onAnswer(undefined, true)}>{t('Not now')}</button></div>
    </fieldset>
  </form>
}

function MCPConnection({ server, busy, onOpen, onView }: { server: MCPServer; busy: boolean; onOpen: () => void; onView: () => void }) {
 const { t } = useLocale(), active = mcpAuthorizationRunning(server.authorization)
 return <div className="extension-account-row"><KeyRound size={18} strokeWidth={1.6} /><div className="extension-identity"><strong>{connectionTitle(server)}</strong><small>{t(connectionLabel(server))}</small></div><button type="button" className="model-secondary" disabled={busy} onClick={onOpen}>{t(active ? 'Continue authorization' : 'Connect in browser')}</button>{!active && server.authorization?.id && <button type="button" className="extension-text" disabled={busy} onClick={onView}>{t('View last result')}</button>}</div>
}
function MCPAuthorizationDialog({ state }: { state: ReturnType<typeof useMCPAuthorization> }) {
 const { t } = useLocale(), dialog = useRef<HTMLDialogElement>(null), server = state.selected!, attempt = state.attempt!, active = mcpAuthorizationRunning(attempt)
 useReturnFocus()
 useEffect(() => { dialog.current?.showModal() }, [])
 return <dialog ref={dialog} className="extension-auth-dialog" aria-labelledby="mcp-authorization-title" onCancel={event => { event.preventDefault(); state.close() }} onClick={event => { if (event.target !== event.currentTarget) return; const rect = event.currentTarget.getBoundingClientRect(); if (event.clientX < rect.left || event.clientX > rect.right || event.clientY < rect.top || event.clientY > rect.bottom) state.close() }}>
  <header className="extension-auth-heading"><KeyRound size={21} strokeWidth={1.6} /><div><h2 id="mcp-authorization-title">{connectionTitle(server)}</h2><p>{t(mcpAuthorizationLabel(attempt))}</p></div><button type="button" className="extension-icon" aria-label={t('Close')} disabled={state.busy} onClick={state.close}><X size={19} /></button></header>
  <div className="extension-auth-content">
   {active && authorizationURL(attempt.url) && <div className="extension-auth-notice"><p>{t('Complete authorization with the service in your browser, then return here.')}</p><a href={authorizationURL(attempt.url)} target="_blank" rel="noopener noreferrer">{t('Continue in browser')}<ExternalLink size={14} /></a></div>}
   {active && <p className="extension-auth-wait" role="status"><LoaderCircle size={17} className="extension-auth-spinner" />{t('Waiting for authorization to finish…')}</p>}
   {attempt.phase === 'authorized' && <p className="extension-auth-complete" role="status"><Check size={19} />{t('Authorization is ready.')}</p>}
   {(state.error || attempt.error || server.error) && <p className="extension-diagnostic" role="alert">{state.error ? t(state.error) : attempt.error || server.error}</p>}
   <div className="extension-actions">{active ? <button className="model-secondary" disabled={state.busy} onClick={() => { void state.cancel() }}>{t('Cancel authorization')}</button> : <button className="model-secondary" disabled={state.busy} onClick={state.close}>{t('Done')}</button>}<button className="extension-text" disabled={state.busy} onClick={state.reload}>{t('Refresh')}</button></div><p className="model-muted">{t('Closing this panel keeps the authorization running. You can return to it from this plugin.')}</p>
  </div>
 </dialog>
}
