const assert = require('node:assert/strict')
async function verifySidebar(page) {
  const separator = page.getByRole('separator', { name: 'Resize chat sidebar', exact: true })
  await separator.waitFor()
  const metrics = () => page.evaluate(() => {
    const sidebar = document.querySelector('#chat-sidebar').getBoundingClientRect()
    const workspace = document.querySelector('.workspace:not([hidden])').getBoundingClientRect()
    const input = document.querySelector('.chat-input').getBoundingClientRect()
    return { sidebar: sidebar.width, left: workspace.left, main: workspace.width, input: input.width, overflow: document.documentElement.scrollWidth > innerWidth }
  })
  const before = await metrics(), box = await separator.boundingBox()
  await page.mouse.move(box.x + box.width / 2, box.y + 100)
  await page.mouse.down(); await page.mouse.move(box.x + box.width / 2 + 120, box.y + 100, { steps: 10 }); await page.mouse.up()
  await page.waitForTimeout(350)
  const after = await metrics()
  assert.ok(after.sidebar > before.sidebar + 90)
  assert.ok(Math.abs((after.left - before.left) - (after.sidebar - before.sidebar)) < 2)
  assert.ok(Math.abs((before.main - after.main) - (after.sidebar - before.sidebar)) < 2, 'chat resizes with sidebar')
  assert.ok(after.input <= before.input, 'composer follows the remaining space')
  assert.equal(after.overflow, false)
  const persisted = after.sidebar
  // Closing and opening keeps the selected width.
  await page.getByRole('button', { name: 'Chats', exact: true }).click()
  await page.getByRole('button', { name: 'Chats', exact: true }).click()
  await separator.waitFor(); await page.waitForTimeout(350)
  assert.ok(Math.abs((await metrics()).sidebar - persisted) < 2)
  await separator.focus(); await separator.press('Home'); await page.waitForTimeout(300)
  assert.equal(Math.round((await metrics()).sidebar), 220)
  await separator.press('End'); await page.waitForTimeout(300)
  assert.ok((await metrics()).sidebar <= 480)
  const start = await metrics(), edge = await separator.boundingBox()
  await page.mouse.move(edge.x + 3, edge.y + 100); await page.mouse.down(); await page.mouse.move(edge.x - 100, edge.y + 100)
  await page.keyboard.press('Escape'); await page.mouse.up(); await page.waitForTimeout(300)
  assert.ok(Math.abs((await metrics()).sidebar - start.sidebar) < 2)
  await separator.dblclick(); await page.waitForTimeout(350)
  assert.equal(Math.round((await metrics()).sidebar), 264)
  assert.equal(await page.evaluate(() => document.body.classList.contains('sidebar-resizing')), false)
}
module.exports = { verifySidebar }
