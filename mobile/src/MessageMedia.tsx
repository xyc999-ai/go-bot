import { useRef, useState, type ComponentType, type ReactElement } from 'react'
import { ActivityIndicator, Image, Pressable, ScrollView, StyleSheet, Text, View, type ViewProps } from 'react-native'
import MaskedView from '@react-native-masked-view/masked-view'
import { LinearGradient } from 'expo-linear-gradient'
import { Check, ChevronDown, CircleAlert, Download, Image as Picture, ImageOff } from 'lucide-react-native'
import type { ChatImage, ToolBlock, Transcript } from '../../frontend/src/chat/transcript'
import type { Controller } from './api'
import { useLocale } from './locale'
import { saveRemote, shareDownload, shareLibraryItem, type RemoteFile, type WorkspaceFileSource } from './library'
import { FileIcon } from './FileIcon'
import { accent, edge, glass, Hint, muted, ink, s } from './ui'

// The library's legacy NativeMethods class intersection predates React 19 types.
// Keep its documented props at this boundary; the native component is unchanged.
const ImageMask = MaskedView as unknown as ComponentType<ViewProps & { maskElement: ReactElement; androidRenderingMode?: 'software' | 'hardware' }>

export type Preview = RemoteFile & { width?: number; height?: number; workspace?: WorkspaceFileSource }
type ImagesProps = { images: ChatImage[]; api: Controller; sessionId: string; openImage: (preview: Preview) => void; compact?: boolean; pending?: number }

export function Images({ images, api, sessionId, openImage, compact, pending = 0 }: ImagesProps) {
  const { t } = useLocale()
  const size = compact ? 118 : 146
  const metrics = useRef({ width: 0, content: 0, offset: 0 })
  const [edges, setEdges] = useState({ left: 1, right: 1, width: 0 })
  const measure = () => {
    const { width, content, offset } = metrics.current
    const left = Math.round((1 - Math.min(1, Math.max(0, offset - 2) / 24)) * 100) / 100
    const right = Math.round((1 - Math.min(1, Math.max(0, content - width - offset - 2) / 24)) * 100) / 100
    setEdges(old => old.left === left && old.right === right && old.width === width ? old : { left, right, width })
  }
  const fade = Math.min(.25, 26 / Math.max(104, edges.width))
  return <ImageMask style={{ height: size, alignSelf: 'stretch' }} androidRenderingMode="software"
    maskElement={<LinearGradient colors={[`rgba(0,0,0,${edges.left})`, '#000', '#000', `rgba(0,0,0,${edges.right})`]}
      locations={[0, fade, 1 - fade, 1]} start={{ x: 0, y: 0 }} end={{ x: 1, y: 0 }} style={{ flex: 1 }} />}>
    <ScrollView horizontal showsHorizontalScrollIndicator={false} nestedScrollEnabled directionalLockEnabled
      accessibilityLabel={t('图片，可左右滑动')} contentContainerStyle={styles.images} scrollEventThrottle={16}
      onLayout={({ nativeEvent: { layout } }) => { metrics.current.width = layout.width; measure() }}
      onContentSizeChange={width => { metrics.current.content = width; measure() }}
      onScroll={({ nativeEvent: { contentOffset } }) => { metrics.current.offset = contentOffset.x; measure() }}>
      {images.map(image => {
        const preview: Preview = { uri: api.fileURL(sessionId, image.file, image.name), headers: api.headers,
          name: image.name || t('图片'), mimeType: image.mediaType, width: image.width, height: image.height,
          workspace: image.file.source === 'presented' ? { account: api.profile.account, path: image.file.path } : undefined }
        return <Thumb key={image.id} preview={preview} size={size} open={() => openImage(preview)} />
      })}
      {Array.from({ length: Math.min(3, pending) }, (_, index) => <View key={'pending/' + index} accessibilityLabel={t('正在加载图片')} style={[styles.thumb, { width: size, height: size }]}><ActivityIndicator color={muted} /></View>)}
    </ScrollView>
  </ImageMask>
}

export function ImageTools({ tools, images, results, ...props }: Omit<ImagesProps, 'compact' | 'pending'> & { tools: ToolBlock[]; results: Transcript['toolResults'] }) {
  const { t } = useLocale()
  const [expanded, setExpanded] = useState(true)
  const running = tools.filter(tool => !results[tool.callId]?.status).length
  const failed = tools.some(tool => results[tool.callId]?.status === 'failed' || results[tool.callId]?.status === 'cancelled')
  const count = images.length + running || tools.length
  return <View style={styles.imageTools}>
    <Pressable accessibilityRole="button" accessibilityState={{ expanded }} accessibilityLabel={t(tools[0].name === 'readimage' ? '查看图片' : '生成图片')}
      style={styles.imageToolHeader} onPress={() => setExpanded(old => !old)}>
      <Picture size={16} color={muted} strokeWidth={1.7} /><Text style={styles.imageToolLabel}>{t(tools[0].name === 'readimage' ? '查看图片' : '生成图片')}</Text>
      <Text style={styles.imageToolCount}>{count === 1 ? t('1 张') : t('{count} 张', { count })}</Text>
      <View style={{ marginLeft: 'auto' }}>{running ? <ActivityIndicator size="small" color={muted} /> : failed ? <CircleAlert size={13} color={muted} /> : <Check size={13} color={muted} />}</View>
      <ChevronDown size={15} color={muted} style={{ transform: [{ rotate: expanded ? '0deg' : '-90deg' }] }} />
    </Pressable>
    <View style={!expanded && { display: 'none' }}>
      {(images.length > 0 || running > 0) ? <Images images={images} {...props} compact pending={running} /> : <Text style={s.meta}>{t('没有可显示的图片。')}</Text>}
    </View>
  </View>
}

function Thumb({ preview, size, open }: { preview: Preview; size: number; open: () => void }) {
  const { t } = useLocale()
  const [failed, setFailed] = useState(false)
  const [loaded, setLoaded] = useState(false)
  const [retry, setRetry] = useState(0)
  if (failed && preview.workspace) return <View style={{ width: 230 }}><FileTile preview={preview} delivery={preview.workspace} /></View>
  return <Pressable accessibilityRole="imagebutton" accessibilityLabel={preview.name} onPress={failed ? () => { setFailed(false); setRetry(value => value + 1) } : open} style={[styles.thumb, { width: size, height: size }]}>
    {failed ? <><ImageOff color={muted} size={20} /><Text style={styles.imageToolCount}>{t('图片暂不可用，重试')}</Text></>
      : <><Image key={retry} source={preview} onLoad={() => setLoaded(true)} onError={() => setFailed(true)} style={{ width: '100%', height: '100%' }} resizeMode="cover" />
        {!loaded && <ActivityIndicator style={{ position: 'absolute' }} size="small" color={muted} />}</>}
  </Pressable>
}

export function FileTile({ preview, delivery }: { preview: Preview; delivery?: WorkspaceFileSource }) {
  const { t } = useLocale()
  const [busy, setBusy] = useState(false)
  const [failed, setFailed] = useState(false)
  return <View>
    <Pressable accessibilityRole="button" accessibilityLabel={t('下载') + ' ' + preview.name} disabled={busy} style={styles.tile} onPress={async () => {
      setBusy(true); setFailed(false)
      try {
        if (delivery) { const saved = await saveRemote(preview, 'file', delivery); await shareLibraryItem(saved) }
        else await shareDownload(preview.uri, preview.headers, preview.name)
      } catch { setFailed(true) } finally { setBusy(false) }
    }}>
      <FileIcon name={preview.name} /><View style={{ flex: 1, gap: 2 }}><Text style={{ color: ink, fontSize: 15, fontWeight: '600' }} numberOfLines={2}>{preview.name}</Text><Text style={{ color: muted, fontSize: 12 }}>{t(delivery ? '已交付' : '附件')}</Text></View>
      {busy ? <ActivityIndicator color={accent} /> : <Download size={17} color={muted} />}
    </Pressable>
    {failed && <Hint error>{t('文件暂不可用。')}</Hint>}
  </View>
}
const styles = StyleSheet.create({
  images: { flexDirection: 'row', gap: 10 },
  thumb: { overflow: 'hidden', borderRadius: 11, backgroundColor: 'rgba(255,255,255,0.45)', alignItems: 'center', justifyContent: 'center', gap: 6 },
  imageTools: { alignSelf: 'stretch', gap: 3, marginBottom: 4 },
  imageToolHeader: { minHeight: 44, flexDirection: 'row', alignItems: 'center', gap: 7 },
  imageToolLabel: { fontSize: 12, color: muted }, imageToolCount: { fontSize: 10, color: muted },
  tile: { minHeight: 60, flexDirection: 'row', alignItems: 'center', gap: 12, paddingHorizontal: 11, paddingVertical: 8, borderRadius: 15, backgroundColor: glass, borderColor: edge, borderWidth: 1 },
})
