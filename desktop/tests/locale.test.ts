import '../../frontend/tests/locale.test.ts'
import test from 'node:test'
import assert from 'node:assert/strict'
import locale from '../electron/locale.cjs'
import { translate } from '../../frontend/src/i18n/translate.ts'
import { menuTemplate, trayTemplate } from '../electron/menus.cjs'

test('packaged native menu and tray dictionaries agree with renderer languages', () => {
  for (const { id } of locale.languages) {
    const menu = menuTemplate(id, 'darwin', () => {}, () => {})
    assert.equal(menu[1].label, translate(id, 'File'))
    assert.equal(menu[2].submenu.find((item: { role?: string }) => item.role === 'copy').label, translate(id, 'Copy'))
    assert.equal(trayTemplate(id, () => {})[0].label, translate(id, 'Open Go Bot'))
    assert.equal(locale.nativeText(id, 'A reply is ready'), translate(id, 'A reply is ready'))
  }
})
