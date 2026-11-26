import { modelProtocol, protocolNames } from '../settings/modelProtocol'
import { useEffect, useRef, useState } from 'react'
import { useLocale } from '../locale'
import { Check, ChevronDown, Cpu } from 'lucide-react'
import { getModelSettings, getSessionModel, saveSessionModel, settingsApplyFailed, type ModelSelection, type ModelSettings, type SessionModel } from '../api/controller'

export function ModelPicker({ userId, id, settingsVersion, onSettings, onSaving, generating }: {
  userId: string
  id?: string
  settingsVersion: number
  onSettings: () => void
  onSaving: (value: boolean) => void
  generating?: boolean
}) {
  const { t } = useLocale()
  const [catalog, setCatalog] = useState<ModelSettings>()
  const [route, setRoute] = useState<SessionModel>()
  const [open, setOpen] = useState(false)
  const [endpointId, setEndpointID] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')

  useEffect(() => {
    const controller = new AbortController()
    void getModelSettings(userId, controller.signal).then(setCatalog, () => {
      if (!controller.signal.aborted) setError('Could not load model settings.')
    })
    if (id) {
      void getSessionModel(userId, id, controller.signal).then(next => {
        setRoute(next)
        setError('')
      }, () => {
        if (!controller.signal.aborted) setError('Could not load the chat model.')
      })
    }
    return () => controller.abort()
  }, [userId, id, settingsVersion])

  useEffect(() => { setNotice('') }, [userId, id])
  useEffect(() => { if (!generating) setNotice('') }, [generating])

  const picker = useRef<HTMLDivElement>(null)
  const trigger = useRef<HTMLButtonElement>(null)
  useEffect(() => {
    if (!open) return
    function outside(event: PointerEvent) {
      if (!picker.current?.contains(event.target as Node)) setOpen(false)
    }
    document.addEventListener('pointerdown', outside)
    return () => document.removeEventListener('pointerdown', outside)
  }, [open])

  async function select(selection: ModelSelection) {
    if (!id) return
    setBusy(true)
    onSaving(true)
    setError('')
    try {
      const next = await saveSessionModel(userId, id, selection)
      setRoute(next)
      setNotice(generating ? 'This model will be used from the next turn.' : '')
      setOpen(false)
      requestAnimationFrame(() => trigger.current?.focus())
    } catch (failure) {
      setError(settingsApplyFailed(failure) ? 'Model choice was saved, but the Host could not apply it. Try again.'
        : 'Could not switch models. Try again.')
    } finally {
      setBusy(false)
      onSaving(false)
    }
  }

  const defaultId = catalog?.defaultEndpointId ?? ''
  const active = id ? route?.selection : defaultId && catalog?.endpoints[defaultId]?.models[0]
    ? { endpointId: defaultId, modelId: catalog.endpoints[defaultId].models[0] } : undefined
  const activeName = active && catalog?.endpoints[active.endpointId]?.name
  const choices = Object.entries(catalog?.endpoints ?? {})
  const label = route?.status === 'unavailable' ? t('Model unavailable') : activeName && active
    ? activeName + ' / ' + active.modelId : id ? t('Choose model') : t('Configure model')
  return <div className="model-picker" ref={picker} onBlur={event => {
    if (event.relatedTarget && !event.currentTarget.contains(event.relatedTarget)) setOpen(false)
  }} onKeyDown={event => {
    if (event.key === 'Escape' && open) {
      event.preventDefault()
      setOpen(false)
      trigger.current?.focus()
    }
  }}>
    <button ref={trigger} type="button" className="model-picker-trigger" title={label} aria-expanded={id && choices.length ? open : undefined}
      disabled={busy} onClick={() => {
        if (!id || !choices.length) onSettings()
        else {
          setEndpointID(active?.endpointId && catalog?.endpoints[active.endpointId] ? active.endpointId : defaultId || choices[0][0])
          setOpen(!open)
        }
      }}>
      <Cpu size={15} aria-hidden="true" />
      <span className="model-picker-label">{activeName && active && route?.status !== 'unavailable'
        ? <><small>{activeName}</small><strong>{active.modelId}</strong></> : label}</span>{id && choices.length > 0 && <ChevronDown size={14} />}
    </button>
    {open && catalog && <div className="model-picker-menu">
      <label>{t('Endpoint')}<select value={endpointId} onChange={event => setEndpointID(event.target.value)}>
        {choices.map(([id, endpoint]) => <option key={id} value={id}>{endpoint.name} · {protocolNames[modelProtocol(endpoint.protocol)]}</option>)}
      </select></label>
      <div className="model-picker-options">
        {catalog.endpoints[endpointId]?.models.map(modelId => <button type="button" key={modelId} disabled={busy}
          aria-pressed={active?.endpointId === endpointId && active.modelId === modelId}
          onClick={() => { void select({ endpointId, modelId }) }}><span>{modelId}</span>
          {active?.endpointId === endpointId && active.modelId === modelId && <Check size={15} aria-hidden="true" />}
        </button>)}
      </div>
    </div>}
    {notice && <small role="status">{t(notice)}</small>}
    {error && <small role="alert">{t(error)}</small>}
  </div>
}
