import { useEffect, useRef, useState } from 'react'
import { CalendarClock, FolderOpen, MessageCircle, Power, Puzzle, Settings2 } from 'lucide-react'
import { HarnessAvatar } from '../HarnessAvatar'
import { useLocale } from '../locale'

type Props = {
  userId: string; section: 'chats' | 'tasks' | 'plugins' | 'settings'; chatsOpen: boolean; filesOpen: boolean
  ready: boolean; stopping: boolean; error: string
  onChats: () => void; onTasks: () => void; onPlugins: () => void; onFiles: () => void; onSettings: () => void
  onStop: () => void
}
export function NavigationRail(props: Props) {
  const { t } = useLocale()
  const [account, setAccount] = useState(false)
  const dock = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!account) return
    const outside = (event: PointerEvent) => { if (!dock.current?.contains(event.target as Node)) setAccount(false) }
    document.addEventListener('pointerdown', outside)
    return () => document.removeEventListener('pointerdown', outside)
  }, [account])
  const items = [
    { id: 'chats', label: 'Chats', Icon: MessageCircle, run: props.onChats },
    { id: 'tasks', label: 'Tasks', Icon: CalendarClock, run: props.onTasks },
    { id: 'plugins', label: 'Plugin market', Icon: Puzzle, run: props.onPlugins },
    { id: 'files', label: 'File system', Icon: FolderOpen, run: props.onFiles },
  ] as const
  return <nav className="navigation-rail" aria-label={t('Main navigation')}>
    <button className="rail-brand" type="button" aria-label={t('Open chats')} title="Go Bot" onClick={props.onChats}><HarnessAvatar className="rail-logo" /></button>
    <div className="rail-links">{items.map(({ id, label, Icon, run }) => <button key={id} type="button" className="rail-button" onClick={run}
      disabled={!props.ready && (id === 'files' || id === 'tasks')} aria-label={t(label)} title={t(label)}
      aria-current={id !== 'files' && props.section === id ? 'page' : undefined}
      aria-expanded={id === 'chats' ? props.chatsOpen : id === 'files' ? props.filesOpen : undefined}
      aria-controls={id === 'chats' ? 'chat-sidebar' : id === 'files' ? 'host-files' : undefined}>
      <Icon size={20} strokeWidth={1.65} /><span className="rail-tooltip">{t(label)}</span>
    </button>)}</div>
    <div className="rail-bottom">
      <button type="button" className="rail-button" aria-label={t('Settings')} title={t('Settings')} aria-current={props.section === 'settings' ? 'page' : undefined} onClick={props.onSettings}><Settings2 size={20} strokeWidth={1.65} /><span className="rail-tooltip">{t('Settings')}</span></button>
      <div className="rail-account" ref={dock} onKeyDown={event => { if (event.key === 'Escape') { setAccount(false); dock.current?.querySelector<HTMLButtonElement>('button')?.focus() } }}>
        <button id="account-menu-toggle" className="rail-avatar" type="button" aria-label={t('Account actions')} title={props.userId} aria-expanded={account} onClick={() => setAccount(value => !value)}>{Array.from(props.userId)[0]?.toUpperCase()}</button>
        {account && <div className="account-popover" role="group" aria-label={t('Account actions')}>
          <div className="rail-account-heading"><strong>{props.userId}</strong><span>{t(props.ready ? 'Personal workspace' : 'Host unavailable')}</span></div>
          <button type="button" onClick={() => { setAccount(false); props.onSettings() }}><Settings2 size={17} />{t('Settings')}</button>
          <button type="button" className="stop-host" disabled={!props.ready || props.stopping} onClick={props.onStop}><Power size={17} />{t(props.stopping ? 'Stopping Host…' : 'Stop Host')}</button>
          {props.error && <p className="feedback" role="alert">{t(props.error)}</p>}
        </div>}
      </div>
    </div>
  </nav>
}
