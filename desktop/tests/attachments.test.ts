import assert from 'node:assert/strict'
import test from 'node:test'
import { addMessageFiles, messageRequest } from '../renderer/src/chat/attachments.ts'

test('keeps the content field first and preserves mixed attachment order', () => {
  const image = new File(['image'], 'pasted.png', { type: 'image/png' })
  const file = new File(['report'], 'report.txt', { type: 'text/plain' })
  const request = messageRequest('', [file, image])
  assert.equal(request.headers, undefined)
  assert(request.body instanceof FormData)
  assert.deepEqual([...request.body.entries()], [['content', ''], ['files', file], ['images', image]])
  assert.equal(messageRequest('Hello', []).body, '{"content":"Hello"}')
})

test('keeps accepted drafts when a file exceeds the attachment count', () => {
  const existing = Array.from({ length: 8 }, (_, index) => new File(['x'], `${index}.txt`))
  const extra = new File(['x'], 'extra.txt')
  const image = new File(['x'], 'preview.webp', { type: 'image/webp' })
  const next = addMessageFiles(existing, [extra, image])
  assert.deepEqual(next.files, [...existing, image])
  assert.deepEqual(next.issues.map(issue => issue.message), ['A message can attach up to 20 images and 8 other files.'])
})
