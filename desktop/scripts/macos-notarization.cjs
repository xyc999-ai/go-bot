const fs = require('node:fs/promises')
const { createReadStream } = require('node:fs')
const path = require('node:path')
const { createHash } = require('node:crypto')
const { execFile, spawn } = require('node:child_process')
const { promisify } = require('node:util')
const exec = promisify(execFile)

async function sha256(file) {
  const hash = createHash('sha256')
  for await (const chunk of createReadStream(file)) hash.update(chunk)
  return hash.digest('hex')
}

function authorizationArgs(credentials) {
  const args = ['--key', credentials.appleApiKey, '--key-id', credentials.appleApiKeyId]
  if (credentials.appleApiIssuer) args.push('--issuer', credentials.appleApiIssuer)
  return args
}

async function jsonTool(args, timeout = 120000) {
  try {
    const { stdout } = await exec('xcrun', ['notarytool', ...args, '--output-format', 'json'], { timeout, maxBuffer: 4 * 1024 * 1024 })
    return JSON.parse(stdout)
  } catch (error) {
    // Do not echo command arguments, signed upload URLs or authentication data.
    throw new Error(`notarytool ${args[0]} failed (${error.code || 'invalid response'})`)
  }
}

function waitTool(args) {
  return new Promise((resolve, reject) => {
    // Normal output prints the job ID and processing progress; JSON mode hides
    // every intermediate update until completion. Credentials are never echoed.
    const proc = spawn('xcrun', ['notarytool', ...args], { stdio: 'inherit' })
    proc.on('error', reject)
    proc.on('exit', code => resolve(code))
  })
}

async function notarizeDmg(file, credentials, recordFile, options = {}) {
  const timeoutMinutes = Number(options.timeoutMinutes ?? process.env.GOBOT_NOTARIZATION_TIMEOUT_MINUTES ?? 90)
  if (!Number.isInteger(timeoutMinutes) || timeoutMinutes < 1 || timeoutMinutes > 110) {
    throw new Error('GOBOT_NOTARIZATION_TIMEOUT_MINUTES must be an integer between 1 and 110')
  }
  const json = options.jsonTool || jsonTool
  const wait = options.waitTool || waitTool
  const auth = authorizationArgs(credentials)
  const record = { artifact: path.basename(file), sha256: await sha256(file), phase: 'uploading', startedAt: new Date().toISOString() }
  const save = () => fs.writeFile(recordFile, JSON.stringify(record, null, 2) + '\n')
  await save()
  console.log('Uploading final DMG to Apple; the App is scanned as nested code in this single submission')
  const submission = await json(['submit', file, ...auth], 15 * 60 * 1000)
  if (!/^[0-9a-f-]{36}$/i.test(submission.id || '')) throw new Error('Apple returned no valid notarization submission ID')
  record.id = submission.id
  record.phase = 'processing'
  record.uploadCompletedAt = new Date().toISOString()
  await save()
  console.log(`Upload complete. Apple submission ID: ${record.id}`)
  console.log(`Waiting for Apple processing, up to ${timeoutMinutes} minutes; this is not another upload`)
  await wait(['wait', record.id, ...auth, '--timeout', `${timeoutMinutes}m`])
  // The process exit code alone is insufficient evidence of acceptance.
  const result = await json(['info', record.id, ...auth])
  record.status = result.status
  record.checkedAt = new Date().toISOString()
  record.phase = result.status === 'Accepted' ? 'accepted' : result.status === 'In Progress' ? 'pending' : 'rejected'
  await save()
  try {
    const log = await json(['log', record.id, ...auth])
    await fs.writeFile(path.join(path.dirname(recordFile), 'notarization-log.json'), JSON.stringify(log, null, 2) + '\n')
    console.log(`Apple notarization diagnostics: ${Array.isArray(log.issues) ? log.issues.length : 0} issue(s)`)
  } catch {
    console.log(`Apple diagnostic log is not yet available. Submission ID retained: ${record.id}`)
  }
  if (result.status !== 'Accepted') {
    throw new Error(`Apple notarization ${result.status || 'unknown'}; no public release produced. Submission: ${record.id}. Diagnostic files: ${path.dirname(recordFile)}`)
  }
  return record
}

module.exports = { notarizeDmg, authorizationArgs, sha256 }
