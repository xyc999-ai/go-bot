import test from 'node:test'
import assert from 'node:assert/strict'
import { validateLoopbackAnswer, loopbackError } from '../src/extensions/useLoopbackAuthorization.ts'
const config = { redirectUri: 'http://localhost:8082/callback', authorizationUrl: 'https://example.test/login', state: 'this-attempt' }
test('automatic answers belong to the current redirect path and state', () => {
 const url = config.redirectUri + '?code=code&state=this-attempt'
 assert.equal(validateLoopbackAnswer(url, config), url)
 for (const value of [url.replace('this-attempt','old-attempt'), url+'&state=other', url+'&code=second', url+'&error=denied',url.replace('/callback','/other'),url.replace('localhost','example.test'),url+'#fragment']) assert.throws(()=>validateLoopbackAnswer(value,config),/INVALID_CALLBACK/)
 assert.throws(()=>validateLoopbackAnswer(config.redirectUri+'?error=access_denied&state=this-attempt',config),/ACCESS_DENIED/)
})
test('native errors expose safe retry guidance instead of URLs or one-time codes', () => {
 assert.match(loopbackError(new Error('PORT_IN_USE')),/already in use/)
 assert.match(loopbackError(new Error('TIMED_OUT')),/timed out/)
 assert.match(loopbackError(new Error('SUBMIT_FAILED')),/submit/)
 assert.equal(loopbackError(new Error('private-code')), 'Could not receive the authorization callback. Start authorization again.')
})
