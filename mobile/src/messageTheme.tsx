// Preserve the existing transcript appearance while the surrounding app is restyled.
import { Text, StyleSheet, type TextProps } from 'react-native'
export { logo, userBubble } from './ui'
export const ink = '#1d2231', muted = '#5c6072', accent = '#2b396d'
export const edge = 'rgba(98,81,138,0.12)', glass = 'rgba(255,255,255,0.72)'
export const s = StyleSheet.create({
  row: { flexDirection: 'row', alignItems: 'center', gap: 10 },
  caption: { color: muted, fontSize: 13, fontWeight: '600' },
  meta: { color: muted, fontSize: 13, lineHeight: 18 },
})
export function Hint({ children, error = false }: TextProps & { error?: boolean }) {
  return children ? <Text accessibilityLiveRegion="polite" style={{ fontSize: 14, color: error ? '#934f50' : muted, lineHeight: 20 }}>{children}</Text> : null
}
