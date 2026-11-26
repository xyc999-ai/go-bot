import { useEffect, useRef, useState } from 'react'
import { useLocale } from '../locale'
import { sidebarBounds, clampSidebarWidth, defaultSidebarWidth } from './sidebarWidth'

const preferenceKey = 'go-bot.desktop-sidebar-width'
function storedWidth() {
  try { const value = Number(localStorage.getItem(preferenceKey)); return value >= 220 && value <= 480 ? value : defaultSidebarWidth } catch { return defaultSidebarWidth }
}

export function SidebarResize({ active }: { active: boolean }) {
  const { t } = useLocale()
  const handle = useRef<HTMLDivElement>(null)
  const preferred = useRef(storedWidth())
  const [width, setWidth] = useState(preferred.current)
  const [maximum, setMaximum] = useState(480)
  const drag = useRef<{ id: number; x: number; width: number; previous: number } | null>(null)
  const update = useRef(() => {})
  const end = useRef((_cancel = false) => {})
  const persist = () => { try { localStorage.setItem(preferenceKey, String(preferred.current)) } catch { /* Keep the in-memory preference. */ } }

  useEffect(() => {
    const node = handle.current!, root = node.closest<HTMLElement>('.app')!
    update.current = () => {
      const bounds = sidebarBounds(window.innerWidth, root.classList.contains('app-files-open'))
      const current = window.innerWidth <= 760 ? defaultSidebarWidth : clampSidebarWidth(preferred.current, bounds.max)
      root.style.setProperty('--sidebar-width', current + 'px')
      setWidth(current); setMaximum(bounds.max)
    }
    end.current = (cancel = false) => {
      const current = drag.current
      if (!current) return
      drag.current = null
      if (cancel) preferred.current = current.previous
      if (node.hasPointerCapture(current.id)) node.releasePointerCapture(current.id)
      root.classList.remove('sidebar-resizing'); document.body.classList.remove('sidebar-resizing')
      update.current(); if (!cancel) persist()
    }
    const blur = () => end.current(true)
    const observer = new MutationObserver(() => update.current())
    observer.observe(root, { attributes: true, attributeFilter: ['class'] })
    window.addEventListener('resize', update.current); window.addEventListener('blur', blur)
    update.current()
    return () => { end.current(true); observer.disconnect(); window.removeEventListener('resize', update.current); window.removeEventListener('blur', blur); root.style.removeProperty('--sidebar-width') }
  }, [])
  useEffect(() => { if (!active) end.current(true) }, [active])

  return <div ref={handle} className="sidebar-resize" role="separator" aria-label={t('Resize chat sidebar')} aria-orientation="vertical"
    aria-valuemin={220} aria-valuemax={maximum} aria-valuenow={width} tabIndex={active ? 0 : -1}
    onPointerDown={event => {
      if (!active || event.button !== 0 || event.pointerType === 'touch' || window.innerWidth <= 760) return
      event.preventDefault()
      event.currentTarget.focus()
      const root = event.currentTarget.closest<HTMLElement>('.app')!
      drag.current = { id: event.pointerId, x: event.clientX, width, previous: preferred.current }
      event.currentTarget.setPointerCapture(event.pointerId)
      root.classList.add('sidebar-resizing'); document.body.classList.add('sidebar-resizing')
    }}
    onPointerMove={event => {
      if (!drag.current || drag.current.id !== event.pointerId) return
      preferred.current = clampSidebarWidth(drag.current.width + event.clientX - drag.current.x, maximum)
      update.current()
    }}
    onPointerUp={() => end.current()} onPointerCancel={() => end.current(true)} onLostPointerCapture={() => end.current()}
    onDoubleClick={() => { preferred.current = defaultSidebarWidth; update.current(); persist() }}
    onKeyDown={event => {
      if (event.key === 'Escape' && drag.current) { event.preventDefault(); event.stopPropagation(); end.current(true); return }
      const next = event.key === 'ArrowLeft' ? width - 16 : event.key === 'ArrowRight' ? width + 16 : event.key === 'Home' ? 220 : event.key === 'End' ? maximum : undefined
      if (next === undefined) return
      event.preventDefault(); preferred.current = clampSidebarWidth(next, maximum); update.current(); persist()
    }} />
}
