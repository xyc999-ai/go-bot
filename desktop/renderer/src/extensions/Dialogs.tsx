import { createContext, useContext, useEffect, useRef, useState, type ReactNode } from 'react'
import { useLocale } from '../locale'
import './extensions.css'

type Confirmation = { message: string; resolve: (accepted: boolean) => void }
const ConfirmContext = createContext<((message: string) => Promise<boolean>) | undefined>(undefined)

// Use the app's own dialog surface. Native window.confirm blocks the renderer
// and prevents live extension state from updating while the user is deciding.
export function ExtensionDialogs({ children }: { children: ReactNode }) {
  const { t } = useLocale()
  const [pending, setPending] = useState<Confirmation>()
  const current = useRef<Confirmation | undefined>(undefined)
  const dialog = useRef<HTMLDialogElement>(null)
  const opener = useRef<Element | null>(null)
  useEffect(() => { if (pending) dialog.current?.showModal() }, [pending])
  useEffect(() => () => { current.current?.resolve(false) }, [])
  function finish(accepted: boolean) {
    const request = current.current
    current.current = undefined
    dialog.current?.close()
    setPending(undefined)
    request?.resolve(accepted)
    requestAnimationFrame(() => { if (opener.current instanceof HTMLElement && opener.current.isConnected) opener.current.focus() })
  }
  function confirm(message: string) {
    if (current.current) return Promise.resolve(false)
    opener.current = document.activeElement
    return new Promise<boolean>(resolve => { const request = { message, resolve }; current.current = request; setPending(request) })
  }
  return <ConfirmContext.Provider value={confirm}>{children}
    <dialog ref={dialog} className="extension-confirm" aria-labelledby="extension-confirm-title" aria-describedby="extension-confirm-copy" onCancel={event => { event.preventDefault(); finish(false) }} onClick={event => {
      if (event.target !== event.currentTarget) return
      const rect = event.currentTarget.getBoundingClientRect()
      if (event.clientX < rect.left || event.clientX > rect.right || event.clientY < rect.top || event.clientY > rect.bottom) finish(false)
    }}>
      {pending && <><h2 id="extension-confirm-title">{t('Confirm change')}</h2><p id="extension-confirm-copy">{t(pending.message)}</p><div className="extension-actions"><button className="model-secondary" type="button" autoFocus onClick={() => finish(false)}>{t('Cancel')}</button><button className="account-submit" type="button" onClick={() => finish(true)}>{t('Confirm')}</button></div></>}
    </dialog>
  </ConfirmContext.Provider>
}
export function useExtensionConfirm() {
  const confirm = useContext(ConfirmContext)
  if (!confirm) throw new Error('ExtensionDialogs is required')
  return confirm
}
