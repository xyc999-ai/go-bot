import { useEffect, useState } from 'react'
import { App } from '../App'
import { setControllerOrigin, setDesktopIdentity } from './bridge'
import './desktop.css'
import { useLocale } from '../locale'

export function DesktopRoot() {
  const { t } = useLocale()
  const [ready, setReady] = useState(false)
  const [error, setError] = useState('')
  useEffect(() => {
    document.documentElement.dataset.platform = window.desktop.platform
    void window.desktop.configuration().then(value => {
      setControllerOrigin(value.controller)
      setDesktopIdentity(value.identity)
      setReady(true)
    }, () => setError('Could not load desktop settings.'))
    return window.desktop.onCommand(command => {
      if (command.type === 'new-chat') window.dispatchEvent(new Event('desktop:new-chat'))
      if (command.type === 'resume') window.dispatchEvent(new Event('desktop:resume'))
      if (command.type === 'navigate' && /^\/chat\/[\da-f-]{36}$/i.test(command.path || '')) {
        window.history.pushState(null, '', command.path)
        window.dispatchEvent(new PopStateEvent('popstate'))
      }
    })
  }, [])
  return <>
    <div className="desktop-titlebar"><span className="desktop-app-name">Go Bot</span></div>
    <div className="desktop-content">{ready ? <App /> : <main className="account-page">{error && <p role="alert">{t(error)}</p>}</main>}</div>
  </>
}
