import { useRef, useState, type ComponentRef } from 'react'
import { Keyboard, Modal, Pressable, StyleSheet, Text, useWindowDimensions, View } from 'react-native'
import { Check, ChevronDown } from 'lucide-react-native'
import { type ModelProtocol, protocolNames } from '../../frontend/src/settings/modelProtocol'
import { useLocale } from './locale'
import { accent, edge, ink, muted, s } from './ui'

export function ProtocolDropdown({ value, onChange, disabled }: { value: ModelProtocol; onChange: (value: ModelProtocol) => void; disabled?: boolean }) {
  const { t } = useLocale()
  const anchor = useRef<ComponentRef<typeof View>>(null)
  const [position, setPosition] = useState<{ x: number; y: number; width: number }>()
  const screen = useWindowDimensions()
  function open() {
    Keyboard.dismiss()
    anchor.current?.measureInWindow((x, y, width, height) => setPosition({ x, y: Math.min(y + height + 6, screen.height - 155), width }))
  }
  return <View style={s.fieldWrap}>
    <Text style={s.caption}>{t('接口协议')}</Text>
    <View ref={anchor} collapsable={false}>
      <Pressable accessibilityRole="combobox" accessibilityLabel={t('接口协议')} accessibilityValue={{ text: protocolNames[value] }} accessibilityState={{ expanded: !!position, disabled }} disabled={disabled} onPress={open}
        style={[s.field, { flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between' }]}>
        <Text style={s.body}>{protocolNames[value]}</Text><ChevronDown size={16} color={muted} />
      </Pressable>
    </View>
    <Modal visible={!!position} transparent animationType="fade" onRequestClose={() => setPosition(undefined)} supportedOrientations={['portrait', 'landscape']}>
      <Pressable accessibilityLabel={t('收起')} style={StyleSheet.absoluteFill} onPress={() => setPosition(undefined)} />
      {position && <View style={{ position: 'absolute', left: Math.max(12, Math.min(position.x, screen.width - position.width - 12)), top: position.y, width: position.width,
        padding: 6, backgroundColor: '#fffdfd', borderRadius: 14, borderWidth: 1, borderColor: edge, boxShadow: '0 8px 24px rgba(29,34,49,0.12)' }}>
        {(Object.keys(protocolNames) as ModelProtocol[]).map(protocol => <Pressable key={protocol} accessibilityRole="menuitem" accessibilityState={{ selected: protocol === value }}
          onPress={() => { onChange(protocol); setPosition(undefined) }} style={{ minHeight: 48, paddingHorizontal: 12, borderRadius: 9, flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between', backgroundColor: protocol === value ? '#eff0f2' : 'transparent' }}>
          <Text style={[s.body, { color: ink }]}>{protocolNames[protocol]}</Text>{protocol === value && <Check size={16} color={accent} />}
        </Pressable>)}
      </View>}
    </Modal>
  </View>
}
