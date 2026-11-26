import assert from 'node:assert/strict'
import test from 'node:test'
import { readAuthorizationJSON } from '../renderer/src/extensions/authorizationFile.ts'
const file = (text: string) => ({size: new TextEncoder().encode(text).length, text: async () => text})
test('client JSON file is parsed and sent as the existing authorization answer', async () => {
  assert.equal(await readAuthorizationJSON(file('\uFEFF{\n "installed": {"client_id":"test", "client_secret":"private"}\n}')), '{"installed":{"client_id":"test","client_secret":"private"}}')
})
test('invalid and oversized files are rejected locally without echoing their contents', async () => {
  for (const content of ['private-invalid-json', 'null', '[]', '"private"']) {
    await assert.rejects(readAuthorizationJSON(file(content)), error => error instanceof Error && error.message === 'Select a valid JSON configuration file.')
  }
  await assert.rejects(readAuthorizationJSON({size:16385,text:async()=>{throw new Error('must not read oversized file')}}), /16 KB/)
})
