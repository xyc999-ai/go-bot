const { execFileSync } = require('node:child_process')

function run(command, args, options = {}) {
  return execFileSync(command, args, { encoding: 'utf8', stdio: 'pipe', ...options })
}

function signatureDetails(file) {
  // codesign writes informational output to stderr, including on success.
  const { spawnSync } = require('node:child_process')
  const result = spawnSync('codesign', ['--display', '--verbose=4', file], { encoding: 'utf8' })
  if (result.status !== 0) throw new Error(`Cannot read signature: ${file}`)
  return result.stderr + result.stdout
}

function verifyIdentity(details, teamId, app = false) {
  if (!teamId || !details.includes(`TeamIdentifier=${teamId}`)) throw new Error('Signing team does not match APPLE_TEAM_ID')
  if (!details.includes('Authority=Developer ID Application: ')) throw new Error('Missing Developer ID Application signature')
  if (app && !/flags=.*\bruntime\b/.test(details)) throw new Error('Hardened Runtime is missing')
}

function verifyAppSignature(appPath, teamId) {
  run('codesign', ['--verify', '--deep', '--strict', '--verbose=2', appPath])
  verifyIdentity(signatureDetails(appPath), teamId, true)
}

function verifyApp(appPath, teamId, { requireStapledTicket = true } = {}) {
  verifyAppSignature(appPath, teamId)
  // For a read-only DMG payload, the distribution container carries the ticket.
  if (requireStapledTicket) run('xcrun', ['stapler', 'validate', appPath])
  run('spctl', ['--assess', '--type', 'execute', '--verbose=2', appPath])
  console.log(requireStapledTicket
    ? 'PASS: app Developer ID signature, team, Hardened Runtime, notarization ticket and Gatekeeper'
    : 'PASS: DMG payload app signature, team, Hardened Runtime and Gatekeeper (container ticket verified separately)')
}

function verifyDmg(dmgPath, teamId) {
  run('hdiutil', ['verify', dmgPath])
  run('codesign', ['--verify', '--strict', '--verbose=2', dmgPath])
  verifyIdentity(signatureDetails(dmgPath), teamId)
  run('xcrun', ['stapler', 'validate', dmgPath])
  run('spctl', ['--assess', '--type', 'open', '--context', 'context:primary-signature', '--verbose=2', dmgPath])
  console.log('PASS: DMG integrity, Developer ID signature, notarization ticket and Gatekeeper')
}

module.exports = { run, verifyIdentity, verifyAppSignature, verifyApp, verifyDmg }

if (require.main === module) {
  try {
    const file = process.argv[2]
    if (process.platform !== 'darwin' || !file) throw new Error('Usage on macOS: node scripts/verify-macos-release.cjs <app-or-dmg>')
    if (file.endsWith('.app')) verifyApp(file, process.env.APPLE_TEAM_ID)
    else if (file.endsWith('.dmg')) verifyDmg(file, process.env.APPLE_TEAM_ID)
    else throw new Error('Expected .app or .dmg')
  } catch (error) { console.error(error.message); process.exitCode = 1 }
}
