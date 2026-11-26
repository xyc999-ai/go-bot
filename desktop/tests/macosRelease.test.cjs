const { test } = require('node:test')
const assert = require('node:assert/strict')
const fs = require('node:fs')
const os = require('node:os')
const path = require('node:path')
const { macReleaseConfig, skipNonCodeResource } = require('../scripts/macos-release-config.cjs')
const { verifyIdentity } = require('../scripts/verify-macos-release.cjs')

const env = {
  GOBOT_DESKTOP_RELEASE: '1', APPLE_TEAM_ID: 'ABCDEFGHIJ',
  APPLE_SIGNING_IDENTITY: 'Developer ID Application: Example (ABCDEFGHIJ)',
  APPLE_API_KEY: __filename, APPLE_API_KEY_ID: 'EXAMPLEKEY',
}

test('release builds fail closed for absent credentials and wrong signing teams', () => {
  assert.deepEqual(macReleaseConfig({}, 'win32'), {})
  for (const key of ['APPLE_SIGNING_IDENTITY', 'APPLE_TEAM_ID', 'APPLE_API_KEY', 'APPLE_API_KEY_ID']) {
    assert.throws(() => macReleaseConfig({ ...env, [key]: '' }, 'darwin'), new RegExp(key))
  }
  assert.throws(() => macReleaseConfig(env, 'win32'), /macOS/)
  assert.throws(() => macReleaseConfig({ ...env, APPLE_SIGNING_IDENTITY: 'Apple Distribution: Example (ABCDEFGHIJ)' }, 'darwin'), /Developer ID/)
  assert.throws(() => macReleaseConfig({ ...env, APPLE_TEAM_ID: 'KLMNOPQRST' }, 'darwin'), /APPLE_TEAM_ID/)
  assert.throws(() => macReleaseConfig({ ...env, APPLE_API_KEY: '/missing/notary-key.p8' }, 'darwin'), /private key file/)
})

test('personal keys omit the issuer while team keys include it', () => {
  const personal = macReleaseConfig(env, 'darwin')
  assert.equal(Object.hasOwn(personal.osxNotarize, 'appleApiIssuer'), false)
  assert.equal(personal.osxSign.hardenedRuntime, true)
  assert.equal(personal.osxSign.continueOnError, false)
  const team = macReleaseConfig({ ...env, APPLE_API_ISSUER: 'example-issuer', APPLE_SIGNING_KEYCHAIN: '/temp/signing.keychain-db' }, 'darwin')
  assert.equal(team.osxNotarize.appleApiIssuer, 'example-issuer')
  assert.equal(team.osxSign.keychain, '/temp/signing.keychain-db')
})

test('Mach-O code is signed regardless of its extension; binary resources are only sealed', () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'go-bot-signing-test-'))
  try {
    assert.equal(skipNonCodeResource(dir), false)
    for (const magic of ['feedface', 'cefaedfe', 'feedfacf', 'cffaedfe', 'cafebabe', 'bebafeca', 'cafebabf', 'bfbafeca']) {
      const code = path.join(dir, `code-${magic}.pak`)
      fs.writeFileSync(code, Buffer.from(magic, 'hex'))
      assert.equal(skipNonCodeResource(code), false)
    }
    const resource = path.join(dir, 'locale.pak')
    fs.writeFileSync(resource, Buffer.from([0, 1, 2, 3, 4, 5]))
    assert.equal(skipNonCodeResource(resource), true)
    fs.writeFileSync(resource, '')
    assert.equal(skipNonCodeResource(resource), true)
  } finally { fs.rmSync(dir, { recursive: true, force: true }) }
})

test('release verification rejects ad hoc, wrong-team and non-hardened signatures', () => {
  const valid = 'Authority=Developer ID Application: Example (ABCDEFGHIJ)\nTeamIdentifier=ABCDEFGHIJ\nCodeDirectory flags=0x10000(runtime)'
  assert.doesNotThrow(() => verifyIdentity(valid, env.APPLE_TEAM_ID, true))
  assert.throws(() => verifyIdentity(valid, 'KLMNOPQRST', true), /team/)
  assert.throws(() => verifyIdentity(valid.replace('Authority=Developer ID Application: Example (ABCDEFGHIJ)', 'Signature=adhoc'), env.APPLE_TEAM_ID, true), /Developer ID/)
  assert.throws(() => verifyIdentity(valid.replace('0x10000(runtime)', '0x0(none)'), env.APPLE_TEAM_ID, true), /Hardened/)
})
