import { useEffect, useState } from 'react'
import { ActivityIndicator, AppState, StyleSheet, Text, View } from 'react-native'
import { Laptop, MonitorSmartphone, RefreshCw, Smartphone, Tablet } from 'lucide-react-native'
import { Controller } from './api'
import { clientDisplayName, clientIdentityKey, clientLastSeen, type ClientDevice } from '../../frontend/src/clients/identity'
import { useLocale } from './locale'
import { edge, Hint, IconButton, ink, muted, SheetScroll, type PageChrome } from './ui'

export function Devices({ api, active, ...chrome }: PageChrome & { api: Controller; active: boolean }) {
 const { t, language } = useLocale()
 const [devices, setDevices] = useState<ClientDevice[]>(), [ownKey, setOwnKey] = useState('')
 const [error, setError] = useState(''), [loading, setLoading] = useState(false), [revision, setRevision] = useState(0)
 useEffect(() => {
  if (!active) return
  let request: AbortController | undefined, stopped = false, inFlight = false
  async function load() {
   if (stopped || inFlight || AppState.currentState !== 'active') return
   inFlight = true; request = new AbortController(); setLoading(true)
   try {
    const [data, identity] = await Promise.all([api.clients(request.signal), api.clientIdentity()])
    if (!request.signal.aborted) { setDevices(data.clients.filter(device => device.type !== 'web')); setOwnKey(clientIdentityKey(identity)); setError('') }
   } catch { if (!request?.signal.aborted) setError('无法读取设备，请重试。') }
   finally { inFlight = false; if (!stopped) setLoading(false) }
  }
  void load()
  const timer = setInterval(() => void load(), 5000)
  const listener = AppState.addEventListener('change', state => { if (state === 'active') void load(); else request?.abort() })
  return () => { stopped = true; clearInterval(timer); listener.remove(); request?.abort(); setLoading(false) }
 }, [api, active, revision])
 const own = devices?.find(device => clientIdentityKey(device) === ownKey)
 const others = devices?.filter(device => clientIdentityKey(device) !== ownKey) ?? []
 function row(device: ClientDevice) {
  const connected = device.status === 'connected'
  const Icon = device.type === 'web' || device.type === 'desktop' ? Laptop : /ipad|tablet/i.test(device.model ?? '') ? Tablet : Smartphone
  const name = t(clientDisplayName(device))
  const status = t(connected ? 'Device connected' : '断开')
  const color = connected ? '#248658' : '#b85860'
  const lastSeen = clientLastSeen(device.lastSeenAt, language)
  const detail = !connected && lastSeen ? t('最近在线 {time}', { time: lastSeen }) : device.type === 'web' ? '' : device.platform === 'ios' ? 'iOS' : device.platform === 'android' ? 'Android' : device.platform ?? ''
  return <View key={device.id} accessible accessibilityLabel={[name, status, detail].filter(Boolean).join(', ')} style={styles.device}>
   <View style={styles.icon}><Icon size={23} color={ink} strokeWidth={1.6} /></View>
   <View style={styles.copy}><Text style={styles.name} numberOfLines={2}>{name}</Text>{!!detail && <Text style={styles.detail} numberOfLines={2}>{detail}</Text>}</View>
   <View style={styles.status}><View style={[styles.dot, { backgroundColor: color }]} /><Text style={[styles.statusText, { color }]}>{status}</Text></View>
  </View>
 }
 return <SheetScroll embedded {...chrome}>
  <View style={styles.header}><Text accessibilityRole="header" style={styles.heading}>{t('设备')}</Text><IconButton label={t('刷新')} onPress={() => setRevision(value => value + 1)}>{loading ? <ActivityIndicator color={muted} size={18} /> : <RefreshCw color={muted} size={18} strokeWidth={1.7} />}</IconButton></View>
  {!!error && <Hint error>{t(error)}</Hint>}
  {!devices && loading && <View accessibilityLabel={t('正在读取设备…')} style={styles.group}>{[0, 1].map(index => <View key={index} style={styles.skeleton} />)}</View>}
  {own && <View style={styles.section}><Text style={styles.label}>{t('此设备')}</Text><View style={styles.group}>{row(own)}</View></View>}
  {devices && <View style={styles.section}>{own && <Text style={styles.label}>{t('其他设备')}</Text>}
   {others.length > 0 ? <View style={styles.group}>{others.map((device, index) => <View key={device.id}>{index > 0 && <View style={styles.divider} />}{row(device)}</View>)}</View> : <View style={styles.empty}><MonitorSmartphone size={28} color={muted} strokeWidth={1.4} /><Text style={styles.emptyTitle}>{t(own ? '还没有其他设备' : '暂无设备记录')}</Text><Text style={styles.emptyText}>{t(own ? '在其他客户端连接这个账号后，会显示在这里。' : '建立事件连接后，设备会显示在这里。')}</Text></View>}
  </View>}
 </SheetScroll>
}
const styles = StyleSheet.create({
 header: { flexDirection: 'row', alignItems: 'center', justifyContent: 'space-between', marginBottom: 0 },
 heading: { color: ink, fontSize: 21, lineHeight: 28, fontWeight: '600', letterSpacing: -.35 },
 section: { gap: 10 }, label: { color: muted, fontSize: 13, lineHeight: 20, paddingHorizontal: 3 },
 group: { backgroundColor: '#fff', borderRadius: 22, borderWidth: StyleSheet.hairlineWidth, borderColor: '#e0e2e5', paddingHorizontal: 18, overflow: 'hidden' },
 device: { flexDirection: 'row', alignItems: 'center', minHeight: 86, paddingVertical: 20, gap: 12 },
 icon: { width: 42, height: 42, borderRadius: 14, backgroundColor: '#f4f5f6', alignItems: 'center', justifyContent: 'center' }, copy: { flex: 1, minWidth: 0, gap: 5 },
 name: { color: ink, fontSize: 16, lineHeight: 22, fontWeight: '500', letterSpacing: -.15 },
 detail: { color: muted, fontSize: 12, lineHeight: 18 },
 status: { flexDirection: 'row', alignItems: 'center', gap: 6, marginLeft: 3, flexShrink: 0 },
 statusText: { fontSize: 12, lineHeight: 18, fontWeight: '500' }, dot: { width: 6, height: 6, borderRadius: 3 },
 divider: { height: StyleSheet.hairlineWidth, backgroundColor: edge, marginLeft: 54 }, skeleton: { height: 66, marginVertical: 10, borderRadius: 16, backgroundColor: '#ececee' },
 empty: { alignItems: 'center', paddingVertical: 34, paddingHorizontal: 26, borderRadius: 23, backgroundColor: '#f5f5f6', gap: 12 },
 emptyTitle: { color: ink, fontSize: 15, fontWeight: '500', marginTop: 3 }, emptyText: { color: muted, fontSize: 13, lineHeight: 21, textAlign: 'center', maxWidth: 250 },
})
