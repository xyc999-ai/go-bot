const { test } = require('node:test')
const assert = require('node:assert/strict')
const { menuTemplate, trayTemplate } = require('../electron/menus.cjs')

test('menu labels and role actions use one language across Mac and Windows', () => {
  for (const platform of ['darwin', 'win32']) {
    const en = menuTemplate('en', platform, () => {}, () => {})
    const zh = menuTemplate('zh-CN', platform, () => {}, () => {})
    const labels = rows => rows.flatMap(row => [row.label || '', ...labels(row.submenu || [])])
    assert.ok(labels(en).every(label => !/[\u4e00-\u9fff]/.test(label)))
    assert.ok(zh.filter(row => row.label !== 'Go Bot').every(row => /[\u4e00-\u9fff]/.test(row.label)))
    assert.ok(labels(zh).every(label => !label.includes(' / ')))
    assert.equal(zh.find(row => row.label === '编辑').submenu.find(row => row.role === 'copy').label, '复制')
    assert.equal(trayTemplate('zh-CN', () => {})[0].label, '打开 Go Bot')
  }
})
