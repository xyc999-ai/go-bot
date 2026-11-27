import { Check, ChevronDown, ExternalLink } from 'lucide-react'
import { useLocale } from '../locale'
import { useEffect, useState } from 'react'
import { APIError, getProviderSettings, removeProviderKey, saveProviderSettings, settingsApplyFailed, type Provider, type ProviderPage, type ProviderSettings } from '../api/controller'

import providerDocs from '../../../docs/providers.md?url&no-inline'

const keyHelp: Record<string, { label: string; url: string }> = {
  brave: { label: 'Brave Search API key', url: 'https://api-dashboard.search.brave.com/' },
  tavily: { label: 'Tavily API key', url: 'https://app.tavily.com/' },
  firecrawl: { label: 'Firecrawl API key', url: 'https://www.firecrawl.dev/app/api-keys' },
  deepseek: { label: 'DeepSeek Search API', url: 'https://platform.deepseek.com/' },
  zhipu: { label: 'BigModel API key', url: 'https://bigmodel.cn/usercenter/proj-mgmt/apikeys' },
  doubao: { label: 'Doubao Search Custom API key', url: 'https://docs.volcengine.com/docs/networked-search/networked-search?lang=zh' },
  seedream: { label: 'Volcengine Ark API key', url: 'https://console.volcengine.com/ark/' },
  openai: { label: 'OpenAI API key', url: 'https://platform.openai.com/api-keys' },
  nanobanana: { label: 'Google AI Studio API key', url: 'https://aistudio.google.com/app/apikey' },
}

type Action = 'save' | 'activate' | 'remove'

export function ToolProviders({ userId, page }: { userId: string; page: ProviderPage }) {
  const { t } = useLocale()
  const isImage = page === 'image-generation'
  const title = t(isImage ? 'Image generation' : 'Web search')
  const [settings, setSettings] = useState<ProviderSettings>()
  const [editing, setEditing] = useState<string>()
  const [model, setModel] = useState('')
  const [apiKey, setAPIKey] = useState('')
  const [action, setAction] = useState<Action>()
  const [error, setError] = useState('')
  const [notice, setNotice] = useState<{ message: string; name: string }>()
  const [loadAttempt, setLoadAttempt] = useState(0)
  const [retry, setRetry] = useState<{ id: string; action: 'activate' | 'remove' }>()

  function edit(provider: Provider, next: ProviderSettings) {
    setEditing(provider.id)
    setModel(next.entries[provider.id]?.model ?? provider.defaultModel ?? '')
    setAPIKey('')
  }

  useEffect(() => {
    const request = new AbortController()
    void getProviderSettings(userId, page, request.signal).then(next => {
      if (!request.signal.aborted) setSettings(next)
    }, () => {
      if (!request.signal.aborted) setError('Could not load provider settings.')
    })
    return () => request.abort()
  }, [userId, page, loadAttempt])

  async function update(provider: Provider, nextAction: Action) {
    setAction(nextAction)
    setError('')
    setNotice(undefined)
    try {
      const next = nextAction === 'remove'
        ? await removeProviderKey(userId, page, provider.id)
        : await saveProviderSettings(userId, page, nextAction === 'activate'
          ? { provider: provider.id, activate: true }
          : { provider: provider.id, activate: false, model: isImage ? model.trim() : undefined, apiKey: apiKey.trim() || undefined })
      setSettings(next)
      if (editing === provider.id) edit(provider, next)
      setRetry(undefined)
      setNotice({ name: provider.name, message: nextAction === 'activate' ? '{name} is now in use.'
        : nextAction === 'remove' ? '{name} API key removed.' : '{name} settings saved.' })
    } catch (failure) {
      if (settingsApplyFailed(failure)) {
        const saved = await getProviderSettings(userId, page).catch(() => undefined)
        if (saved) {
          setSettings(saved)
          if (editing === provider.id) edit(provider, saved)
        }
        setRetry({ id: provider.id, action: nextAction === 'remove' ? 'remove' : 'activate' })
        setError('Settings were saved, but the Host could not apply them. Retry or reopen the account.')
      } else {
        const code = failure instanceof APIError ? failure.message : ''
        setError(code === 'invalid_image_model' ? 'Enter a valid model name or endpoint ID.'
          : code === 'provider_key_required' || code === 'invalid_web_search_key' ? 'Save an API key before using this provider.'
          : code === 'invalid_provider_key' ? 'Enter a valid API key.'
          : 'Could not confirm the change. Try again.')
      }
    } finally {
      setAction(undefined)
    }
  }

  const active = settings?.providers.find(provider => provider.id === settings.provider)
  const activeReady = active && settings?.entries[active.id]?.hasApiKey

  return <div className="provider-settings">
    <div className="provider-settings-intro">
      <h2 className="settings-section-title">{title}</h2>
      <p className="model-muted">{isImage
        ? t('Choose the service your agent uses to generate images.')
        : t('Choose the service your agent uses to search the web.')}</p>
    </div>
    {error && <p className="account-error" role="alert">{t(error)}</p>}
    {notice && <p className="provider-notice" role="status">{t(notice.message, { name: notice.name })}</p>}
    {!settings && (error
      ? <button type="button" className="model-secondary" onClick={() => { setError(''); setLoadAttempt(attempt => attempt + 1) }}>{t('Retry')}</button>
      : <p role="status">{t('Loading provider settings…')}</p>)}
    {settings && <>
      <p className="provider-current">
        {retry ? <>{t('Waiting for the Host to apply the saved change.')}</> : activeReady ? <>{t('In use')} <strong>{active.name}</strong></> : <>{t('No provider is in use. Save a key and choose a provider.')}</>}
      </p>
      <ul className="provider-list" aria-label={t('{title} providers', { title })}>
        {settings.providers.map(provider => {
          const entry = settings.entries[provider.id]
          const hasKey = !!entry?.hasApiKey
          const selected = provider.id === settings.provider
          const expanded = editing === provider.id
          const help = keyHelp[provider.id]
          const dirty = !!apiKey.trim() || (isImage && model.trim() !== (entry?.model ?? provider.defaultModel))
          const pending = retry?.id === provider.id ? retry : undefined
          const formId = `provider-${page}-${provider.id}`
          return <li className="provider-row" key={provider.id}>
            <div className="provider-row-main">
              <div className="provider-row-identity">
                <h3>{provider.name}</h3>
                <p>{isImage && hasKey ? entry?.model ?? provider.defaultModel : hasKey ? t('API key saved') : t('No API key')}</p>
              </div>
              <div className="provider-row-actions">
                {selected && hasKey && !pending && <span className="provider-active"><Check size={15} aria-hidden="true" /> {t('In use')}</span>}
                {(pending || (hasKey && !selected)) && <button type="button" className="model-secondary provider-use"
                  aria-label={t(pending ? 'Retry applying {name}' : 'Use {name}', { name: provider.name })}
                  disabled={!!action || (expanded && dirty)} onClick={() => { void update(provider, pending?.action ?? 'activate') }}>
                  {pending ? t('Retry apply') : t('Use provider')}
                </button>}
                <button type="button" className="provider-edit" aria-label={t(hasKey ? 'Edit {name}' : 'Set up {name}', { name: provider.name })}
                  aria-expanded={expanded} aria-controls={expanded ? formId : undefined} disabled={!!action}
                  onClick={() => {
                    setNotice(undefined)
                    if (!retry) setError('')
                    if (expanded) { setEditing(undefined); setAPIKey('') }
                    else edit(provider, settings)
                  }}>
                  {hasKey ? t('Edit') : t('Set up')} <ChevronDown size={15} aria-hidden="true" />
                </button>
              </div>
            </div>
            {expanded && <form id={formId} className="model-form provider-form" onSubmit={event => {
              event.preventDefault()
              void update(provider, 'save')
            }}>
              <fieldset disabled={!!action}>
                {isImage && <label>{provider.id === 'seedream' ? t('Model name or endpoint ID') : t('Model name')}
                  <input value={model} required maxLength={256} onChange={event => setModel(event.target.value)} />
                </label>}
                <label>{t('API key')}
                  <input type="password" value={apiKey} required={!hasKey} maxLength={4096} autoComplete="new-password"
                    placeholder={hasKey ? t('Leave blank to keep the saved key') : undefined}
                    onChange={event => setAPIKey(event.target.value)} />
                </label>
                {help && <a className="provider-doc-link" href={help.url} target="_blank" rel="noreferrer">{t(help.label)}<ExternalLink size={13} aria-hidden="true" /></a>}
                <div className="model-settings-actions">
                  <button type="submit" className="account-submit" disabled={!dirty || (!hasKey && !apiKey.trim())}>
                    {action === 'save' ? t('Saving…') : t('Save settings')}
                  </button>
                  {hasKey && <button type="button" className="model-secondary" onClick={() => { void update(provider, 'remove') }}>{t('Remove key')}</button>}
                </div>
              </fieldset>
            </form>}
          </li>
        })}
      </ul>
      <a className="provider-doc-link" href={providerDocs} target="_blank" rel="noreferrer">{t('Build a provider adapter')} <ExternalLink size={13} aria-hidden="true" /></a>
    </>}
  </div>
}
