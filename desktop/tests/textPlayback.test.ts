import test from 'node:test'
import assert from 'node:assert/strict'
import { TextPlayback } from '../renderer/src/chat/textPlayback.ts'
import { applySessionEvent, applyStreamEvent, emptyTranscript, attemptMessage, messageText, textMessage, type Message } from '../renderer/src/chat/transcript.ts'

const assistant = (text: string, id = 'draft', step = 1): Message => ({ ...textMessage(id, text, 'assistant'), turn: 1, step })
const texts = (player: TextPlayback) => player.view().messages.map(messageText)
function tick(player: TextPlayback, from: number, duration: number) {
  for (let at = from + 32; at <= from + duration; at += 32) player.advance(at)
}
function ready(history: Message[] = []) {
  const player = new TextPlayback()
  player.sync(history, true, 0, 0)
  return player
}

test('a later delta appends to the target without restarting the visible prefix', () => {
  const player = ready()
  const start = '这是一个连续展示的回答。'.repeat(8)
  player.sync([assistant(start)], true, 0, 0)
  tick(player, 0, 128)
  const prefix = texts(player)[0]
  assert.ok(prefix.length > 0 && prefix.length < start.length)
  player.sync([assistant(start + '下一段增量。')], true, 0, 128)
  assert.equal(texts(player)[0], prefix)
  tick(player, 128, 5000)
  assert.deepEqual(texts(player), [start + '下一段增量。'])
  assert.equal(player.pending, false)
})

test('actual reducer draft → committed message → turn/end keeps the display position and stable keys', () => {
  let transcript = emptyTranscript()
  const player = ready()
  transcript = applyStreamEvent(transcript, 'assistant-stream', { frame: {
    type: 'start', attemptId: 'attempt', turn: 1, step: 1, revision: 1, startedAfterSeq: 0,
  } })
  const full = '持续输出，不被完整消息跳过。'.repeat(10)
  transcript = applyStreamEvent(transcript, 'assistant-stream', { frame: {
    type: 'chunk', attemptId: 'attempt', index: 0, revision: 2, chunk: { type: 'text-delta', text: full },
  } })
  player.sync([attemptMessage(transcript.draft!)], true, 0, 0)
  tick(player, 0, 128)
  const before = player.view()
  assert.ok(before.pending)
  transcript = applySessionEvent(transcript, { seq: 1, type: 'assistant/message', data: {
    turn: 1, step: 1, message: { id: 'committed', blocks: [{ id: 'settled-text', type: 'text', text: full }] },
  } })
  transcript = applySessionEvent(transcript, { seq: 2, type: 'turn/end', data: { turn: 1, reason: { kind: 'completed' } } })
  player.sync(transcript.messages, true, 0, 128)
  assert.equal(transcript.activeTurn, null)
  assert.deepEqual(player.view().messages.map(message => ({ id: message.id, blocks: message.blocks })),
    before.messages.map(message => ({ id: message.id, blocks: message.blocks })))
  assert.ok(player.pending)
  tick(player, 128, 5000)
  assert.equal(texts(player)[0], full)
})

test('a fast complete message is also played; later blocks and steps retain their order', () => {
  const player = ready()
  const one = assistant('前面的文字。'.repeat(15), 'committed')
  one.blocks.push({ id: 'tool', type: 'tool', callId: 'call', name: 'bash' },
    { id: 'image', type: 'image', attachment: { attachmentId: 'image' } },
    { id: 'file', type: 'file', file: { source: 'presented', path: '/root/harness/report.docx' } })
  const two = assistant('后面的结果。'.repeat(10), 'next-step', 2)
  player.sync([one, two], true, 0, 0)
  tick(player, 0, 96)
  assert.equal(player.view().messages.length, 1)
  assert.deepEqual(player.view().messages[0].blocks.map(block => block.type), ['text'])
  tick(player, 96, 5000)
  assert.deepEqual(player.view().messages.flatMap(message => message.blocks.map(block => block.type)), ['text', 'tool', 'image', 'file', 'text'])
})

test('user messages stay immediate while an earlier assistant text is still playing', () => {
  const player = ready()
  const user = textMessage('steer', '请继续')
  player.sync([assistant('正在生成的文字。'.repeat(20)), user], true, 0, 0)
  assert.deepEqual(player.view().messages, [user])
  assert.ok(player.pending)
})

test('initial history and prepended older pages do not replay', () => {
  const history = assistant('已有历史', 'old', 2)
  const player = ready([history])
  assert.equal(player.pending, false)
  player.sync([assistant('更早的内容', 'older', 1), history], true, 0, 32)
  assert.deepEqual(texts(player), ['更早的内容', '已有历史'])
  assert.equal(player.pending, false)
  player.sync([assistant('更早的内容', 'older', 1), history, assistant('新消息'.repeat(50), 'new', 3)], true, 0, 64)
  assert.equal(player.pending, true)
})

test('restored baseline, hidden view and reduced motion snap to the received content', () => {
  const player = ready()
  const message = assistant('长内容'.repeat(80))
  player.sync([message], true, 0, 0)
  assert.ok(player.pending)
  player.sync([message], true, 1, 32)
  assert.equal(player.pending, false)
  player.sync([assistant(messageText(message) + '更多'.repeat(80))], true, 1, 64)
  assert.ok(player.pending)
  player.sync([message], false, 1, 96)
  assert.equal(player.pending, false)
  player.sync([assistant('后台新到的完整内容')], true, 1, 128)
  assert.deepEqual(texts(player), ['后台新到的完整内容'])
  assert.equal(player.pending, false)
  const otherSession = new TextPlayback()
  otherSession.sync([assistant('另一个会话的历史')], true, 0, 0)
  assert.equal(otherSession.pending, false)
})

test('cancellation removes an uncommitted draft and retries do not concatenate abandoned content', () => {
  const player = ready()
  player.sync([assistant('旧尝试'.repeat(40))], true, 0, 0)
  tick(player, 0, 128)
  player.sync([], true, 0, 128)
  assert.equal(player.pending, false)
  assert.deepEqual(player.view().messages, [])
  player.sync([assistant('全新尝试'.repeat(40), 'retry')], true, 0, 160)
  tick(player, 160, 5000)
  assert.deepEqual(texts(player), ['全新尝试'.repeat(40)])
})

test('non-prefix correction preserves only the already visible common prefix', () => {
  const player = ready()
  player.sync([assistant('相同前缀，然后是旧内容。'.repeat(10))], true, 0, 0)
  tick(player, 0, 128)
  player.sync([assistant('相同前缀，新的答案。'.repeat(10), 'replacement')], true, 0, 128)
  assert.ok('相同前缀，'.startsWith(texts(player)[0] ?? ''))
  tick(player, 128, 5000)
  assert.deepEqual(texts(player), ['相同前缀，新的答案。'.repeat(10)])
})

test('Unicode clusters are not split, even when a delta completes a combined emoji', () => {
  const player = ready()
  const full = '👩🏽‍💻🇨🇳e\u0301你好'.repeat(20)
  player.sync([assistant(full)], true, 0, 0)
  const validEnds = new Set(Array.from(new Intl.Segmenter(undefined, { granularity: 'grapheme' }).segment(full), part => part.index + part.segment.length))
  for (let at = 32; at < 3000; at += 32) {
    player.advance(at)
    const length = (texts(player)[0] ?? '').length
    assert.ok(length === 0 || validEnds.has(length))
  }
  player.sync([assistant(full + '👩')], true, 0, 3000)
  tick(player, 3000, 100)
  player.sync([assistant(full + '👩‍💻')], true, 0, 3100)
  assert.ok((texts(player)[0] ?? '').endsWith('👩‍💻'))
})

test('large backlog accelerates but cannot turn a long frame suspension into an instant jump', () => {
  const player = ready()
  player.sync([assistant('字'.repeat(3000))], true, 0, 0)
  player.advance(10000)
  assert.ok(texts(player)[0].length > 0 && texts(player)[0].length <= 36)
  tick(player, 10000, 960)
  assert.ok(texts(player)[0].length > 300)
})

test('a next-step draft can arrive before the previous step commit without losing its playback position', () => {
  const player = ready()
  const first = { ...assistant('第一步的说明。'.repeat(30)), streaming: true }
  const second = { ...assistant('第二步的结论。'.repeat(20), 'next-attempt', 2), streaming: true }
  player.sync([first], true, 0, 0)
  tick(player, 0, 128)
  const prefix = texts(player)[0]
  player.sync([{ ...second, blocks: [] }], true, 0, 128)
  assert.equal(texts(player)[0], prefix)
  player.sync([second], true, 0, 128)
  assert.equal(texts(player)[0], prefix)
  player.sync([assistant(messageText(first), 'committed'), second], true, 0, 160)
  assert.equal(texts(player)[0], prefix)
  tick(player, 160, 10000)
  assert.deepEqual(texts(player), [messageText(first), messageText(second)])
  player.sync([], true, 0, 10200)
  assert.deepEqual(player.view().messages, [])
  assert.equal(player.pending, false)
})
