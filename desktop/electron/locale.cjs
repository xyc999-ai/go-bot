const { languages, isLanguage, resolveLanguage } = require('./locales/languages.cjs')
const messages = require('./locales/messages.json')
function nativeText(language, key) {
  const dictionary = Object.hasOwn(messages, language) ? messages[language] : messages.en
  return Object.hasOwn(dictionary, key) ? dictionary[key] : key
}
module.exports = { languages, isLanguage, resolveLanguage, nativeText }
