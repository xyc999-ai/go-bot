import { useRef, useState, type ComponentRef } from 'react'
import { Keyboard, Modal, Pressable, ScrollView, StyleSheet, Text, useWindowDimensions, View } from 'react-native'
import { Check, ChevronDown } from 'lucide-react-native'
import { useLocale } from '../locale'
import { accent, edge, ink, muted, s } from '../ui'

export function Choice<T extends string>({ label, value, options, onChange, disabled }: { label: string; value: T; options: readonly { value: T; label: string }[]; onChange: (value: T) => void; disabled?: boolean }) {
  const { t } = useLocale()
  const anchor = useRef<ComponentRef<typeof View>>(null), screen = useWindowDimensions()
  const [position, setPosition] = useState<{ x: number; y: number; width: number }>()
  const menuHeight = Math.min(screen.height * .55, options.length * 49 + 12)
  const selected = options.find(row => row.value === value)
  return <View style={s.fieldWrap}><Text style={s.caption}>{t(label)}</Text><View ref={anchor} collapsable={false}>
    <Pressable accessibilityRole="combobox" accessibilityLabel={t(label)} accessibilityValue={{ text: t(selected?.label ?? value) }} accessibilityState={{ expanded: !!position, disabled }} disabled={disabled} onPress={() => {
      Keyboard.dismiss(); anchor.current?.measureInWindow((x, y, width, height) => setPosition({ x, y: Math.max(12, Math.min(y + height + 6, screen.height - menuHeight - 26)), width }))
    }} style={[s.field, s.row, { justifyContent: 'space-between' }, disabled && s.disabled]}><Text numberOfLines={1} style={[s.body, { flex: 1 }]}>{t(selected?.label ?? value)}</Text><ChevronDown size={16} color={muted} /></Pressable>
  </View><Modal visible={!!position} transparent animationType="fade" onRequestClose={() => setPosition(undefined)} supportedOrientations={['portrait', 'landscape']}>
    <Pressable accessibilityLabel={t('收起')} style={StyleSheet.absoluteFill} onPress={() => setPosition(undefined)} />
    {position && <View accessibilityViewIsModal style={{ position: 'absolute', left: Math.max(12, Math.min(position.x, screen.width - position.width - 12)), top: position.y, width: Math.min(position.width, screen.width - 24), padding: 6, borderRadius: 15, borderWidth: 1, borderColor: edge, backgroundColor: '#fffdfd', boxShadow: '0 8px 24px rgba(29,34,49,0.12)' }}>
      <ScrollView style={{ maxHeight: menuHeight }} keyboardShouldPersistTaps="handled">{options.map(option => <Pressable key={option.value} accessibilityRole="menuitem" accessibilityState={{ selected: option.value === value }} onPress={() => { onChange(option.value); setPosition(undefined) }} style={({ pressed }) => [{ minHeight: 48, paddingHorizontal: 11, borderRadius: 9, flexDirection: 'row', gap: 8, alignItems: 'center', justifyContent: 'space-between', backgroundColor: option.value === value ? '#eff0f2' : 'transparent' }, pressed && s.pressed]}>
        <Text style={[s.meta, { color: ink, flexShrink: 1 }]}>{t(option.label)}</Text>{option.value === value && <Check size={16} color={accent} />}
      </Pressable>)}</ScrollView>
    </View>}
  </Modal></View>
}
