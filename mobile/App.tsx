import { languages, LocaleProvider, useLocale } from './src/locale'
import { useEffect, useRef, useState } from 'react'
import { AppState, Animated, Easing, Image, Keyboard, KeyboardAvoidingView, Platform, Pressable, ScrollView, StyleSheet, Text, View, useWindowDimensions, BackHandler } from 'react-native'
import { StatusBar } from 'expo-status-bar'
import { File, Paths } from 'expo-file-system'
import { SafeAreaProvider, useSafeAreaInsets } from 'react-native-safe-area-context'
import { Languages, Link2, MessageCircle, UserRound } from 'lucide-react-native'
import { Controller, normalizeProfile, type Profile } from './src/api'
import { Chat } from './src/Chat'
import { LanguageSheet } from './src/LanguageSheet'
import { Me, type LeaveGuard, type MePage, type Section } from './src/Me'
import { TranscriptCache } from '../frontend/src/chat/transcriptCache'
import { HostConnection } from '../frontend/src/chat/hostConnection'
import { accent, Ambient, Button, edge, Field, Hint, ink, logo, muted, Panel, s, useReducedMotion } from './src/ui'

const profileFile = new File(Paths.document, 'controller-profile.json')

function saveProfile(profile: Profile, autoConnect: boolean) {
  try {
    if (!profileFile.exists) profileFile.create()
    profileFile.write(JSON.stringify({ ...profile, autoConnect }))
  } catch {}
}


function connectionError(error: unknown) {
  const message = error instanceof Error ? error.message : ''
  return /[\u4e00-\u9fff]/.test(message) ? message : '无法连接。请确认地址、账号和网络，并检查 Controller 是否已启动。'
}

const tabs = [{ id: 'chat', label: '聊天', Icon: MessageCircle }, { id: 'me', label: '我的', Icon: UserRound }] as const
type Tab = typeof tabs[number]['id']

export default function App() {
  return <SafeAreaProvider><StatusBar style="dark" /><LocaleProvider><Main /></LocaleProvider></SafeAreaProvider>
}

function Main() {
  const { language, t } = useLocale()
  const inset = useSafeAreaInsets()
  const [fields, setFields] = useState<Profile>({ server: '', account: '' })
  const [api, setApi] = useState<Controller>()
  const [busy, setBusy] = useState(false)
  const [restoring, setRestoring] = useState(true)
  const [error, setError] = useState('')
  const [languageOpen, setLanguageOpen] = useState(false)

  useEffect(() => {
    let active = true
    async function restore() {
      try {
        if (!profileFile.exists) return
        const saved = JSON.parse(await profileFile.text())
        const profile = normalizeProfile(saved)
        if (!active) return
        setFields(profile)
        if (saved.autoConnect === false) return
        const controller = new Controller(profile)
        try {
          await controller.connect(false, language)
          if (active) {
            setLanguageOpen(false)
            setApi(controller)
          }
        } catch (error) {
          if (active) setError(connectionError(error))
        }
      } catch {} finally {
        if (active) setRestoring(false)
      }
    }
    void restore()
    return () => { active = false }
  }, [])
  async function connect(create = false) {
    setError('')
    setBusy(true)
    try {
      const profile = normalizeProfile(fields)
      const controller = new Controller(profile)
      await controller.connect(create, language)
      saveProfile(profile, true)
      setFields(profile)
      setLanguageOpen(false)
      setApi(controller)
    } catch (e) {
      setError(connectionError(e))
    } finally { setBusy(false) }
  }

  return <View style={[styles.app, !api && { paddingTop: inset.top }]}>
    <Ambient />
    {api ? <Workspace api={api} exitAccount={() => { saveProfile(api.profile, false); setApi(undefined) }} /> : <KeyboardAvoidingView style={styles.app} behavior={Platform.OS === 'ios' ? 'padding' : undefined}>
      <ScrollView keyboardShouldPersistTaps="handled" contentContainerStyle={[styles.connect, { paddingBottom: Math.max(inset.bottom, 20) }]}>
        <View style={styles.brand}><Image source={logo} style={styles.connectLogo} /><Text style={styles.wordmark}>Go Bot</Text></View>
        <Panel style={{ gap: 17, borderColor: '#d1d4d8' }}>
          <View style={styles.connectHeader}>
            <Text accessibilityRole="header" style={styles.connectHeading}>{t('连接服务器')}</Text>
            <Pressable accessibilityRole="button" accessibilityLabel={t('语言')} accessibilityState={{ expanded: languageOpen }}
              style={({ pressed }) => [styles.languageButton, pressed && s.pressed]} onPress={() => { Keyboard.dismiss(); setLanguageOpen(true) }}>
              <Languages size={16} color={muted} />
              <Text style={s.caption}>{languages.find(item => item.id === language)?.name}</Text>
            </Pressable>
          </View>
          <Field label={t('服务器地址')} keyboardType="url" autoCorrect={false} autoComplete="url" editable={!restoring && !busy} value={fields.server}
            placeholder="https://harness.example.com" onChangeText={server => setFields(old => ({ ...old, server }))} />
          <Field label={t('账号')} autoComplete="username" autoCorrect={false} editable={!restoring && !busy} value={fields.account}
            placeholder={t('输入已有账号')} onChangeText={account => setFields(old => ({ ...old, account }))} />
          <Button label={busy || restoring && fields.server ? t('正在连接…') : t('连接')} primary onPress={() => connect()} busy={busy || (restoring && !!fields.server)} disabled={restoring || !fields.server.trim() || !fields.account.trim()}><Link2 color="white" size={18} /></Button>
          <Pressable accessibilityRole="button" onPress={() => connect(true)} disabled={restoring || busy || !fields.server.trim() || !fields.account.trim()}
            style={{ alignSelf: 'center', paddingVertical: 10, paddingHorizontal: 12 }}>
            <Text style={s.caption}>{t('创建账号并连接')}</Text>
          </Pressable>
          <Hint error>{t(error)}</Hint>
        </Panel>
      </ScrollView>
      {languageOpen && <LanguageSheet onClose={() => setLanguageOpen(false)} />}
    </KeyboardAvoidingView>}
  </View>
}

function Workspace({ api, exitAccount }: { api: Controller; exitAccount: () => void }) {
  const [transcriptCache] = useState(() => new TranscriptCache())
  const { t } = useLocale()
  const inset = useSafeAreaInsets()
  const { width } = useWindowDimensions()
  const reduced = useReducedMotion()
  const [tab, setTab] = useState<Tab>('chat')
  const [mePage, setMePage] = useState<MePage>('tasks')
  const [returnToChat, setReturnToChat] = useState(false)
  const leaveGuard = useRef<LeaveGuard | undefined>(undefined)
  const [keyboard, setKeyboard] = useState(false)
  useEffect(() => {
    const shown = Keyboard.addListener(Platform.OS === 'ios' ? 'keyboardWillShow' : 'keyboardDidShow', () => setKeyboard(true))
    const hidden = Keyboard.addListener(Platform.OS === 'ios' ? 'keyboardWillHide' : 'keyboardDidHide', () => setKeyboard(false))
    return () => { shown.remove(); hidden.remove() }
  }, [])
  const [section, setSection] = useState<Section | null>(null)
  const [session, setSession] = useState<string | undefined>(api.mainSessionId)
  const [lookupError, setLookupError] = useState(false)
  const [lookupAttempt, setLookupAttempt] = useState(0)
  const pagePosition = useRef(new Animated.Value(0)).current
  const barWidth = Math.min(width - 80, 148)
  const itemWidth = (barWidth - 12) / 2
  useEffect(() => {
    const animation = Animated.timing(pagePosition, {
      toValue: tab === 'me' ? 1 : 0,
      duration: reduced ? 0 : 280,
      easing: Easing.inOut(Easing.cubic),
      useNativeDriver: true,
    })
    animation.start()
    return () => animation.stop()
  }, [tab, reduced, pagePosition])

  useEffect(() => {
    const connection = new HostConnection(transcriptCache, (cursors, signal, opened, receive) =>
      api.hostStream(cursors, signal, opened, receive))
    if (AppState.currentState !== 'background') connection.start()
    const listener = AppState.addEventListener('change', state => {
      if (state === 'background') connection.stop()
      else if (state === 'active') connection.start()
    })
    return () => { listener.remove(); connection.stop() }
  }, [api, transcriptCache])

  const [preparation, setPreparation] = useState<'running' | 'failed' | ''>('running')
  async function prepare() {
    setPreparation('running')
    try { await api.preparePlugins(); setPreparation('') }
    catch { setPreparation('failed') }
  }
  useEffect(() => { void prepare() }, [api])

  function unavailable(id: string) {
    if (session !== id) return
    if (id === api.mainSessionId) { setLookupError(true); return }
    setSession(api.mainSessionId)
  }

  function activate(next: Tab) {
    if (next === tab) return
    Keyboard.dismiss()
    const change = () => {
      setSection(null)
      setTab(next)
    }
    if (tab === 'me' && leaveGuard.current) leaveGuard.current(change)
    else change()
  }
  useEffect(() => {
    if (tab !== 'me' || mePage === 'plugins') return
    const listener = BackHandler.addEventListener('hardwareBackPress', () => { activate('chat'); return true })
    return () => listener.remove()
  }, [tab, mePage])
  function openModelSettings() { activate('me'); setMePage('settings'); setSection('models'); setReturnToChat(true) }
  function openPlugins() { activate('me'); setMePage('plugins'); setReturnToChat(true) }
  const bottomInset = keyboard ? Math.max(inset.bottom, 8) : Math.max(inset.bottom, 12) + 74

  return <KeyboardAvoidingView style={styles.app} behavior={Platform.OS === 'ios' ? 'padding' : undefined}>
    <View style={styles.pages}>
    <Animated.View style={{ flex: 1, flexDirection: 'row', width: width * 2, transform: [{ translateX: Animated.multiply(pagePosition, -width) }] }}>
      <View style={{ width, flexShrink: 0 }} pointerEvents={tab === 'chat' ? 'auto' : 'none'} accessibilityElementsHidden={tab !== 'chat'} importantForAccessibility={tab === 'chat' ? 'auto' : 'no-hide-descendants'}><Chat api={api} cache={transcriptCache} id={session ?? ''} active={tab === 'chat'} bottomInset={bottomInset} openPlugins={openPlugins}
        preparation={preparation} retryPreparation={() => void prepare()} resolving={session === undefined} lookupError={lookupError} retryLookup={() => setLookupAttempt(value => value + 1)}
        selectSession={setSession} onUnavailable={unavailable} openModelSettings={openModelSettings} /></View>
      <View style={{ width, flexShrink: 0 }} pointerEvents={tab === 'me' ? 'auto' : 'none'} accessibilityElementsHidden={tab !== 'me'} importantForAccessibility={tab === 'me' ? 'auto' : 'no-hide-descendants'}><Me api={api} cache={transcriptCache} active={tab === 'me'} page={mePage} onPageChange={setMePage}
        section={section} open={setSection} close={() => setSection(null)} exitAccount={exitAccount} bottomInset={bottomInset} leaveGuard={leaveGuard} backToChat={() => activate('chat')}
        openChat={id => { setSession(id); activate('chat'); setReturnToChat(false) }} returnToChat={returnToChat ? () => { activate('chat'); setReturnToChat(false) } : undefined} /></View>
    </Animated.View>
    </View>
    {!keyboard && <View accessibilityRole="tablist" style={[styles.navDock, { bottom: Math.max(inset.bottom, 12), width: barWidth }]}>
      <Animated.View pointerEvents="none" style={[styles.pill, { width: 44, left: 6 + (itemWidth - 44) / 2, transform: [{ translateX: Animated.multiply(pagePosition, itemWidth) }] }]} />
      {tabs.map(({ id, label, Icon }) => <Pressable accessibilityRole="tab" accessibilityLabel={t(label)} accessibilityState={{ selected: tab === id }} key={id}
        onPress={() => { if (id === 'me') setReturnToChat(false); activate(id) }} style={({ pressed }) => [styles.tab, pressed && s.pressed]}>
        <Icon size={24} color={ink} strokeWidth={2} />
      </Pressable>)}
    </View>}
  </KeyboardAvoidingView>
}

const styles = StyleSheet.create({
  app: { flex: 1 },
  pages: { flex: 1, overflow: 'hidden' },
  connect: { flexGrow: 1, justifyContent: 'center', paddingHorizontal: 23, width: '100%', maxWidth: 480, alignSelf: 'center', gap: 21 },
  connectHeader: { flexDirection: 'row', flexWrap: 'wrap', justifyContent: 'space-between', alignItems: 'center', columnGap: 8 },
  connectHeading: { color: ink, fontSize: 23, lineHeight: 30, fontWeight: '600', letterSpacing: -0.3 },
  languageButton: { flexDirection: 'row', alignItems: 'center', gap: 6, minHeight: 44, paddingHorizontal: 4 },
  brand: { alignItems: 'center', gap: 6 },
  connectLogo: { width: 104, height: 104 },
  wordmark: { color: ink, fontSize: 24, fontWeight: '600', letterSpacing: -0.5 },
  navDock: { position: 'absolute', alignSelf: 'center', flexDirection: 'row', backgroundColor: 'rgba(255,255,255,0.96)', borderRadius: 32, padding: 6,
    boxShadow: '0 4px 22px rgba(23,25,28,0.07)', borderWidth: StyleSheet.hairlineWidth, borderColor: '#eceef0' },
  tab: { flex: 1, height: 46, borderRadius: 25, alignItems: 'center', justifyContent: 'center', gap: 3 },
  pill: { position: 'absolute', left: 6, top: 7, bottom: 7, borderRadius: 23, backgroundColor: '#f0f1f2' },
})
