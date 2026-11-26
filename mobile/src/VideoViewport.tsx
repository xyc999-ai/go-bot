import { createContext, useContext, useEffect, useRef, useState, type ReactNode, type ComponentRef } from 'react'
import { AppState, View } from 'react-native'

// Scroll notifications avoid re-rendering every message for every scroll offset.
const listeners = new Set<() => void>()
export function notifyVideoViewport() { for (const listener of listeners) listener() }
const Viewport = createContext<{ active: boolean; frame: React.RefObject<ComponentRef<typeof View> | null> } | null>(null)
export function VideoViewport({ active, children }: { active: boolean; children: ReactNode }) {
  const frame = useRef<ComponentRef<typeof View>>(null)
  return <Viewport.Provider value={{ active, frame }}><View ref={frame} style={{ flex: 1 }} onLayout={notifyVideoViewport}>{children}</View></Viewport.Provider>
}
export function useVideoVisibility(frame: React.RefObject<ComponentRef<typeof View> | null>) {
  const viewport = useContext(Viewport)
  const [state, setState] = useState({ near: false, visible: false })
  const active = viewport?.active ?? false
  useEffect(() => {
    if (!active) { setState({ near: false, visible: false }); return }
    let disposed = false, timer: ReturnType<typeof setTimeout> | undefined
    const measure = () => {
      clearTimeout(timer)
      timer = setTimeout(() => {
        viewport?.frame.current?.measureInWindow((_x, top, _w, height) => {
          frame.current?.measureInWindow((_vx, y, _vw, h) => {
            if (disposed) return
            const next = { near: y + h > top - 180 && y < top + height + 180, visible: y + h > top && y < top + height }
            setState(old => old.near === next.near && old.visible === next.visible ? old : next)
          })
        })
      }, 80)
    }
    listeners.add(measure); measure()
    const subscription = AppState.addEventListener('change', value => {
      if (value !== 'active') setState({ near: false, visible: false })
      else measure()
    })
    return () => { disposed = true; clearTimeout(timer); listeners.delete(measure); subscription.remove() }
  }, [active, frame, viewport?.frame])
  return state
}
