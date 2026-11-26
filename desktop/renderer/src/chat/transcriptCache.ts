import { applySessionEvent, applyStreamEvent, emptyTranscript, type Transcript } from './transcript.ts'
import type { SessionEvent, TranscriptPage } from '../api/controller.ts'

export type CachedTranscript = {
  transcript: Transcript
  before: number | null
  cursor: number | null
}

const maxCachedChats = 20

// A cache belongs to one signed-in workspace. Each entry keeps only the
// pages the user has opened, plus the events received by the live stream.
export type HostEvent = { sessionId?: string; type: string; data: unknown }
export type SessionSummary = { generating: boolean; pending: 'approval' | 'question' | null }
type BufferedEvent = { type: string; data: unknown }

export class TranscriptCache {
  private readonly chats = new Map<string, CachedTranscript>()
  private readonly summaries = new Map<string, SessionSummary>()
  private readonly loading = new Map<string, BufferedEvent[]>()
  private readonly listeners = new Map<string, Set<() => void>>()
  private readonly unavailable = new Set<string>()
  connecting = true

  get(id: string) { return this.chats.get(id) }
  summary(id: string) { return this.summaries.get(id) }
  isUnavailable(id: string) { return this.unavailable.has(id) }
  cursors() { return Object.fromEntries([...this.chats].map(([id, entry]) => [id, entry.cursor])) }
  subscribe(key: string, listener: () => void) {
    let listeners = this.listeners.get(key)
    if (!listeners) this.listeners.set(key, listeners = new Set())
    listeners.add(listener)
    return () => { listeners.delete(listener); if (!listeners.size) this.listeners.delete(key) }
  }
  private notify(key: string) { this.listeners.get(key)?.forEach(listener => listener()) }
  connection(connecting: boolean) {
    if (this.connecting !== connecting) { this.connecting = connecting; this.notify('@connection') }
  }
  keep(id: string, entry: CachedTranscript) {
    this.unavailable.delete(id)
    this.chats.delete(id)
    this.chats.set(id, entry)
    if (this.chats.size > maxCachedChats) {
      const oldest = [...this.chats.keys()].find(key => !this.listeners.has(key))
      if (oldest !== undefined) this.chats.delete(oldest)
    }
  }
  delete(id: string) {
    this.chats.delete(id)
    this.summaries.delete(id)
    this.loading.delete(id)
    this.notify('@summaries')
  }
  beginLoad(id: string) { this.loading.set(id, []) }
  cancelLoad(id: string) { this.loading.delete(id) }
  finishLoad(id: string, page: TranscriptPage) {
    const buffered = this.loading.get(id) ?? []
    this.loading.delete(id)
    const entry = cacheTranscript(page)
    if (page.permission) entry.transcript = { ...entry.transcript, permission: page.permission }
    if (page.baseline) updateCachedTranscript(entry, 'assistant-stream-baseline', { baseline: page.baseline })
    // A frame already represented in the snapshot is ignored by its index;
    // durable events already represented in history are ignored by sequence.
    for (const event of buffered) {
      if (event.type === 'assistant-stream-baseline' && page.baseline &&
        (event.data as { baseline: { revision: number } }).baseline.revision < page.baseline.revision) continue
      updateCachedTranscript(entry, event.type, event.data)
    }
    this.keep(id, entry)
    this.notify(id)
    return entry
  }
  receive(event: HostEvent) {
    const { sessionId: id, type, data } = event
    if (!id) {
      if (type === 'schedule/changed') this.notify('@schedule')
      else if (type === 'extensions.changed' || type === 'plugins.state' || type === 'mcp.state') this.notify('@extensions')
      else if (type === 'sessions.changed') this.notify('@sessions')
      return
    }
    if (type === 'session.unavailable') {
      this.delete(id); this.unavailable.add(id); this.notify(id); this.notify('@sessions'); return
    }
    this.loading.get(id)?.push({ type, data })
    const entry = this.chats.get(id)
    if (entry) { updateCachedTranscript(entry, type, data); this.notify(id) }
    const previous = this.summaries.get(id) ?? { generating: false, pending: null }
    let next = previous
    if (type === 'session.state') next = { ...next, generating: (data as { activeTurn: number | null }).activeTurn !== null }
    if (type === 'session') {
      const event = data as SessionEvent
      if (event.type === 'turn/start' || event.type === 'turn/end') next = { ...next, generating: event.type === 'turn/start' }
      if (event.type === 'user/message' || event.type === 'session/title') this.notify('@sessions')
    }
    if (type === 'questions.state') {
      const questions = (data as { questions?: Transcript['questions'] }).questions ?? []
      next = { ...next, pending: questions.some(question => question.approval) ? 'approval' : questions.length ? 'question' : null }
    }
    if (next.generating !== previous.generating || next.pending !== previous.pending) {
      this.summaries.set(id, next)
      this.notify('@summaries')
    }
  }
}

export function cacheTranscript(page: TranscriptPage): CachedTranscript {
  const history = page.events.reduce(applySessionEvent, emptyTranscript())
  return {
    before: page.before,
    cursor: page.throughSeq,
    transcript: { ...history, seenThrough: page.throughSeq ?? -1, queue: page.queue, questions: page.questions ?? [] },
  }
}

export function updateCachedTranscript(entry: CachedTranscript, kind: string, data: unknown): Transcript {
  if (kind === 'session.cursor') {
    const cursor = (data as { cursor: number | null }).cursor
    if (cursor !== null) entry.cursor = Math.max(entry.cursor ?? -1, cursor)
  }
  if (kind === 'session') entry.cursor = Math.max(entry.cursor ?? -1, (data as SessionEvent).seq)
  entry.transcript = applyStreamEvent(entry.transcript, kind, data)
  return entry.transcript
}

export function prependTranscriptPage(entry: CachedTranscript, page: TranscriptPage) {
  const history = page.events.reduce(applySessionEvent, emptyTranscript())
  const current = entry.transcript
  const ids = new Set(current.messages.map(message => message.id))
  entry.before = page.before
  entry.transcript = {
    ...current,
    messages: [...history.messages.filter(message => !ids.has(message.id)), ...current.messages],
    toolResults: { ...history.toolResults, ...current.toolResults },
  }
}
