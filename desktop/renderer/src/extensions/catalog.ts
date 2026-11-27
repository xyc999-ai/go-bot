import type { Localized, PluginTutorial } from '../api/extensions'

export function localize(text: Localized, language: string): string {
 return text[language] || text.en
}


export function selectTutorial(items: PluginTutorial[] | undefined, language: string): PluginTutorial | undefined {
 return items?.find(item=>item.language===language) ?? items?.find(item=>item.language==='en') ?? items?.[0]
}
