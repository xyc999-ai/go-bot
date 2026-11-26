import { useCallback, useEffect, useRef, useState } from 'react'
import { Download, LoaderCircle, Maximize2, Pause, Play, RotateCcw, Volume2, VolumeX, X } from 'lucide-react'
import { chatVideoURL, fetchChatFile } from '../api/controller'
import { useLocale } from '../locale'
import { claimVideoPlayback, releaseVideoPlayback, videoTime } from './video'
import './video.css'

export function ChatVideo({ userId, sessionId, path, name, description }: {
  userId: string; sessionId?: string; path: string; name?: string; description?: string
}) {
  const { t } = useLocale()
  const video = useRef<HTMLVideoElement>(null), dialog = useRef<HTMLDialogElement>(null)
  const root = useRef<HTMLDivElement>(null), expand = useRef<HTMLButtonElement>(null)
  const seeking = useRef(false)
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const [near, setNear] = useState(false), [visible, setVisible] = useState(true)
  const [fullscreen, setFullscreen] = useState(false), [playing, setPlaying] = useState(false)
  const [controls, setControls] = useState(true), [buffering, setBuffering] = useState(false)
  const [duration, setDuration] = useState(0), [position, setPosition] = useState(0)
  const [muted, setMuted] = useState(false), [failed, setFailed] = useState(false)
  const [ratio, setRatio] = useState(16 / 9)
  const [downloading, setDownloading] = useState(false), [downloadError, setDownloadError] = useState(false)
  const pause = useCallback(() => video.current?.pause(), [])
  const filename = name || path.split('/').at(-1) || 'video.mp4'
  const source = sessionId ? chatVideoURL(userId, sessionId, path) : ''
  const reveal = useCallback(() => {
    setControls(true)
    clearTimeout(timer.current)
    if (video.current && !video.current.paused && !seeking.current) timer.current = setTimeout(() => setControls(false), 2200)
  }, [])
  useEffect(() => {
    const node = root.current
    if (!node) return
    const prepare = new IntersectionObserver(entries => {
      if (entries[0].isIntersecting) { setNear(true); prepare.disconnect() }
    }, { rootMargin: '180px' })
    const visibility = new IntersectionObserver(entries => setVisible(entries[0].isIntersecting))
    prepare.observe(node); visibility.observe(node)
    return () => { prepare.disconnect(); visibility.disconnect(); clearTimeout(timer.current); releaseVideoPlayback(pause) }
  }, [pause])
  useEffect(() => { if (!visible && !fullscreen) pause() }, [visible, fullscreen, pause])
  useEffect(() => {
    const hide = () => { if (document.hidden) pause() }
    document.addEventListener('visibilitychange', hide)
    return () => { document.removeEventListener('visibilitychange', hide); pause() }
  }, [pause])
  async function download() {
    if (!sessionId) return
    setDownloading(true); setDownloadError(false)
    try {
      const blob = await fetchChatFile(userId, sessionId, { source: 'presented', path }, filename)
      const href = URL.createObjectURL(blob), link = document.createElement('a')
      link.href = href; link.download = filename; link.click()
      setTimeout(() => URL.revokeObjectURL(href), 1000)
    } catch { setDownloadError(true) } finally { setDownloading(false) }
  }
  function close() {
    pause(); setFullscreen(false); setControls(true)
    dialog.current?.close(); dialog.current?.show()
    expand.current?.focus({ preventScroll: true })
  }
  function enter() {
    dialog.current?.close(); dialog.current?.showModal()
    setFullscreen(true); reveal()
  }
  async function toggle() {
    const player = video.current
    if (!player) return
    reveal()
    if (!player.paused) { player.pause(); return }
    if (player.ended) player.currentTime = 0
    try { await player.play() } catch { setFailed(true) }
  }
  return <div className="video-delivery" ref={root}>
    <div className="chat-video-placeholder">
      <dialog ref={dialog} open className={'chat-video-dialog' + (fullscreen ? ' is-fullscreen' : '')}
        aria-label={t('Video player')} onCancel={event => { event.preventDefault(); close() }}
        onClick={event => { if (fullscreen && event.target === event.currentTarget) close() }}>
        {fullscreen && <button type="button" className="video-close video-control" aria-label={t('Close video')} onClick={close}><X size={22} /></button>}
        <div className={'chat-video-frame' + (controls || !playing || failed ? ' controls-visible' : '')}
          style={fullscreen ? { aspectRatio: ratio, width: `min(calc(100vw - 32px), calc((100dvh - 112px) * ${ratio}))` } : undefined}
          onPointerMove={reveal} onPointerLeave={() => { if (playing && !seeking.current) setControls(false) }} onFocus={reveal}>
          <video ref={video} src={near && source ? source : undefined} preload="metadata" playsInline
            onClick={() => { if (playing) setControls(value => !value); else void toggle() }}
            onLoadedMetadata={event => { const v = event.currentTarget; setDuration(v.duration); if (v.videoWidth && v.videoHeight) setRatio(v.videoWidth / v.videoHeight) }}
            onTimeUpdate={event => setPosition(event.currentTarget.currentTime)}
            onPlay={() => { claimVideoPlayback(pause); setPlaying(true); reveal() }}
            onPause={() => { setPlaying(false); setControls(true); setBuffering(false) }}
            onEnded={() => setPlaying(false)} onWaiting={() => setBuffering(true)}
            onPlaying={() => { setBuffering(false); setFailed(false) }} onCanPlay={() => setBuffering(false)} onError={() => { setFailed(true); setBuffering(false) }} />
          {!fullscreen && <button ref={expand} type="button" className="video-expand video-control" aria-label={t('Expand video')} onClick={enter}><Maximize2 size={18} /></button>}
          {failed ? <div className="video-error" role="status"><span>{t('Video unavailable or unsupported. You can still download the file.')}</span><button type="button" className="video-control" onClick={() => { setFailed(false); video.current?.load() }}><RotateCcw size={16} />{t('Retry')}</button></div>
            : !playing && <button type="button" className="video-play video-control" aria-label={t('Play video')} onClick={() => void toggle()}><Play size={25} fill="currentColor" /></button>}
          {buffering && !failed && <LoaderCircle className="video-buffering spinner" size={24} aria-label={t('Loading video')} />}
          {!failed && <div className="video-controls" onPointerDown={event => event.stopPropagation()}>
            <button type="button" className="video-control" aria-label={t(playing ? 'Pause video' : 'Play video')} onClick={() => void toggle()}>{playing ? <Pause size={18} /> : <Play size={18} />}</button>
            <span className="video-time">{videoTime(position)} / {videoTime(duration)}</span>
            <input type="range" min="0" max={Number.isFinite(duration) ? duration : 0} step="0.1" value={Math.min(position, duration || 0)} aria-label={t('Video progress')}
              onPointerDown={() => { seeking.current = true; clearTimeout(timer.current); setControls(true) }} onPointerUp={() => { seeking.current = false; reveal() }} onPointerCancel={() => { seeking.current = false; reveal() }}
              onChange={event => { const time = Number(event.target.value); if (video.current) video.current.currentTime = time; setPosition(time) }} />
            <button type="button" className="video-control" aria-label={t(muted ? 'Unmute video' : 'Mute video')} onClick={() => { if (video.current) video.current.muted = !muted; setMuted(!muted); reveal() }}>{muted ? <VolumeX size={18} /> : <Volume2 size={18} />}</button>
          </div>}
        </div>
      </dialog>
    </div>
    <div className="video-caption"><span title={filename}>{filename}</span>{source && <button type="button" className="video-download" disabled={downloading} onClick={() => void download()} aria-label={t('Download video')}>{downloading ? <LoaderCircle className="spinner" size={17} /> : <Download size={17} />}</button>}</div>
    {downloadError && <p className="feedback" role="status">{t('File unavailable.')}</p>}
    {description && <p className="video-description">{description}</p>}
  </div>
}
