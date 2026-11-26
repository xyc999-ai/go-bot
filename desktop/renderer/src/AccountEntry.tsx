import { useState, type FormEvent } from 'react'
import { LanguagePicker, useLocale } from './locale'
import { LoaderCircle } from 'lucide-react'
import { APIError, getModelSettings, registerAccount } from './api/controller'
import { HarnessAvatar } from './HarnessAvatar'
import { ThemeToggle } from './layout/Theme'

export function AccountEntry({ onOpen }: { onOpen: (userId: string, created: boolean) => void }) {
  const { t } = useLocale()
  const [create, setCreate] = useState(false)
  const [name, setName] = useState('')
  const [phase, setPhase] = useState<'creating' | 'starting'>()
  const [error, setError] = useState('')

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setError('')
    try {
      if (create) {
        setPhase('creating')
        await registerAccount(name)
        onOpen(name, true)
      } else {
        setPhase('starting')
        await getModelSettings(name)
        onOpen(name, false)
      }
    } catch (error) {
      if (error instanceof APIError && error.status === 404) {
        setError('Account not found. Create it or check the name.')
      } else if (error instanceof APIError && error.status === 409) {
        setError('This account already exists. Open it instead.')
      } else {
        setError('Could not open the account. Try again.')
      }
    } finally {
      setPhase(undefined)
    }
  }

  return (
    <main className="account-page">
      <form className="account-card" onSubmit={submit}>
        <div className="account-card-heading">
          <div className="brand"><HarnessAvatar className="brand-mark" />Go Bot</div>
          <div className="account-preferences"><ThemeToggle /><LanguagePicker /></div>
        </div>
        <h1>{create ? t('Create an account') : t('Open your account')}</h1>
        <fieldset disabled={Boolean(phase)}>
          <label>{t('Account name')}
            <input autoComplete="username" autoCapitalize="none" spellCheck={false}
              required pattern="[a-z0-9][a-z0-9_\x2d]{0,63}" value={name} onChange={event => setName(event.target.value)}
              title={t('Use 1–64 lowercase letters, digits, underscores or hyphens. Start with a letter or digit.')} />
          </label>
          <button className="account-submit" type="submit">{create ? t('Create account') : t('Open account')}</button>
        </fieldset>
        {phase && <p className="account-status" role="status"><LoaderCircle size={16} />{phase === 'creating' ? t('Creating account…') : t('Opening your account…')}</p>}
        {error && <p className="account-error" role="alert">{t(error)}</p>}
        <button className="account-switch" type="button" disabled={Boolean(phase)} onClick={() => { setCreate(!create); setError('') }}>
          {create ? t('Open an existing account') : t('Create an account')}
        </button>
      </form>
    </main>
  )
}
