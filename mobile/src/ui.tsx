import { useLocale } from './locale'
import { useEffect, useRef, useState, type ReactNode } from 'react'
import { AccessibilityInfo, ActivityIndicator, Animated, Easing, Image, KeyboardAvoidingView, Modal, Platform, Pressable, ScrollView, StyleSheet, Text, TextInput, useWindowDimensions, View, type AccessibilityState, type StyleProp, type TextInputProps, type ViewStyle } from 'react-native'
import { useSafeAreaInsets } from 'react-native-safe-area-context'
import { LinearGradient } from 'expo-linear-gradient'
import { X } from 'lucide-react-native'

export const ink = '#17191c'
export const muted = '#70747a'
export const quiet = '#81858a'
export const line = '#e9eaec'
export const edge = '#e9eaec'
export const glass = '#ffffff'
export const accent = '#222427'
export const wash = '#eff0f2'
export const danger = '#a54343'
export const success = '#3e6a52'
export const warning = '#88601f'
export const userBubble = '#cce4fc'
export const logo = require('../assets/harness-whale.png')

export function Ambient() {
  return <View pointerEvents="none" style={[StyleSheet.absoluteFill, { backgroundColor: '#fff' }]} />
}

export function SectionTitle({ children }: { children: ReactNode }) {
  return <View style={{ gap: 0, alignItems: 'flex-start' }}>
    <Image source={logo} style={{ width: 98, height: 76, marginLeft: -3 }} resizeMode="contain" />
    <Text accessibilityRole="header" style={s.title}>{children}</Text>
  </View>
}

export function TintIcon({ children, background }: { children: ReactNode; background: string }) {
  return <View style={[s.tintIcon, { backgroundColor: background }]}>{children}</View>
}

export function useReducedMotion() {
  const [reduced, setReduced] = useState(false)
  useEffect(() => {
    AccessibilityInfo.isReduceMotionEnabled().then(setReduced)
    const listener = AccessibilityInfo.addEventListener('reduceMotionChanged', setReduced)
    return () => listener.remove()
  }, [])
  return reduced
}

export function IconButton({ label, children, onPress, disabled, style, accessibilityState }: { label: string; children: ReactNode; onPress: () => void; disabled?: boolean; style?: StyleProp<ViewStyle>; accessibilityState?: AccessibilityState }) {
  return <Pressable accessibilityRole="button" accessibilityLabel={label} accessibilityState={{ ...accessibilityState, disabled }} onPress={onPress} disabled={disabled}
    style={({ pressed }) => [s.icon, style, pressed && s.pressed, disabled && s.disabled]}>{children}</Pressable>
}

export function Button({ label, children, onPress, primary, soft, dangerTone, disabled, busy }: { label: string; children?: ReactNode; onPress: () => void; primary?: boolean; soft?: boolean; dangerTone?: boolean; disabled?: boolean; busy?: boolean }) {
  return <Pressable accessibilityRole="button" onPress={onPress} disabled={disabled || busy}
    style={({ pressed }) => [s.button, primary && s.primary, soft && s.soft, (disabled || busy) && s.disabled, pressed && s.pressed]}>
    {busy ? <ActivityIndicator color={primary ? '#fff' : accent} /> : children}
    <Text style={[s.buttonLabel, primary && { color: '#fff' }, dangerTone && { color: danger }]}>{label}</Text>
  </Pressable>
}

export function Field({ label, style, ...props }: TextInputProps & { label: string }) {
  return <View style={s.fieldWrap}>
    <Text style={s.caption}>{label}</Text>
    <TextInput accessibilityLabel={label} placeholderTextColor={quiet} autoCapitalize="none" style={[s.field, props.multiline && s.multiline, style]} {...props} />
  </View>
}

type AfterClose = (action: () => void) => void
type SheetProps = {
  beforeDismiss?: (proceed: () => void) => void
  title: string
  children: ReactNode | ((afterClose: AfterClose) => ReactNode)
  onClose: () => void
  drawer?: boolean
  raised?: number
  headerAction?: ReactNode
  hideTitle?: boolean
  surfaceColors?: readonly [string, string, string]
}

export function Sheet({ title, children, onClose, drawer = false, raised = 0, headerAction, hideTitle = false, surfaceColors, beforeDismiss }: SheetProps) {
  const { t } = useLocale()
  const inset = useSafeAreaInsets()
  const [visible, setVisible] = useState(true)
  const { height, width } = useWindowDimensions()
  const reduced = useReducedMotion()
  const motion = useRef(new Animated.Value(0)).current
  const closing = useRef(false)
  const deferred = useRef<(() => void) | undefined>(undefined)

  useEffect(() => {
    if (closing.current) return
    if (reduced) { motion.setValue(1); return }
    const animation = Animated.timing(motion, { toValue: 1, duration: 255, easing: Easing.out(Easing.cubic), useNativeDriver: true })
    animation.start()
    return () => animation.stop()
  }, [motion, reduced])

  function finish() {
    const action = deferred.current
    deferred.current = undefined
    onClose()
    if (action) requestAnimationFrame(action)
  }
  function dismiss(action?: () => void) {
    if (closing.current) return
    closing.current = true
    deferred.current = action
    Animated.timing(motion, { toValue: 0, duration: reduced ? 0 : 175, easing: Easing.in(Easing.quad), useNativeDriver: true })
      .start(({ finished }) => {
        if (!finished) return
        if (Platform.OS === 'ios') setVisible(false)
        else finish()
      })
  }
  function requestDismiss() {
    if (beforeDismiss) beforeDismiss(() => dismiss())
    else dismiss()
  }
  const transform = drawer
    ? [{ translateX: motion.interpolate({ inputRange: [0, 1], outputRange: [-Math.min(width, 390), 0] }) }]
    : [{ translateY: motion.interpolate({ inputRange: [0, 1], outputRange: [raised ? 24 : height * 0.87, 0] }) }]

  return <Modal visible={visible} transparent animationType="none" onRequestClose={requestDismiss} onDismiss={finish} supportedOrientations={['portrait', 'landscape']}>
    <KeyboardAvoidingView behavior={Platform.OS === 'ios' ? 'padding' : undefined} style={[s.overlay, drawer && s.left]}>
      <Animated.View style={[StyleSheet.absoluteFill, s.backdrop, { opacity: motion }]}>
        <Pressable accessibilityRole="button" accessibilityLabel={t('收起') + ' ' + title} style={StyleSheet.absoluteFill} onPress={requestDismiss} />
      </Animated.View>
      <Animated.View accessibilityViewIsModal style={[s.sheet, drawer ? [s.drawer, { paddingTop: inset.top + 12, paddingBottom: inset.bottom + 12 }] : raised ? [s.raised, { marginBottom: Math.max(inset.bottom, 8) + raised }] : { paddingBottom: Math.max(16, inset.bottom), marginTop: inset.top + 18 }, { transform }, !!raised && { opacity: motion }]}>
        <LinearGradient pointerEvents="none" colors={surfaceColors ?? ['#f7f7f8', '#f7f7f8', '#f7f7f8']} locations={[0, 0.68, 1]}
          style={[StyleSheet.absoluteFill, s.sheetTint, drawer && s.drawerTint, !!raised && s.raisedTint]} />
        {!drawer && <View style={s.handle} />}
        <View style={s.sheetHead}>{!hideTitle && <Text accessibilityRole="header" style={s.sheetTitle}>{title}</Text>}<View style={[s.row, hideTitle && { marginLeft: 'auto' }]}>{headerAction}<IconButton label={t('关闭')} onPress={requestDismiss}><X color={muted} size={20} /></IconButton></View></View>
        {typeof children === 'function' ? children(dismiss) : children}
      </Animated.View>
    </KeyboardAvoidingView>
  </Modal>
}

export type PageChrome = { header?: ReactNode; navigation?: ReactNode; bottomInset?: number }

export function SheetScroll({ children, header, navigation, bottomInset = 0, embedded = false, scrollOffset }: PageChrome & { children: ReactNode; embedded?: boolean; scrollOffset?: { current: number } }) {
  if (!embedded) return <ScrollView bounces={false} keyboardShouldPersistTaps="handled" style={{ flexGrow: 0 }} contentContainerStyle={s.sheetBody}>{children}</ScrollView>
  return <ScrollView keyboardShouldPersistTaps="handled" style={{ flex: 1 }} contentOffset={{ x: 0, y: scrollOffset?.current ?? 0 }} scrollEventThrottle={120} onScroll={event => { if (scrollOffset) scrollOffset.current = event.nativeEvent.contentOffset.y }} stickyHeaderIndices={navigation ? [1] : undefined}
    contentContainerStyle={{ paddingBottom: bottomInset + 24 }}>
    <View>{header}</View>
    {navigation && <View style={{ backgroundColor: '#fff' }}>{navigation}</View>}
    <View style={{ paddingHorizontal: 20, paddingTop: 18, gap: 16, width: '100%', maxWidth: 620, alignSelf: 'center' }}>{children}</View>
  </ScrollView>
}

export function Hint({ children, error = false }: { children?: ReactNode; error?: boolean }) {
  return children ? <Text accessibilityLiveRegion="polite" style={[s.hint, error && { color: danger }]}>{children}</Text> : null
}

export function Panel({ children, style }: { children: ReactNode; style?: StyleProp<ViewStyle> }) {
  return <View style={[s.panel, style]}>{children}</View>
}

export const s = StyleSheet.create({
  icon: { width: 44, height: 44, alignItems: 'center', justifyContent: 'center', borderRadius: 22 },
  button: { minHeight: 46, paddingHorizontal: 16, gap: 8, flexDirection: 'row', alignItems: 'center', justifyContent: 'center', borderRadius: 16, backgroundColor: '#f1f2f3' },
  primary: { backgroundColor: accent },
  soft: { backgroundColor: '#f1f2f3', borderColor: '#eceef0', borderWidth: 1, borderRadius: 24 },
  buttonLabel: { color: ink, fontSize: 15, fontWeight: '600' },
  pressed: { opacity: 0.72 },
  disabled: { opacity: 0.45 },
  fieldWrap: { gap: 6 },
  caption: { color: muted, fontSize: 13, fontWeight: '600' },
  meta: { color: muted, fontSize: 13, lineHeight: 18 },
  field: { minHeight: 48, borderRadius: 14, borderWidth: 1, borderColor: edge, backgroundColor: '#fff', color: ink, fontSize: 16, paddingHorizontal: 13, paddingVertical: 10 },
  multiline: { minHeight: 82, textAlignVertical: 'top' },
  overlay: { flex: 1, justifyContent: 'flex-end' },
  left: { alignItems: 'flex-start' },
  backdrop: { backgroundColor: 'rgba(23,25,28,0.18)' },
  sheet: { maxHeight: '86%', backgroundColor: '#f7f7f8', borderTopLeftRadius: 28, borderTopRightRadius: 28, borderWidth: 1, borderColor: '#ffffff', paddingHorizontal: 16, gap: 5 },
  sheetTint: { borderTopLeftRadius: 28, borderTopRightRadius: 28 },
  drawerTint: { borderTopLeftRadius: 0, borderTopRightRadius: 27, borderBottomRightRadius: 27 },
  raisedTint: { borderRadius: 25 },
  raised: { borderRadius: 25, marginHorizontal: 15, paddingBottom: 12, maxHeight: '73%', boxShadow: '0 8px 27px rgba(23,25,28,0.10)' },
  tintIcon: { width: 39, height: 39, borderRadius: 12, alignItems: 'center', justifyContent: 'center' },
  drawer: { maxHeight: '100%', height: '100%', width: '86%', maxWidth: 380, borderTopLeftRadius: 0, borderTopRightRadius: 27, borderBottomRightRadius: 27 },
  handle: { alignSelf: 'center', width: 32, height: 4, marginTop: 9, borderRadius: 2, backgroundColor: '#d9dbde' },
  sheetHead: { flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between' },
  sheetTitle: { color: ink, fontWeight: '600', fontSize: 20, flexShrink: 1 },
  sheetBody: { gap: 12, paddingTop: 2, paddingBottom: 10 },
  hint: { fontSize: 14, color: muted, lineHeight: 20 },
  panel: { backgroundColor: glass, borderColor: edge, borderWidth: StyleSheet.hairlineWidth, borderRadius: 20, padding: 16, gap: 12 },
  row: { flexDirection: 'row', alignItems: 'center', gap: 10 },
  title: { color: ink, fontSize: 34, fontWeight: '700', letterSpacing: -1 },
  body: { color: ink, fontSize: 16, lineHeight: 24 },
})
