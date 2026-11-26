const { test } = require('node:test')
const assert = require('node:assert/strict')
const { releaseConfig, certificateDigest, validateBadging, validateSigner } = require('../scripts/android-release.cjs')
const { unsignedRelease } = require('../plugins/with-unsigned-android-release')

const config = { version: '0.0.3', versionCode: 3, packageName: 'com.gobot.mobile' }
const badging = "package: name='com.gobot.mobile' versionCode='3' versionName='0.0.3' platformBuildVersionName='16'\nnative-code: 'arm64-v8a'\n"
const digest = 'a'.repeat(64)

test('APK metadata must match independent mobile version, package and ARM64 release', () => {
  assert.equal(releaseConfig().version, require('../package.json').version)
  validateBadging(badging, config)
  for (const invalid of [badging.replace('0.0.3', '0.3.2'), badging.replace("versionCode='3'", "versionCode='4'"),
    badging.replace('com.gobot.mobile', 'com.other.app'), badging.replace('arm64-v8a', 'x86_64'),
    badging + 'application-debuggable\n', badging.replace("native-code: 'arm64-v8a'", "native-code: 'arm64-v8a' 'x86_64'")]) {
    assert.throws(() => validateBadging(invalid, config), /mismatch/)
  }
})

test('only the existing release certificate is accepted; debug and multiple signers fail', () => {
  const output = `Signer #1 certificate DN: CN=Go Bot\nSigner #1 certificate SHA-256 digest: ${digest}\n`
  validateSigner('Number of signers: 1\n' + output.replaceAll('Signer #1', 'V3.0 Signer:'), digest)
  assert.throws(() => validateSigner('Number of signers: 2\n' + output.replaceAll('Signer #1', 'V3.0 Signer:'), digest), /mismatch/)
  validateSigner(output, digest.toUpperCase().match(/../g).join(':'))
  assert.throws(() => validateSigner(output, 'b'.repeat(64)), /mismatch/)
  assert.throws(() => validateSigner(output.replace('CN=Go Bot', 'CN=Android Debug'), digest), /debug key/)
  assert.throws(() => validateSigner(output + output.replaceAll('#1', '#2'), digest), /mismatch/)
  assert.throws(() => certificateDigest(''), /fingerprint/)
})

test('Expo release disables debug signing and preserves development signing', () => {
  const input = 'buildTypes { debug { signingConfig signingConfigs.debug } release { // template\n signingConfig signingConfigs.debug\n minifyEnabled true } }'
  const result = unsignedRelease(input)
  assert.match(result, /debug \{ signingConfig signingConfigs.debug/)
  assert.match(result, /release \{ \/\/ template\n signingConfig null/)
  assert.equal(unsignedRelease(result), result)
  assert.throws(() => unsignedRelease('buildTypes { release {} }'), /Expected one/)
})
