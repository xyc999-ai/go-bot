import { useEffect, useRef } from 'react'
import { Cpu, X } from 'lucide-react'
import { useLocale } from '../locale'

export function ModelRequiredDialog({ selection, message, keepDraft = true, onClose, onSettings, onChoose }: {
  selection?: boolean; message?: string; keepDraft?: boolean; onClose: () => void; onSettings: () => void; onChoose?: () => void
}) {
  const { t } = useLocale()
  const dialog = useRef<HTMLDialogElement>(null)
  useEffect(() => { dialog.current?.showModal() }, [])
  return <dialog className="model-required-dialog" ref={dialog} onCancel={onClose} onClick={event => {
    if (event.target === event.currentTarget) {
      const box = event.currentTarget.getBoundingClientRect()
      if (event.clientX < box.left || event.clientX > box.right || event.clientY < box.top || event.clientY > box.bottom) onClose()
    }
  }} aria-labelledby="model-required-title">
    <button className="icon-button model-required-close" aria-label={t('Close')} onClick={onClose}><X size={19} /></button>
    <Cpu size={25} strokeWidth={1.5} />
    <h2 id="model-required-title">{t(selection ? 'Choose model' : 'Configure model')}</h2>
    <p>{t(message ?? (selection ? 'The model for this chat is unavailable. Choose another model.' : 'Set up a model to start chatting.'))}</p>
    {keepDraft && <small>{t('Your message and attachments are kept in the composer.')}</small>}
    <div className="model-required-actions">
      {selection && onChoose && <button className="account-submit" onClick={onChoose}>{t('Choose model')}</button>}
      <button className={selection ? 'text-button' : 'account-submit'} onClick={onSettings}>{t('Open model settings')}</button>
    </div>
  </dialog>
}
