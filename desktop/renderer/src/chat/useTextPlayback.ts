import { useLayoutEffect, useMemo, useReducer } from 'react'
import type { Message } from './transcript'
import { TextPlayback } from './textPlayback'

// Both clients provide visibility/reduced-motion preferences. Keep messages
// memoized at the caller: playback ticks must not recreate the source array.
export function useTextPlayback(scope: string, messages: Message[], enabled: boolean, epoch = 0) {
  const player = useMemo(() => new TextPlayback(), [scope])
  const animation = useMemo(() => ({ frame: 0, lastRender: 0, running: false }), [player])
  const [, redraw] = useReducer(value => value + 1, 0)
  useLayoutEffect(() => {
    player.sync(messages, enabled, epoch, performance.now())
    redraw()
    if (!player.pending) {
      animation.running = false
      cancelAnimationFrame(animation.frame)
      return
    }
    // New network chunks update the target without cancelling/restarting
    // the animation clock. This also keeps the 30fps cap across dense chunks.
    if (animation.running) return
    animation.running = true
    animation.lastRender = 0
    const tick = (now: number) => {
      if (!animation.running) return
      if (now - animation.lastRender >= 32) {
        animation.lastRender = now
        if (player.advance(now)) redraw()
      }
      if (player.pending) animation.frame = requestAnimationFrame(tick)
      else animation.running = false
    }
    animation.frame = requestAnimationFrame(tick)
  }, [player, animation, messages, enabled, epoch])
  useLayoutEffect(() => () => {
    animation.running = false
    cancelAnimationFrame(animation.frame)
  }, [animation])
  return player.view()
}
