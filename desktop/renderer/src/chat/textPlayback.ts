import type { Block, Message } from './transcript.ts'

type Text = { target: string; ends: number[]; shown: number }
export type PlaybackView = { messages: Message[]; pending: boolean; pendingTurn?: number }
const segmenter = typeof Intl.Segmenter === 'function' ? new Intl.Segmenter(undefined, { granularity: 'grapheme' }) : undefined

function boundaries(text: string) {
  if (segmenter) return Array.from(segmenter.segment(text), part => part.index + part.segment.length)
  let end = 0
  return Array.from(text, point => (end += point.length))
}

// Draft IDs and committed message/block IDs differ. turn/step is the common
// identity already carried by both; file-only deliveries keep their own ID.
function messageKey(message: Message) {
  return message.role === 'assistant' && message.turn !== undefined && message.step !== undefined
    ? `step/${message.turn}/${message.step}` : `message/${message.id}`
}
function textKeys(message: Message) {
  let index = 0
  return message.blocks.map(block => block.type === 'text' ? `${messageKey(message)}/text/${index++}` : '')
}

/** Presentation only. Received messages and stream cursors are never modified. */
export class TextPlayback {
  private source: Message[] = []
  private texts = new Map<string, Text>()
  private known = new Set<string>()
  private ordered: { key: string; turn?: number }[] = []
  private initialized = false
  private enabled = false
  private epoch = 0
  private lastAt = 0
  private credit = 0

  sync(messages: Message[], enabled: boolean, epoch: number, now: number) {
    const wasPending = this.pending
    const snap = !this.initialized || !enabled || enabled !== this.enabled || epoch !== this.epoch
    if (!snap) {
      // A later step's live frame can outrun the 500ms journal projection.
      // Keep the previous step's provisional text until its commit arrives,
      // but only while a later live draft proves this is still the same turn.
      // Cancellation, a reset baseline and same-step retries do not retain it.
      const received = new Set(messages.map(messageKey))
      for (const previous of this.source) {
        if (!previous.streaming || received.has(messageKey(previous))) continue
        const later = messages.findIndex(message => message.streaming && message.turn === previous.turn &&
          message.step !== undefined && previous.step !== undefined && message.step > previous.step)
        if (later >= 0) messages = [...messages.slice(0, later), previous, ...messages.slice(later)]
      }
    }
    const lastKnown = messages.reduce((last, message, index) => this.known.has(messageKey(message)) ? index : last, -1)
    const next = new Map<string, Text>()
    const ordered: typeof this.ordered = []
    messages.forEach((message, position) => {
      if (message.role !== 'assistant') return
      const keys = textKeys(message)
      message.blocks.forEach((block, index) => {
        if (block.type !== 'text') return
        const key = keys[index]
        const previous = this.texts.get(key)
        const ends = previous?.target === block.text ? previous.ends : boundaries(block.text)
        let shown = previous?.shown ?? 0
        if (snap || !previous && position < lastKnown) shown = ends.length
        else if (previous && !block.text.startsWith(previous.target)) {
          // An abandoned attempt can be replaced by corrected text. Keep only
          // the already visible common prefix; never append the old attempt.
          const visibleEnd = previous.ends[previous.shown - 1] ?? 0
          let common = 0
          while (common < visibleEnd && previous.target[common] === block.text[common]) common++
          shown = ends.filter(end => end <= common).length
        }
        next.set(key, { target: block.text, ends, shown: Math.min(shown, ends.length) })
        ordered.push({ key, turn: message.turn })
      })
    })
    this.source = messages
    this.texts = next
    this.ordered = ordered
    this.known = new Set(messages.map(messageKey))
    this.enabled = enabled
    this.epoch = epoch
    this.initialized = true
    if (snap || !wasPending) { this.lastAt = now; this.credit = 0 }
  }

  get pending() { return this.ordered.some(({ key }) => { const text = this.texts.get(key)!; return text.shown < text.ends.length }) }

  advance(now: number) {
    const elapsed = Math.max(0, Math.min(100, now - this.lastAt)) / 1000
    this.lastAt = now
    const remaining = this.ordered.reduce((sum, { key }) => { const text = this.texts.get(key)!; return sum + text.ends.length - text.shown }, 0)
    if (!remaining) { this.credit = 0; return false }
    this.credit += elapsed * Math.min(360, Math.max(60, remaining / 0.8))
    let budget = Math.floor(this.credit)
    this.credit -= budget
    let changed = false
    for (const { key } of this.ordered) {
      if (!budget) break
      const text = this.texts.get(key)!
      const count = Math.min(budget, text.ends.length - text.shown)
      text.shown += count
      budget -= count
      changed ||= count > 0
    }
    return changed
  }

  view(): PlaybackView {
    let blocked = false
    let pendingTurn: number | undefined
    const messages: Message[] = []
    for (const message of this.source) {
      if (message.role !== 'assistant') { messages.push(message); continue }
      if (blocked) continue
      const keys = textKeys(message)
      const blocks: Block[] = []
      for (let index = 0; index < message.blocks.length; index++) {
        const block = message.blocks[index]
        if (block.type !== 'text') { blocks.push(block); continue }
        const text = this.texts.get(keys[index])!
        const value = text.target.slice(0, text.ends[text.shown - 1] ?? 0)
        if (value) blocks.push({ ...block, id: keys[index], text: value })
        if (text.shown < text.ends.length) { blocked = true; pendingTurn = message.turn; break }
      }
      if (blocks.length) messages.push({ ...message, id: messageKey(message), blocks })
    }
    return { messages, pending: this.pending, pendingTurn }
  }
}
