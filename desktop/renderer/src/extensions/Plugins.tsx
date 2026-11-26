import { useEffect, useRef, useState } from 'react'
import { ArrowLeft, ChevronRight, LoaderCircle, MoreHorizontal, Plus, Puzzle, RefreshCw, Trash2 } from 'lucide-react'
import { getPlugins, installPackage, removePackage, restartPlugins, setPluginEnabled, updatePackage, type CatalogEntry } from '../api/extensions'
import type { TranscriptCache } from '../chat/transcriptCache'
import { useLocale } from '../locale'
import { ExtensionFeedback, ExtensionLoading, ExtensionState, useManagedView } from './shared'
import { useExtensionConfirm } from './Dialogs'
import { Authorizations } from './Authorizations'
import { localize } from './catalog'
import { MarkdownText } from '../chat/ChatContent'
import { PluginTutorial } from './PluginTutorial'
import { PluginIcon } from './PluginIcon'

export function Plugins({ userId, cache, selected, onSelect }: { userId: string; cache: TranscriptCache; selected?: string; onSelect: (name: string | undefined) => void }) {
 const { t, language } = useLocale(), confirm = useExtensionConfirm(), managed = useManagedView(userId, getPlugins, cache)
 const { view, busy, run } = managed
 const selectedBusy = !!selected && busy.endsWith(':' + selected)
 const busyName = view?.catalog.find(row => busy.endsWith(':' + row.package))?.name
 const [tab, setTab] = useState<'market' | 'installed'>('market'), [menu, setMenu] = useState<string>()
 const menuRef = useRef<HTMLDivElement>(null), menuTrigger = useRef<HTMLButtonElement | null>(null)
 const detailHeading = useRef<HTMLHeadingElement>(null)
 const names = Object.keys(view?.settings.packages ?? {}).sort()
 useEffect(() => {
  if (!menu) return
  const outside = (event: PointerEvent) => { if (!menuRef.current?.contains(event.target as Node)) setMenu(undefined) }
  const escape = (event: KeyboardEvent) => { if (event.key === 'Escape') { setMenu(undefined); menuTrigger.current?.focus() } }
  document.addEventListener('pointerdown', outside); document.addEventListener('keydown', escape)
  return () => { document.removeEventListener('pointerdown', outside); document.removeEventListener('keydown', escape) }
 }, [menu])
 useEffect(() => { if (selected) detailHeading.current?.focus(); else setTab('market') }, [selected])
 const open = (name: string) => { setMenu(undefined); onSelect(name) }
 const selection = selected ? view?.settings.packages[selected] : undefined
 const release = view?.catalog.find(row => row.package === selected)
 const info = view?.status.packages.find(row => row.name === selected)
 const actual = view?.status.instances.find(row => row.package === selected)
 const preparing = ['installing', 'loading'].includes(actual?.state ?? '')
 const title = release ? release.name : info?.title || selected
 const install = async (entry: CatalogEntry) => {
  await run('install:' + entry.package, () => installPackage(userId, entry.pluginId), 'Plugin and dependencies installed. Enable it to use its capabilities.')
 }
 const uninstall = async (name: string) => {
  setMenu(undefined)
  if (await confirm('Uninstall this package? Its credentials and workspace files will stay on disk.')) {
   if (await run('remove:' + name, () => removePackage(userId, name))) onSelect(undefined)
  }
 }
 const more = (name: string, entry?: CatalogEntry) => <div className="plugin-more" ref={menu === name ? menuRef : undefined}>
  <button type="button" className="plugin-icon-button" aria-label={t('More options')} aria-haspopup="menu" aria-expanded={menu === name} disabled={!!busy || view?.status.instances.some(row => row.package === name && ['installing', 'loading'].includes(row.state))} onClick={event => { menuTrigger.current = event.currentTarget; setMenu(menu === name ? undefined : name) }}><MoreHorizontal size={20} /></button>
  {menu === name && <div className="plugin-action-menu" role="menu">
   {!selected && <button role="menuitem" onClick={() => open(name)}><ChevronRight size={16} />{t('Plugin details')}</button>}
   {view?.status.packages.find(row => row.name === name)?.availableVersion && entry && <button role="menuitem" onClick={() => { setMenu(undefined); void run('install:' + name, () => updatePackage(userId, name, entry.pluginId)) }}><RefreshCw size={16} />{t('Update plugin')}</button>}
   {entry && <button role="menuitem" onClick={() => { setMenu(undefined); void run('install:' + name, () => updatePackage(userId, name, entry.pluginId)) }}><RefreshCw size={16} />{t('Reinstall plugin')}</button>}
   <button role="menuitem" className="danger" onClick={() => { void uninstall(name) }}><Trash2 size={16} />{t('Uninstall')}</button>
  </div>}
 </div>
 return <section className={`extensions-settings plugin-market${selected ? ' plugin-show-detail' : ''}`}>
  {selected ? <>
   <div className="plugin-detail-navigation">{selection && more(selected, release)}</div>
   <header className="plugin-detail-header"><PluginIcon icon={release?.icon} large /><div><h1 ref={detailHeading} tabIndex={-1}>{title}</h1><p>{release ? localize(release.description, language).split('\n')[0] : info?.description}</p></div></header>
   {release && localize(release.description, language).includes('\n') && <details className="plugin-setup-guide markdown"><summary>{t('Connection setup')}</summary><MarkdownText text={localize(release.description, language).split('\n').slice(1).join('\n').trim()} /></details>}
   {release && <PluginTutorial tutorials={release.tutorials} />}
   <ExtensionFeedback error={managed.error} notice={managed.notice} />
   {busy && <p className="plugin-progress" role="status"><LoaderCircle size={15} />{busyName ? busyName + ' · ' : ''}{t(busy.startsWith('install:') ? 'Installing…' : 'Applying plugin changes…')}</p>}
   {info?.availableVersion && release && <div className="plugin-update-banner"><div><strong>{t('Update available')}</strong><small>{info.version} → {info.availableVersion}</small></div><button className="model-secondary" disabled={!!busy} onClick={() => { void run('install:' + selected, () => updatePackage(userId, selected!, release.pluginId)) }}>{t('Update plugin')}</button></div>}
   {!selection && release ? <button className="account-submit plugin-primary" disabled={!!busy} onClick={() => { void install(release) }}>{selectedBusy ? <LoaderCircle size={16} /> : <Plus size={16} />}{t('Install plugin')}</button> : selection && <div className="plugin-activation">
    <div><strong>{t('Enable plugin')}</strong>{!selectedBusy && <ExtensionState state={actual?.state ?? (selection.enabled ? 'unavailable' : 'disabled')} />}</div>
    <button type="button" className="plugin-switch" role="switch" aria-label={t('Enable plugin')} aria-checked={selection.enabled} disabled={!!busy || preparing} onClick={() => { void run('settings:' + selected, () => setPluginEnabled(userId, selected, !selection.enabled)) }}><span /></button>
   </div>}
   {!selectedBusy && (actual?.error || view?.status.error) && <p className="extension-diagnostic" role="alert">{actual?.error || view?.status.error}</p>}
   {release && <>
    <dl className="plugin-detail-facts"><div><dt>{t('Version')}</dt><dd>{info?.present ? info.version : release.version}</dd></div><div><dt>{t('Maintainer')}</dt><dd>{release.maintainer}</dd></div><div><dt>{t('License')}</dt><dd>{release.license}</dd></div></dl>

   </>}
   {selection?.enabled && actual && <Authorizations user={userId} cache={cache} instanceIds={[actual.id]} />}
  </> : <>
   <div className="plugin-market-intro"><p>{t('Extend your agent with plugins reviewed for this project.')}</p><button type="button" className="plugin-icon-button" aria-label={t('Refresh extensions')} onClick={managed.refresh}><RefreshCw size={17} /></button></div>
   <div className="extension-tabs" role="tablist" aria-label={t('Plugins')}>{(['market', 'installed'] as const).map(value => <button key={value} type="button" role="tab" aria-selected={tab === value} onClick={() => { setMenu(undefined); setTab(value) }}>{t(value === 'market' ? 'Plugin market' : 'Installed')}{value === 'installed' && <span>{names.length}</span>}</button>)}</div>
   <ExtensionFeedback error={managed.error} notice={managed.notice} />
   {busy && <p className="plugin-progress" role="status"><LoaderCircle size={15} />{busyName ? busyName + ' · ' : ''}{t('Plugin changes continue while you use other features.')}</p>}
   {!view && managed.loading && <ExtensionLoading />}
   {view && <>
    {!(tab === 'market' ? view.catalog.length : names.length) && <div className="extension-empty"><Puzzle size={30} strokeWidth={1.4} /><h3>{t(tab === 'market' ? 'More capabilities are on the way' : 'No plugins installed')}</h3><p>{t('Your built-in agent works without plugins. Add packages when you need more capabilities.')}</p></div>}
    <div className="plugin-grid">{(tab === 'market' ? view.catalog.map(entry => entry.package) : names).map(name => {
     const entry = view.catalog.find(row => row.package === name), saved = view.settings.packages[name], state = view.status.instances.find(row => row.package === name)
     const label = entry ? entry.name : view.status.packages.find(row => row.name === name)?.title || name
     return <article className="plugin-tile" key={name}>
      <button type="button" className="plugin-tile-main" onClick={() => open(name)}><PluginIcon icon={entry?.icon} /><span className="plugin-tile-copy"><strong>{label}</strong><small>{entry ? localize(entry.description, language).split('\n')[0] : name}</small>{view.status.packages.find(row => row.name === name)?.availableVersion && <span className="plugin-update-tag">{t('Update available')}</span>}{(tab === 'installed' || ['installing', 'installation-failed'].includes(state?.state ?? '')) && <ExtensionState state={state?.state ?? (saved?.enabled ? 'unavailable' : 'disabled')} />}</span></button>
      {busy === 'install:' + name || state?.state === 'installing' ? <span className="plugin-icon-button plugin-progress" role="status" aria-label={t('Installing…')}><LoaderCircle size={19} /></span> : saved ? more(name, entry) : <button type="button" className="plugin-icon-button" disabled={!!busy} aria-label={t('Install plugin') + ': ' + label} onClick={() => { if (entry) void install(entry) }}><Plus size={22} /></button>}
     </article>
    })}</div>
    {tab === 'installed' && <details className="plugin-runtime-details"><summary>{t('Plugin runtime')}</summary><div className="extension-runtime"><ExtensionState state={view.status.runtime} /><button className="extension-text" disabled={!!busy} onClick={() => { void run('restart', () => restartPlugins(userId)) }}>{t('Restart')}</button></div>{view.status.error && <p className="extension-diagnostic">{view.status.error}</p>}</details>}
   </>}
  </>}
 </section>
}
