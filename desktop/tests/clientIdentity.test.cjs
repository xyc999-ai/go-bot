const { test } = require('node:test')
const assert = require('node:assert/strict')
const { computerName, desktopIdentity } = require('../electron/client-identity.cjs')

test('desktop reports the system computer name without a generated installation ID or product prefix', () => {
  const first = desktopIdentity(' xyc的MacBook Air\n', 'darwin')
  assert.deepEqual(first, { type: 'desktop', platform: 'darwin', model: 'xyc的MacBook Air' })
  assert.deepEqual(desktopIdentity('xyc的MacBook Air', 'darwin'), first)
  assert.equal(desktopIdentity('WORK-PC', 'win32').model, 'WORK-PC')
  assert.equal(desktopIdentity('dev-linux', 'linux').model, 'dev-linux')
  assert.equal(desktopIdentity(undefined, 'darwin').model, 'Mac')
  assert.equal(Array.from(desktopIdentity('名'.repeat(130)).model).length, 120)
})

test('macOS prefers the friendly computer name; other systems and failed lookups use the hostname', async () => {
  const execute = async (file, args, options) => {
    assert.equal(file, '/usr/sbin/scutil')
    assert.deepEqual(args, ['--get', 'ComputerName'])
    assert.equal(options.timeout, 2000)
    return { stdout: 'xyc的MacBook Air\n' }
  }
  assert.equal(await computerName('darwin', { execute, hostname: () => 'air.local' }), 'xyc的MacBook Air')
  const failing = async () => { throw new Error('unavailable') }
  assert.equal(await computerName('darwin', { execute: failing, hostname: () => 'air.local' }), 'air.local')
  assert.equal(await computerName('win32', { execute: failing, hostname: () => 'WORK-PC' }), 'WORK-PC')
  assert.equal(await computerName('linux', { execute: failing, hostname: () => 'linux-dev' }), 'linux-dev')
  assert.equal(await computerName('darwin', { execute: failing, hostname: () => { throw new Error('no hostname') } }), '')
})
