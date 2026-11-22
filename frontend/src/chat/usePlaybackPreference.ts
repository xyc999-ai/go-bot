import { useEffect, useState } from 'react'

export function usePlaybackPreference() {
  const [enabled, setEnabled] = useState(() => !document.hidden && !matchMedia('(prefers-reduced-motion: reduce)').matches)
  useEffect(() => {
    const motion = matchMedia('(prefers-reduced-motion: reduce)')
    const update = () => setEnabled(!document.hidden && !motion.matches)
    document.addEventListener('visibilitychange', update)
    motion.addEventListener('change', update)
    return () => {
      document.removeEventListener('visibilitychange', update)
      motion.removeEventListener('change', update)
    }
  }, [])
  return enabled
}
