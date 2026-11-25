import { ProtocolDropdown } from './ProtocolDropdown'
import { imageGenerationSettingsEnabled } from '../../frontend/src/features'
import { changeProtocolURL, modelProtocol, modelRequestURL, protocolNames, protocolURLs, type ModelProtocol } from '../../frontend/src/settings/modelProtocol'
import { languages, useLocale } from './locale'
import { useEffect, useRef, useState, type RefObject } from 'react'
import { ActivityIndicator, Alert, Image, Pressable, StyleSheet, Text, View, Animated, Easing } from 'react-native'
import { ArrowLeft, Camera, ChevronRight, Clock3, Images, Image as Drawing, Languages, Layers2, LogOut, MonitorSmartphone, Pencil, Power, Puzzle, Search, Server, Settings2, Sparkles } from 'lucide-react-native'
import { useSafeAreaInsets } from 'react-native-safe-area-context'
import type { TranscriptCache } from '../../frontend/src/chat/transcriptCache'
import { Tasks } from './Tasks'
import { Devices } from './Devices'
import { Plugins } from './extensions/Plugins'
import { Proactive } from './Proactive'
import { Controller, problem, type ModelSettings, type ProviderPage, type ProviderSettings } from './api'
import { getAvatar, pickAvatar } from './library'
import { LanguageSheet } from './LanguageSheet'
import { accent, Button, danger, Field, Hint, IconButton, edge, glass, ink, logo, muted, Panel, s, Sheet, SheetScroll, useReducedMotion, wash, type PageChrome } from './ui'

const providerLabels = { 'web-search': '网页搜索', 'image-generation': '图片生成' }
export type Section = 'models' | ProviderPage | 'proactive'

export type MePage = 'tasks' | 'settings' | 'plugins' | 'devices'
export type LeaveGuard = (proceed: () => void) => void
const pages = [
  { id: 'tasks', label: 'Scheduled tasks', Icon: Clock3 },
  { id: 'settings', label: '设置', Icon: Settings2 },
  { id: 'plugins', label: 'Plugin market', Icon: Puzzle },
  { id: 'devices', label: '设备', Icon: MonitorSmartphone },
] as const

export function Me({ api, cache, active, page, onPageChange, section, open, close, exitAccount, bottomInset, openChat, returnToChat, backToChat, leaveGuard }: {
  api: Controller; cache: TranscriptCache; active: boolean; page: MePage; onPageChange: (page: MePage) => void
  section: Section | null; open: (section: Section) => void; close: () => void; exitAccount: () => void
  bottomInset: number; openChat: (id: string) => void; returnToChat?: () => void; backToChat: () => void; leaveGuard: RefObject<LeaveGuard | undefined>
}) {
  const { t } = useLocale(), inset = useSafeAreaInsets(), account = api.profile.account
  const [avatar, setAvatar] = useState<string | undefined>(() => getAvatar(account))
  const [avatarMenu, setAvatarMenu] = useState(false), [avatarError, setAvatarError] = useState('')
  const [dirty, setDirty] = useState(false), [pluginKey, setPluginKey] = useState(0)
  function leave(proceed: () => void) {
    if (!dirty) { proceed(); return }
    Alert.alert(t('放弃未保存的修改？'), undefined, [
      { text: t('取消'), style: 'cancel' },
      { text: t('放弃修改'), style: 'destructive', onPress: () => { setDirty(false); setPluginKey(value => value + 1); proceed() } },
    ])
  }
  useEffect(() => { leaveGuard.current = leave; return () => { leaveGuard.current = undefined } }, [dirty])
  async function changeAvatar(camera: boolean) {
    setAvatarError('')
    try { const uri = await pickAvatar(account, camera); if (uri) setAvatar(uri) }
    catch (error) { setAvatarError(error instanceof Error ? error.message : '无法更新头像。') }
  }
  const header = <View style={me.profile}>
    {returnToChat && <View style={me.return}><IconButton label={t('返回聊天')} onPress={returnToChat}><ArrowLeft size={21} color={ink} /></IconButton></View>}
    <Pressable accessibilityRole="button" accessibilityLabel={t('更换头像')} onPress={() => setAvatarMenu(true)} style={({ pressed }) => [me.avatar, pressed && s.pressed]}>
      {avatar ? <Image source={{ uri: avatar }} style={me.avatarImage} /> : <Image source={logo} style={me.defaultAvatar} resizeMode="contain" />}
      <View style={me.edit}><Pencil size={16} color={ink} strokeWidth={1.6} /></View>
    </Pressable>
    <Text accessibilityRole="header" numberOfLines={2} style={me.account}>{account}</Text>
    {!!avatarError && <Hint error>{t(avatarError)}</Hint>}
  </View>
  const navigation = <ProfileTabs page={page} choose={next => leave(() => { close(); onPageChange(next) })} />
  const chrome = { header, navigation, bottomInset }
  return <View style={{ flex: 1, paddingTop: inset.top, backgroundColor: '#fff' }}>
    <View style={page === 'tasks' ? me.page : me.hidden}><Tasks api={api} cache={cache} active={active && page === 'tasks'} openChat={openChat} {...chrome} /></View>
    <View style={page === 'settings' ? me.page : me.hidden}><Settings api={api} section={section} open={open} close={close} exitAccount={exitAccount} chrome={chrome} /></View>
    <View style={page === 'plugins' ? me.page : me.hidden}><Plugins key={pluginKey} api={api} cache={cache} active={active && page === 'plugins'} embedded {...chrome} onDirtyChange={setDirty} onBusyChange={ignoreBusy} onExit={backToChat} /></View>
    <View style={page === 'devices' ? me.page : me.hidden}><Devices api={api} active={active && page === 'devices'} {...chrome} /></View>
    {avatarMenu && <Sheet title={t('更换头像')} onClose={() => setAvatarMenu(false)}>{afterClose => <SheetScroll>
      <Button label={t('从图库选择')} onPress={() => afterClose(() => changeAvatar(false))}><Images size={20} color={ink} /></Button>
      <Button label={t('拍照')} onPress={() => afterClose(() => changeAvatar(true))}><Camera size={20} color={ink} /></Button>
    </SheetScroll>}</Sheet>}
  </View>
}
function ignoreBusy(_busy: boolean) {}

function ProfileTabs({ page, choose }: { page: MePage; choose: (page: MePage) => void }) {
  const { t } = useLocale(), reduced = useReducedMotion()
  const [width, setWidth] = useState(0), position = useRef(new Animated.Value(pages.findIndex(item => item.id === page))).current
  useEffect(() => { const animation = Animated.timing(position, { toValue: pages.findIndex(item => item.id === page), duration: reduced ? 0 : 200, easing: Easing.out(Easing.cubic), useNativeDriver: true }); animation.start(); return () => animation.stop() }, [page, reduced, position])
  const itemWidth = (width - 6) / pages.length
  return <View style={me.navigation}><View accessibilityRole="tablist" style={me.track} onLayout={event => setWidth(event.nativeEvent.layout.width)}>
    {width > 0 && <Animated.View pointerEvents="none" style={[me.selected, { width: itemWidth, transform: [{ translateX: Animated.multiply(position, itemWidth) }] }]} />}
    {pages.map(({ id, label, Icon }, index) => <Pressable key={id} accessibilityRole="tab" accessibilityState={{ selected: id === page }} accessibilityLabel={t(label)} onPress={() => choose(id)} style={({ pressed }) => [me.tab, pressed && s.pressed]}>
      <Icon size={20} color={id === page ? ink : muted} strokeWidth={1.55} />
      {index < pages.length - 1 && page !== id && page !== pages[index + 1].id && <View pointerEvents="none" style={me.separator} />}
    </Pressable>)}
  </View></View>
}
const me = StyleSheet.create({
  page: { flex: 1 }, hidden: { display: 'none' },
  profile: { alignItems: 'center', paddingTop: 29, paddingBottom: 35, paddingHorizontal: 24, gap: 16 },
  avatar: { width: 96, height: 96, borderRadius: 48, backgroundColor: '#f0f0f1', alignItems: 'center', justifyContent: 'center' },
  avatarImage: { width: 96, height: 96, borderRadius: 48 }, defaultAvatar: { width: 88, height: 88 },
  edit: { position: 'absolute', right: -5, bottom: -3, width: 32, height: 32, borderRadius: 16, backgroundColor: '#f5f5f6', borderWidth: 3, borderColor: '#fff', alignItems: 'center', justifyContent: 'center' },
  account: { color: ink, fontSize: 24, lineHeight: 30, fontWeight: '500', letterSpacing: -.5, textAlign: 'center' },
  return: { position: 'absolute', top: 8, left: 12 },
  navigation: { paddingHorizontal: 20, paddingVertical: 8, width: '100%', maxWidth: 620, alignSelf: 'center', backgroundColor: '#fff' },
  track: { flexDirection: 'row', padding: 3, borderRadius: 24, backgroundColor: '#f3f4f5' },
  selected: { position: 'absolute', top: 3, bottom: 3, left: 3, borderRadius: 21, backgroundColor: '#fff', boxShadow: '0 2px 9px rgba(23,25,28,0.07)' },
  separator: { position: 'absolute', right: 0, height: 18, width: StyleSheet.hairlineWidth, backgroundColor: '#dadddf' },
  tab: { flex: 1, minHeight: 36, flexDirection: 'row', alignItems: 'center', justifyContent: 'center', gap: 6, paddingHorizontal: 6 },
})

function Settings({ api, section, open, close, exitAccount, chrome }: {
  chrome: PageChrome; api: Controller; section: Section | null; open: (section: Section) => void; close: () => void; exitAccount: () => void
}) {
  const { t, language } = useLocale()
  const [languageOpen, setLanguageOpen] = useState(false)
  const [serverOpen, setServerOpen] = useState(false)
  const [stopping, setStopping] = useState(false)
  const [modelDirty, setModelDirty] = useState(false)
  const [error, setError] = useState('')
  async function stop() {
    setStopping(true)
    setError('')
    try { await api.stopHost(); exitAccount() }
    catch (e) { setError(problem(e, '无法停止 Host，请重试。')); setStopping(false) }
  }

  return <>
    <SheetScroll embedded {...chrome}>
      <Panel style={{ gap: 0, paddingVertical: 4 }}>
        {([
          ['models', '模型', Layers2],
          ['web-search', '网页搜索', Search],
          ['proactive', 'Proactive assistance', Sparkles],
          ['image-generation', '图片生成', Drawing],
          ['language', '语言', Languages],
        ] as const).filter(([id]) => id !== 'image-generation' || imageGenerationSettingsEnabled).map(([id, label, Icon], index) => <Pressable accessibilityRole="button" key={id}
          onPress={() => id === 'language' ? setLanguageOpen(true) : open(id)}
          style={[s.row, { minHeight: 60, gap: 14 }, index > 0 && { borderTopWidth: StyleSheet.hairlineWidth, borderColor: edge }]}>
          <Icon size={20} color={muted} strokeWidth={1.7} />
          <Text style={[s.body, { flex: 1 }]}>{t(label)}</Text>
          {id === 'language' && <Text style={s.caption}>{languages.find(item => item.id === language)?.name}</Text>}
          <ChevronRight size={15} color={muted} />
        </Pressable>)}
      </Panel>
      <Pressable accessibilityRole="button" accessibilityLabel={t('服务器地址')} onPress={() => setServerOpen(true)}>
        <Panel style={[s.row, { paddingVertical: 15, gap: 12 }]}><Server size={20} color={muted} strokeWidth={1.7} />
          <Text style={s.body}>{t('服务器地址')}</Text><Text numberOfLines={1} style={[s.caption, { flex: 1, textAlign: 'right' }]}>{api.profile.server}</Text>
          <ChevronRight size={15} color={muted} />
        </Panel>
      </Pressable>
      <Pressable accessibilityRole="button" disabled={stopping} onPress={exitAccount}>
        <Panel style={[s.row, { paddingVertical: 15, gap: 12 }]}>
          <LogOut size={20} color={muted} strokeWidth={1.7} />
          <Text style={s.body}>{t('退出登录')}</Text>
        </Panel>
      </Pressable>
      <Pressable accessibilityRole="button" disabled={stopping} onPress={() => Alert.alert(t('停止 Host？'), t('停止后会回到连接页。'), [
        { text: t('返回'), style: 'cancel' }, { text: 'Stop Host', style: 'destructive', onPress: stop },
      ])}>
        <Panel style={[s.row, { paddingVertical: 15, gap: 12 }]}>
          <Power size={20} color={danger} strokeWidth={1.7} />
          <Text style={[s.body, { color: danger }]}>Stop Host</Text>{stopping && <ActivityIndicator color={accent} />}
        </Panel>
      </Pressable>
      <Hint error>{t(error)}</Hint>
    </SheetScroll>
    {languageOpen && <LanguageSheet onClose={() => setLanguageOpen(false)} />}
    {serverOpen && <Sheet title={t('服务器地址')} onClose={() => setServerOpen(false)} raised={62}><SheetScroll>
      <Panel><Text selectable style={s.body}>{api.profile.server}</Text></Panel>
    </SheetScroll></Sheet>}
    {section === 'models' && <Sheet title={t('模型')} onClose={() => { setModelDirty(false); close() }} beforeDismiss={proceed => {
      if (!modelDirty) { proceed(); return }
      Alert.alert(t('放弃未保存的修改？'), undefined, [{ text: t('取消'), style: 'cancel' }, { text: t('放弃修改'), style: 'destructive', onPress: proceed }])
    }}><Models api={api} onDirtyChange={setModelDirty} /></Sheet>}
    {section === 'proactive' && <Sheet title={t('Proactive assistance')} onClose={close}>{afterClose => <Proactive api={api} onModels={() => afterClose(() => open('models'))} />}</Sheet>}
    {section && section !== 'models' && section !== 'proactive' && (section !== 'image-generation' || imageGenerationSettingsEnabled) && <Sheet title={t(providerLabels[section])} onClose={close}><Providers key={section} api={api} page={section} /></Sheet>}

  </>
}

function Models({ api, onDirtyChange }: { api: Controller; onDirtyChange: (dirty: boolean) => void }) {
  const { t } = useLocale()
  const [settings, setSettings] = useState<ModelSettings>()
  const [editing, setEditing] = useState<string | null>(null)
  const [name, setName] = useState('')
  const [baseUrl, setURL] = useState('')
  const [protocol, setProtocol] = useState<ModelProtocol>('responses')
  const [models, setModels] = useState('')
  const [apiKey, setKey] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  function load() { return api.modelSettings().then(setSettings).catch(e => setError(problem(e, '无法读取模型设置。'))) }
  useEffect(() => { void load() }, [api])

  function edit(id: string) {
    const endpoint = settings?.endpoints[id]
    setEditing(id)
    setName(endpoint?.name ?? '')
    setProtocol(modelProtocol(endpoint?.protocol))
    setURL(endpoint?.baseUrl ?? protocolURLs.responses)
    setModels(endpoint?.models.join('\n') ?? '')
    setKey('')
    setError('')
  }
  async function update(work: () => Promise<unknown>) {
    setBusy(true)
    setError('')
    try { await work(); setEditing(null); await load() }
    catch (e) { setError(problem(e, '无法保存。请检查端点地址、密钥与模型名。')); await load() }
    finally { setBusy(false) }
  }
  const list = models.split(/[\n,]/).map(model => model.trim()).filter(Boolean)
  useEffect(() => {
    const initial = editing === null ? undefined : settings?.endpoints[editing]
    onDirtyChange(editing !== null && (name !== (initial?.name ?? '') || baseUrl !== (initial?.baseUrl ?? protocolURLs.responses) || protocol !== modelProtocol(initial?.protocol) || apiKey !== '' || models !== (initial?.models.join('\n') ?? '')))
  }, [editing, settings, name, baseUrl, protocol, apiKey, models, onDirtyChange])


  return <SheetScroll>
    <Hint>{t('支持 OpenAI Responses 和 Anthropic Messages 兼容接口。默认端点的第一个模型用于新会话。')}</Hint>
    {Object.entries(settings?.endpoints ?? {}).map(([id, endpoint]) => <Pressable key={id} accessibilityRole="button" onPress={() => edit(id)}>
      <Panel style={{ backgroundColor: id === editing ? wash : glass }}>
        <View style={s.row}><Text style={[s.body, { flex: 1, fontWeight: '600' }]}>{endpoint.name}</Text>
          {settings?.defaultEndpointId === id && <Text style={s.caption}>{t('默认')}</Text>}<ChevronRight size={16} color={muted} /></View>
        <Hint>{protocolNames[modelProtocol(endpoint.protocol)]} · {endpoint.models.join(' · ')}</Hint>
      </Panel>
    </Pressable>)}
    {editing === null ? <Button label={t('添加端点')} onPress={() => edit('')} /> : <>
      <Field label={t('名称')} value={name} onChangeText={setName} />
      <ProtocolDropdown value={protocol} disabled={busy} onChange={next => { setURL(changeProtocolURL(baseUrl, protocol, next)); setProtocol(next) }} />
      <Hint>{t('按服务商提供的接口格式选择，与模型名称无关。')}</Hint>
      <Field label={t('端点地址')} keyboardType="url" autoCorrect={false} placeholder={protocolURLs[protocol]} value={baseUrl} onChangeText={setURL} />
      <Hint>{t('请求地址')}: {modelRequestURL(baseUrl, protocol)}</Hint>
      <Field label="API key" autoCorrect={false} secureTextEntry value={apiKey} onChangeText={setKey}
        placeholder={settings?.endpoints[editing]?.hasApiKey ? t('已保存，留空保留当前密钥') : t('输入 API key')} />
      <Field label={t('模型名（每行一个）')} autoCorrect={false} multiline value={models} onChangeText={setModels} />
      <Button primary label={t('保存端点')} busy={busy} disabled={!name.trim() || !baseUrl.trim() || !list.length || (!apiKey.trim() && !settings?.endpoints[editing]?.hasApiKey)}
        onPress={() => update(() => api.saveEndpoint(editing || undefined, { protocol, name: name.trim(), baseUrl: baseUrl.trim(), models: list, ...(apiKey.trim() && { apiKey: apiKey.trim() }) }))} />
      <Button label={t('取消')} disabled={busy} onPress={() => setEditing(null)} />
      {!!editing && settings?.defaultEndpointId !== editing && <Button label={t('设为默认端点')} disabled={busy} onPress={() => update(() => api.setDefaultEndpoint(editing))} />}
      {!!editing && <Button label={t('删除端点')} dangerTone disabled={busy} onPress={() => Alert.alert(t('删除端点？'), name, [{ text: t('返回'), style: 'cancel' }, { text: t('删除'), style: 'destructive', onPress: () => update(() => api.removeEndpoint(editing)) }])} />}
    </>}
    <Hint error>{t(error)}</Hint>
  </SheetScroll>
}

function Providers({ api, page }: { api: Controller; page: ProviderPage }) {
  const { t } = useLocale()
  const [data, setData] = useState<ProviderSettings>()
  const [selected, setSelected] = useState('')
  const [key, setKey] = useState('')
  const [model, setModel] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  function select(id: string, settings: ProviderSettings) {
    setSelected(id)
    setKey('')
    setModel(settings.entries[id]?.model ?? settings.providers.find(p => p.id === id)?.defaultModel ?? '')
  }
  useEffect(() => {
    api.providers(page).then(settings => {
      setData(settings)
      select(settings.provider || settings.defaultProvider || settings.providers[0]?.id || '', settings)
    }).catch(e => setError(problem(e, '无法读取供应商。')))
  }, [api, page])

  async function update(work: () => Promise<ProviderSettings>) {
    setBusy(true)
    setError('')
    try { const settings = await work(); setData(settings); select(selected, settings) }
    catch (e) {
      setError(problem(e, '无法保存供应商设置。'))
      api.providers(page).then(setData).catch(() => {})
    } finally { setBusy(false) }
  }
  const entry = data?.entries[selected]
  return <SheetScroll>
    {data?.providers.map(provider => <Pressable accessibilityRole="button" accessibilityState={{ selected: provider.id === selected }} key={provider.id} onPress={() => select(provider.id, data)}>
      <View style={[s.row, { minHeight: 48, paddingHorizontal: 12, borderRadius: 15, backgroundColor: provider.id === selected ? wash : glass }]}>
        <Text style={[s.body, { flex: 1 }]}>{provider.name}</Text>
        <Text style={s.caption}>{data.provider === provider.id ? t('当前使用') : data.entries[provider.id]?.hasApiKey ? t('已配置') : ''}</Text>
      </View>
    </Pressable>)}
    {!!selected && <>
      <Field label="API key" secureTextEntry autoCorrect={false} value={key} onChangeText={setKey} placeholder={entry?.hasApiKey ? t('已保存，留空保留当前密钥') : t('输入 API key')} />
      {page === 'image-generation' && <Field label={t('模型名')} value={model} onChangeText={setModel} autoCorrect={false} />}
      <Button label={t('保存配置')} disabled={!key.trim() && !entry?.hasApiKey} busy={busy} onPress={() => update(() => api.saveProvider(page, {
        provider: selected, ...(key.trim() && { apiKey: key.trim() }), ...(page === 'image-generation' && model.trim() && { model: model.trim() }), activate: false,
      }))} />
      <Button primary label={t('保存并使用')} disabled={!key.trim() && !entry?.hasApiKey} busy={busy} onPress={() => update(() => api.saveProvider(page, {
        provider: selected, ...(key.trim() && { apiKey: key.trim() }), ...(page === 'image-generation' && model.trim() && { model: model.trim() }), activate: true,
      }))} />
      {entry?.hasApiKey && <Button label={t('移除密钥')} dangerTone disabled={busy} onPress={() => update(() => api.removeProvider(page, selected))} />}
    </>}
    <Hint error>{t(error)}</Hint>
  </SheetScroll>
}
