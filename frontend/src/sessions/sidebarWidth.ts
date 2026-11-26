export const defaultSidebarWidth = 264
export function sidebarBounds(viewport: number, filesOpen: boolean) {
  return { min: 220, max: Math.max(220, Math.min(480, viewport - 64 - (filesOpen && viewport >= 1200 ? 320 : 0) - 480)) }
}
export function clampSidebarWidth(value: number, maximum: number) {
  return Math.max(220, Math.min(maximum, Number.isFinite(value) ? value : defaultSidebarWidth))
}
