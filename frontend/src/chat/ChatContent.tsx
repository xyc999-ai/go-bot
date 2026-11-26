import { useEffect, useRef, useState, type CSSProperties, type ReactNode } from 'react'
import { useLocale } from '../locale'
import { Check, ChevronLeft, ChevronRight, CircleAlert, Download, Image as Picture, LoaderCircle, RotateCcw, Terminal, Wrench, X } from 'lucide-react'
import { MarkdownText } from './MarkdownText'
import { ChatVideo } from './ChatVideo'
export { MarkdownText } from './MarkdownText'
import { fetchChatFile } from '../api/controller'
import { HarnessAvatar } from '../HarnessAvatar'
import { FileIcon } from '../files/FileIcon'
import { groupDisplayBlocks, messageBlocksForDisplay, messageText, type ChatImage, type FileRef, type Message, type ToolBlock, type Transcript } from './transcript'

type SessionAddress = { userId: string; sessionId?: string }

export function MessageBubble({ message, tools, ...address }: SessionAddress & { message: Message; tools: Transcript['toolResults'] }) {
  const { t } = useLocale()
  if (message.role === 'notice') return <p className="chat-status">{t(messageText(message))}</p>
  const isAssistant = message.role === 'assistant'
  return (
    <article className={'message ' + (isAssistant ? 'assistant-message' : 'user-message')}>
      {isAssistant && <div className="assistant-identity"><HarnessAvatar className="reply-mark" /><span>Go Bot</span></div>}
      <div className={isAssistant ? 'message-blocks markdown' : 'message-blocks'}>
        {groupDisplayBlocks(messageBlocksForDisplay(message), tools).map(block => {
          switch (block.type) {
            case 'text':
              return isAssistant ? <MarkdownText key={block.id} text={block.text} /> : <div key={block.id}>{block.text}</div>
            case 'tool':
              return <ToolCard key={block.id} block={block} progress={tools[block.callId]} />
            case 'tool-group':
              return <ToolGroup key={block.id} blocks={block.tools} tools={tools} />
            case 'image-tools':
              return <ImageToolGroup key={block.id} blocks={block.tools} images={block.images} tools={tools} {...address} />
            case 'video':
              return block.file.source === 'presented' && <ChatVideo key={block.id} path={block.file.path} name={block.name} description={block.description} {...address} />
            case 'file':
              return <FileBlock key={block.id} name={block.name} description={block.description} file={block.file} {...address} />
            case 'image-row':
              return <ImageStrip key={block.id}>
                {block.images.map(image => <ImageBlock key={image.id} image={image} {...address} />)}
              </ImageStrip>
          }
        })}
      </div>
    </article>
  )
}

const toolLabels: Record<string, string> = {
  exit_plan_mode: 'Review the plan',
  read_file: 'Read file', write_file: 'Write file', edit_file: 'Edit file',
  readimage: 'View images', generateimage: 'Generate image',
  bash: 'Run command', shell: 'Run command', shellexecute: 'Run command',
  web_search: 'Search the web', web_fetch: 'Open web page',
  present: 'Present files', skill: 'Load skill',
  schedule_create: 'Create scheduled task', schedule_list: 'List scheduled tasks',
  schedule_view: 'View scheduled task', schedule_edit: 'Edit scheduled task',
}

function ToolGroup({ blocks, tools }: { blocks: ToolBlock[]; tools: Transcript['toolResults'] }) {
  const { t } = useLocale()
  const statuses = blocks.map(block => tools[block.callId]?.status)
  const remaining = statuses.filter(status => !status).length
  const issues = statuses.filter(status => status === 'failed' || status === 'cancelled').length
  return <details className="tool-card tool-group">
    <summary>
      <Wrench size={15} aria-hidden="true" />
      <span>{t('{count} tool steps', { count: blocks.length })}</span>
      {remaining > 0 ? <small>{t('{count} running', { count: remaining })}</small> : issues > 0 ? <small>{t('{count} interrupted', { count: issues })}</small> : <Check size={15} className="tool-state done" aria-label={t('Finished')} />}
      <ChevronRight size={15} className="tool-chevron" aria-hidden="true" />
    </summary>
    <div className="tool-group-items">
      {blocks.map(block => <ToolCard key={block.id} block={block} progress={tools[block.callId]} />)}
    </div>
  </details>
}

function ToolCard({ block, progress }: { block: ToolBlock; progress?: Transcript['toolResults'][string] }) {
  const { t } = useLocale()
  const label = toolLabels[block.name] ?? block.name.replace(/[_-]+/g, ' ')
  const status = progress?.status
  const outcome = status === 'succeeded' ? 'Done' : status === 'failed' ? 'Failed' : status === 'cancelled' ? 'Stopped' : 'Running'
  const duration = progress?.startedAt !== undefined && progress.endedAt !== undefined
    ? Math.max(0, progress.endedAt - progress.startedAt) : undefined
  const timing = duration === undefined ? '' : duration < 1000 ? duration + ' ms' : (duration / 1000).toFixed(1) + ' s'
  const row = <><Terminal size={14} aria-hidden="true" /><span>{t(label)}</span>
    {status === 'succeeded' ? <Check size={14} className="tool-state done" aria-label={t(outcome)} />
      : status ? <CircleAlert size={14} className="tool-state issue" aria-label={t(outcome)} />
        : <LoaderCircle size={14} className="tool-state spinner" aria-label={t(outcome)} />}</>
  if (!timing && !progress?.code) return <div className="tool-card"><div className="tool-summary">{row}</div></div>
  return <details className="tool-card">
    <summary>{row}<ChevronRight size={14} className="tool-chevron" aria-hidden="true" /></summary>
    <div className="tool-detail">{[timing && t('Duration {time}', { time: timing }), progress?.code?.replaceAll('_', ' ').toLowerCase()].filter(Boolean).join(' · ')}</div>
  </details>
}

function FileBlock({ name, description, file, userId, sessionId }: SessionAddress & { name?: string; description?: string; file: FileRef }) {
  const { t } = useLocale()
  const filename = name || t('Attachment')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  async function download() {
    if (!sessionId) return
    setBusy(true)
    setError('')
    try {
      const blob = await fetchChatFile(userId, sessionId, file, name)
      const href = URL.createObjectURL(blob)
      const link = document.createElement('a')
      link.href = href
      link.download = filename
      link.click()
      window.setTimeout(() => URL.revokeObjectURL(href), 1000)
    } catch {
      setError('File unavailable.')
    } finally {
      setBusy(false)
    }
  }
  return <div className="file-delivery">
    <button type="button" className="chat-file" disabled={busy || !sessionId} onClick={() => { void download() }}>
      <FileIcon name={filename} /><span className="chat-file-copy"><strong>{filename}</strong><small>{description || t('Download file')}</small></span>
      {busy ? <LoaderCircle size={17} className="spinner" /> : <Download size={17} aria-hidden="true" />}
    </button>
    {error && <div className="feedback" role="status">{t(error)}</div>}
  </div>
}

function ImageStrip({ children, compact = false }: { children: ReactNode; compact?: boolean }) {
  const { t } = useLocale()
  const ref = useRef<HTMLDivElement>(null)
  const [edges, setEdges] = useState({ left: 1, right: 1 })
  useEffect(() => {
    const node = ref.current
    if (!node) return
    const measure = () => setEdges({
      left: 1 - Math.min(1, Math.max(0, node.scrollLeft - 2) / 24),
      right: 1 - Math.min(1, Math.max(0, node.scrollWidth - node.clientWidth - node.scrollLeft - 2) / 24),
    })
    const observer = new ResizeObserver(measure)
    observer.observe(node)
    if (node.firstElementChild) observer.observe(node.firstElementChild)
    node.addEventListener('scroll', measure, { passive: true })
    measure()
    return () => { observer.disconnect(); node.removeEventListener('scroll', measure) }
  }, [children])
  const shift = (direction: number) => ref.current?.scrollBy({ left: direction * 140, behavior: window.matchMedia('(prefers-reduced-motion: reduce)').matches ? 'instant' : 'smooth' })
  return <div className="chat-image-scroller"><div ref={ref} className={'chat-image-row' + (compact ? ' compact-images' : '')}
    role="region" aria-label={t('Images — scroll horizontally')} tabIndex={0}
    style={{ '--image-edge-left': edges.left, '--image-edge-right': edges.right } as CSSProperties}
    onKeyDown={event => {
      if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return
      event.preventDefault()
      shift(event.key === 'ArrowLeft' ? -1 : 1)
    }}>{children}</div>
    {edges.left < 1 && <button className="image-scroll-arrow previous" type="button" aria-label={t('Previous images')} onClick={() => shift(-1)}><ChevronLeft size={16} /></button>}
    {edges.right < 1 && <button className="image-scroll-arrow next" type="button" aria-label={t('Next images')} onClick={() => shift(1)}><ChevronRight size={16} /></button>}
  </div>
}

function ImageToolGroup({ blocks, images, tools, ...address }: SessionAddress & { blocks: ToolBlock[]; images: ChatImage[]; tools: Transcript['toolResults'] }) {
  const { t } = useLocale()
  const [open, setOpen] = useState(true)
  const running = blocks.filter(block => !tools[block.callId]?.status).length
  const failed = blocks.some(block => tools[block.callId]?.status === 'failed' || tools[block.callId]?.status === 'cancelled')
  const count = images.length + running || blocks.length
  return <section className="image-tool-group">
    <button className="image-tool-header" type="button" aria-expanded={open} onClick={() => setOpen(value => !value)}>
      <Picture size={15} aria-hidden="true" /><span>{t(blocks[0].name === 'readimage' ? 'View images' : 'Generate image')}</span>
      <small>{count === 1 ? t('1 image') : t('{count} images', { count })}</small>
      {running ? <LoaderCircle size={13} className="spinner image-tool-state" aria-label={t('Running')} />
        : failed ? <CircleAlert size={13} className="image-tool-state" aria-label={t('Failed')} />
          : <Check size={13} className="image-tool-state" aria-label={t('Finished')} />}
      <ChevronRight size={14} className={open ? 'image-tool-chevron expanded' : 'image-tool-chevron'} aria-hidden="true" />
    </button>
    <div className="image-tool-body" hidden={!open}>
      {(images.length > 0 || running > 0) && <ImageStrip compact>
        {images.map(image => <ImageBlock key={image.id} image={image} {...address} />)}
        {Array.from({ length: Math.min(3, running) }, (_, index) => <div className="chat-image-placeholder" key={'pending/' + index} aria-label={t('Loading image')}><LoaderCircle size={18} className="spinner" /></div>)}
      </ImageStrip>}
      {!images.length && !running && <p className="chat-status">{t('No images to display.')}</p>}
    </div>
  </section>
}

function ImageBlock({ image, userId, sessionId }: SessionAddress & { image: ChatImage }) {
  const { t } = useLocale()
  const { file, name, width, height } = image
  const identity = file.source === 'attachment' ? file.attachmentId : file.path
  const [href, setHref] = useState('')
  const [failed, setFailed] = useState(false)
  const [retry, setRetry] = useState(0)
  const [preview, setPreview] = useState(false)
  const dialog = useRef<HTMLDialogElement>(null)
  useEffect(() => {
    if (preview) dialog.current?.showModal()
    else dialog.current?.close()
  }, [preview])
  useEffect(() => {
    if (!sessionId) return
    const abort = new AbortController()
    let objectURL = ''
    setHref('')
    setFailed(false)
    void fetchChatFile(userId, sessionId, file, name, abort.signal).then(blob => {
      if (abort.signal.aborted) return
      objectURL = URL.createObjectURL(blob)
      setHref(objectURL)
    }).catch(() => { if (!abort.signal.aborted) setFailed(true) })
    return () => { abort.abort(); if (objectURL) URL.revokeObjectURL(objectURL) }
  }, [userId, sessionId, file.source, identity, name, retry])
  if (failed) return file.source === 'presented'
    ? <FileBlock name={name} file={file} userId={userId} sessionId={sessionId} />
    : <button className="chat-image-retry" type="button" onClick={() => setRetry(value => value + 1)}><RotateCcw size={18} /><span>{t('Image unavailable. Retry')}</span></button>
  if (!href) return <div className="chat-image-placeholder" aria-label={t('Loading image')}><LoaderCircle size={18} className="spinner" /></div>
  return <><button type="button" className="chat-image-button" aria-label={name || t('Open image')} onClick={() => setPreview(true)}>
    <img className="chat-image" src={href} alt={name || t('Attachment')} width={width} height={height} onError={() => setFailed(true)} />
  </button><dialog ref={dialog} className="image-preview" aria-label={t('Image preview')} onCancel={event => { event.preventDefault(); setPreview(false) }} onClick={event => { if (event.target === event.currentTarget) setPreview(false) }}>
    <div className="image-preview-actions"><a className="icon-button" href={href} download={name || 'image'} aria-label={t('Download image')}><Download size={21} /></a><button type="button" className="icon-button" aria-label={t('Close image')} onClick={() => setPreview(false)}><X size={23} /></button></div>
    <img src={href} alt={name || t('Attachment')} />
  </dialog></>
}
