import type { PermissionMode, SessionPermission } from './permissions'
import type { SessionEvent } from '../api/controller.ts'

export type AttachmentRef = { attachmentId: string; name?: string; mediaType?: string; width?: number; height?: number }
export type FileRef = { source: 'attachment'; attachmentId: string } | { source: 'presented'; path: string }
type ImageBlock = { id: string; type: 'image'; attachment: AttachmentRef }
export type Block =
  | { id: string; type: 'text'; text: string }
  | ImageBlock
  | { id: string; type: 'file'; name?: string; description?: string; file: FileRef }
  | { id: string; type: 'tool'; name: string; callId: string }

export type ToolBlock = Extract<Block, { type: 'tool' }>
type ToolGroup = { id: string; type: 'tool-group'; tools: ToolBlock[] }
export type ChatImage = { id: string; file: FileRef; name?: string; mediaType?: string; width?: number; height?: number }
type ImageRow = { id: string; type: 'image-row'; images: ChatImage[] }
type ImageTools = { id: string; type: 'image-tools'; name: string; tools: ToolBlock[]; images: ChatImage[] }
type VideoBlock = Omit<Extract<Block, { type: 'file' }>, 'type'> & { type: 'video' }
type DisplayBlock = Exclude<Block, ImageBlock> | ToolGroup | ImageRow | ImageTools | VideoBlock

function attachmentImage(block: ImageBlock): ChatImage {
  const { attachmentId, ...metadata } = block.attachment
  return { id: block.id, file: { source: 'attachment', attachmentId }, ...metadata }
}

// Only explicit presented raster files become previews. Markdown links and uploaded
// file blocks keep their existing semantics. A failed preview falls back to a file.
export function isPresentedImage(file: FileRef) {
  return file.source === 'presented' && /\.(png|jpe?g|webp|gif|avif|bmp)$/i.test(file.path)
}

export function isPresentedVideo(file: FileRef) {
  return file.source === 'presented' && /\.(mp4|webm|mov|m4v)$/i.test(file.path)
}

export function groupDisplayBlocks(blocks: Block[], toolResults: Transcript['toolResults']): DisplayBlock[] {
  const groups: DisplayBlock[] = []
  const image = (item: ChatImage) => {
    const previous = groups.at(-1)
    if (previous?.type === 'image-row') previous.images.push(item)
    else groups.push({ id: item.id, type: 'image-row', images: [item] })
  }
  for (const block of blocks) {
    const previous = groups.at(-1)
    if (block.type === 'tool' && (block.name === 'readimage' || block.name === 'generateimage')) {
      const images = (toolResults[block.callId]?.images ?? []).map(attachmentImage)
      if (previous?.type === 'image-tools' && previous.name === block.name) {
        previous.tools.push(block)
        previous.images.push(...images)
      } else groups.push({ id: block.id, type: 'image-tools', name: block.name, tools: [block], images })
    } else if (block.type === 'image') image(attachmentImage(block))
    else if (block.type === 'file' && isPresentedImage(block.file)) {
      image({ id: block.id, file: block.file, name: block.name })
    } else if (block.type === 'file' && isPresentedVideo(block.file)) {
      groups.push({ ...block, type: 'video' })
    } else {
      if (block.type === 'tool' && previous?.type === 'tool-group') previous.tools.push(block)
      else if (block.type === 'tool' && previous?.type === 'tool') {
        groups[groups.length - 1] = { id: previous.id, type: 'tool-group', tools: [previous, block] }
      } else groups.push(block)
      if (block.type === 'tool') for (const result of toolResults[block.callId]?.images ?? []) image(attachmentImage(result))
    }
  }
  return groups
}

// User attachments precede the instruction visually, including older histories.
// Assistant blocks always retain their original interleaving.
export function messageBlocksForDisplay(message: Message): Block[] {
  if (message.role !== 'user') return message.blocks
  return [...message.blocks.filter(block => block.type === 'image' || block.type === 'file'),
    ...message.blocks.filter(block => block.type !== 'image' && block.type !== 'file')]
}

type MessageData = { id: string; blocks: Block[] }
export type Message = MessageData & { role: 'user' | 'assistant' | 'notice'; turn?: number; step?: number; streaming?: boolean }
export type QueueItem = MessageData & { placement: 'queued' | 'steering' }
export type Question = {
  id: string
  question: string
  header?: string
  detail?: string
  multiSelect?: boolean
  options?: { label: string; description?: string }[]
}
export type PendingQuestion = { callId: string; turn: number; step: number; state?: string; questions: Question[]; approval?: { tool: string; reason: string; detail: string; target?: PermissionMode; toolCallId: string } }
export type Answer = { id: string; selected: string[]; custom?: string }
type ToolStatus = 'succeeded' | 'failed' | 'cancelled'
type ToolProgress = { startedAt?: number; endedAt?: number; status?: ToolStatus; code?: string; images?: ImageBlock[] }
type ToolResult = Pick<ToolProgress, 'code' | 'images'> & { callId: string; status: ToolStatus }

type Attempt = {
  attemptId: string
  revision: number
  turn: number
  step: number
  startedAfterSeq: number
  nextIndex: number
  texts: Record<number, string>
}

export type Transcript = {
  messages: Message[]
  seenThrough: number
  activeTurn: number | null
  draft: Attempt | null
  queue: QueueItem[]
  questions: PendingQuestion[]
  permission?: SessionPermission
  toolResults: Record<string, ToolProgress>
  // Client-only reset boundary for a restored stream snapshot, not a log fact.
  streamEpoch?: number
}

type Chunk = { type: string; index?: number; blockType?: string; text?: string; block?: { type: string; text?: string } }
type Frame = Omit<Attempt, 'nextIndex' | 'texts'> & {
  type: 'start' | 'chunk' | 'end'
  index?: number
  chunk?: Chunk
}

export function emptyTranscript(): Transcript {
  return { messages: [], seenThrough: -1, activeTurn: null, draft: null, queue: [], questions: [], toolResults: {} }
}

export function textMessage(id: string, text: string, role: Message['role'] = 'user'): Message {
  return { id, role, blocks: [{ id: id + '/0', type: 'text', text }] }
}

export function messageText(message: MessageData) {
  return message.blocks.filter(block => block.type === 'text').map(block => block.text).join('\n')
}

export function visibleQueue(state: Transcript, displayedMessages: Message[]) {
  if (state.activeTurn === null) return []
  const displayed = new Set(displayedMessages.map(message => message.id))
  return state.queue.filter(item => !displayed.has(item.id))
}

export function groupAssistantReplies(messages: Message[]): Message[] {
  const groups: Message[] = []
  for (const message of messages) {
    const previous = groups.at(-1)
    if (message.role === 'assistant' && previous?.role === 'assistant' && message.turn === previous.turn) {
      previous.blocks.push(...message.blocks)
    } else {
      groups.push(message.role === 'assistant' ? { ...message, blocks: [...message.blocks] } : message)
    }
  }
  return groups
}

function appendMessage(state: Transcript, role: Message['role'], message: MessageData, turn?: number, step?: number): Transcript {
  if (!message.blocks?.length || state.messages.some(item => item.id === message.id)) return state
  return { ...state, messages: [...state.messages, { ...message, role, turn, ...(step === undefined ? {} : { step }) }] }
}

export function applySessionEvent(previous: Transcript, event: SessionEvent): Transcript {
  if (event.seq <= previous.seenThrough) return previous
  let state = { ...previous, seenThrough: event.seq }
  switch (event.type) {
    case 'user/message': {
      const message = event.data as MessageData & { source?: string }
      if (message.source === 'schedule' || message.source === 'background') {
        return appendMessage(state, 'notice', textMessage(message.id, message.source === 'schedule' ? 'Scheduled task' : 'Background task', 'notice'))
      }
      return appendMessage(state, 'user', message)
    }
    case 'assistant/message': {
      const { message, turn, step, delivery } = event.data as { message: MessageData; turn: number; step?: number; delivery?: boolean }
      if (delivery) {
        const files = new Map(message.blocks.map(block => [block.id, block]))
        const messages = state.messages.map(existing => {
          if (existing.role !== 'assistant' || existing.turn !== turn) return existing
          const blocks = existing.blocks.map(block => {
            const update = files.get(block.id)
            if (!update) return block
            files.delete(block.id)
            return update
          })
          return { ...existing, blocks }
        })
        return appendMessage({ ...state, messages }, 'assistant', { ...message, blocks: [...files.values()] }, turn)
      }
      if (state.draft && state.draft.turn === turn && state.draft.step === step && event.seq > state.draft.startedAfterSeq) {
        state = { ...state, draft: null }
      }
      return appendMessage(state, 'assistant', message, turn, step)
    }
    case 'tool/call': {
      const { callId } = event.data as { callId: string }
      return { ...state, toolResults: { ...state.toolResults, [callId]: { startedAt: event.time } } }
    }
    case 'tool/result': {
      const { callId, status, code, images } = event.data as ToolResult
      return { ...state, toolResults: { ...state.toolResults,
        [callId]: { ...state.toolResults[callId], status, code, images, endedAt: event.time } } }
    }
    case 'turn/start':
      return { ...state, activeTurn: (event.data as { turn: number }).turn }
    case 'turn/end': {
      const { turn, reason } = event.data as { turn: number; reason?: { kind?: string } }
      state = {
        ...state,
        activeTurn: state.activeTurn === turn ? null : state.activeTurn,
        draft: state.draft && state.draft.turn === turn && event.seq > state.draft.startedAfterSeq ? null : state.draft,
      }
      if (reason?.kind && reason.kind !== 'completed') {
        const issue = reason.kind === 'aborted' || reason.kind === 'interrupted'
          ? 'The reply was interrupted.' : 'The agent could not finish its reply.'
        return appendMessage(state, 'notice', textMessage('turn-end/' + event.seq, issue, 'notice'))
      }
      return state
    }
    default:
      return state
  }
}

type AttemptSnapshot = Omit<Attempt, 'texts' | 'revision'> & {
  stream: { type: string; index?: number; texts?: string[]; chunk?: Chunk }[]
}

function applyChunk(texts: Record<number, string>, chunk: Chunk) {
  const index = chunk.index ?? 0
  if (chunk.type === 'text-delta') texts[index] = (texts[index] ?? '') + (chunk.text ?? '')
  if (chunk.type === 'block-end' && chunk.block?.type === 'text') texts[index] = chunk.block.text ?? ''
}

export function attemptMessage(attempt: Attempt): Message {
  const blocks = Object.entries(attempt.texts).map(([index, text]) => ({
    id: attempt.attemptId + '/' + index, type: 'text' as const, text,
  }))
  return { id: attempt.attemptId, role: 'assistant', turn: attempt.turn, step: attempt.step, streaming: true, blocks }
}

export function applyStreamEvent(state: Transcript, kind: string, data: unknown): Transcript {
  if (kind === 'session') return applySessionEvent(state, data as SessionEvent)
  const through = (data as { throughSeq?: number | null })?.throughSeq
  if (through !== undefined && through !== null && through < state.seenThrough) return state
  if (kind === 'session.state') {
    const { activeTurn } = data as { activeTurn: number | null }
    return { ...state, activeTurn, draft: activeTurn === null ? null : state.draft }
  }
  if (kind === 'chat.idle') return { ...state, draft: null, activeTurn: null }
  if (kind === 'queue.state') return { ...state, queue: (data as { queue: QueueItem[] }).queue }
  if (kind === 'permissions.state') return { ...state, permission: data as SessionPermission }
  if (kind === 'questions.state') return { ...state, questions: (data as { questions?: PendingQuestion[] }).questions ?? [] }
  if (kind === 'assistant-stream-baseline') {
    state = { ...state, streamEpoch: (state.streamEpoch ?? 0) + 1 }
    const { baseline } = data as { baseline: { revision: number; activeAttempt?: AttemptSnapshot } }
    const current = baseline.activeAttempt
    if (!current) return { ...state, draft: null }
    const { stream, ...attempt } = current
    const texts: Record<number, string> = {}
    for (const record of stream) {
      if (record.type === 'text-chunks') {
        applyChunk(texts, { type: 'text-delta', index: record.index, text: record.texts?.join('') })
      } else if (record.type === 'chunk' && record.chunk) {
        applyChunk(texts, record.chunk)
      }
    }
    return { ...state, draft: { ...attempt, revision: baseline.revision, texts } }
  }
  if (kind !== 'assistant-stream') return state
  const { frame } = data as { frame: Frame }
  if (frame.type === 'start') {
    if (state.draft && frame.revision < state.draft.revision) return state
    if (state.draft?.attemptId === frame.attemptId) return state
    return { ...state, draft: { ...frame, nextIndex: 0, texts: {} } }
  }
  const draft = state.draft
  if (!draft || frame.attemptId !== draft.attemptId || frame.revision < draft.revision) return state
  if (frame.index !== undefined && frame.index < draft.nextIndex) return state
  if (frame.index !== draft.nextIndex) return { ...state, draft: null }
  if (frame.type === 'end') return state
  const texts = { ...draft.texts }
  if (frame.chunk) applyChunk(texts, frame.chunk)
  return { ...state, draft: { ...draft, revision: frame.revision, texts, nextIndex: draft.nextIndex + 1 } }
}
