import type { Language } from './languages.cjs'
import { translate } from './translate.ts'

export function weekdayLabels(language: string) {
  const formatter = new Intl.DateTimeFormat(language, { weekday: 'short', timeZone: 'UTC' })
  return Array.from({ length: 7 }, (_, day) => formatter.format(new Date(Date.UTC(2026, 0, 5 + day))))
}

export function intervalLabel(language: Language, count: number, unit: 'second' | 'minute' | 'hour' | 'day') {
  const duration = new Intl.NumberFormat(language, { style: 'unit', unit, unitDisplay: 'long', maximumFractionDigits: 0 }).format(count)
  return translate(language, 'Every') + ' ' + duration
}
