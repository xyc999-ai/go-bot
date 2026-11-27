declare namespace locale {
  type Language = 'en' | 'zh-CN' | 'zh-TW' | 'es' | 'ja' | 'pt-BR' | 'fr' | 'de' | 'ko'
}
declare const locale: {
  languages: readonly { id: locale.Language; name: string }[]
  isLanguage(value: unknown): value is locale.Language
  resolveLanguage(preferred?: readonly string[], saved?: unknown): locale.Language
}
export = locale
