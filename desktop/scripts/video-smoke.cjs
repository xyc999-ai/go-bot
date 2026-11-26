const assert = require('node:assert/strict')
const fs = require('node:fs/promises')
const path = require('node:path')
const os = require('node:os')
const { _electron, chromium } = require('playwright')
const { startFixture } = require('./controller-fixture.cjs')

async function run() {
  const web = Boolean(process.env.WEB_REFERENCE_DIST)
  const fixture = await startFixture({ videos: true, webRoot: process.env.WEB_REFERENCE_DIST })
  const profile = await fs.mkdtemp(path.join(os.tmpdir(), 'gobot-video-'))
  const output = path.resolve(process.env.DESKTOP_QA_OUTPUT || 'test-results/video')
  await fs.mkdir(output, { recursive: true })
  let app, browser, page
  try {
    if (web) {
      browser = await chromium.launch({ headless: true, ...(process.env.CHROME_PATH ? { executablePath: process.env.CHROME_PATH } : {}) })
      page = await browser.newPage({ viewport: { width: 1440, height: 829 }, locale: 'en-US' })
      await page.goto(fixture.origin)
    } else {
      app = await _electron.launch({
        ...(process.env.DESKTOP_EXECUTABLE ? { executablePath: process.env.DESKTOP_EXECUTABLE, args: [] } : { args: [path.resolve('.')] }),
        env: { ...process.env, GOBOT_DESKTOP_USER_DATA: profile, GOBOT_DESKTOP_CONTROLLER: fixture.origin },
      })
      page = await app.firstWindow()
      await app.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows()[0].setContentSize(1440, 998))
    }
    page.setDefaultTimeout(15000)
    await page.emulateMedia({ reducedMotion: 'reduce' })
    await page.locator('input[autocomplete=username]').waitFor()
    await page.evaluate(key => localStorage.setItem(key, 'en'), web ? 'go-bot.web-language' : 'go-bot.desktop-language')
    await page.reload()
    await page.locator('input[autocomplete=username]').fill('desktop-fixture')
    await page.locator('button.account-submit').click()
    const card = page.locator('.video-delivery').first(), video = card.locator('video')
    await card.scrollIntoViewIfNeeded()
    await page.waitForFunction(() => document.querySelector('video')?.readyState >= 1)
    assert.ok(await video.evaluate(v => v.duration > 0 && v.paused))
    const capture = async name => {
      await page.mouse.move(1400, 100)
      await page.waitForTimeout(150)
      await (web ? page.locator('body') : page.locator('.desktop-content')).screenshot({ path: path.join(output, `${web ? 'web' : 'desktop'}-${name}.png`), scale: 'css', animations: 'disabled' })
    }
    await capture('video-inline')
    await card.getByRole('button', { name: 'Play video', exact: true }).first().click()
    await page.waitForFunction(() => { const v = document.querySelector('video'); return !v.paused && v.currentTime > 0 })
    await card.getByRole('button', { name: 'Pause video', exact: true }).click()
    assert.ok(await video.evaluate(v => v.paused))
    await card.getByRole('button', { name: 'Mute video', exact: true }).click()
    assert.ok(await video.evaluate(v => v.muted))
    await card.getByRole('slider', { name: 'Video progress' }).fill('2')
    assert.ok(await video.evaluate(v => Math.abs(v.currentTime - 2) < .3))
    await card.getByRole('button', { name: 'Expand video', exact: true }).click()
    await card.locator('dialog.is-fullscreen').waitFor()
    await capture('video-fullscreen')
    await page.keyboard.press('Escape')
    assert.equal(await card.locator('dialog').evaluate(d => d.matches(':modal')), false)
    await card.getByRole('button', { name: 'Play video', exact: true }).first().click()
    await page.getByRole('button', { name: 'Tasks', exact: true }).click()
    await page.waitForFunction(() => document.querySelector('video').paused)
    await page.getByRole('button', { name: 'Chats', exact: true }).click()
    const second = page.locator('.video-delivery').nth(1)
    await second.scrollIntoViewIfNeeded()
    await second.getByRole('button', { name: 'Play video', exact: true }).first().click()
    await page.waitForFunction(() => !document.querySelectorAll('video')[1].paused)
    assert.ok(await video.evaluate(v => v.paused))
    await second.getByRole('button', { name: 'Pause video', exact: true }).click()
    const unsupported = page.locator('.video-delivery').nth(2)
    await unsupported.scrollIntoViewIfNeeded()
    await unsupported.getByText('Video unavailable or unsupported. You can still download the file.').waitFor()
    assert.ok(await unsupported.getByRole('button', { name: 'Download video' }).isEnabled())
    await capture('video-fallback')
    const range = await page.evaluate(async id => {
      const response = await fetch(`/v1/sessions/${id}/files/content?path=/root/harness/playback.webm&userId=desktop-fixture&inline=1`, { headers: { Range: 'bytes=0-31' } })
      return { status: response.status, range: response.headers.get('content-range'), size: (await response.arrayBuffer()).byteLength }
    }, fixture.id)
    assert.equal(range.status, 206); assert.equal(range.size, 32); assert.match(range.range, /^bytes 0-31\//)
    if (app) {
      const filename = path.join(output, 'playback.webm')
      await app.evaluate(({ session }, filename) => session.defaultSession.once('will-download', (_event, item) => item.setSavePath(filename)), filename)
      await card.scrollIntoViewIfNeeded()
      await card.getByRole('button', { name: 'Download video' }).click()
      for (let attempt = 0; attempt < 100; attempt++) {
        try { assert.deepEqual(await fs.readFile(filename), await fs.readFile('scripts/fixtures/playback.webm')); break }
        catch (error) { if (attempt === 99) throw error; await page.waitForTimeout(50) }
      }
    }
    console.log(`PASS ${web ? 'Web' : 'Electron'}: actual decode/play, pause, seek, mute, fullscreen/Escape, navigation pause, single playback, unsupported fallback, ranges${app ? ' and download' : ''}`)
  } catch (error) {
    if (page) await page.screenshot({ path: path.join(output, 'failure.png'), scale: 'css' }).catch(() => {})
    throw error
  } finally { if (app) await app.close(); if (browser) await browser.close(); await fixture.close(); await fs.rm(profile, { recursive: true, force: true }) }
}
run().catch(error => { console.error(error); process.exitCode = 1 })
