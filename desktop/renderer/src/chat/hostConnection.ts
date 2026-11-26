import type { HostEvent, TranscriptCache } from './transcriptCache.ts'

export type HostStreamReader = (
  cursors: Record<string, number | null>, signal: AbortSignal,
  onOpen: () => void, onEvent: (kind: string, data: unknown) => void,
) => Promise<void>

// One owner per signed-in workspace. Session switches never start a reader.
export class HostConnection {
  private controller?: AbortController
  private timer?: ReturnType<typeof setTimeout>
  private stopped = true
  private wait = 500
  private cache: TranscriptCache
  private read: HostStreamReader
  constructor(cache: TranscriptCache, read: HostStreamReader) { this.cache = cache; this.read = read }
  start() {
    if (!this.stopped) return
    this.stopped = false
    void this.follow()
  }
  stop() {
    this.stopped = true
    clearTimeout(this.timer)
    this.controller?.abort()
    this.cache.connection(true)
  }
  private async follow() {
    if (this.stopped) return
    const controller = new AbortController()
    this.controller = controller
    this.cache.connection(true)
    try {
      await this.read(this.cache.cursors(), controller.signal, () => {
        if (controller.signal.aborted) return
        this.wait = 500
        this.cache.connection(false)
        this.cache.receive({ type: 'sessions.changed', data: {} })
      }, (kind, data) => {
        if (!controller.signal.aborted && kind === 'host') this.cache.receive(data as HostEvent)
      })
    } catch {
      // HTTP reads and mutations surface actionable errors. This connection
      // retries without resending any user message or approval decision.
    } finally {
      if (!controller.signal.aborted && !this.stopped) {
        this.cache.connection(true)
        this.timer = setTimeout(() => void this.follow(), this.wait)
        this.wait = Math.min(this.wait * 2, 5000)
      }
    }
  }
}
