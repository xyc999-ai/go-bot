import assert from 'node:assert/strict'
import { test } from 'node:test'
import { applySessionEvent, applyStreamEvent, attemptMessage, emptyTranscript, groupAssistantReplies, groupDisplayBlocks, messageBlocksForDisplay, messageText, textMessage, visibleQueue } from '../renderer/src/chat/transcript.ts'

test('replays history and replaces the in-flight text once its durable event arrives', () => {
  let state = applySessionEvent(emptyTranscript(), {
    seq: 4, type: 'user/message',
    data: { id: 'user', blocks: [{ id: 'u/0', type: 'text', text: 'Hello' }] },
  })
  state = applyStreamEvent(state, 'assistant-stream-baseline', {
    baseline: {
      revision: 2,
      activeAttempt: { attemptId: 'current', turn: 2, step: 1, startedAfterSeq: 9, nextIndex: 2,
        stream: [{ type: 'text-chunks', texts: ['Good', ' '] }, { type: 'reasoning-chunks', texts: ['private'] }] },
    },
  })
  state = applySessionEvent(state, { seq: 9, type: 'assistant/message',
    data: { turn: 2, step: 1, message: { id: 'older', blocks: [] } } })
  assert.equal(state.draft && messageText(attemptMessage(state.draft)), 'Good ')
  state = applyStreamEvent(state, 'assistant-stream', {
    frame: { type: 'chunk', attemptId: 'current', revision: 2, index: 2, chunk: { type: 'text-delta', text: 'day' } },
  })
  assert.equal(state.draft && messageText(attemptMessage(state.draft)), 'Good day')
  const durable = { seq: 10, type: 'assistant/message',
    data: { turn: 2, step: 1, message: { id: 'final', blocks: [{ id: 'f/0', type: 'text', text: 'Good day' }] } } }
  state = applySessionEvent(state, durable)
  assert.equal(state.draft, null)
  assert.equal(applySessionEvent(state, durable), state)
  assert.deepEqual(state.messages.map(messageText), ['Hello', 'Good day'])
})

test('discard a draft whose sequence has a gap', () => {
  const state = applyStreamEvent(emptyTranscript(), 'assistant-stream', {
    frame: { type: 'start', attemptId: 'gap', revision: 1, turn: 0, step: 0, startedAfterSeq: 0 },
  })
  assert.equal(applyStreamEvent(state, 'assistant-stream', {
    frame: { type: 'chunk', attemptId: 'gap', revision: 1, index: 1, chunk: { type: 'text-delta', text: 'lost' } },
  }).draft, null)
})


test('the idle first message skips the queue, while later messages stay in the queue until claimed', () => {
  const first = textMessage('first', 'A')
  const queued = { ...first, placement: 'queued' }
  let state = applyStreamEvent(emptyTranscript(), 'queue.state', { queue: [queued] })
  assert.deepEqual(visibleQueue(state, []), [])
  state = applySessionEvent(state, { seq: 1, type: 'turn/start', data: { turn: 1 } })
  assert.deepEqual(visibleQueue(state, [first]), [])
  const second = { ...textMessage('second', 'B'), placement: 'queued' }
  state = applyStreamEvent(state, 'queue.state', { queue: [second] })
  assert.deepEqual(visibleQueue(state, [first]).map(item => item.id), ['second'])
  state = applySessionEvent(state, { seq: 2, type: 'user/message', data: second })
  assert.deepEqual(visibleQueue(state, state.messages), [])
})

test('keeps attachment-only messages, ordered text and tool blocks, and independent tool results', () => {
  let state = applySessionEvent(emptyTranscript(), { seq: 1, type: 'user/message',
    data: { id: 'image', blocks: [{ id: 'pic', type: 'image', attachment: { attachmentId: 'asset' } }] } })
  state = applySessionEvent(state, { seq: 2, type: 'assistant/message',
    data: { message: { id: 'assistant', blocks: [
      { id: 'before', type: 'text', text: 'Before' },
      { id: 'a', type: 'tool', name: 'first_tool', callId: 'a' },
      { id: 'b', type: 'tool', name: 'second_tool', callId: 'b' },
      { id: 'after', type: 'text', text: 'After' },
    ] } } })
  state = applySessionEvent(state, { seq: 3, type: 'tool/result', data: { callId: 'b', status: 'succeeded' } })
  state = applySessionEvent(state, { seq: 4, type: 'tool/result', data: { callId: 'a', status: 'failed' } })
  assert.deepEqual(state.messages.map(message => message.blocks.map(block => block.id)), [['pic'], ['before', 'a', 'b', 'after']])
  assert.equal(state.toolResults.a.status, 'failed')
  assert.equal(state.toolResults.b.status, 'succeeded')
})

test('groups assistant steps and live text under one reply until a turn boundary or user message', () => {
  const assistant = (id: string, turn: number) => ({ ...textMessage(id, id, 'assistant'), turn })
  const groups = groupAssistantReplies([
    assistant('first', 1), assistant('streaming', 1), textMessage('steer', 'Focus on the results'),
    assistant('after-steer', 1), assistant('another-turn', 2),
  ])
  assert.deepEqual(groups.map(message => [message.id, messageText(message)]), [
    ['first', 'first\nstreaming'], ['steer', 'Focus on the results'], ['after-steer', 'after-steer'], ['another-turn', 'another-turn'],
  ])
})

test('groups consecutive tool calls and keeps intervening text in place', () => {
  const tool = (id: string) => ({ id, type: 'tool' as const, name: 'tool', callId: id })
  const text = textMessage('text', 'Between tools').blocks[0]
  const groups = groupDisplayBlocks([tool('a'), tool('b'), tool('c'), text, tool('d')], {})
  assert.deepEqual(groups.map(block => block.type === 'tool-group' ? block.tools.map(tool => tool.id) : block.id), [
    ['a', 'b', 'c'], 'text/0', 'd',
  ])
})

test('tool images follow their calls and split adjacent tool groups in live and replayed chat', () => {
  const image = (id: string) => ({ id, type: 'image', attachment: { attachmentId: id } })
  const assistant = { seq: 1, type: 'assistant/message', data: { turn: 1, step: 1, message: {
    id: 'reply', blocks: ['a', 'b', 'c', 'd', 'e'].map(id => ({ id, type: 'tool', name: 'tool', callId: id })),
  } } }
  const generated = { seq: 2, type: 'tool/result',
    data: { callId: 'd', status: 'succeeded', images: [image('generated')] } }
  const read = { seq: 3, type: 'tool/result',
    data: { callId: 'b', status: 'succeeded', images: [image('first'), image('second')] } }
  const events = [assistant, generated, read]
  const live = events.reduce((state, event) => applyStreamEvent(state, 'session', event), emptyTranscript())
  const history = events.reduce(applySessionEvent, emptyTranscript())
  assert.deepEqual(live, history)
  assert.equal(applyStreamEvent(live, 'session', read), live)
  assert.deepEqual(groupDisplayBlocks(live.messages[0].blocks, live.toolResults).map(block => {
    if (block.type === 'tool-group') return block.tools.map(tool => tool.id)
    if (block.type === 'image-row') return block.images.map(image => image.id)
    return block.id
  }), [['a', 'b'], ['first', 'second'], ['c', 'd'], ['generated'], 'e'])
})
test('renders a delivered file immediately and updates a repeated declaration', () => {
  const start = { seq: 1, type: 'turn/start', data: { turn: 1 } }
  const assistant = { seq: 2, type: 'assistant/message', data: {
    turn: 1, step: 1, message: { id: 'reply', blocks: [{ id: 'call', type: 'tool', name: 'present', callId: 'p' }] },
  } }
  const delivery = (seq: number, description: string) => ({ seq, type: 'assistant/message', data: {
    turn: 1, delivery: true, message: { id: 'delivery/' + seq, blocks: [{
      id: 'file/1/report', type: 'file', name: 'report.docx', description,
      file: { source: 'presented', path: '/root/harness/workspaces/chat/report.docx' },
    }] },
  } })
  const first = delivery(3, 'Draft report')
  const latest = delivery(4, 'Final report')
  const end = { seq: 5, type: 'turn/end', data: { turn: 1, reason: { kind: 'completed' } } }
  const live = [start, assistant, first].reduce((state, event) => applyStreamEvent(state, 'session', event), emptyTranscript())
  assert.equal(live.activeTurn, 1)
  assert.deepEqual(groupAssistantReplies(live.messages)[0].blocks.map(block => block.type), ['tool', 'file'])
  const updated = applyStreamEvent(live, 'session', latest)
  assert.equal(applyStreamEvent(updated, 'session', latest), updated)
  const history = [start, assistant, first, latest, end].reduce(applySessionEvent, emptyTranscript())
  assert.deepEqual(groupAssistantReplies(history.messages), groupAssistantReplies(updated.messages))
  assert.deepEqual(groupAssistantReplies(updated.messages)[0].blocks[1], latest.data.message.blocks[0])
})

test('consecutive image tools form their own group and preserve call order as results arrive', () => {
  const tools = ['a', 'b', 'c'].map(id => ({ id, type: 'tool' as const, name: 'readimage', callId: id }))
  const image = (id: string) => ({ id, type: 'image' as const, attachment: { attachmentId: id, name: id + '.png' } })
  const blocks = [tools[0], tools[1], { id: 'break', type: 'text' as const, text: 'A new comparison' }, tools[2]]
  const pending = groupDisplayBlocks(blocks, {})
  assert.deepEqual(pending.map(block => block.type), ['image-tools', 'text', 'image-tools'])
  const groups = groupDisplayBlocks(blocks, {
    b: { status: 'succeeded', images: [image('second')] }, a: { status: 'succeeded', images: [image('first')] },
  })
  assert.equal(groups[0].type, 'image-tools')
  if (groups[0].type !== 'image-tools') throw new Error('missing image tool group')
  assert.deepEqual(groups[0].tools.map(tool => tool.callId), ['a', 'b'])
  assert.deepEqual(groups[0].images.map(image => image.id), ['first', 'second'])
  assert.deepEqual(groups[0].images[0].file, { source: 'attachment', attachmentId: 'first' })
  assert.equal(blocks.length, 4)
})

test('other tools break image tool groups; only explicit presented raster files get image previews', () => {
  const read = (id: string) => ({ id, type: 'tool' as const, name: 'readimage', callId: id })
  const file = (name: string) => ({ id: name, type: 'file' as const, name, file: { source: 'presented' as const, path: '/root/harness/' + name } })
  const grouped = groupDisplayBlocks([
    read('a'), { id: 'shell', type: 'tool', callId: 'shell', name: 'shellexecute' }, read('b'),
    file('first.PNG'), file('second.jpg'), file('report.docx'), file('third.webp'), file('vector.svg'),
    { id: 'upload', type: 'file', name: 'upload.png', file: { source: 'attachment', attachmentId: 'upload' } },
  ], {})
  assert.deepEqual(grouped.map(block => block.type), ['image-tools', 'tool', 'image-tools', 'image-row', 'file', 'image-row', 'file', 'file'])
  assert.equal(grouped[3].type, 'image-row')
  if (grouped[3].type !== 'image-row') throw new Error('missing delivered images')
  assert.deepEqual(grouped[3].images.map(image => image.name), ['first.PNG', 'second.jpg'])
  assert.deepEqual(grouped[3].images[0].file, { source: 'presented', path: '/root/harness/first.PNG' })
})

test('user attachments retain mixed upload order before text; assistant interleaving is unchanged', () => {
  const message = { ...textMessage('message', 'Explain these'), blocks: [
    ...textMessage('message', 'Explain these').blocks,
    { id: 'file', type: 'file' as const, file: { source: 'attachment' as const, attachmentId: 'file' } },
    { id: 'image', type: 'image' as const, attachment: { attachmentId: 'image' } },
  ] }
  assert.deepEqual(messageBlocksForDisplay(message).map(block => block.type), ['file', 'image', 'text'])
  assert.deepEqual(message.blocks.map(block => block.type), ['text', 'file', 'image'])
  const assistant = { ...message, role: 'assistant' as const }
  assert.equal(messageBlocksForDisplay(assistant), assistant.blocks)
})
