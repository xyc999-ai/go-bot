import assert from 'node:assert/strict'
import { test } from 'node:test'
import { webClientIdentity, validClientId, encodeClientIdentity, clientDisplayName, clientLastSeen } from '../src/clients/identity.ts'

test('browser reconnects use a persistent identity and never report a machine model', () => {
 const original = Object.getOwnPropertyDescriptor(globalThis, 'localStorage')
 const values = new Map<string, string>([['go-bot-client-id', 'browser-persistent']])
 Object.defineProperty(globalThis, 'localStorage', { configurable: true, value: {
  getItem: (key: string) => values.get(key) ?? null,
  setItem: (key: string, value: string) => values.set(key, value),
 } })
 try {
  const first = webClientIdentity(), second = webClientIdentity()
  assert.deepEqual(first, { id: 'browser-persistent', type: 'web' })
  assert.deepEqual(second, first)
  assert.ok(validClientId(first.id))
  assert.equal(clientDisplayName({ ...first, model: 'private model', platform: 'macOS' }), 'Web')
 } finally {
  if (original) Object.defineProperty(globalThis, 'localStorage', original)
  else Reflect.deleteProperty(globalThis, 'localStorage')
 }
})

test('native identity survives the header encoding and missing model has a usable label', () => {
 const identity = { id: 'native-fixture', type: 'mobile' as const, platform: 'android', model: '测试手机' }
 assert.deepEqual(JSON.parse(decodeURIComponent(encodeClientIdentity(identity))), identity)
 assert.equal(clientDisplayName(identity), '测试手机')
 assert.equal(clientDisplayName({ id: 'native-fixture', type: 'mobile', platform: 'ios' }), 'iPhone')
 const time = new Date(2026, 10, 26, 4, 5).getTime()
 assert.equal(clientLastSeen(time, 'zh-CN'), '11月26日 04:05')
 assert.equal(clientLastSeen(time, 'en'), 'Nov 26, 04:05')
 assert.equal(clientLastSeen(NaN, 'en'), '')
})
