const { test } = require('node:test')
const assert = require('node:assert/strict')
const fs = require('node:fs/promises')
const os = require('node:os')
const path = require('node:path')
const { collectArtifacts, macChecks, hashFile } = require('../scripts/collect-desktop-release.cjs')

async function fixture(t) {
  const dir = await fs.mkdtemp(path.join(os.tmpdir(), 'gobot-release-artifacts-'))
  t.after(() => fs.rm(dir, { recursive: true, force: true }))
  for (const arch of ['arm64', 'x64']) {
    const base = `go-bot-1.2.3-macos-${arch}`
    const files = []
    for (const type of ['dmg', 'zip']) {
      const name = `${base}.${type}`
      await fs.writeFile(path.join(dir, name), `fixture ${arch} ${type}`)
      files.push({ name, sha256: await hashFile(path.join(dir, name)) })
    }
    await fs.writeFile(path.join(dir, `${base}.sha256`), files.map(file => `${file.sha256}  ${file.name}\n`).join(''))
    await fs.writeFile(path.join(dir, `${base}.verification.json`), JSON.stringify({
      version: '1.2.3', platform: 'darwin', arch, teamId: 'ABCDEFGHIJ', checks: macChecks, files,
      notarization: { container: 'dmg', status: 'Accepted', id: '12345678-1234-1234-1234-123456789abc' },
    }))
  }
  await fs.writeFile(path.join(dir, 'go-bot-1.2.3-windows-x64-setup.exe'), 'unsigned installer fixture')
  return dir
}

test('complete verified releases produce reproducible checksums and explicit unsigned Windows notes', async t => {
  const dir = await fixture(t)
  const notes = await collectArtifacts(dir, '1.2.3', 'ABCDEFGHIJ')
  assert.match(notes, /Windows x64: unsigned/)
  assert.match(notes, /127\.0\.0\.1:8080/)
  const first = await fs.readFile(path.join(dir, 'SHA256SUMS'), 'utf8')
  assert.equal(first.trim().split('\n').length, 9)
  await collectArtifacts(dir, '1.2.3', 'ABCDEFGHIJ')
  assert.equal(await fs.readFile(path.join(dir, 'SHA256SUMS'), 'utf8'), first)
})

test('tampered signed artifacts cannot be published', async t => {
  const dir = await fixture(t)
  await fs.appendFile(path.join(dir, 'go-bot-1.2.3-macos-arm64.dmg'), 'tamper')
  await assert.rejects(collectArtifacts(dir, '1.2.3', 'ABCDEFGHIJ'), /hash mismatch/)
})

test('missing architectures and unexpected files prevent release assembly', async t => {
  const dir = await fixture(t)
  await fs.writeFile(path.join(dir, 'unexpected.p8'), 'not a key')
  await assert.rejects(collectArtifacts(dir, '1.2.3', 'ABCDEFGHIJ'), /unexpected/)
  await fs.unlink(path.join(dir, 'unexpected.p8'))
  await fs.unlink(path.join(dir, 'go-bot-1.2.3-macos-x64.dmg'))
  await assert.rejects(collectArtifacts(dir, '1.2.3', 'ABCDEFGHIJ'), /Missing/)
})

test('wrong signing teams and omitted verification gates prevent publishing', async t => {
  const dir = await fixture(t)
  await assert.rejects(collectArtifacts(dir, '1.2.3', 'KLMNOPQRST'), /verification report/)
  const file = path.join(dir, 'go-bot-1.2.3-macos-arm64.verification.json')
  const report = JSON.parse(await fs.readFile(file, 'utf8'))
  report.checks = report.checks.filter(check => check !== 'dmg-gatekeeper')
  await fs.writeFile(file, JSON.stringify(report))
  await assert.rejects(collectArtifacts(dir, '1.2.3', 'ABCDEFGHIJ'), /verification report/)
})
