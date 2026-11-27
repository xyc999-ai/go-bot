import { chinese } from './chinese.ts'
import mobileAliases from './mobile-aliases.json' with { type: 'json' }
import mobileNotices from './mobile-notices.json' with { type: 'json' }
import nativeChinese from './native-zh-CN.json' with { type: 'json' }
import es from './es.json' with { type: 'json' }
import ja from './ja.json' with { type: 'json' }
import pt from './pt-BR.json' with { type: 'json' }
import fr from './fr.json' with { type: 'json' }
import de from './de.json' with { type: 'json' }
import ko from './ko.json' with { type: 'json' }
import traditional from './zh-TW.json' with { type: 'json' }
import type { Language } from './languages.cjs'

export type { Language } from './languages.cjs'
export type Values = Record<string, string | number>
export const dictionaries: Readonly<Record<Exclude<Language, 'en'>, Readonly<Record<string, string>>>> = {
  'zh-CN': {
    ...Object.fromEntries(Object.entries(mobileAliases).map(([zh, en]) => [en, zh])),
    ...chinese, ...mobileNotices, ...nativeChinese,
    'Save': '保存', '{count} tool calls': '{count} 次工具调用',
    'days': '天', 'Every {count} {unit}': '每 {count} {unit}',
    'Could not load desktop settings.': '无法读取桌面设置。',
    'Complete authorization with the service in your browser, then return here.': '请在浏览器中完成服务授权，再回到这里。',
  },
  'zh-TW': traditional, es, ja, 'pt-BR': pt, fr, de, ko,
}
const aliases: Readonly<Record<string, string>> = {
  ...Object.fromEntries(Object.entries(dictionaries['zh-CN']).map(([en, zh]) => [zh, en])),
  ...mobileAliases,
}

export function translate(language: Language, message: string, values?: Values) {
  const dictionary = language === 'en' ? undefined : dictionaries[language]
  const text = dictionary && Object.hasOwn(dictionary, message) && dictionary[message] ? dictionary[message] : message
  return text.replace(/\{(\w+)\}/g, (token, key: string) => values?.[key] === undefined ? token : String(values[key]))
}

// Mobile keeps its existing Chinese source labels; all new languages use the
// same English message keys and dictionaries as Web and Desktop.
export function translateMobile(language: Language, key: string, values?: Values) {
  const message = language === 'zh-CN' && /[\u3400-\u9fff]/.test(key) ? key : aliases[key] ?? key
  return translate(language, message, values)
}
