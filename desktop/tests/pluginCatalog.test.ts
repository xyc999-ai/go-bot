import assert from 'node:assert/strict'
import test from 'node:test'
import { localize } from '../renderer/src/extensions/catalog.ts'
test('reviewed plugin copy follows the local interface language', () => {
 assert.equal(localize({en:'Browser','zh-CN':'浏览器'}, 'zh-CN'), '浏览器')
 assert.equal(localize({en:'Browser','zh-CN':'浏览器'}, 'en'), 'Browser')
})

test('tutorial selection follows interface language without provider-specific URLs', async()=>{
 const {selectTutorial}=await import('../renderer/src/extensions/catalog.ts')
 const en={language:'en' as const,title:'Connect account',video:'/assets/plugins/service/setup.en.mp4'}
 const zh={language:'zh-CN' as const,title:'连接账号',video:'/assets/plugins/service/setup.zh-CN.mp4'}
 assert.equal(selectTutorial([en,zh],'zh-CN'),zh)
 assert.equal(selectTutorial([en,zh],'en'),en)
 assert.equal(selectTutorial([en],'zh-CN'),en)
 assert.equal(selectTutorial([zh],'en'),zh)
 assert.equal(selectTutorial(undefined,'en'),undefined)
})
