const languages = require('./languages.json')

function isLanguage(value) {
  return typeof value === 'string' && languages.some(item => item.id === value)
}

function resolveLanguage(preferred = [], saved) {
  if (isLanguage(saved)) return saved
  for (const value of preferred) {
    if (typeof value !== 'string') continue
    const tag = value.replace(/_/g, '-').toLowerCase()
    const exact = languages.find(item => item.id.toLowerCase() === tag)
    if (exact) return exact.id
    const parts = tag.split('-')
    if (parts[0] === 'zh') {
      if (parts.includes('hant')) return 'zh-TW'
      if (parts.includes('hans')) return 'zh-CN'
      return parts.some(part => ['tw', 'hk', 'mo'].includes(part)) ? 'zh-TW' : 'zh-CN'
    }
    if (parts[0] === 'pt') return 'pt-BR'
    const base = languages.find(item => item.id === parts[0])
    if (base) return base.id
  }
  return 'en'
}

module.exports = { languages, isLanguage, resolveLanguage }
