import { useCallback, useEffect, useRef, useState, type ComponentRef } from 'react'
import { ActivityIndicator, AppState, Modal, PanResponder, Pressable, StatusBar, StyleSheet, Text, View, useWindowDimensions } from 'react-native'
import { useEvent } from 'expo'
import { useVideoPlayer, VideoView } from 'expo-video'
import { Download, Maximize2, Pause, Play, RotateCcw, Volume2, VolumeX, X } from 'lucide-react-native'
import { LinearGradient } from 'expo-linear-gradient'
import { useLocale } from './locale'
import { Controller } from './api'
import { saveRemote, shareLibraryItem } from './library'
import { muted, Hint } from './messageTheme'
import { useReducedMotion } from './ui'
import { useVideoVisibility, notifyVideoViewport } from './VideoViewport'
import { claimVideoPlayback, releaseVideoPlayback, videoTime } from '../../frontend/src/chat/video'

export function ChatVideo({ api, sessionId, path, name, description }: { api: Controller; sessionId: string; path: string; name?: string; description?: string }) {
  const { t } = useLocale(), frame = useRef<ComponentRef<typeof View>>(null), { near, visible } = useVideoVisibility(frame)
  const [fullscreen, setFullscreen] = useState(false), [busy, setBusy] = useState(false), [error, setError] = useState(false)
  const progress = useRef(0), filename = name || path.split('/').at(-1) || 'video.mp4'
  const uri = api.fileURL(sessionId, { source: 'presented', path }, filename) + '&inline=1'
  return <View ref={frame} style={styles.delivery} onLayout={notifyVideoViewport}>
    {near || fullscreen ? <Player key={uri} uri={uri} headers={api.headers} visible={visible} progress={progress} fullscreen={fullscreen} setFullscreen={setFullscreen} />
      : <View style={styles.placeholder}><Play size={26} color="#fff" /></View>}
    <View style={styles.caption}><Text numberOfLines={1} style={styles.filename}>{filename}</Text>
      <Pressable accessibilityRole="button" accessibilityLabel={t('Download video')} disabled={busy} style={styles.download} onPress={async () => {
        setBusy(true); setError(false)
        try {
          const saved = await saveRemote({ uri: api.fileURL(sessionId, { source: 'presented', path }, filename), headers: api.headers, name: filename }, 'file', { account: api.profile.account, path })
          await shareLibraryItem(saved)
        } catch { setError(true) } finally { setBusy(false) }
      }}>{busy ? <ActivityIndicator color={muted} size="small" /> : <Download size={18} color={muted} />}</Pressable>
    </View>
    {!!description && <Text style={styles.description}>{description}</Text>}
    {error && <Hint error>{t('文件暂不可用。')}</Hint>}
  </View>
}
function Player({ uri, headers, visible, progress, fullscreen, setFullscreen }: {
  uri: string; headers: Record<string, string>; visible: boolean; progress: React.RefObject<number>; fullscreen: boolean; setFullscreen: (value: boolean) => void
}) {
  const { t } = useLocale(), dimensions = useWindowDimensions(), reducedMotion = useReducedMotion()
  // The URL already carries an explicit inline media request; headers route the account.
  const player = useVideoPlayer({ uri, headers }, value => { value.timeUpdateEventInterval = .25 })
  const { isPlaying } = useEvent(player, 'playingChange', { isPlaying: player.playing })
  const { status } = useEvent(player, 'statusChange', { status: player.status })
  const { currentTime } = useEvent(player, 'timeUpdate', { currentTime: progress.current, bufferedPosition: 0, currentLiveTimestamp: null, currentOffsetFromLive: null })
  const [controls, setControls] = useState(true), [isMuted, setMuted] = useState(false)
  const [ratio, setRatio] = useState(16 / 9), [retrying, setRetrying] = useState(false)
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined), restore = useRef(progress.current)
  const pause = useCallback(() => player.pause(), [player])
  const reveal = () => {
    setControls(true); clearTimeout(timer.current)
    if (player.playing) timer.current = setTimeout(() => setControls(false), 2200)
  }
  useEffect(() => {
    const playing = player.addListener('playingChange', ({ isPlaying }) => { if (isPlaying) claimVideoPlayback(pause); else setControls(true) })
    const time = player.addListener('timeUpdate', event => { progress.current = event.currentTime })
    const load = player.addListener('sourceLoad', event => {
      const size = event.availableVideoTracks[0]?.size
      if (size?.width && size?.height) setRatio(size.width / size.height)
      if (restore.current > 0) { player.currentTime = restore.current; restore.current = 0 }
    })
    const app = AppState.addEventListener('change', state => { if (state !== 'active') pause() })
    return () => { clearTimeout(timer.current); playing.remove(); time.remove(); load.remove(); app.remove(); releaseVideoPlayback(pause) }
  }, [player, pause, progress])
  useEffect(() => { if (!visible && !fullscreen) pause() }, [visible, fullscreen, pause])
  useEffect(() => { if (isPlaying) { setControls(true); clearTimeout(timer.current); timer.current = setTimeout(() => setControls(false), 2200) } }, [isPlaying])
  const toggle = () => { if (player.playing) player.pause(); else { if (player.duration > 0 && player.currentTime >= player.duration - .1) player.currentTime = 0; player.play() }; reveal() }
  const close = () => { player.pause(); setFullscreen(false); setControls(true) }
  const width = Math.min(dimensions.width - 24, (dimensions.height - 112) * ratio)
  const button = (label: string, icon: React.ReactNode, action: () => void, style?: object) => <Pressable accessibilityRole="button" accessibilityLabel={t(label)} onPress={action} style={[styles.control, style]}>{icon}</Pressable>
  // Reattach a paused frame when switching between inline and fullscreen views.
  const refreshFrame = () => {
    if (!player.playing && player.duration > 0) player.currentTime = Math.min(player.currentTime, Math.max(0, player.duration - .05))
  }
  const video = <View style={[styles.frame, fullscreen && { width, aspectRatio: ratio }]}>
    <VideoView onLayout={refreshFrame} player={player} nativeControls={false} surfaceType="textureView" contentFit="contain" style={StyleSheet.absoluteFill} />
    <Pressable style={StyleSheet.absoluteFill} accessibilityRole="button" accessibilityLabel={t(isPlaying ? 'Show video controls' : 'Play video')} onPress={() => { if (!isPlaying) toggle(); else { setControls(old => !old); clearTimeout(timer.current) } }} />
    {!fullscreen && button('Expand video', <Maximize2 size={18} color="#fff" />, () => { setFullscreen(true); reveal() }, styles.expand)}
    {status === 'error' ? <View style={styles.error}><Text style={styles.errorText}>{t('Video unavailable or unsupported. You can still download the file.')}</Text>
      {button('Retry', retrying ? <ActivityIndicator color="#fff" /> : <RotateCcw size={19} color="#fff" />, () => { setRetrying(true); void player.replaceAsync({ uri, headers }).catch(() => {}).finally(() => setRetrying(false)) })}</View>
      : <>
        {!isPlaying && button('Play video', <Play size={25} color="#fff" fill="#fff" />, toggle, styles.play)}
        {status === 'loading' && <ActivityIndicator style={styles.loading} color="#fff" />}
        {(controls || !isPlaying) && <LinearGradient colors={['transparent', '#07090bcc']} style={styles.controls}>
          {button(isPlaying ? 'Pause video' : 'Play video', isPlaying ? <Pause size={18} color="#fff" /> : <Play size={18} color="#fff" />, toggle, styles.flat)}
          <Text style={styles.time}>{videoTime(currentTime)} / {videoTime(player.duration)}</Text>
          <SeekBar value={currentTime} duration={player.duration} seek={value => { player.currentTime = value; progress.current = value }} touch={dragging => { clearTimeout(timer.current); if (!dragging) reveal() }} />
          {button(isMuted ? 'Unmute video' : 'Mute video', isMuted ? <VolumeX size={18} color="#fff" /> : <Volume2 size={18} color="#fff" />, () => { player.muted = !isMuted; setMuted(!isMuted); reveal() }, styles.flat)}
        </LinearGradient>}
      </>}
  </View>
  return <>
    {fullscreen ? <View style={styles.placeholder} /> : video}
    <Modal visible={fullscreen} onShow={refreshFrame} animationType={reducedMotion ? 'none' : 'fade'} presentationStyle="fullScreen" statusBarTranslucent navigationBarTranslucent supportedOrientations={['portrait', 'landscape']} onRequestClose={close}>
      <StatusBar hidden /><View style={styles.fullscreen}>
        <Pressable style={StyleSheet.absoluteFill} accessibilityRole="button" accessibilityLabel={t('Close video')} onPress={close} />
        {fullscreen && video}
        {button('Close video', <X size={23} color="#fff" />, close, styles.close)}
      </View>
    </Modal>
  </>
}
function SeekBar({ value, duration, seek, touch }: { value: number; duration: number; seek: (value: number) => void; touch: (dragging: boolean) => void }) {
  const { t } = useLocale(), width = useRef(1), latest = useRef({ duration, seek, touch })
  latest.current = { duration, seek, touch }
  const move = (x: number) => latest.current.seek(Math.max(0, Math.min(1, x / width.current)) * (latest.current.duration || 0))
  const pan = useRef(PanResponder.create({
    onStartShouldSetPanResponder: () => true, onMoveShouldSetPanResponder: () => true,
    onPanResponderGrant: event => { latest.current.touch(true); move(event.nativeEvent.locationX) },
    onPanResponderMove: event => move(event.nativeEvent.locationX),
    onPanResponderRelease: () => latest.current.touch(false), onPanResponderTerminate: () => latest.current.touch(false),
  })).current
  const fraction = duration > 0 ? Math.min(1, value / duration) : 0
  return <View accessibilityRole="adjustable" accessibilityLabel={t('Video progress')} accessibilityValue={{ min: 0, max: duration || 0, now: value || 0 }}
    accessibilityActions={[{ name: 'increment' }, { name: 'decrement' }]} onAccessibilityAction={event => seek(Math.max(0, Math.min(duration, value + (event.nativeEvent.actionName === 'increment' ? 5 : -5))))}
    style={styles.seek} onLayout={event => { width.current = event.nativeEvent.layout.width }} {...pan.panHandlers}>
    <View pointerEvents="none" style={styles.track}><View style={[styles.fill, { width: `${fraction * 100}%` }]} /></View>
    <View pointerEvents="none" style={[styles.knob, { left: `${fraction * 100}%` }]} />
  </View>
}
const styles = StyleSheet.create({
  delivery: { alignSelf: 'flex-start', width: '80%', marginTop: 4, marginBottom: 12 },
  placeholder: { width: '100%', aspectRatio: 16 / 9, borderRadius: 12, backgroundColor: '#17191c', alignItems: 'center', justifyContent: 'center' },
  frame: { width: '100%', aspectRatio: 16 / 9, borderRadius: 12, overflow: 'hidden', backgroundColor: '#17191c' },
  control: { width: 44, height: 44, borderRadius: 9, backgroundColor: '#14171b9c', alignItems: 'center', justifyContent: 'center' },
  flat: { backgroundColor: 'transparent' }, expand: { position: 'absolute', right: 8, top: 8 },
  play: { position: 'absolute', width: 50, height: 50, borderRadius: 25, left: '50%', top: '50%', marginLeft: -25, marginTop: -25, paddingLeft: 3 },
  controls: { position: 'absolute', left: 0, right: 0, bottom: 0, paddingTop: 18, paddingHorizontal: 4, paddingBottom: 3, flexDirection: 'row', alignItems: 'center', gap: 2 },
  time: { color: '#fff', fontSize: 10, fontVariant: ['tabular-nums'] },
  seek: { flex: 1, height: 44, minWidth: 24, justifyContent: 'center', marginHorizontal: 8 },
  track: { height: 3, backgroundColor: '#ffffff60', borderRadius: 2, overflow: 'hidden' }, fill: { height: 3, backgroundColor: '#fff' },
  knob: { position: 'absolute', width: 9, height: 9, borderRadius: 5, backgroundColor: '#fff', marginLeft: -4 },
  caption: { flexDirection: 'row', alignItems: 'center', minHeight: 40, gap: 10 },
  filename: { flex: 1, color: muted, fontSize: 12 }, description: { color: muted, fontSize: 12, lineHeight: 18 },
  download: { width: 44, height: 44, alignItems: 'center', justifyContent: 'center' },
  loading: { position: 'absolute', left: '50%', top: '50%', marginLeft: -10, marginTop: -10 },
  fullscreen: { flex: 1, backgroundColor: '#08090b', justifyContent: 'center', alignItems: 'center' },
  close: { position: 'absolute', left: 16, top: 28 },
  error: { position: 'absolute', inset: 0, padding: 20, justifyContent: 'center', alignItems: 'center', gap: 12 },
  errorText: { color: '#e5e7eb', fontSize: 12, lineHeight: 18, textAlign: 'center' },
})
