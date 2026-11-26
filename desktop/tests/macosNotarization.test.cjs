const { test } = require('node:test')
const assert = require('node:assert/strict')
const fs = require('node:fs/promises')
const os = require('node:os')
const path = require('node:path')
const { notarizeDmg, authorizationArgs } = require('../scripts/macos-notarization.cjs')

const id = '12345678-1234-1234-1234-123456789abc'
const credentials = { appleApiKey: '/test/not-a-real-key.p8', appleApiKeyId: 'EXAMPLEKEY' }

async function fixture(t, status, logAvailable = true) {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'gobot-notary-test-'))
  t.after(() => fs.rm(dir, { recursive: true, force: true }))
  const file = path.join(dir, 'release.dmg')
  await fs.writeFile(file, 'DMG fixture')
  const calls = []
  return {
    file, record: path.join(dir, 'submission.json'), calls,
    options: {
      timeoutMinutes: 1,
      jsonTool: async args => {
        calls.push(args)
        if (args[0] === 'submit') return { id }
        if (args[0] === 'info') return { id, status }
        if (!logAvailable) throw new Error('log unavailable')
        return { issues: [] }
      },
      waitTool: async args => { calls.push(args); return 69 },
    },
  }
}

test('one DMG submission is enough; acceptance is checked even if wait times out', async t => {
  const f = await fixture(t, 'Accepted')
  const record = await notarizeDmg(f.file, credentials, f.record, f.options)
  assert.equal(record.phase, 'accepted')
  assert.equal(f.calls.filter(args => args[0] === 'submit').length, 1)
  assert.equal(f.calls[0].includes('--wait'), false)
  assert.equal(f.calls.find(args => args[0] === 'wait')[1], id)
  assert.match(record.sha256, /^[0-9a-f]{64}$/)
})

test('pending Apple processing fails the release and retains its ID for diagnosis', async t => {
  const f = await fixture(t, 'In Progress', false)
  await assert.rejects(notarizeDmg(f.file, credentials, f.record, f.options), /In Progress; no public release/)
  const record = JSON.parse(await fs.readFile(f.record, 'utf8'))
  assert.equal(record.id, id)
  assert.equal(record.phase, 'pending')
  assert.equal(f.calls.filter(args => args[0] === 'submit').length, 1)
})

test('a rejected scan cannot become a release even when upload succeeded', async t => {
  const f = await fixture(t, 'Invalid')
  await assert.rejects(notarizeDmg(f.file, credentials, f.record, f.options), /Invalid; no public release/)
  assert.equal(JSON.parse(await fs.readFile(f.record, 'utf8')).phase, 'rejected')
  assert.ok(await fs.stat(path.join(path.dirname(f.record), 'notarization-log.json')))
})

test('personal and team API keys produce distinct authentication arguments', () => {
  assert.equal(authorizationArgs(credentials).includes('--issuer'), false)
  assert.deepEqual(authorizationArgs({ ...credentials, appleApiIssuer: 'example-issuer' }).slice(-2), ['--issuer', 'example-issuer'])
})
