// Playback arbitration is local to a client, never a Host/Redis setting.
let active: (() => void) | undefined
export function claimVideoPlayback(pause: () => void) {
  if (active !== pause) active?.()
  active = pause
}
export function releaseVideoPlayback(pause: () => void) {
  if (active === pause) active = undefined
}
export function videoTime(seconds: number) {
  const value = Number.isFinite(seconds) ? Math.max(0, Math.floor(seconds)) : 0
  return `${Math.floor(value / 60)}:${String(value % 60).padStart(2, '0')}`
}

export const videoChinese: Record<string, string> = {
  'Video player': '视频播放器', 'Play video': '播放视频', 'Pause video': '暂停视频',
  'Expand video': '全屏查看视频', 'Close video': '关闭视频', 'Video progress': '视频进度',
  'Mute video': '静音', 'Unmute video': '取消静音', 'Download video': '下载视频',
  'Loading video': '正在加载视频', 'Show video controls': '显示播放控件',
  'Video unavailable or unsupported. You can still download the file.': '视频暂不可用或当前设备不支持播放，可下载后打开。',
}
