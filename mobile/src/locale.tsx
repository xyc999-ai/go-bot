import { createContext, useContext, useEffect, useState, type ReactNode } from 'react'
import { File, Paths } from 'expo-file-system'
import { getLocales } from 'expo-localization'
import locale from '../../frontend/src/i18n/languages.cjs'
import { translateMobile, type Language } from '../../frontend/src/i18n/translate'

export type { Language } from '../../frontend/src/i18n/translate'
export const { languages } = locale
const { isLanguage, resolveLanguage } = locale
const preference = new File(Paths.document, 'ui-language.txt')
const Locale = createContext({ language: 'en' as Language, changeLanguage: async (_language: Language) => {}, t: (key: string, _values?: Record<string, string | number>) => key })

function deviceLanguage(): Language {
  return resolveLanguage(getLocales().map(locale => locale.languageTag))
}

export function LocaleProvider({ children }: { children: ReactNode }) {
  const [language, setLanguage] = useState(deviceLanguage)
  const [ready, setReady] = useState(false)
  useEffect(() => {
    preference.text().then(value => {
      if (isLanguage(value)) setLanguage(value)
    }).catch(() => {}).finally(() => setReady(true))
  }, [])
  async function changeLanguage(next: Language) {
    if (!preference.exists) preference.create()
    preference.write(next)
    setLanguage(next)
  }
  const t = (key: string, values?: Record<string, string | number>) => translateMobile(language, key, values)
  return <Locale.Provider value={{ language, changeLanguage, t }}>{ready && children}</Locale.Provider>
}

export const useLocale = () => useContext(Locale)
