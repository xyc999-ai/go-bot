const { test } = require('node:test')
const assert = require('node:assert/strict')
const fs = require('node:fs/promises')
const path = require('node:path')
const os = require('node:os')
const { spawnSync } = require('node:child_process')
const { clientReleaseConfig } = require('../../scripts/client-release-config.cjs')

async function configuration(t) {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), 'gobot-client-version-'))
  t.after(() => fs.rm(root, { recursive: true, force: true }))
  for (const [folder, name] of [['desktop', 'go-bot-desktop'], ['mobile', 'go-bot-mobile'], ['frontend', 'go-bot-web']]) {
    await fs.mkdir(path.join(root, folder))
    await fs.writeFile(path.join(root, folder, 'package.json'), JSON.stringify({ name, version: '0.0.4' }))
  }
  await fs.writeFile(path.join(root, 'mobile/app.json'), JSON.stringify({ expo: { version: '0.0.4' } }))
  return root
}

test('release version comes from desktop and must match every client', async t => {
  const root = await configuration(t)
  assert.deepEqual(clientReleaseConfig(root), { version: '0.0.4' })
  for (const file of ['mobile/package.json', 'frontend/package.json', 'mobile/app.json']) {
    const target = path.join(root, file)
    const original = await fs.readFile(target, 'utf8')
    await fs.writeFile(target, original.replace('0.0.4', '0.0.5'))
    assert.throws(() => clientReleaseConfig(root), /versions must match/)
    await fs.writeFile(target, original)
  }
})

test('desktop package identity is go-bot-desktop', async t => {
  const root = await configuration(t)
  await fs.writeFile(path.join(root, 'desktop/package.json'), JSON.stringify({ name: 'wrong-name', version: '0.0.4' }))
  assert.throws(() => clientReleaseConfig(root), /package name/)
})

async function publish(t, state) {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), 'gobot-publish-client-'))
  t.after(() => fs.rm(root, { recursive: true, force: true }))
  await fs.mkdir(path.join(root, 'release-artifacts'))
  await fs.writeFile(path.join(root, 'release-artifacts', 'package.apk'), 'fixture')
  await fs.writeFile(path.join(root, 'RELEASE_NOTES.md'), 'fixture notes')
  const mock = path.join(root, 'gh')
  await fs.writeFile(mock, `#!/usr/bin/env node
const fs = require('node:fs')
const args = process.argv.slice(2)
fs.appendFileSync(process.env.GH_TEST_LOG, JSON.stringify(args) + '\\n')
if (args[1] === 'view') {
  if (process.env.GH_TEST_STATE === 'missing') process.exit(1)
  console.log(JSON.stringify({ isDraft: process.env.GH_TEST_STATE === 'draft' }))
}
`)
  await fs.chmod(mock, 0o755)
  const result = spawnSync('bash', [path.resolve(__dirname, '../../scripts/publish-client-release.sh')], {
    cwd: root, encoding: 'utf8', env: { ...process.env, PATH: `${root}${path.delimiter}${process.env.PATH}`,
      RUNNER_TEMP: root, RELEASE_TAG: 'my-client-build', RELEASE_VERSION: '0.0.4', RELEASE_COMMIT: 'a'.repeat(40),
      GH_TEST_STATE: state, GH_TEST_LOG: path.join(root, 'calls.jsonl') },
  })
  const calls = (await fs.readFile(path.join(root, 'calls.jsonl'), 'utf8')).trim().split('\n').map(line => JSON.parse(line))
  return { result, calls }
}

test('custom tags create draft releases at the build commit with an automatic version title', { skip: process.platform === 'win32' }, async t => {
  const { result, calls } = await publish(t, 'missing')
  assert.equal(result.status, 0, result.stderr)
  const create = calls.find(args => args[1] === 'create')
  assert.equal(create.at(-1), 'my-client-build')
  assert.equal(create[create.indexOf('--target') + 1], 'a'.repeat(40))
  assert.equal(create[create.indexOf('--title') + 1], 'Go Bot v0.0.4')
  assert.ok(create.includes('--draft'))
  assert.ok(!create.includes('--verify-tag'))
  assert.ok(calls.some(args => args[1] === 'upload'))
})

test('existing draft releases can be updated', { skip: process.platform === 'win32' }, async t => {
  const { result, calls } = await publish(t, 'draft')
  assert.equal(result.status, 0, result.stderr)
  assert.ok(calls.some(args => args[1] === 'edit' && args.includes('--draft')))
  assert.ok(!calls.some(args => args[1] === 'create'))
})

test('published releases cannot be overwritten', { skip: process.platform === 'win32' }, async t => {
  const { result, calls } = await publish(t, 'published')
  assert.notEqual(result.status, 0)
  assert.match(result.stderr, /Refusing to modify a published release/)
  assert.deepEqual(calls.map(args => args[1]), ['view'])
})
