// Package the same language matcher and menu translations used by the clients.
const fs = require('node:fs')
const path = require('node:path')
const source = path.resolve(__dirname, '../../frontend/src/i18n')
const target = path.resolve(__dirname, '../electron/locales')
const { languages } = require(path.join(source, 'languages.cjs'))
const chinese = require(path.join(source, 'native-zh-CN.json'))
const messages = {}
for (const { id } of languages) {
  const dictionary = id === 'en' ? {} : id === 'zh-CN' ? chinese : require(path.join(source, id + '.json'))
  messages[id] = Object.fromEntries(Object.keys(chinese).map(key => [key, dictionary[key] || key]))
}
fs.mkdirSync(target, { recursive: true })
for (const file of ['languages.cjs', 'languages.json']) fs.copyFileSync(path.join(source, file), path.join(target, file))
fs.writeFileSync(path.join(target, 'messages.json'), JSON.stringify(messages, null, 2) + '\n')
