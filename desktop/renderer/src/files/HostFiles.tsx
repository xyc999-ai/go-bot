import { useCallback, useEffect, useRef, useState } from 'react'
import { useLocale } from '../locale'
import { ChevronDown, ChevronRight, Clipboard, File, Folder, RefreshCw, X } from 'lucide-react'
import { APIError, listHostFiles, type HostFile } from '../api/controller'

type Listing = {
  files: HostFile[]
  nextOffset?: number
  lastOffset?: number
  loading: boolean
  error?: string
}

const emptyListing: Listing = { files: [], loading: false }

function parentPath(path: string) {
  return path.slice(0, Math.max(0, path.lastIndexOf('/')))
}

function fileSize(bytes: number, language: string) {
  if (bytes < 1024) return bytes + ' B'
  const unit = bytes < 1024 ** 2 ? 'KB' : bytes < 1024 ** 3 ? 'MB' : 'GB'
  const divisor = unit === 'KB' ? 1024 : unit === 'MB' ? 1024 ** 2 : 1024 ** 3
  return new Intl.NumberFormat(language, { maximumFractionDigits: 1 }).format(bytes / divisor) + ' ' + unit
}

function readError(error: unknown) {
  if (error instanceof APIError && error.status === 404) return 'This directory is no longer available.'
  if (error instanceof APIError && (error.status === 502 || error.status === 503)) return 'The Host is unavailable. Try again after it reconnects.'
  return 'Could not list this directory.'
}

type Props = { userId: string; open: boolean; overlay: boolean; onClose: () => void }

export function HostFiles({ userId, open, overlay, onClose }: Props) {
  const { language, t } = useLocale()
  const [listings, setListings] = useState(() => new Map<string, Listing>())
  const [expanded, setExpanded] = useState(() => new Set<string>())
  const [selected, setSelected] = useState<HostFile>()
  const [root, setRoot] = useState<{ path: string; storage: string | null; source: string | null }>()
  const [copyStatus, setCopyStatus] = useState('')
  const [refreshing, setRefreshing] = useState(false)
  const pending = useRef(new Map<string, AbortController>())
  const closeButton = useRef<HTMLButtonElement>(null)

  const load = useCallback(async (path: string, offset = 0, refreshThrough?: number) => {
    const inFlight = pending.current.get(path)
    if (inFlight && refreshThrough === undefined) return
    inFlight?.abort()
    const abort = new AbortController()
    pending.current.set(path, abort)
    setListings(current => {
      const next = new Map(current)
      next.set(path, { ...current.get(path) ?? emptyListing, loading: true, error: undefined })
      return next
    })
    try {
      let lastOffset = offset
      let page = await listHostFiles(userId, path, lastOffset, abort.signal)
      const received = new Map(page.files.map(file => [file.path, file]))
      while (page.nextOffset !== undefined && page.nextOffset <= (refreshThrough ?? offset)) {
        lastOffset = page.nextOffset
        page = await listHostFiles(userId, path, lastOffset, abort.signal)
        for (const file of page.files) received.set(file.path, file)
      }
      if (abort.signal.aborted) return
      setRoot({ path: page.rootPath, storage: page.storage, source: page.storageSource })
      setListings(current => {
        const next = new Map(current)
        const files = new Map((offset ? current.get(path)?.files ?? [] : []).map(file => [file.path, file]))
        for (const [filePath, file] of received) files.set(filePath, file)
        next.set(path, { files: [...files.values()], nextOffset: page.nextOffset, lastOffset, loading: false })
        return next
      })
      setSelected(current => {
        const prefix = path ? path + '/' : ''
        if (!current || current.path === path || !current.path.startsWith(prefix)) return current
        if (parentPath(current.path) === path && received.has(current.path)) return received.get(current.path)
        const childPath = prefix + current.path.slice(prefix.length).split('/')[0]
        return offset === 0 && page.nextOffset === undefined && !received.has(childPath) ? undefined : current
      })
    } catch (error) {
      if (abort.signal.aborted) return
      setListings(current => {
        const next = new Map(current)
        next.set(path, { ...current.get(path) ?? emptyListing, loading: false, error: readError(error) })
        return next
      })
    } finally {
      if (pending.current.get(path) === abort) pending.current.delete(path)
    }
  }, [userId])

  useEffect(() => {
    if (open && !listings.has('')) void load('')
  }, [open, listings, load])

  useEffect(() => {
    const requests = pending.current
    return () => {
      for (const request of requests.values()) request.abort()
      requests.clear()
    }
  }, [])

  useEffect(() => {
    if (open) closeButton.current?.focus()
  }, [open])

  function choose(file?: HostFile) {
    setSelected(file)
    setCopyStatus('')
    if (!file?.isDir) return
    const next = new Set(expanded)
    if (next.has(file.path)) {
      next.delete(file.path)
    } else {
      next.add(file.path)
      void load(file.path, 0, listings.get(file.path)?.lastOffset)
    }
    setExpanded(next)
  }

  async function refreshTree() {
    const directories = ['']
    for (const path of directories) {
      for (const file of listings.get(path)?.files ?? []) {
        if (file.isDir && expanded.has(file.path)) directories.push(file.path)
      }
    }
    setRefreshing(true)
    setCopyStatus('')
    try {
      await Promise.all(directories.map(path => load(path, 0, listings.get(path)?.lastOffset ?? 0)))
    } finally {
      setRefreshing(false)
    }
  }

  const path = root && (selected ? root.path + '/' + selected.path : root.path)
  const storageLabel = root?.storage === 'docker-volume' ? 'Docker volume'
    : root?.storage === 'nas' ? 'NAS'
    : root?.storage === 'instance' ? 'Instance filesystem' : null

  async function copyPath() {
    if (!path) return
    try {
      await navigator.clipboard.writeText(path)
      setCopyStatus('Path copied.')
    } catch {
      setCopyStatus('Could not copy the path.')
    }
  }

  function renderDirectory(path: string) {
    const listing = listings.get(path)
    if (!listing) return null
    const files = [...listing.files].sort((a, b) => Number(b.isDir) - Number(a.isDir) || a.name.localeCompare(b.name))
    return <>
      {files.length > 0 && <ul className="host-file-list">
        {files.map(file => {
          const isOpen = expanded.has(file.path)
          return <li key={file.path}>
            <button type="button" className="host-file-row" aria-expanded={file.isDir ? isOpen : undefined}
              aria-current={selected?.path === file.path ? 'true' : undefined} title={file.name} onClick={() => choose(file)}>
              {file.isDir ? <>
                {isOpen ? <ChevronDown size={15} aria-hidden="true" /> : <ChevronRight size={15} aria-hidden="true" />}
                <Folder size={16} aria-hidden="true" />
              </> : <File size={16} className="host-file-leaf" aria-hidden="true" />}
              <span>{file.name}</span>
              {!file.isDir && <small>{fileSize(file.size, language)}</small>}
            </button>
            {file.isDir && isOpen && <div className="host-file-branch">{renderDirectory(file.path)}</div>}
          </li>
        })}
      </ul>}
      {listing.loading && <p className="host-files-state" role="status">{t('Loading…')}</p>}
      {listing.error && <p className="host-files-state" role="alert">{t(listing.error)}
        <button type="button" className="text-button" onClick={() => { void load(path) }}>{t('Retry')}</button>
      </p>}
      {!listing.loading && !listing.error && files.length === 0 && listing.nextOffset === undefined &&
        <p className="host-files-state">{t('Empty directory')}</p>}
      {!listing.loading && listing.nextOffset !== undefined && <button type="button" className="text-button"
        onClick={() => { void load(path, listing.nextOffset) }}>{t('Load more')}</button>}
    </>
  }

  return <>
    {open && overlay && <button type="button" className="host-files-scrim" onClick={onClose}
      tabIndex={-1} aria-label={t('Close file system')} />}
    <aside id="host-files" className="host-files" hidden={!open} role={overlay ? 'dialog' : undefined}
      aria-modal={overlay || undefined} aria-labelledby="host-files-title"
      onKeyDown={event => { if (event.key === 'Escape') onClose() }}>
      <header className="host-files-header">
        <h2 id="host-files-title">{t('File system')}</h2>
        <button type="button" className="icon-button" title={t('Refresh file tree')} aria-label={t('Refresh file tree')}
          disabled={refreshing} onClick={() => { void refreshTree() }}><RefreshCw size={17} /></button>
        <button ref={closeButton} type="button" className="icon-button" title={t('Close file system')}
          aria-label={t('Close file system')} onClick={onClose}><X size={20} /></button>
      </header>
      {root && <div className="host-files-location">
        <div className="host-files-path">
          <code title={path}>{path}</code>
          <button type="button" className="icon-button" title={t('Copy path')} aria-label={t('Copy path')}
            onClick={() => { void copyPath() }}><Clipboard size={16} /></button>
        </div>
        {storageLabel && <div className="host-files-state">{t(storageLabel)}{root.source && <> · <code>{root.source}</code></>}</div>}
        {copyStatus && <div className="host-files-state" role="status">{t(copyStatus)}</div>}
      </div>}
      <div className="host-files-tree">
        {root && <button type="button" className="host-file-row host-file-root" aria-current={selected === undefined ? 'true' : undefined}
          onClick={() => choose()}><Folder size={16} aria-hidden="true" /><span>{root.path}</span></button>}
        {renderDirectory('')}
      </div>
    </aside>
  </>
}
