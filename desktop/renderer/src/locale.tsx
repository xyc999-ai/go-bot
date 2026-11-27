import { createContext, useContext, useEffect, useState, type ReactNode } from 'react'
import { Languages } from 'lucide-react'
import { translate, type Language } from './messages'
import locale from '../../../frontend/src/i18n/languages.cjs'

const { languages, resolveLanguage } = locale

const preferenceKey = 'go-bot.desktop-language'

function initialLanguage(native?: Language): Language {
  let saved: string | null = null
  try {
    saved = localStorage.getItem(preferenceKey)
  } catch {
    // A browser may disable storage for this origin.
  }
  return resolveLanguage(native ? [native] : navigator.languages?.length ? navigator.languages : [navigator.language], saved)
}

type Locale = {
  language: Language
  setLanguage: (language: Language) => void
  t: (message: string, values?: Record<string, string | number>) => string
}

const LocaleContext = createContext<Locale | undefined>(undefined)

export function LocaleProvider({ children, initial }: { children: ReactNode; initial?: Language }) {
  const [language, setSelectedLanguage] = useState(() => initialLanguage(initial))

  useEffect(() => {
    document.documentElement.lang = language
    void window.desktop.setLanguage(language).catch(() => {})
  }, [language])

  function setLanguage(next: Language) {
    setSelectedLanguage(next)
    try {
      localStorage.setItem(preferenceKey, next)
    } catch {
      // The selected language still applies until the page is reloaded.
    }
  }

  return <LocaleContext.Provider value={{
    language,
    setLanguage,
    t: (message, values) => translate(language, message, values),
  }}>{children}</LocaleContext.Provider>
}

export function useLocale() {
  const locale = useContext(LocaleContext)
  if (!locale) throw new Error('LocaleProvider is required')
  return locale
}

export function LanguagePicker() {
  const { language, setLanguage, t } = useLocale()
  return <label className="language-picker">
    <Languages size={16} aria-hidden="true" />
    <span className="sr-only">{t('Language')}</span>
    <select aria-label={t('Language')} value={language} onChange={event => {
      const next = languages.find(option => option.id === event.currentTarget.value)
      if (next) setLanguage(next.id)
    }}>
      {languages.map(option => <option key={option.id} value={option.id} lang={option.id}>{option.name}</option>)}
    </select>
  </label>
}
