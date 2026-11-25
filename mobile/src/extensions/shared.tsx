import { useCallback, useEffect, useRef, useState, useSyncExternalStore, type ReactNode } from 'react'
import { ActivityIndicator, AppState, Alert, Platform, Pressable, StyleSheet, Text, View } from 'react-native'
import { ArrowLeft, ChevronDown, RefreshCw } from 'lucide-react-native'
import { extensionOperations } from '../../../frontend/src/extensions/operations'
import type { TranscriptCache } from '../../../frontend/src/chat/transcriptCache'
import { ApiError, type Controller } from '../api'
import { useLocale } from '../locale'
import { accent, Button, danger, edge, Field, glass, Hint, IconButton, ink, muted, Panel, s, success } from '../ui'

export type ScreenProps = { api: Controller; cache: TranscriptCache; onDirtyChange: (value: boolean) => void; onBusyChange: (value: boolean) => void }
export function useManagement<T>({ api, cache }: ScreenProps, load: (api: Controller, signal?: AbortSignal, refresh?: boolean) => Promise<T>) {
  const [view, setView] = useState<T>(), [loading, setLoading] = useState(true), [version, setVersion] = useState(0)
  const operations = extensionOperations(cache)
  const { busy, error, notice, revision } = useSyncExternalStore(operations.subscribe, operations.snapshot)
  const force = useRef(false)
  const reload = useCallback(() => setVersion(value => value + 1), [])
  const refresh = useCallback(() => { force.current = true; reload() }, [reload])
  useEffect(() => cache.subscribe('@extensions', reload), [cache, reload])
  useEffect(() => { const listener = AppState.addEventListener('change', state => { if (state === 'active') reload() }); return () => listener.remove() }, [reload])
  useEffect(() => {
    const request = new AbortController()
    setLoading(true)
    const requestedRefresh = force.current; force.current = false
    void load(api, request.signal, requestedRefresh).then(next => { if (!request.signal.aborted) { setView(next); if (operations.snapshot().error === 'Could not load extensions. Try again.') operations.setError('') } }, () => { if (!request.signal.aborted) operations.setError('Could not load extensions. Try again.') }).finally(() => { if (!request.signal.aborted) setLoading(false) })
    return () => request.abort()
  }, [api, load, version, revision, operations])
  const run = (label: string, work: () => Promise<unknown>, success = 'Configuration saved. Review the runtime state below.') =>
    operations.run(label, work, error => error instanceof ApiError && error.code === 'settings_saved_host_apply_failed' ? 'Settings were saved, but the Host could not apply them. Refresh to check the actual state.'
      : error instanceof ApiError && error.code === 'host_not_running' ? 'Start your Host before managing extensions.'
      : error instanceof ApiError && error.code === 'oauth_requires_https_or_loopback_http' ? 'OAuth requires HTTPS or localhost HTTP. Open the Controller through one of these addresses.'
      : error instanceof ApiError && [400, 422].includes(error.status) && /\s/.test(error.code) ? error.code.slice(0, 1024)
      : 'Could not complete this operation. Refresh the actual state before trying again.', success)
  return { view, error, notice, busy, loading, reload, refresh, run }
}
export type Runner = ReturnType<typeof useManagement>['run']

const stateLabels: Record<string, string> = { installed: 'Installed', active: 'Active', running: 'Running', connected: 'Connected', ready: 'Ready', installing: 'Installing', 'installation-failed': 'Installation failed', loading: 'Starting', applying: 'Starting', connecting: 'Connecting', pending: 'Pending', failed: 'Failed', unavailable: 'Unavailable', stopped: 'Stopped', disabled: 'Disabled', stopping: 'Stopping', 'waiting-credentials': 'Needs credentials', 'waiting-authorization': 'Authorization needed' }
export function State({ value }: { value: string }) {
  const { t } = useLocale()
  const color = ['active', 'connected', 'running', 'ready'].includes(value) ? success : ['failed', 'installation-failed'].includes(value) ? danger : muted
  return <View style={x.state}>{['installing', 'loading'].includes(value) ? <ActivityIndicator size={12} color={color} /> : <View style={[x.dot, { backgroundColor: color }]} />}<Text style={[x.stateLabel, { color }]}>{t(stateLabels[value] ?? value)}</Text></View>
}
export function Toolbar({ title, busy, onBack, onRefresh, children }: { title: string; busy?: boolean; onBack?: () => void; onRefresh?: () => void; children?: ReactNode }) {
  const { t } = useLocale()
  return <View style={[s.row, { gap: 8, marginBottom: 6 }]}>{onBack && <IconButton label={t('返回')} disabled={busy} onPress={onBack}><ArrowLeft size={19} color={muted} /></IconButton>}<Text accessibilityRole="header" style={[x.heading, { flex: 1 }]}>{t(title)}</Text>{onRefresh && <IconButton label={t('Refresh extensions')} disabled={busy} onPress={onRefresh}><RefreshCw size={18} color={muted} /></IconButton>}{children}</View>
}
export function TextAction({ label, onPress, disabled, destructive = false }: { label: string; onPress: () => void; disabled?: boolean; destructive?: boolean }) {
  const { t } = useLocale()
  return <Pressable accessibilityRole="button" disabled={disabled} onPress={onPress} style={({ pressed }) => [x.textAction, pressed && s.pressed, disabled && s.disabled]}><Text style={[x.actionText, destructive && { color: danger }]}>{t(label)}</Text></Pressable>
}
export function Feedback({ error, notice }: { error: string; notice: string }) { const { t } = useLocale(); return <><Hint error>{t(error)}</Hint><Hint>{t(notice)}</Hint></> }
export function Empty({ title, text, children }: { title: string; text: string; children: ReactNode }) {
  const { t } = useLocale()
  return <View style={x.empty}>{children}<Text style={x.heading}>{t(title)}</Text><Text style={[s.hint, { textAlign: 'center', maxWidth: 300 }]}>{t(text)}</Text></View>
}
export function Loading() { return <View accessibilityLabel="Loading" style={{ gap: 14 }}>{[0, 1, 2].map(i => <View key={i} style={{ height: 66, borderRadius: 17, backgroundColor: '#f1f2f3' }} />)}</View> }
export function Fact({ label, value }: { label: string; value: string | number }) { const { t } = useLocale(); return <View style={x.fact}><Text style={[s.caption, { width: 98 }]}>{t(label)}</Text><Text selectable style={[s.meta, { flex: 1, color: ink }]}>{value}</Text></View> }
export function JSONField({ label, value, onChange, editable = true }: { label: string; value: string; onChange: (text: string) => void; editable?: boolean }) {
  const { t } = useLocale()
  return <Field label={t(label)} value={value} onChangeText={onChange} editable={editable} multiline autoCorrect={false} spellCheck={false} style={{ fontFamily: Platform.OS === 'ios' ? 'Menlo' : 'monospace', fontSize: 13, lineHeight: 21, minHeight: 125 }} />
}
export function Toggle({ label, checked, onChange, disabled }: { label: string; checked: boolean; onChange: (value: boolean) => void; disabled?: boolean }) {
  const { t } = useLocale()
  return <Pressable accessibilityRole="switch" accessibilityState={{ checked, disabled }} accessibilityLabel={t(label)} disabled={disabled} onPress={() => onChange(!checked)} style={[s.row, { minHeight: 48, justifyContent: 'space-between', gap: 16 }, disabled && s.disabled]}><Text style={[s.meta, { flex: 1, color: ink }]}>{t(label)}</Text><View style={[x.toggle, checked && { backgroundColor: accent }]}><View style={[x.thumb, checked && { alignSelf: 'flex-end' }]} /></View></Pressable>
}
export function Fold({ label, children }: { label: string; children: ReactNode }) {
  const [open, setOpen] = useState(false); const { t } = useLocale()
  return <View><Pressable accessibilityRole="button" accessibilityState={{ expanded: open }} onPress={() => setOpen(!open)} style={[s.row, { minHeight: 50 }]}><Text style={[s.caption, { flex: 1 }]}>{t(label)}</Text><ChevronDown size={16} color={muted} style={open ? { transform: [{ rotate: '180deg' }] } : undefined} /></Pressable>{open && <View style={{ gap: 16, paddingVertical: 8 }}>{children}</View>}</View>
}
export function confirmChange(message: string, t: (key: string) => string): Promise<boolean> {
  return new Promise(resolve => Alert.alert(t('Confirm change'), t(message), [{ text: t('取消'), style: 'cancel', onPress: () => resolve(false) }, { text: t('Confirm'), style: 'destructive', onPress: () => resolve(true) }], { cancelable: true, onDismiss: () => resolve(false) }))
}
export function useDirty(dirty: boolean, onDirty: (value: boolean) => void) { useEffect(() => { onDirty(dirty); return () => onDirty(false) }, [dirty, onDirty]) }
export function objectJSON(value: string): Record<string, unknown> { const data: unknown = JSON.parse(value); if (!data || typeof data !== 'object' || Array.isArray(data)) throw new Error('Enter a JSON object.'); return data as Record<string, unknown> }
export function stringsJSON(value: string) { const data = objectJSON(value); if (Object.values(data).some(item => typeof item !== 'string')) throw new Error('Use string values for environment variables and headers.'); if (Object.values(data).some(item => String(item).includes('[redacted]'))) throw new Error('Replace redacted values with credential references before saving.'); return data as Record<string, string> }


export const x = StyleSheet.create({
  heading: { color: ink, fontSize: 18, fontWeight: '600', letterSpacing: -.3 },
  state: { flexDirection: 'row', alignItems: 'center', gap: 6, flexShrink: 1 }, dot: { width: 5, height: 5, borderRadius: 3 }, stateLabel: { fontSize: 12, flexShrink: 1 },
  listRow: { flexDirection: 'row', alignItems: 'center', gap: 12, minHeight: 76, paddingVertical: 17 }, identity: { flex: 1, gap: 5, minWidth: 0 }, title: { color: ink, fontSize: 15, fontWeight: '600', lineHeight: 22 },
  fact: { flexDirection: 'row', alignItems: 'flex-start', gap: 12, paddingVertical: 5 }, textAction: { minHeight: 44, paddingHorizontal: 6, justifyContent: 'center' }, actionText: { color: accent, fontSize: 14, fontWeight: '500' },
  empty: { alignItems: 'center', gap: 14, paddingVertical: 42, paddingHorizontal: 8 }, actions: { flexDirection: 'row', flexWrap: 'wrap', alignItems: 'center', columnGap: 14, rowGap: 4 },
  toggle: { width: 43, height: 27, padding: 3, borderRadius: 16, backgroundColor: '#d9dce5' }, thumb: { width: 21, height: 21, borderRadius: 12, backgroundColor: '#fff' },
  list: { backgroundColor: glass, borderRadius: 22, paddingHorizontal: 16, borderWidth: 1, borderColor: edge },
})
