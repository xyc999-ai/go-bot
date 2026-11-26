import assert from 'node:assert/strict'
import { test } from 'node:test'
import { clientIdentityKey, encodeClientIdentity, clientDisplayName, clientLastSeen } from '../src/clients/identity.ts'

test('this device is matched by type, platform and name rather than an installation ID', () => {
 const own = { type: 'mobile' as const, platform: 'ios', model: 'iPhone 13 mini' }
 const oldInstall = { ...own, id: 'old-installation' }
 const newInstall = { ...own, id: 'new-installation' }
 assert.equal(clientIdentityKey(oldInstall), clientIdentityKey(newInstall))
 assert.equal(clientIdentityKey(own), clientIdentityKey(newInstall))
 for (const other of [{ ...own, type: 'desktop' as const }, { ...own, platform: 'android' }, { ...own, model: 'iPhone 14' }]) {
  assert.notEqual(clientIdentityKey(other), clientIdentityKey(own))
 }
})

test('native identity survives the header encoding and missing model has a usable label', () => {
 const identity = { type: 'mobile' as const, platform: 'android', model: '测试手机' }
 assert.deepEqual(JSON.parse(decodeURIComponent(encodeClientIdentity(identity))), identity)
 assert.equal(clientDisplayName(identity), '测试手机')
 assert.equal(clientDisplayName({ type: 'mobile', platform: 'ios' }), 'iPhone')
 assert.equal(clientDisplayName({ type: 'desktop', platform: 'darwin', model: 'xyc的MacBook Air' }), 'xyc的MacBook Air')
 assert.equal(clientDisplayName({ type: 'desktop', platform: 'darwin', model: 'Go Bot · macOS' }), 'macOS')
 const time = new Date(2026, 10, 26, 4, 5).getTime()
 assert.equal(clientLastSeen(time, 'zh-CN'), '11月26日 04:05')
 assert.equal(clientLastSeen(time, 'en'), 'Nov 26, 04:05')
 assert.equal(clientLastSeen(NaN, 'en'), '')
})
