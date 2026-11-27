import test from 'node:test'
import assert from 'node:assert/strict'
import locale from '../src/i18n/languages.cjs'
import source from '../src/i18n/en.json' with { type: 'json' }
import { dictionaries, translate, translateMobile } from '../src/i18n/translate.ts'
import { intervalLabel, weekdayLabels } from '../src/i18n/format.ts'
import { localize, selectTutorial } from '../src/extensions/catalog.ts'

test('language matching respects saved choices and ordered system preferences', () => {
  assert.equal(locale.resolveLanguage(['ja-JP'], 'fr'), 'fr')
  assert.equal(locale.resolveLanguage(['xx', 'es-MX', 'de']), 'es')
  assert.equal(locale.resolveLanguage(['xx', 'fr-CA'], 'invalid'), 'fr')
  assert.equal(locale.resolveLanguage(['pt-PT']), 'pt-BR')
  assert.equal(locale.resolveLanguage(['zh-Hant-HK']), 'zh-TW')
  assert.equal(locale.resolveLanguage(['zh_HK']), 'zh-TW')
  assert.equal(locale.resolveLanguage(['zh-MO']), 'zh-TW')
  assert.equal(locale.resolveLanguage(['zh-Hans-TW']), 'zh-CN')
  assert.equal(locale.resolveLanguage(['zh-SG']), 'zh-CN')
  assert.equal(locale.resolveLanguage(['en-GB', 'ja']), 'en')
  assert.equal(locale.resolveLanguage(['ar', 'xx']), 'en')
  assert.equal(locale.resolveLanguage([]), 'en')
  assert.equal(locale.isLanguage('__proto__'), false)
  assert.deepEqual(locale.languages.map(item => item.id), ['en', 'zh-CN', 'zh-TW', 'es', 'ja', 'pt-BR', 'fr', 'de', 'ko'])
})

test('every new dictionary covers client and native menu messages and preserves placeholders', () => {
  const tokens = (text: string) => (text.match(/\$?\{[^{}]+\}/g) ?? []).sort()
  for (const { id } of locale.languages) {
    if (id === 'en') continue
    const dictionary = dictionaries[id]
    for (const key of Object.keys(source)) {
      assert.ok(Object.hasOwn(dictionary, key) && dictionary[key].trim(), `${id}: ${key}`)
      assert.deepEqual(tokens(dictionary[key]), tokens(key), `${id}: ${key}`)
      assert.doesNotMatch(dictionary[key], /ZXQ\d|⟦\d|⟧/, `${id}: ${key}`)
      for (const protocol of ['OpenAI Responses', 'Anthropic Messages']) {
        if (key.includes(protocol)) assert.ok(dictionary[key].includes(protocol), `${id}: changed protocol name ${protocol}`)
      }
    }
  }
})

test('Web, Desktop and mobile resolve shared labels, interpolate values and fall back to English', () => {
  for (const { id } of locale.languages) {
    assert.equal(translateMobile(id, '语言'), translate(id, 'Language'))
    assert.equal(translateMobile(id, '模型'), translate(id, 'Models'))
    assert.equal(translate(id, 'New untranslated message'), 'New untranslated message')
    assert.equal(translateMobile(id, 'New untranslated message'), 'New untranslated message')
    assert.equal(translate(id, 'Keep {name}', { name: 'client' }), 'Keep client')
    assert.ok(translate(id, '{count} tool calls', { count: 3 }).includes('3'))
    assert.ok(!translate(id, '{count} tool calls', { count: 3 }).includes('{count}'))
    assert.equal(translate(id, 'toString'), 'toString')
  }
  assert.equal(translate('zh-TW', 'Models'), '模型')
  assert.equal(translate('zh-TW', 'Save'), '儲存')
  assert.equal(translate('ja', 'Working'), '処理中')
  assert.equal(translate('ko', 'Every'), '매')
  assert.equal(translate('en', 'Keep {name}'), 'Keep {name}')
})

test('weekday and interval formatting uses the selected language', () => {
  assert.equal(weekdayLabels('ja')[0], '月')
  assert.match(weekdayLabels('de')[0], /Mo/)
  assert.equal(intervalLabel('en', 1, 'hour'), 'Every 1 hour')
  assert.equal(intervalLabel('en', 2, 'hour'), 'Every 2 hours')
  assert.match(intervalLabel('es', 2, 'hour'), /Cada 2 horas/)
  assert.match(intervalLabel('ko', 2, 'hour'), /매 2시간/)
})

test('plugin metadata and tutorials use exact translations or English fallback', () => {
  const text = { en: 'Browser', 'zh-CN': '浏览器', ja: 'ブラウザー' }
  assert.equal(localize(text, 'ja'), 'ブラウザー')
  assert.equal(localize(text, 'fr'), 'Browser')
  assert.equal(localize({ ...text, ja: '' }, 'ja'), 'Browser')
  const en = { language: 'en', title: 'Guide', video: 'en.mp4' }
  const ja = { language: 'ja', title: 'ガイド', video: 'ja.mp4' }
  assert.equal(selectTutorial([en, ja], 'ja'), ja)
  assert.equal(selectTutorial([en, ja], 'fr'), en)
})
