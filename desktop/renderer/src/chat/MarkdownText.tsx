import { memo, useEffect, useRef, useState, type ReactNode } from 'react'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { Check, Copy } from 'lucide-react'
import { useLocale } from '../locale'

async function copyMarkdown(text: string) {
  if (navigator.clipboard && window.isSecureContext) return navigator.clipboard.writeText(text)
  // Plain HTTP deployments still support a user-initiated copy.
  const previous = document.activeElement as HTMLElement | null
  const input = document.createElement('textarea')
  input.value = text
  input.style.cssText = 'position:fixed;left:-9999px;top:0'
  document.body.append(input)
  try {
    input.select()
    if (!document.execCommand('copy')) throw new Error('copy failed')
  } finally { input.remove(); previous?.focus({ preventScroll: true }) }
}

function MarkdownPanel({ kind, raw, language, children }: { kind: 'table' | 'code'; raw: string; language?: string; children: ReactNode }) {
  const { t } = useLocale()
  const [status, setStatus] = useState<'idle' | 'copied' | 'failed'>('idle')
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  useEffect(() => () => clearTimeout(timer.current), [])
  const label = status === 'copied' ? 'Copied' : status === 'failed' ? 'Copy failed. Try again.' : kind === 'table' ? 'Copy table' : 'Copy code'
  return <div className={'markdown-panel markdown-' + kind + '-panel'}>
    <div className="markdown-panel-toolbar"><span>{kind === 'table' ? t('Table') : language || t('Code')}</span>
      <button type="button" className="markdown-copy" aria-label={t(label)} onClick={async () => {
        clearTimeout(timer.current)
        try { await copyMarkdown(raw); setStatus('copied') } catch { setStatus('failed') }
        timer.current = setTimeout(() => setStatus('idle'), 2200)
      }}>{status === 'copied' ? <Check size={13} /> : <Copy size={13} />}<span aria-live="polite">{t(label)}</span></button>
    </div>
    <div className={kind === 'table' ? 'markdown-table-scroll' : 'markdown-code-scroll'} role="region" aria-label={t(kind === 'table' ? 'Table — scroll horizontally' : 'Code — scroll horizontally')} tabIndex={0}>{children}</div>
  </div>
}

export const MarkdownText = memo(function MarkdownText({ text }: { text: string }) {
  const raw = (node: { position?: { start: { offset?: number }; end: { offset?: number } } } | undefined) => {
    const start = node?.position?.start.offset, end = node?.position?.end.offset
    return start !== undefined && end !== undefined ? text.slice(start, end) : ''
  }
  return <div><ReactMarkdown remarkPlugins={[remarkGfm]} skipHtml components={{
    a: ({ children, href }) => href && /^(https?:|mailto:|#)/i.test(href) ? <a href={href} target="_blank" rel="noreferrer">{children}</a> : <span>{children}</span>,
    img: ({ node, alt }) => raw(node) || alt,
    table: ({ node, children }) => <MarkdownPanel kind="table" raw={raw(node)}><table>{children}</table></MarkdownPanel>,
    pre: ({ node, children }) => {
      const code = node?.children.find(child => child.type === 'element' && child.tagName === 'code')
      const language = code?.type === 'element' ? String(code.properties.className || '').match(/language-([\w+-]+)/)?.[1] : undefined
      return <MarkdownPanel kind="code" raw={raw(node)} language={language}><pre>{children}</pre></MarkdownPanel>
    },
  }}>{text}</ReactMarkdown></div>
})
