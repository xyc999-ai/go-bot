const assert = require('node:assert/strict')
const fs = require('node:fs/promises')
const os = require('node:os')
const path = require('node:path')
const { chromium, _electron } = require('playwright')
const { startFixture } = require('./controller-fixture.cjs')
const { languages, resolveLanguage } = require('../../frontend/src/i18n/languages.cjs')

async function run() {
  const { translate } = await import('../../frontend/src/i18n/translate.ts')
  const root = path.resolve(__dirname, '..')
  const output = path.resolve(process.env.LOCALE_QA_OUTPUT || path.join(root, '../.local/client-locales'))
  const fixture = await startFixture({ webRoot: path.join(root, '../frontend/dist') })
  const profile = await fs.mkdtemp(path.join(os.tmpdir(), 'gobot-locales-'))
  let browser, app
  const errors = []
  const watch = page => { page.setDefaultTimeout(15000); page.on('pageerror', error => errors.push(error.message)) }
  const screenshot = async (page, name) => {
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), name + ' page overflow')
    await page.screenshot({ path: path.join(output, name + '.png'), animations: 'disabled' })
  }
  try {
    await fs.mkdir(output, { recursive: true })
    browser = await chromium.launch({ channel: 'chrome', headless: true })
    for (const preferred of ['en-US', 'zh-CN', 'zh-TW', 'es-MX', 'ja-JP', 'pt-PT', 'fr-CA', 'de-DE', 'ko-KR', 'ar']) {
      const context = await browser.newContext({ locale: preferred, viewport: { width: 1280, height: 900 } })
      const page = await context.newPage(); watch(page)
      await page.goto(fixture.origin)
      const expected = resolveLanguage([preferred])
      const picker = page.locator('.language-picker select').first()
      await picker.waitFor()
      assert.equal(await picker.inputValue(), expected)
      assert.deepEqual(await picker.locator('option').evaluateAll(options => options.map(option => option.value)), languages.map(item => item.id))
      await page.getByRole('heading', { name: translate(expected, 'Open your account'), exact: true }).waitFor()
      if (preferred !== 'ar') await screenshot(page, 'web-account-' + expected)
      if (expected === 'pt-BR') {
        await page.setViewportSize({ width: 375, height: 812 })
        await screenshot(page, 'web-account-compact-pt-BR')
        const brand = await page.locator('.account-card-heading .brand').boundingBox()
        const preferences = await page.locator('.account-preferences').boundingBox()
        assert.ok(brand.y + brand.height <= preferences.y || brand.x + brand.width <= preferences.x, 'account controls do not overlap')
      }
      const next = expected === 'ja' ? 'fr' : 'ja'
      await picker.selectOption(next); await page.reload()
      assert.equal(await picker.inputValue(), next, 'saved choice overrides browser language')
      await context.close()
    }
    const context = await browser.newContext({ locale: 'fr-CA', viewport: { width: 1440, height: 960 } })
    const web = await context.newPage(); watch(web); await web.goto(fixture.origin)
    const launches = []
    web.on('request', request => { if (request.url().endsWith('/v1/host/start')) launches.push(request.postDataJSON()) })
    await web.locator('input[autocomplete=username]').fill('locale-fixture')
    await web.locator('button.account-submit').click()
    await web.getByText('desktop-plan.txt', { exact: true }).waitFor()
    assert.deepEqual(launches, [{ welcomeLocale: 'fr' }], 'the selected language reaches Controller for the initial welcome')

    const launchDesktop = async () => {
      app = await _electron.launch({
        ...(process.env.DESKTOP_EXECUTABLE ? { executablePath: path.resolve(process.env.DESKTOP_EXECUTABLE), args: [root] } : { args: [root] }),
        env: { ...process.env, GOBOT_DESKTOP_USER_DATA: profile, GOBOT_DESKTOP_CONTROLLER: fixture.origin },
      })
      const page = await app.firstWindow(); watch(page)
      await app.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows()[0].setContentSize(1440, 998))
      await page.locator('.language-picker select').first().waitFor()
      return page
    }
    let desktop = await launchDesktop()
    await desktop.locator('input[autocomplete=username]').fill('locale-fixture')
    await desktop.locator('button.account-submit').click()
    await desktop.getByText('desktop-plan.txt', { exact: true }).waitFor()
    for (const [name, page] of [['web', web], ['desktop', desktop]]) {
      for (const { id } of languages) {
        await page.locator('.language-picker select').first().selectOption(id)
        await page.waitForFunction(language => document.documentElement.lang === language, id)
        await page.getByRole('button', { name: translate(id, 'Settings'), exact: true }).click()
        await page.getByRole('heading', { name: translate(id, 'Models'), exact: true }).waitFor()
        if (name === 'desktop') {
          await page.waitForTimeout(75)
          const file = await app.evaluate(({ Menu }) => Menu.getApplicationMenu().items.find(item => item.label !== 'Go Bot').label)
          assert.equal(file, translate(id, 'File'))
        }
        await screenshot(page, name + '-settings-' + id)
      }
    }
    await desktop.locator('.language-picker select').first().selectOption('ja')
    await desktop.waitForTimeout(100)
    await assert.rejects(desktop.evaluate(() => window.desktop.setLanguage('invalid')), /Invalid language/)
    await app.close(); app = undefined
    desktop = await launchDesktop()
    assert.equal(await desktop.locator('.language-picker select').first().inputValue(), 'ja', 'Desktop retains language after app restart')
    assert.equal((await desktop.evaluate(() => window.desktop.configuration())).language, 'ja')
    assert.deepEqual(errors, [])
    console.log('PASS: all nine Web/Desktop languages, browser matching, English fallback, saved preferences, native menus, Controller welcome compatibility and app restart; screenshots:', output)
  } finally {
    if (app) await app.close()
    if (browser) await browser.close()
    await fixture.close()
    await fs.rm(profile, { recursive: true, force: true })
  }
}
run().catch(error => { console.error(error); process.exitCode = 1 })
