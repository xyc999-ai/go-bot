import { useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { useLocale } from '../locale'
import { ArrowUp, FileText, LoaderCircle, PanelLeft, Plus, Square, X } from 'lucide-react'
import { HarnessAvatar } from '../HarnessAvatar'
import { APIError, readTranscript, sendMessage, stopTurn, updateQueue } from '../api/controller'
import { MessageBubble } from './ChatContent'
import { ApprovalPanel, PermissionPicker } from './PermissionControls'
import { QuestionPanel } from './QuestionPanel'
import { addMessageFiles, isMessageImage } from './attachments'
import { ModelRequiredDialog } from './ModelRequiredDialog'
import { ModelPicker } from './ModelPicker'
import { useTextPlayback } from './useTextPlayback'
import { usePlaybackPreference } from './usePlaybackPreference'
import { WorkspaceHeader } from '../layout/WorkspaceHeader'
import { attemptMessage, emptyTranscript, groupAssistantReplies, messageText, textMessage, visibleQueue, type Message } from './transcript'
import { prependTranscriptPage, type TranscriptCache } from './transcriptCache'

function greeting() {
  const hour = new Date().getHours()
  if (hour < 12) return 'Good morning'
  if (hour < 18) return 'Good afternoon'
  return 'Good evening'
}

function sendError(error: unknown) {
  if (error instanceof APIError && error.message === 'model_default_required') return 'Set up an endpoint and a default model before creating a chat.'
  if (error instanceof APIError && (error.message === 'model_unconfigured' || error.message === 'model_unavailable')) return 'Choose an available model for this chat.'
  if (error instanceof APIError && error.message === 'host_model_settings_unavailable') return 'The Host could not apply the current model settings. Reopen the account or save the model settings again.'
  if (error instanceof APIError && error.status === 413) return 'The message or its attachments exceed the upload limit.'
  if (error instanceof APIError && error.message === 'invalid_image') return 'An attached image could not be read. Remove it and try again.'
  if (error instanceof APIError && error.status === 404) return 'This chat is no longer available.'
  if (error instanceof APIError && error.status < 500) return 'The message was not accepted. Check it and try again.'
  return 'Delivery could not be confirmed. Check the conversation before sending again.'
}

type ChatProps = {
  hidden?: boolean
  userId: string
  cache: TranscriptCache
  id?: string
  drawerOpen: boolean
  filesOverlay: boolean
  settingsVersion: number
  onSettings: () => void
  onPlugins: () => void
  isMain: boolean
  onOpenMenu: () => void
  onAccepted: (id: string, text: string) => void
  onUnavailable: (id: string) => void
  onTitleRefresh: () => void
  headerActions?: ReactNode
}

export function Chat({ hidden, userId, cache, id, drawerOpen, filesOverlay, settingsVersion, onSettings, onPlugins, isMain, onOpenMenu, onAccepted, onUnavailable, onTitleRefresh, headerActions }: ChatProps) {
  const { t } = useLocale()
  const cached = id ? cache.get(id) : undefined
  const [transcript, setTranscript] = useState(() => cached?.transcript ?? emptyTranscript())
  const [pending, setPending] = useState<Message[]>([])
  const [input, setInput] = useState('')
  const [files, setFiles] = useState<File[]>([])
  const [fileIssues, setFileIssues] = useState<ReturnType<typeof addMessageFiles>['issues']>([])
  const filePicker = useRef<HTMLInputElement>(null)
  const [failure, setFailure] = useState('')
  const [modelRequired, setModelRequired] = useState<'setup' | 'selection'>()
  const [historyError, setHistoryError] = useState('')
  const [connecting, setConnecting] = useState(false)
  const [loading, setLoading] = useState(Boolean(id && !cached))
  const [modelSaving, setModelSaving] = useState(false)
  const [permissionSaving, setPermissionSaving] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [awaitingMessage, setAwaitingMessage] = useState('')
  const [stopping, setStopping] = useState(false)
  const [before, setBefore] = useState<number | null>(() => cached?.before ?? null)
  const [loadingOlder, setLoadingOlder] = useState(false)
  const [olderError, setOlderError] = useState('')
  const [actionId, setActionId] = useState('')
  const [atBottom, setAtBottom] = useState(true)
  const scrollAnchor = useRef<{ height: number; top: number } | null>(null)
  const [loadAttempt, setLoadAttempt] = useState(0)
  const historySignal = useRef<AbortSignal>(undefined)
  const composer = useRef<HTMLTextAreaElement>(null)
  const scroll = useRef<HTMLDivElement>(null)
  const following = useRef(true)
  const mounted = useRef(false)
  const submittingNow = useRef(false)
  const refresh = useRef(onTitleRefresh)
  refresh.current = onTitleRefresh
  const missing = useRef(onUnavailable)
  missing.current = onUnavailable

  useEffect(() => { setFailure('') }, [settingsVersion])

  useEffect(() => {
    mounted.current = true
    return () => { mounted.current = false }
  }, [])

  useEffect(() => {
    if (!id) return
    const sessionId = id
    const abort = new AbortController()
    historySignal.current = abort.signal
    setLoadingOlder(false)
    const update = () => {
      if (cache.isUnavailable(sessionId)) { missing.current(sessionId); return }
      const entry = cache.get(sessionId)
      if (entry) { setTranscript(entry.transcript); setBefore(entry.before) }
    }
    const unsubscribe = cache.subscribe(sessionId, update)
    const connection = () => setConnecting(cache.connecting)
    const unsubscribeConnection = cache.subscribe('@connection', connection)
    connection()
    const entry = cache.get(sessionId)
    if (entry) {
      cache.keep(sessionId, entry)
      update()
      setLoading(false)
      setHistoryError('')
    } else {
      cache.beginLoad(sessionId)
      setLoading(true)
      setHistoryError('')
      void readTranscript(userId, sessionId, null, abort.signal).then(page => {
        if (abort.signal.aborted) return
        cache.finishLoad(sessionId, page)
        refresh.current()
      }).catch(error => {
        if (abort.signal.aborted) return
        cache.cancelLoad(sessionId)
        if (error instanceof APIError && error.status === 404) missing.current(sessionId)
        else setHistoryError('Chat history could not be loaded.')
      }).finally(() => { if (!abort.signal.aborted) setLoading(false) })
    }
    return () => { abort.abort(); unsubscribe(); unsubscribeConnection(); cache.cancelLoad(sessionId) }
  }, [cache, id, loadAttempt, userId])

  const pendingMessages = pending.filter(item => !transcript.messages.some(message => message.id === item.id))
  const displayedMessages = useMemo(() => {
    const messages = [...transcript.messages, ...pending.filter(item => !transcript.messages.some(message => message.id === item.id))]
    const draft = transcript.draft ? attemptMessage(transcript.draft) : null
    if (draft) messages.push(draft)
    return messages
  }, [transcript.messages, transcript.draft, pending])
  const queue = visibleQueue(transcript, displayedMessages)
  const draft = transcript.draft ? attemptMessage(transcript.draft) : null
  const playbackAllowed = usePlaybackPreference()
  const playback = useTextPlayback(userId + '/' + id, displayedMessages,
    playbackAllowed && !hidden && !loading && !connecting, transcript.streamEpoch)
  const replies = groupAssistantReplies(playback.messages)
  const activeTurn = transcript.activeTurn
  const hasReply = activeTurn !== null && replies.some(message => message.role === 'assistant' && message.turn === activeTurn)
  const messageObserved = transcript.messages.some(message => message.id === awaitingMessage) ||
    transcript.queue.some(message => message.id === awaitingMessage)
  const waiting = activeTurn !== null ? !hasReply && transcript.questions.length === 0
    : Boolean(awaitingMessage && !messageObserved)
  const generating = (activeTurn !== null || playback.pending) && transcript.questions.length === 0
  const showWelcome = !loading && !historyError && transcript.messages.length === 0 && pendingMessages.length === 0 &&
    !draft && !waiting && activeTurn === null && transcript.questions.length === 0
  useEffect(() => {
    if (awaitingMessage && (activeTurn !== null || messageObserved)) {
      setAwaitingMessage('')
    }
  }, [awaitingMessage, activeTurn, messageObserved])
  useEffect(() => {
    setPending(items => {
      const remaining = items.filter(item => !transcript.messages.some(message => message.id === item.id))
      return remaining.length === items.length ? items : remaining
    })
  }, [transcript.messages])
  useEffect(() => {
    if (!hidden && !drawerOpen && !filesOverlay && window.matchMedia('(min-width: 681px)').matches &&
        (document.activeElement === document.body || document.activeElement === composer.current)) {
      composer.current?.focus({ preventScroll: true })
    }
  }, [showWelcome, hidden, drawerOpen, filesOverlay])
  useLayoutEffect(() => {
    const panel = scroll.current
    if (!panel) return
    if (scrollAnchor.current) {
      panel.scrollTop = scrollAnchor.current.top + panel.scrollHeight - scrollAnchor.current.height
      scrollAnchor.current = null
    } else if (following.current) {
      panel.scrollTop = panel.scrollHeight
    }
  }, [transcript, playback.messages, pending.length, waiting, files.length])

  async function loadOlder() {
    const entry = id ? cache.get(id) : undefined
    const signal = historySignal.current
    if (!id || !entry || !signal || entry.before === null || loadingOlder) return
    setLoadingOlder(true)
    setOlderError('')
    try {
      const page = await readTranscript(userId, id, entry.before, signal)
      if (signal.aborted || cache.get(id) !== entry) return
      const panel = scroll.current
      if (panel) scrollAnchor.current = { height: panel.scrollHeight, top: panel.scrollTop }
      prependTranscriptPage(entry, page)
      setBefore(entry.before)
      setTranscript(entry.transcript)
    } catch {
      if (!signal.aborted) setOlderError('Older messages could not be loaded.')
    } finally {
      if (!signal.aborted) setLoadingOlder(false)
    }
  }

  async function queueAction(messageId: string, action: 'steer' | 'delete') {
    if (!id || actionId) return
    setActionId(messageId)
    setFailure('')
    try {
      await updateQueue(userId, id, messageId, action)
    } catch {
      if (mounted.current) setFailure('The queued message could not be updated.')
    } finally {
      if (mounted.current) setActionId('')
    }
  }

  async function stopGenerating() {
    if (!id || activeTurn === null || stopping) return
    setStopping(true)
    try {
      await stopTurn(userId, id, activeTurn)
    } catch {
      if (mounted.current) setFailure('Generation could not be stopped.')
    } finally {
      if (mounted.current) setStopping(false)
    }
  }

  function addFiles(incoming: File[]) {
    if (!incoming.length || submittingNow.current) return
    const next = addMessageFiles(files, incoming)
    setFiles(next.files)
    setFileIssues(next.issues)
  }

  async function submit() {
    const text = input.trim()
    if (!id || (!text && !files.length) || submittingNow.current || modelSaving || permissionSaving || loading || historyError) return
    submittingNow.current = true
    setSubmitting(true)
    setFailure('')
    try {
      const showImmediately = transcript.activeTurn === null && transcript.queue.length === 0 && pendingMessages.length === 0
      const result = await sendMessage(userId, id, text, files)
      onAccepted(id, text)
      if (!mounted.current) return
      if (showImmediately) {
        setAwaitingMessage(result.messageId)
        if (!files.length) setPending(items => [...items, textMessage(result.messageId, text)])
      }
      setInput(value => value.trim() === text ? '' : value)
      setFiles([])
      setFileIssues([])
    } catch (error) {
      if (mounted.current) {
        setFailure(sendError(error))
        if (error instanceof APIError && ['model_unconfigured', 'model_unavailable', 'model_default_required'].includes(error.message)) {
          setModelRequired(error.message === 'model_unavailable' ? 'selection' : 'setup')
        }
      }
    } finally {
      submittingNow.current = false
      if (mounted.current) {
        setSubmitting(false)
        composer.current?.focus({ preventScroll: true })
      }
    }
  }

  const form = (
    <form className="composer" onSubmit={event => { event.preventDefault(); void submit() }}
      onPaste={event => {
        const listed = Array.from(event.clipboardData.files)
        const incoming = listed.length ? listed : Array.from(event.clipboardData.items).flatMap(item => {
          const file = item.kind === 'file' ? item.getAsFile() : null
          return file ? [file] : []
        })
        if (!incoming.length) return
        event.preventDefault()
        addFiles(incoming)
      }}>
      <input type="file" multiple hidden ref={filePicker} onChange={event => {
        addFiles(Array.from(event.currentTarget.files ?? []))
        event.currentTarget.value = ''
        composer.current?.focus()
      }} />
      {files.length > 0 && <ul className="draft-files" aria-label={t('Attachments to send')}>
        {files.map((file, index) => <li key={file.name + ':' + file.lastModified + ':' + index}>
          <DraftFilePreview file={file} />
          <span className="draft-file-label"><strong title={file.name}>{file.name}</strong>
            <small>{file.size >= 1048576 ? (file.size / 1048576).toFixed(1) + ' MB' : Math.max(1, Math.round(file.size / 1024)) + ' KB'}</small>
          </span>
          <button type="button" disabled={submitting} aria-label={t('Remove {name}', { name: file.name })} onClick={() => {
            setFiles(current => current.filter((_, i) => i !== index))
            setFileIssues([])
          }}><X size={15} /></button>
        </li>)}
      </ul>}
      {fileIssues.length > 0 && <p role="alert" className="feedback attachment-error">{fileIssues.map(issue => t(issue.message, issue.values)).join(' ')}</p>}
      <label className="sr-only" htmlFor="message">{t('Message')}</label>
      <textarea id="message" ref={composer} value={input} rows={2} placeholder={t('Ask anything…')}
        onChange={event => setInput(event.target.value)}
        onKeyDown={event => {
          if (event.key === 'Enter' && !event.shiftKey && !event.nativeEvent.isComposing && event.keyCode !== 229) {
            event.preventDefault()
            void submit()
          }
        }}
      />
      <div className="composer-actions">
        <div className="composer-tools">
          <button type="button" className="attachment-button" aria-label={t('Add attachments')} title={t('Add attachments')}
            disabled={submitting} onClick={() => filePicker.current?.click()}><Plus size={21} /></button>
          <span className="composer-hint">{t('Message Go Bot')}</span>
        </div>
        <div className="composer-submit">
          {activeTurn !== null && <button className="stop-generation" type="button" title={t('Stop generating')}
            aria-label={t('Stop generating')} disabled={stopping} onClick={() => { void stopGenerating() }}>
            {stopping ? <LoaderCircle className="spinner" size={16} /> : <Square size={15} />}
          </button>}
          <button className="send-button" type="submit" aria-label={t('Send message')}
            disabled={(!input.trim() && !files.length) || submitting || modelSaving || permissionSaving || loading || Boolean(historyError)}>
            {submitting ? <LoaderCircle className="spinner" size={20} /> : <ArrowUp size={21} />}
          </button>
        </div>
      </div>
    </form>
  )

  return (
    <main className="workspace" hidden={hidden} inert={drawerOpen || filesOverlay}>
      <WorkspaceHeader title={isMain ? 'Main chat' : 'Go Bot Chat'} actions={headerActions} start={<>
        <button className="icon-button drawer-toggle" type="button" onClick={onOpenMenu} aria-controls="chat-sidebar" aria-label={t('Open chats')} title={t('Open chats')}><PanelLeft size={18} /></button>
        <ModelPicker generating={activeTurn !== null} userId={userId} id={id} settingsVersion={settingsVersion} onSettings={onSettings} onSaving={setModelSaving} />
        {id && <PermissionPicker key={id} userId={userId} sessionId={id} streamState={transcript.permission}
          disabled={activeTurn !== null || submitting} onSaving={setPermissionSaving} />}
      </>} />
      {modelRequired && <ModelRequiredDialog selection={modelRequired === 'selection'} onClose={() => setModelRequired(undefined)}
        onSettings={() => { setModelRequired(undefined); onSettings() }} onChoose={() => {
          setModelRequired(undefined); requestAnimationFrame(() => document.querySelector<HTMLButtonElement>('.model-picker-trigger')?.click())
        }} />}
      {!id ? (
        <div className="chat-empty">
          <HarnessAvatar className="welcome-avatar" />
        </div>
      ) : (
        <div className={'conversation' + (showWelcome ? ' is-welcome' : '')}>
          <div className="welcome-copy" aria-hidden={!showWelcome}>
            <HarnessAvatar className="welcome-avatar" />
            <h1>{t(greeting())}</h1>
            <p>{t('How can I help you today?')}</p>
          </div>
          <div className="transcript-scroll" ref={scroll} onScroll={event => {
            const panel = event.currentTarget
            following.current = panel.scrollHeight - panel.scrollTop - panel.clientHeight < 88
            setAtBottom(following.current)
          }}>
            <div className="transcript" aria-label={t('Conversation')}>
              {loading && <p className="chat-status" role="status">{t('Loading chat…')}</p>}
              {historyError && <div className="chat-status" role="alert">{t(historyError)} <button type="button" className="text-button" onClick={() => setLoadAttempt(attempt => attempt + 1)}>{t('Retry')}</button></div>}
              {before !== null && <div className="history-actions">
                <button type="button" className="text-button" onClick={() => { void loadOlder() }} disabled={loadingOlder}>
                  {loadingOlder ? t('Loading…') : t('Load earlier messages')}
                </button>
                {olderError && <p className="feedback" role="alert">{t(olderError)}</p>}
              </div>}
              {replies.map(message => <MessageBubble key={message.id} message={message} tools={transcript.toolResults} userId={userId} sessionId={id} />)}
              {!loading && !historyError && (waiting || generating) && <div className={waiting ? 'generation-row' : 'generation-row generation-follow'} role="status">
                {waiting && <HarnessAvatar className="reply-mark" />}
                <span className="generation-copy"><span className="generation-dots" aria-hidden="true"><i /><i /><i /></span>{waiting ? t('Preparing a reply…') : t('Generating…')}</span>
              </div>}
            </div>
          </div>
          <div className="chat-input">
            {isMain && transcript.messages.length <= 1 && !loading && <div className="onboarding-actions">
              <button type="button" className="text-button" onClick={onSettings}>{t('Configure model')}</button>
              <button type="button" className="text-button" onClick={onPlugins}>{t('View plugins')}</button>
            </div>}
            {connecting && <p className="chat-status reconnecting" role="status">{t('Reconnecting…')}</p>}
            {failure && <p className="feedback" role="alert">{t(failure)}</p>}
            {!atBottom && <button type="button" className="text-button" onClick={() => {
              const panel = scroll.current
              if (panel) panel.scrollTop = panel.scrollHeight
            }}>{t('Back to latest')}</button>}
            {queue.length > 0 && <ul className="message-queue" aria-label={t('Queued messages')}>
              {queue.map(item => <li key={item.id}>
                <span className="queue-summary">{messageText(item) || t('Attachment')}</span>
                {item.placement === 'steering' ? <small>{t('Waiting to steer')}</small> : <>
                  <button type="button" className="text-button" disabled={actionId === item.id}
                    onClick={() => { void queueAction(item.id, 'steer') }}>{t('Steer')}</button>
                  <button type="button" className="text-button" disabled={actionId === item.id}
                    onClick={() => { void queueAction(item.id, 'delete') }}>{t('Delete')}</button>
                </>}
              </li>)}
            </ul>}
            {id && transcript.questions.length > 0 && <div className="question-dock">
              {transcript.questions.map(item => item.approval
                ? <ApprovalPanel key={item.callId} item={item} userId={userId} sessionId={id} />
                : <QuestionPanel key={item.callId} item={item} userId={userId} sessionId={id} />)}
            </div>}
            {form}
          </div>
        </div>
      )}
    </main>
  )
}



function DraftFilePreview({ file }: { file: File }) {
  const [src, setSrc] = useState('')
  useEffect(() => {
    if (!isMessageImage(file)) return
    const url = URL.createObjectURL(file)
    setSrc(url)
    return () => URL.revokeObjectURL(url)
  }, [file])
  return <span className="draft-file-preview">{isMessageImage(file) && src
    ? <img src={src} alt="" onError={() => setSrc('')} />
    : <FileText size={17} aria-hidden="true" />}</span>
}
