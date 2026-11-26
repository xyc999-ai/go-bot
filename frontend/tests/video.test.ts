import { test } from 'node:test'
import assert from 'node:assert/strict'
import { groupDisplayBlocks, type Block } from '../src/chat/transcript.ts'
import { claimVideoPlayback, releaseVideoPlayback, videoTime } from '../src/chat/video.ts'

test('presented videos each occupy their own ordered display item', () => {
  const file = (id: string, path: string): Block => ({ id, type: 'file', file: { source: 'presented', path } })
  const blocks: Block[] = [file('1', '/workspace/A.MP4'), file('2', '/workspace/b.webm'), file('3', '/workspace/image.png'), file('4', '/workspace/c.mov'), file('5', '/workspace/report.docx'), { id: '6', type: 'text', text: 'Next' }, { id:'7',type:'tool',callId:'7',name:'present' }, file('8','/workspace/d.m4v'), { id:'9', type:'file', file:{source:'attachment',attachmentId:'uploaded.mp4'} }]
  assert.deepEqual(groupDisplayBlocks(blocks, {}).map(block => block.type), ['video','video','image-row','video','file','text','tool','video','file'])
  assert.deepEqual(groupDisplayBlocks(blocks, {}).map(block => block.id), blocks.map(block => block.id))
})
test('new playback pauses the previous video, old cleanup does not release a new player', () => {
  let pauses = 0
  const first = () => { pauses++ }, second = () => { pauses += 10 }
  claimVideoPlayback(first); claimVideoPlayback(first); assert.equal(pauses, 0)
  claimVideoPlayback(second); assert.equal(pauses, 1)
  releaseVideoPlayback(first); claimVideoPlayback(first); assert.equal(pauses, 11)
  releaseVideoPlayback(first)
  assert.equal(videoTime(65.8), '1:05'); assert.equal(videoTime(Infinity),'0:00')
})
