import { useState } from 'react'
import { Image, Modal, Pressable, StyleSheet, Text, View } from 'react-native'
import { StatusBar } from 'expo-status-bar'
import { Download } from 'lucide-react-native'
import type { Preview } from './Message'
import { useLocale } from './locale'
import { saveRemote, saveToDevicePhotos } from './library'
import { accent, Button, Hint, Sheet, SheetScroll } from './ui'

export function ImageViewer({ preview, close }: { preview: Preview; close: () => void }) {
  const { t } = useLocale()
  const [viewport, setViewport] = useState({ width: 0, height: 0 })
  const [image, setImage] = useState({ width: preview.width ?? 0, height: preview.height ?? 0 })
  const [actions, setActions] = useState(false)
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)
  const [feedback, setFeedback] = useState('')
  const [loadFailed, setLoadFailed] = useState(false)

  async function save() {
    setSaving(true)
    setFeedback('')
    try {
      const item = await saveRemote(preview, 'image', preview.workspace)
      try {
        if (!await saveToDevicePhotos(item)) {
          setFeedback('已保存到本地图片。请在系统设置中允许添加到图库。')
          return false
        }
      } catch {
        setFeedback('已保存到本地图片，但无法写入手机图库。')
        return false
      }
      setSaved(true)
      return true
    } catch {
      setFeedback('无法保存图片，请重试。')
      return false
    } finally { setSaving(false) }
  }

  // Match the interactive rectangle to the actual photo, leaving a dismissible black margin.
  const available = { width: Math.max(1, viewport.width - 24), height: Math.max(1, viewport.height - 24) }
  const scale = image.width > 0 && image.height > 0 ? Math.min(available.width / image.width, available.height / image.height) : 0
  const size = scale ? { width: image.width * scale, height: image.height * scale } : available
  const openActions = () => { if (!loadFailed) { setFeedback(''); setActions(true) } }

  return <Modal animationType="fade" presentationStyle="fullScreen" statusBarTranslucent navigationBarTranslucent
    supportedOrientations={['portrait', 'landscape']} onRequestClose={close}>
    <StatusBar hidden />
    <View style={styles.screen} onLayout={({ nativeEvent: { layout } }) => setViewport({ width: layout.width, height: layout.height })}>
      <Pressable style={StyleSheet.absoluteFill} accessibilityRole="button" accessibilityLabel={t('关闭图片预览')} onPress={close} />
      {loadFailed ? <Text pointerEvents="none" style={styles.feedback}>{t('无法显示图片。')}</Text>
        : viewport.width > 24 && viewport.height > 24 && <Pressable style={size} accessibilityRole="imagebutton" accessibilityLabel={preview.name}
          accessibilityActions={[{ name: 'save', label: t('保存图片') }]} onAccessibilityAction={({ nativeEvent }) => {
            if (nativeEvent.actionName === 'save') openActions()
          }} onLongPress={openActions}>
          <Image source={{ uri: preview.uri, headers: preview.headers }} style={styles.image} resizeMode="contain"
            onLoad={({ nativeEvent: { source: { width, height } } }) => { if (width && height) setImage({ width, height }) }}
            onError={() => setLoadFailed(true)} />
        </Pressable>}
      {saved && <Text pointerEvents="none" accessibilityLiveRegion="polite" style={[styles.feedback, styles.saved]}>{t('已保存到本地图片和手机图库。')}</Text>}
    </View>
    {actions && <Sheet title={preview.name} onClose={() => setActions(false)}>{afterClose => <SheetScroll>
      <Button label={t('保存图片')} disabled={saved} busy={saving} onPress={async () => { if (await save()) afterClose(() => {}) }}>
        <Download size={19} color={accent} />
      </Button>
      <Hint error>{t(feedback)}</Hint>
    </SheetScroll>}</Sheet>}
  </Modal>
}

const styles = StyleSheet.create({
  image: { width: '100%', height: '100%' },
  screen: { flex: 1, backgroundColor: '#000', alignItems: 'center', justifyContent: 'center' },
  feedback: { color: '#fff', fontSize: 14, textAlign: 'center', paddingHorizontal: 12, paddingVertical: 8 },
  saved: { position: 'absolute', bottom: 54, alignSelf: 'center', borderRadius: 15, overflow: 'hidden', backgroundColor: '#303030' },
})
