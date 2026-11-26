import assert from 'node:assert/strict'
import test from 'node:test'
import { extensionOperations } from '../renderer/src/extensions/operations.ts'

test('installation survives leaving and reopening the plugin screen without starting twice', async () => {
  const client = {}, operations = extensionOperations(client)
  let finish!: () => void, calls = 0, notifications = 0
  const unsubscribe = operations.subscribe(() => notifications++)
  const pending = operations.run('install:browser', () => new Promise<void>(resolve => { calls++; finish = resolve }), () => 'failed', 'installed')
  assert.equal(operations.snapshot().busy, 'install:browser')
  unsubscribe() // The screen closes while the request stays in flight.
  const reopened = extensionOperations(client)
  assert.equal(reopened, operations)
  assert.equal(reopened.snapshot().busy, 'install:browser')
  assert.equal(await reopened.run('install:office', async () => { calls++ }, () => 'failed', 'installed'), false)
  assert.equal(calls, 1)
  assert.equal(extensionOperations({}).snapshot().busy, '')
  finish()
  assert.equal(await pending, true)
  assert.deepEqual(reopened.snapshot(), { busy: '', error: '', notice: 'installed', revision: 1 })
  assert.equal(notifications, 1)
})

test('a failed background install remains visible on return and permits a retry', async () => {
  const operations = extensionOperations({})
  assert.equal(await operations.run('install:x', async () => { throw new Error('download failed') }, () => 'Could not install', 'installed'), false)
  assert.equal(operations.snapshot().error, 'Could not install')
  assert.equal(operations.snapshot().busy, '')
  assert.equal(await operations.run('install:x', async () => {}, () => 'failed', 'installed'), true)
  assert.equal(operations.snapshot().error, '')
  assert.equal(operations.snapshot().notice, 'installed')
})
