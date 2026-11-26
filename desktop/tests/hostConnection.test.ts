import assert from 'node:assert/strict'
import { test } from 'node:test'
import { HostConnection } from '../renderer/src/chat/hostConnection.ts'
import { TranscriptCache, cacheTranscript } from '../renderer/src/chat/transcriptCache.ts'
import { messageText, attemptMessage } from '../renderer/src/chat/transcript.ts'

const page = (throughSeq = 3) => ({ events: [], before: null, throughSeq, queue: [], questions: [] })
const question = { callId: 'ask', turn: 1, step: 1, questions: [{ id: 'choice', question: 'Choose?' }] }

test('Host extension events invalidate management state without touching chat or session lists', () => {
  const cache = new TranscriptCache()
  cache.keep('a', cacheTranscript(page()))
  let extensions = 0, sessions = 0, chats = 0
  cache.subscribe('@extensions', () => extensions++)
  cache.subscribe('@sessions', () => sessions++)
  cache.subscribe('a', () => chats++)
  cache.receive({ type: 'extensions.changed', data: {} })
  assert.equal(extensions, 1)
  assert.equal(sessions, 0)
  assert.equal(chats, 0)
  assert.equal(cache.get('a')?.transcript.messages.length, 0)
})

test('multiplexes sessions without loading unopened histories or sharing their questions', () => {
  const cache = new TranscriptCache()
  cache.keep('a', cacheTranscript(page()))
  cache.receive({ sessionId: 'b', type: 'questions.state', data: { questions: [question] } })
  assert.equal(cache.summary('b')?.pending, 'question')
  assert.equal(cache.get('b'), undefined)
  assert.deepEqual(cache.get('a')?.transcript.questions, [])
  cache.receive({ sessionId: 'b', type: 'questions.state', data: { questions: [] } })
  assert.equal(cache.summary('b')?.pending, null)
  assert.deepEqual(cache.cursors(), { a: 3 })
})

test('merges messages received while history loads exactly once and rejects older control snapshots', () => {
  const cache = new TranscriptCache()
  cache.beginLoad('a')
  cache.receive({ sessionId: 'a', type: 'session.state', data: { throughSeq: 1, activeTurn: 1 } })
  const event = { seq: 4, type: 'user/message', data: { id: 'new', blocks: [{ id: 'text', type: 'text', text: 'new message' }] } }
  cache.receive({ sessionId: 'a', type: 'session', data: event })
  cache.receive({ sessionId: 'a', type: 'questions.state', data: { throughSeq: 4, questions: [question] } })
  cache.finishLoad('a', page())
  assert.deepEqual(cache.get('a')?.transcript.messages.map(messageText), ['new message'])
  assert.equal(cache.get('a')?.transcript.activeTurn, null)
  assert.equal(cache.get('a')?.transcript.questions.length, 1)
  cache.receive({ sessionId: 'a', type: 'session', data: event })
  assert.equal(cache.get('a')?.transcript.messages.length, 1)
})

test('merges a live snapshot and increasing stream revisions without losing its prefix', () => {
  const cache = new TranscriptCache()
  cache.beginLoad('a')
  cache.receive({ sessionId: 'a', type: 'assistant-stream', data: { frame: {
    type: 'start', attemptId: 'one', revision: 1, turn: 1, step: 1, startedAfterSeq: 3,
  } } })
  for (const [index, text] of ['Hello', ' world'].entries()) cache.receive({ sessionId: 'a', type: 'assistant-stream', data: { frame: {
    type: 'chunk', attemptId: 'one', revision: index + 2, index, chunk: { type: 'text-delta', text },
  } } })
  const entry = cache.finishLoad('a', { ...page(), baseline: { revision: 2, activeAttempt: {
    attemptId: 'one', turn: 1, step: 1, startedAfterSeq: 3, nextIndex: 1,
    stream: [{ type: 'text-chunks', texts: ['Hello'] }],
  } } })
  assert.ok(entry.transcript.draft)
  assert.equal(messageText(attemptMessage(entry.transcript.draft)), 'Hello world')
})

test('one connection survives session changes and reconnects using all cached cursors', async () => {
  const cache = new TranscriptCache()
  let calls = 0
  let terminate: (() => void) | undefined
  const controller = new HostConnection(cache, async (cursors, signal, opened) => {
    calls++
    if (calls === 2) assert.deepEqual(cursors, { a: 3, b: 9 })
    opened()
    await new Promise<void>(resolve => { terminate = resolve; signal.addEventListener('abort', () => resolve(), { once: true }) })
  })
  controller.start()
  cache.keep('a', cacheTranscript(page()))
  cache.keep('b', cacheTranscript(page(9)))
  assert.equal(calls, 1)
  assert.equal(cache.connecting, false)
  terminate?.()
  await new Promise(resolve => setTimeout(resolve, 550))
  assert.equal(calls, 2)
  controller.stop()
})

test('title events refresh lists for unopened chats without creating chat messages', () => {
  const cache = new TranscriptCache()
  cache.keep('a', cacheTranscript(page()))
  let changes = 0
  cache.subscribe('@sessions', () => changes++)
  const data = { seq: 4, type: 'session/title', data: { title: 'Updated title', messageSeqs: [1], source: { kind: 'provider' } } }
  cache.receive({ sessionId: 'b', type: 'session', data })
  assert.equal(changes, 1)
  assert.equal(cache.get('b'), undefined)
  cache.receive({ sessionId: 'a', type: 'session', data })
  assert.equal(changes, 2)
  assert.equal(cache.get('a')?.transcript.messages.length, 0)
})
