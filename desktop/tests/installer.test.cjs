const assert = require('node:assert/strict')
const { test } = require('node:test')
const { installerStartup } = require('../electron/installer.cjs')

test('Windows installation maintains shortcuts with correctly separated executable arguments', () => {
  const calls = []
  const spawn = (...args) => { calls.push(args); return { on() {}, unref() {} } }
  const options = { platform: 'win32', executable: 'C:\\Users\\Jane Doe\\GoBot\\app-0.1.0\\go-bot.exe', spawn }
  assert.equal(installerStartup({ ...options, argv: ['app', '--squirrel-install'] }), true)
  assert.equal(installerStartup({ ...options, argv: ['app', '--squirrel-uninstall'] }), true)
  assert.deepEqual(calls.map(call => call.slice(0, 2)), [
    ['C:\\Users\\Jane Doe\\GoBot\\Update.exe', ['--createShortcut', 'go-bot.exe']],
    ['C:\\Users\\Jane Doe\\GoBot\\Update.exe', ['--removeShortcut', 'go-bot.exe']],
  ])
})

test('normal startup and first run launch the client; obsolete releases do not', () => {
  assert.equal(installerStartup({ platform: 'win32', argv: ['app'] }), false)
  assert.equal(installerStartup({ platform: 'win32', argv: ['app', '--squirrel-firstrun'] }), false)
  assert.equal(installerStartup({ platform: 'darwin', argv: ['app', '--squirrel-install'] }), false)
  assert.equal(installerStartup({ platform: 'win32', argv: ['app', '--squirrel-obsolete'] }), true)
})
