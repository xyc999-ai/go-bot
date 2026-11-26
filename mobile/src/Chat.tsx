import { VideoViewport, notifyVideoViewport } from './VideoViewport'
import { useLocale } from './locale'
import { useCallback, useEffect, useMemo, useRef, useState, type ComponentRef, type ComponentType, type ReactElement } from 'react'
import { ActivityIndicator, Alert, Animated, AppState, Easing, Image, Keyboard, Pressable, ScrollView, StyleSheet, Text, TextInput, View, type ViewProps } from 'react-native'
import { ArrowUp, ChevronDown, Ellipsis, FileText, ImagePlus, Images, ListFilter, MessageCircle, MessageCircleQuestion, Pin, Plus, Shield, ShieldAlert, Square, X } from 'lucide-react-native'
import { attemptMessage, groupAssistantReplies, messageText, visibleQueue, type Message } from '../../frontend/src/chat/transcript'
import type { TranscriptCache } from '../../frontend/src/chat/transcriptCache'
import { ApiError, Controller, problem, type ModelSelection, type SessionRow } from './api'
import { LibrarySheet, ModelSheet, QuestionSheet } from './ChatSheets'
import MaskedView from '@react-native-masked-view/masked-view'
import { BlurTargetView, BlurView } from 'expo-blur'
import { LinearGradient } from 'expo-linear-gradient'
import { useSafeAreaInsets } from 'react-native-safe-area-context'
import { FileIcon } from './FileIcon'
import { ChatMenu } from './ChatMenu'
import { HostFilesSheet } from './HostFilesSheet'
import { ImageViewer } from './ImageViewer'
import { addAttachments, pickPhotos, readLibrary, type LibraryEntry } from './library'
import { MessageView, type Preview } from './Message'
import { accent, Button, Field, edge, glass, Hint, IconButton, ink, logo, muted, Panel, s, Sheet, SheetScroll, TintIcon, useReducedMotion, userBubble, warning, wash } from './ui'
import { ApprovalSheet, PermissionSheet, permissionNames } from './PermissionSheets'
import type { PermissionMode } from '../../frontend/src/chat/permissions'
import { useTranscript } from './useTranscript'
import { useTextPlayback } from '../../frontend/src/chat/useTextPlayback'

type Props = {
  api: Controller; cache: TranscriptCache; id: string; active: boolean
  preparation: 'running' | 'failed' | ''; retryPreparation: () => void
  resolving: boolean; lookupError: boolean; retryLookup: () => void
  bottomInset: number; openPlugins: () => void
  selectSession: (id: string) => void; onUnavailable: (id: string) => void; openModelSettings: () => void
}
const HeaderMask = MaskedView as unknown as ComponentType<ViewProps & { maskElement: ReactElement }>
type Overlay = 'chats' | 'files' | 'model' | 'permission' | 'tools' | 'image' | 'file' | 'rename' | ''

export function Chat({ api, cache, id, active, preparation, retryPreparation, resolving, lookupError, retryLookup, selectSession, onUnavailable, openModelSettings, bottomInset, openPlugins }: Props) {
  const { t } = useLocale()
  const inset = useSafeAreaInsets()
  const headerHeight = inset.top + 80
  const blurTarget = useRef<ComponentRef<typeof View>>(null)
  const [rows, setRows] = useState<SessionRow[]>([])
  const [renameRow, setRenameRow] = useState<SessionRow>()
  const [renameTitle, setRenameTitle] = useState('')
  const [renameBusy, setRenameBusy] = useState(false)
  const [renameError, setRenameError] = useState('')
  function chatActions(row: SessionRow, afterClose: (action: () => void) => void) {
    if (row.isMain) return
    Alert.alert(row.title || t('未命名会话'), undefined, [
      {text:t('返回'),style:'cancel'},
      {text:t('重命名会话'),onPress:()=>afterClose(()=>{setRenameRow(row);setRenameTitle(row.title || '');setRenameError('');show('rename')})},
      {text:t('归档'),style:'destructive',onPress:()=>Alert.alert(t('归档此会话？'),t('会话将从列表隐藏。'),[
        {text:t('返回'),style:'cancel'}, {text:t('归档'),style:'destructive',onPress:()=>void archiveChat(row)},
      ])},
    ])
  }
  async function saveTitle(close: () => void) {
    if (!renameRow || !renameTitle.trim() || renameBusy) return
    setRenameBusy(true);setRenameError('')
    try {
      const updated = await api.rename(renameRow.sessionId,renameTitle)
      setRows(current=>current.map(row=>row.sessionId===renameRow.sessionId?{...row,title:updated.title}:row))
      close()
    } catch { setRenameError('无法重命名会话，请重试。') }
    finally { setRenameBusy(false) }
  }
  const [, updateSummaries] = useState(0)
  useEffect(() => cache.subscribe('@summaries', () => updateSummaries(value => value + 1)), [cache])

  async function archiveChat(row: SessionRow, stopActivity = false) {
    try {
      await api.archive(row.sessionId, stopActivity)
      cache.delete(row.sessionId)
      if (row.sessionId === id) switchChat()
      void refreshChats()
    } catch (error) {
      if (!stopActivity && error instanceof ApiError && ['session_has_schedule', 'session_busy'].includes(error.code)) {
        Alert.alert(t('停止并归档会话？'), t('将停止正在运行的会话和关联的启用中任务。会话记录和文件会保留。'), [
          { text: t('返回'), style: 'cancel' },
          { text: t('停止并归档'), style: 'destructive', onPress: () => void archiveChat(row, true) },
        ])
      } else {
        setListError(problem(error, '无法归档会话。'))
      }
    }
  }

  const [cursor, setCursor] = useState('')
  const [listError, setListError] = useState('')
  const [loadingRows, setLoadingRows] = useState(false)
  const [draft, setDraft] = useState('')
  const [attachments, setAttachments] = useState<LibraryEntry[]>([])
  const [selectedModel, setSelectedModel] = useState<ModelSelection | null>(null)
  const [defaultModel, setDefaultModel] = useState('')
  const [permission, setPermission] = useState<{ id: string; mode: PermissionMode }>()
  const [overlay, show] = useState<Overlay>('')
  useEffect(() => {
    if (overlay !== 'chats') return
    let timer: ReturnType<typeof setTimeout> | undefined
    const unsubscribe = cache.subscribe('@sessions', () => {
      clearTimeout(timer)
      timer = setTimeout(() => void refreshChats(), 150)
    })
    return () => { unsubscribe(); clearTimeout(timer) }
  }, [cache, overlay, api])
  const [sending, setSending] = useState(false)
  const [activity, setActivity] = useState({ id: '', working: false })
  const reportWorking = useCallback((working: boolean) => setActivity(previous => previous.id === id && previous.working === working ? previous : { id, working }), [id])
  const [creating, setCreating] = useState(false)
  const createInFlight = useRef(false)
  const [picking, setPicking] = useState(false)
  const [notice, setNotice] = useState('')
  const [modelRequired, setModelRequired] = useState<'setup' | 'selection'>()
  const [pending, setPending] = useState<{ id: string; session: string; text: string; files: LibraryEntry[] }>()
  const scopedId = useRef(id)
  scopedId.current = id

  async function refreshChats(next = '') {
    setLoadingRows(true)
    setListError('')
    try {
      const data = await api.sessions(next)
      setRows(previous => next ? [...previous, ...data.sessions.filter(item => !previous.some(old => old.sessionId === item.sessionId))] : data.sessions)
      setCursor(data.nextCursor ?? '')
    } catch (e) { setListError(problem(e, '无法读取会话。')) }
    finally { setLoadingRows(false) }
  }

  useEffect(() => {
    if (!active || !id) return
    let stale = false
    setSelectedModel(null)
    api.modelSettings().then(settings => {
      if (!stale) setDefaultModel(settings.endpoints[settings.defaultEndpointId]?.models[0] ?? '')
    }).catch(() => {})
    api.sessionPermission(id).then(state => { if (!stale) setPermission({ id, mode: state.mode }) }).catch(() => {})
    api.sessionModel(id).then(model => { if (!stale) setSelectedModel(model.selection) }).catch(() => {})
    return () => { stale = true }
  }, [api, active, id])

  async function choose(model: ModelSelection) {
    if (!id) return
    await api.selectModel(id, model)
    if (scopedId.current === id) { setSelectedModel(model); if (cache.get(id)?.transcript.activeTurn != null) setNotice('将在下一轮使用此模型。') }
  }
  function switchChat(next?: string) {
    Keyboard.dismiss()
    setDraft('')
    setAttachments([])
    setNotice('')
    setPending(undefined)
    if (next) selectSession(next)
    else onUnavailable(id)
    show('')
  }
  async function createChat() {
    if (createInFlight.current || sending) return
    createInFlight.current = true
    setCreating(true)
    setNotice('')
    try {
      const { sessionId } = await api.createSession()
      switchChat(sessionId)
    } catch (error) {
      setNotice(problem(error, '无法创建会话，请重试。'))
      if (error instanceof ApiError && error.code === 'model_default_required') { show(''); setModelRequired('setup') }
    } finally {
      createInFlight.current = false
      setCreating(false)
    }
  }
  function add(items: LibraryEntry[]) { setAttachments(addAttachments(attachments, items)) }
  async function photos() {
    setNotice('')
    setPicking(true)
    try { add(await pickPhotos()) }
    catch (e) { setNotice(e instanceof Error ? e.message : '无法添加图片。') }
    finally { setPicking(false) }
  }
  async function send(idle: boolean) {
    const content = draft.trim()
    const files = attachments
    if (!id || sending || (!content && !files.length)) return
    setSending(true)
    setNotice('')
    try {
      const next = await api.send(id, content, files)
      if (idle) setPending({ id: next.messageId, session: id, text: content, files })
      setDraft('')
      setAttachments([])
    } catch (e) {
      setNotice(problem(e, '发送失败，输入内容已保留。'))
      if (e instanceof ApiError && ['model_unconfigured', 'model_unavailable', 'model_default_required'].includes(e.code)) setModelRequired(e.code === 'model_unavailable' ? 'selection' : 'setup')
    } finally { setSending(false) }
  }

  const preparationNotice = preparation !== '' ? <View style={styles.preparation}>
    <Text style={[s.meta, { flex: 1 }]}>{t(preparation === 'running' ? '正在准备浏览器与 Office 能力…' : '部分插件未能准备完成。')}</Text>
    <Pressable accessibilityRole="button" onPress={preparation === 'failed' ? retryPreparation : openPlugins} style={{ paddingVertical: 8 }}><Text style={s.caption}>{t(preparation === 'failed' ? '重试' : '查看插件')}</Text></Pressable>
  </View> : null
  return <>
    <View style={{ flex: 1, paddingBottom: bottomInset, backgroundColor: '#fff' }}>
    <BlurTargetView ref={blurTarget} style={{ flex: 1 }}>
    {!id ? <View style={[styles.emptyChat, { paddingTop: headerHeight }]}>
      <Image source={logo} accessibilityLabel="Go Bot" resizeMode="contain" style={styles.emptyLogo} />
      {resolving && !lookupError ? <ActivityIndicator color={accent} accessibilityLabel={t('正在读取会话…')} />
        : <Button label={t('创建新会话')} primary busy={creating} onPress={createChat}><Plus size={18} color="white" /></Button>}
      {resolving && lookupError && <Button label={t('重新加载')} onPress={retryLookup} />}
      {!overlay && !!notice && <View style={styles.createError}><Hint error={notice !== '将在下一轮使用此模型。'}>{t(notice)}</Hint>
        {notice.includes('默认模型') && <Button label={t('打开模型设置')} onPress={openModelSettings} />}
      </View>}
    </View> : <Conversation key={id} api={api} cache={cache} id={id} topInset={headerHeight} lead={preparationNotice} active={active && !overlay} onUnavailable={onUnavailable}
      pending={pending?.session === id ? pending : undefined} sending={sending} onWorkingChange={reportWorking}
      renderComposer={({ idle, generation, cancel, questionButton, livePermission }) => <View style={styles.composerShell}>
        {questionButton}
        {attachments.length > 0 && <View style={styles.draftRail}>
          <Text style={s.caption}>{t('草稿')} ({attachments.length})</Text>
          <ScrollView horizontal keyboardShouldPersistTaps="always" showsHorizontalScrollIndicator={false} contentContainerStyle={{ alignItems: 'center', gap: 6 }}>
            {attachments.map(item => <View key={item.id} style={styles.chip}>
              {item.kind === 'image' ? <Image source={{ uri: item.uri }} style={{ width: 29, height: 29, borderRadius: 7 }} /> : <FileIcon name={item.name} size={29} />}
              <Text style={styles.chipName} numberOfLines={1}>{item.name}</Text>
              <IconButton label={t('移除') + ' ' + item.name} onPress={() => setAttachments(previous => previous.filter(entry => entry.id !== item.id))}><X size={15} color={muted} /></IconButton>
            </View>)}
          </ScrollView>
        </View>}
        {id === api.mainSessionId && (cache.get(id)?.transcript.messages.length ?? 0) <= 1 && <View style={{ flexDirection: 'row', justifyContent: 'center', gap: 24, marginBottom: 10 }}>
          <Pressable onPress={openModelSettings}><Text style={[s.caption, { color: accent }]}>{t('配置模型')}</Text></Pressable>
          <Pressable onPress={openPlugins}><Text style={[s.caption, { color: accent }]}>{t('查看插件')}</Text></Pressable>
        </View>}
        <View style={styles.composer}>
          <TextInput accessibilityLabel={t('消息')} placeholder={t('问点什么，或交给我一项任务…')} placeholderTextColor={muted} multiline value={draft}
            onChangeText={setDraft} editable={!sending} style={styles.input} />
          <View style={styles.toolbar}>
            <IconButton label={t('工具栏')} disabled={picking || sending} style={styles.toolButton} onPress={() => { Keyboard.dismiss(); show('tools') }}><Plus size={23} color={ink} /></IconButton>
            <Pressable accessibilityRole="button" accessibilityLabel={t('选择模型')} disabled={sending} style={styles.model} onPress={() => { Keyboard.dismiss(); show('model') }}>
              <Text numberOfLines={1} style={styles.modelName}>{selectedModel?.modelId || defaultModel || t('选择模型')}</Text><ChevronDown size={14} color={accent} />
            </Pressable>
            <Pressable accessibilityRole="button" accessibilityLabel={t('当前会话权限')} disabled={!idle || sending || !(livePermission || permission?.id === id)}
              style={styles.model} onPress={() => {
                if (livePermission) setPermission({ id, mode: livePermission })
                Keyboard.dismiss(); show('permission')
              }}>
              <Shield size={15} color={accent} />
              <Text numberOfLines={1} style={styles.modelName}>{t(permissionNames[livePermission ?? (permission?.id === id ? permission.mode : 'danger-full-access')])}</Text>
            </Pressable>
            <View style={{ flex: 1 }} />
            {generation && <IconButton label={t('停止生成')} onPress={cancel} style={styles.toolButton}><Square fill={accent} size={15} color={accent} /></IconButton>}
            <IconButton label={t('发送消息')} disabled={sending || (!draft.trim() && !attachments.length)} style={styles.send} onPress={() => { Keyboard.dismiss(); void send(idle) }}>
              {sending ? <ActivityIndicator color="white" /> : <ArrowUp size={21} color="#fff" strokeWidth={2.6} />}
            </IconButton>
          </View>
        </View>
        {!!notice && <View><Hint error={notice !== '将在下一轮使用此模型。'}>{t(notice)}</Hint>{notice.includes('默认模型') && <Button label={t('打开模型设置')} onPress={openModelSettings} />}</View>}
      </View>}
    />}
    </BlurTargetView>
    <View pointerEvents="box-none" style={[styles.headerLayer, { height: headerHeight + 16 }]}>
      <View pointerEvents="none" style={StyleSheet.absoluteFill}>
        <HeaderMask style={StyleSheet.absoluteFill} maskElement={<LinearGradient colors={['#000', 'rgba(0,0,0,0.88)', 'rgba(0,0,0,0.48)', 'rgba(0,0,0,0.12)', 'transparent']} locations={[0, .35, .65, .88, 1]} style={StyleSheet.absoluteFill} />}>
          <BlurView blurTarget={blurTarget} blurMethod="dimezisBlurViewSdk31Plus" tint="light" intensity={30} style={StyleSheet.absoluteFill} />
        </HeaderMask>
      </View>
      <View pointerEvents="box-none" style={[styles.head, { marginTop: inset.top }]}>
        {!!id && <View style={styles.brandPosition} pointerEvents="none"><FloatingLogo active={active} working={activity.id === id && activity.working} /></View>}
        <IconButton label={t('打开会话列表')} style={styles.headButton} onPress={() => { Keyboard.dismiss(); show('chats'); void refreshChats() }}><ListFilter size={21} color={ink} strokeWidth={1.65} /></IconButton>
        <View pointerEvents="none" style={{ flex: 1 }} />
        <ChatMenu onFiles={() => show('files')} onPlugins={openPlugins} />
      </View>
    </View>
    </View>
    {overlay === 'permission' && permission?.id === id && <PermissionSheet mode={permission.mode} close={() => show('')}
      choose={async mode => { const state = await api.selectPermission(id, mode); if (scopedId.current === id) setPermission({ id, mode: state.mode }) }} />}
    {overlay === 'chats' && <Sheet title={t('会话')} drawer onClose={() => show('')}>{afterClose => <SheetScroll>
      <Button label={t('新会话')} primary disabled={sending} busy={creating} onPress={createChat}><Plus size={20} color="white" /></Button>
      {[{ sessionId: api.mainSessionId, title: 'Go Bot', createdAt: '', isMain: true }, ...rows.filter(row => row.sessionId !== api.mainSessionId)].map(row => <View key={row.sessionId} style={[s.row, { borderRadius: 13, backgroundColor: row.sessionId === id ? wash : glass, paddingLeft: 11 }]}>
        <Pressable accessibilityRole="button" onPress={() => switchChat(row.sessionId)} onLongPress={() => chatActions(row, afterClose)} disabled={sending || creating} style={[s.row, { flex: 1, minHeight: 44 }]}>
          <MessageCircle size={17} color={accent} /><View style={{ flex: 1, gap: 3 }}>
            <Text style={s.body} numberOfLines={2}>{row.isMain ? 'Go Bot' : row.title || t('未命名会话')}</Text>
            {cache.summary(row.sessionId)?.pending && <Text style={[s.caption, { color: warning }]}>{t(cache.summary(row.sessionId)?.pending === 'approval' ? '待审批' : '待回答')}</Text>}
          </View>
          {row.isMain && <View accessible accessibilityLabel={t('主会话')} style={{ width: 36, alignItems: 'center', justifyContent: 'center' }}><Pin size={15} color={muted} strokeWidth={1.6} /></View>}
        </Pressable>
        {!row.isMain && <IconButton label={t('会话操作')} disabled={sending} onPress={() => chatActions(row, afterClose)}><Ellipsis size={18} color={muted} /></IconButton>}
      </View>)}
      {!!cursor && <Button label={t('查看更早的会话')} busy={loadingRows} onPress={() => refreshChats(cursor)} />}
      {loadingRows && !rows.length && <Hint>{t('正在读取会话…')}</Hint>}
      {!loadingRows && !rows.length && !listError && <Hint>{t('还没有会话。')}</Hint>}
      <Hint error>{t(listError)}</Hint>
      <Hint error={notice !== '将在下一轮使用此模型。'}>{t(notice)}</Hint>
      {notice.includes('默认模型') && <Button label={t('打开模型设置')} onPress={() => { show(''); openModelSettings() }} />}
    </SheetScroll>}</Sheet>}
    {overlay === 'rename' && <Sheet title={t('重命名会话')} beforeDismiss={dismiss => { if (!renameBusy) dismiss() }} onClose={() => {
      show(''); setRenameRow(undefined); requestAnimationFrame(() => show('chats'))
    }}>{afterClose => <SheetScroll>
      <Field label={t('会话标题')} value={renameTitle} onChangeText={setRenameTitle} maxLength={160} autoFocus editable={!renameBusy} returnKeyType="done" onSubmitEditing={() => void saveTitle(() => afterClose(() => {}))} />
      <Hint error>{t(renameError)}</Hint>
      <Button primary label={t('保存')} busy={renameBusy} disabled={!renameTitle.trim()} onPress={() => void saveTitle(() => afterClose(() => {}))} />
      <Button label={t('返回')} disabled={renameBusy} onPress={() => afterClose(() => {})} />
    </SheetScroll>}</Sheet>}
    {modelRequired && <Sheet title={t(modelRequired === 'selection' ? '选择模型' : '配置模型')} onClose={() => setModelRequired(undefined)}>{afterClose => <SheetScroll>
      <Text style={s.body}>{t(modelRequired === 'selection' ? '当前会话的模型不可用，请重新选择。' : '请先配置模型，再开始聊天。')}</Text>
      <Hint>{t('文字和附件已保留在输入框中。')}</Hint>
      {modelRequired === 'selection' && <Button primary label={t('选择模型')} onPress={() => afterClose(() => { setModelRequired(undefined); show('model') })} />}
      <Button primary={modelRequired !== 'selection'} label={t('打开模型设置')} onPress={() => afterClose(() => { setModelRequired(undefined); openModelSettings() })} />
    </SheetScroll>}</Sheet>}
    {overlay === 'files' && <HostFilesSheet api={api} close={() => show('')} />}
    {overlay === 'model' && <ModelSheet api={api} choice={selectedModel} choose={choose} close={() => show('')} settingsOpen={openModelSettings} />}
    {overlay === 'tools' && <Sheet title={t('工具栏')} raised={122} onClose={() => show('')}>{afterClose => <SheetScroll>
      <Panel style={{ paddingHorizontal: 12, paddingVertical: 2, gap: 0 }}>
        {([
          ['相册', ImagePlus, '#f1f2f3', ink, () => afterClose(photos)],
          ['图片', Images, '#f1f2f3', ink, () => {
            try { if (readLibrary('image').length) afterClose(() => show('image')); else afterClose(photos) }
            catch { setNotice('无法读取本地图片。'); show('') }
          }],
          ['文件', FileText, '#f1f2f3', ink, () => afterClose(() => show('file'))],
        ] as const).map(([label, Icon, background, color, action], index) =>
          <Pressable key={label} accessibilityRole="button" onPress={action}
            style={[s.row, { minHeight: 52 }, index > 0 && { borderTopWidth: StyleSheet.hairlineWidth, borderColor: edge }]}>
            <TintIcon background={background}><Icon color={color} size={20} strokeWidth={1.8} /></TintIcon>
            <Text style={s.body}>{t(label)}</Text>
          </Pressable>)}
      </Panel>
    </SheetScroll>}</Sheet>}
    {(overlay === 'image' || overlay === 'file') && <LibrarySheet kind={overlay} close={() => show('')} add={add}
      remove={ids => setAttachments(previous => previous.filter(item => !ids.includes(item.id)))} />}
  </>
}

function FloatingLogo({ active, working }: { active: boolean; working: boolean }) {
  const { t } = useLocale()
  const reduced = useReducedMotion()
  const offset = useRef(new Animated.Value(0)).current
  const progress = useRef(new Animated.Value(working ? 1 : 0)).current
  const pulse = useRef(new Animated.Value(1)).current
  useEffect(() => {
    const transition = Animated.timing(progress, { toValue: working ? 1 : 0, duration: reduced ? 0 : 220, easing: Easing.out(Easing.cubic), useNativeDriver: true })
    transition.start()
    return () => transition.stop()
  }, [working, reduced, progress])
  useEffect(() => {
    if (!working || !active || reduced) { pulse.setValue(1); return }
    const breathing = Animated.loop(Animated.sequence([
      Animated.timing(pulse, { toValue: .3, duration: 750, easing: Easing.inOut(Easing.sin), useNativeDriver: true }),
      Animated.timing(pulse, { toValue: 1, duration: 750, easing: Easing.inOut(Easing.sin), useNativeDriver: true }),
    ]))
    breathing.start()
    return () => breathing.stop()
  }, [working, active, reduced, pulse])
  useEffect(() => {
    if (!active || reduced) { offset.setValue(0); return }
    const settings = { duration: 1450, easing: Easing.inOut(Easing.sin), useNativeDriver: true }
    const floating = Animated.loop(Animated.sequence([
      Animated.timing(offset, { ...settings, toValue: -4 }),
      Animated.timing(offset, { ...settings, toValue: 0 }),
    ]))
    floating.start()
    return () => floating.stop()
  }, [active, offset, reduced])
  return <Animated.View accessible accessibilityLabel={working ? 'Go Bot · ' + t('Working') : 'Go Bot'} accessibilityLiveRegion="polite" style={[styles.brandBadge, { transform: [{ translateY: offset }] }]}>
    <View style={styles.brandAvatar}><Image source={logo} resizeMode="contain" style={styles.brandLogo} /></View>
    <View style={styles.brandNamePlate}>
      <Animated.View accessibilityElementsHidden importantForAccessibility="no-hide-descendants" style={[styles.brandStatus, { opacity: Animated.subtract(1, progress), transform: [{ translateY: Animated.multiply(progress, -3) }] }]}><Text style={styles.brandName}>Go Bot</Text></Animated.View>
      <Animated.View accessibilityElementsHidden importantForAccessibility="no-hide-descendants" style={[styles.brandStatus, { opacity: progress, transform: [{ translateY: progress.interpolate({ inputRange: [0, 1], outputRange: [3, 0] }) }] }]}>
        <Animated.View style={[styles.workingDot, { opacity: pulse }]} /><Text style={styles.brandName}>{t('Working')}</Text>
      </Animated.View>
    </View>
  </Animated.View>
}

type Pending = { id: string; session: string; text: string; files: LibraryEntry[] }
function Conversation({ api, cache, id, pending, active, sending, onUnavailable, onWorkingChange, renderComposer, topInset, lead }: {
  api: Controller; cache: TranscriptCache; id: string; pending?: Pending; active: boolean; sending: boolean
  onUnavailable: (id: string) => void; topInset: number; lead?: React.ReactNode
  onWorkingChange: (working: boolean) => void
  renderComposer: (controls: { idle: boolean; generation: boolean; cancel: () => void; questionButton?: React.ReactNode; livePermission?: PermissionMode }) => React.ReactNode
}) {
  const { t } = useLocale()
  const { state, before, older, loadOlder, loading, connecting, error, unavailable, reload } = useTranscript(api, id, cache)
  useEffect(() => { if (unavailable) onUnavailable(id) }, [id, onUnavailable, unavailable])
  const [closedQuestion, hideQuestion] = useState('')
  const [preview, setPreview] = useState<Preview>()
  const [failed, setFailed] = useState('')
  const scroll = useRef<ComponentRef<typeof ScrollView>>(null)
  const following = useRef(true)
  const turn = state.activeTurn
  const question = state.questions[0]
  const messages = useMemo(() => {
    const messages = state.messages.slice()
    const draft = state.draft ? attemptMessage(state.draft) : null
    if (draft) messages.push(draft)
    return messages
  }, [state.messages, state.draft])
  const reduced = useReducedMotion()
  const [foreground, setForeground] = useState(AppState.currentState === 'active')
  useEffect(() => {
    const listener = AppState.addEventListener('change', state => setForeground(state === 'active'))
    return () => listener.remove()
  }, [])
  const playback = useTextPlayback(api.profile.account + '/' + id, messages,
    active && foreground && !reduced && !loading && !connecting, state.streamEpoch)
  const awaiting = pending && !state.messages.some(message => message.id === pending.id) && !state.queue.some(item => item.id === pending.id)
  const replies = groupAssistantReplies(playback.messages)
  const idle = turn === null && !state.queue.length
  const generating = turn !== null || playback.pending
  const displayTurn = turn ?? playback.pendingTurn
  const hasReply = generating && replies.some(message => message.role === 'assistant' && message.turn === displayTurn)
  const waiting = awaiting || generating && !hasReply
  useEffect(() => { onWorkingChange(Boolean(awaiting || generating || sending)) }, [awaiting, generating, sending, onWorkingChange])
  const title = waiting ? t('正在等待回复…') : t('生成中')
  const queue = visibleQueue(state, state.messages)

  async function action(work: () => Promise<unknown>) {
    setFailed('')
    try { await work() } catch (e) { setFailed(problem(e)) }
  }
  function message(item: Message) {
    return <MessageView api={api} id={id} key={item.id} message={item} generating={generating && item.role === 'assistant' && item.turn === displayTurn} results={state.toolResults} openImage={setPreview} />
  }

  return <>
    <VideoViewport active={active}><ScrollView ref={scroll} style={{ flex: 1 }} contentContainerStyle={[styles.thread, { paddingTop: topInset + 12 }]} keyboardShouldPersistTaps="handled"
      maintainVisibleContentPosition={{ minIndexForVisible: 1 }} scrollEventThrottle={120}
      onScroll={({ nativeEvent: { layoutMeasurement, contentOffset, contentSize } }) => {
        notifyVideoViewport()
        following.current = contentSize.height - layoutMeasurement.height - contentOffset.y < 92
      }}
      onContentSizeChange={() => { notifyVideoViewport(); if (following.current) scroll.current?.scrollToEnd({ animated: false }) }}>
      {lead}
      {before !== null && <Button label={t('加载更早的消息')} onPress={() => { following.current = false; void loadOlder() }} busy={older} />}
      {loading && <Hint>{t('正在读取会话…')}</Hint>}
      {!!error && <><Hint error>{t(error)}</Hint><Button label={t('重新加载')} onPress={reload} /></>}
      {!loading && !error && !replies.length && !waiting && <View style={styles.welcome}>
        <Image source={logo} accessibilityLabel="Go Bot" style={{ width: 206, height: 182 }} />
        <Text style={styles.welcomeTitle}>{t('今天想做什么？')}</Text>
      </View>}
      {replies.map(message)}
      {awaiting && <View style={styles.pending}>
        {pending.files.map(file => <Text key={file.id} numberOfLines={1} style={s.caption}>{file.name}</Text>)}
        {!!pending.text && <Text style={s.body}>{pending.text}</Text>}
      </View>}
      {waiting && !hasReply && <View style={s.row}>
        <Image source={logo} style={{ width: 36, height: 36 }} />
        <ActivityIndicator size="small" color={accent} /><Text accessibilityLiveRegion="polite" style={s.caption}>{title}</Text>
      </View>}
      {connecting && <Hint>{t('正在重新连接…')}</Hint>}
    </ScrollView></VideoViewport>
    {queue.length > 0 && <View style={styles.queue}>
      {queue.map(item => <View key={item.id} style={s.row}>
        <Text numberOfLines={1} style={[s.caption, { flex: 1 }]}>{t(item.placement === 'steering' ? '引导中' : '排队中') + ' · '}{messageText(item) || t('附件')}</Text>
        <Pressable accessibilityRole="button" style={styles.steer} onPress={() => action(() => api.updateQueue(id, item.id, true))}><Text style={s.caption}>{t('引导')}</Text></Pressable>
        <IconButton label={t('删除排队消息')} onPress={() => action(() => api.updateQueue(id, item.id, false))}><X size={16} color={muted} /></IconButton>
      </View>)}
    </View>}
    {!!failed && <View style={{ paddingHorizontal: 20 }}><Hint error>{t(failed)}</Hint></View>}
    {!loading && !unavailable && renderComposer({ idle, generation: turn !== null, cancel: () => { if (turn !== null) void action(() => api.stopTurn(id, turn)) },
      livePermission: state.permission?.mode,
      questionButton: question && <Button label={t(question.approval ? '查看执行审批' : '回答助手的问题')}
        onPress={() => { Keyboard.dismiss(); hideQuestion('') }}>
        {question.approval ? <ShieldAlert size={19} color={accent} /> : <MessageCircleQuestion size={19} color={accent} />}
      </Button> })}
    {question && active && !preview && question.callId !== closedQuestion && (question.approval
      ? <ApprovalSheet key={question.callId} item={question} close={() => hideQuestion(question.callId)}
        decide={decision => api.answer(id, question.callId, [{ id: 'approval', selected: [decision] }])} />
      : <QuestionSheet key={question.callId} pending={question} close={() => hideQuestion(question.callId)}
        submit={answers => api.answer(id, question.callId, answers)} cancel={() => api.cancelQuestion(id, question.callId)} />)}
    {preview && <ImageViewer preview={preview} close={() => setPreview(undefined)} />}
  </>
}

const styles = StyleSheet.create({
  headerLayer: { position: 'absolute', top: 0, left: 0, right: 0 },
  preparation: { flexDirection: 'row', alignItems: 'center', gap: 12, paddingHorizontal: 12, paddingVertical: 4, backgroundColor: '#f6f6f7', borderRadius: 14 },
  head: { flexDirection: 'row', alignItems: 'center', height: 64, paddingHorizontal: 16, gap: 6 },
  brandPosition: { position: 'absolute', left: 0, right: 0, top: 4, alignItems: 'center' },
  brandBadge: { alignItems: 'center' },
  brandAvatar: { width: 48, height: 44, alignItems: 'center', justifyContent: 'center' },
  brandLogo: { width: 48, height: 44 },
  brandNamePlate: { marginTop: -1, height: 28, width: 80, borderRadius: 16, backgroundColor: '#fff', boxShadow: '0 4px 14px rgba(23,25,28,0.075)' },
  brandStatus: { ...StyleSheet.absoluteFill, flexDirection: 'row', alignItems: 'center', justifyContent: 'center', gap: 6 },
  workingDot: { width: 5, height: 5, borderRadius: 3, backgroundColor: ink },
  brandName: { fontSize: 13, lineHeight: 18, fontWeight: '600', color: ink, letterSpacing: -.2 },
  headButton: { backgroundColor: 'rgba(255,255,255,0.7)' },
  thread: { flexGrow: 1, paddingHorizontal: 18, paddingVertical: 15, gap: 20, width: '100%', maxWidth: 760, alignSelf: 'center' },
  welcome: { alignItems: 'center', justifyContent: 'center', flex: 1, minHeight: 160, gap: 9 },
  welcomeTitle: { color: ink, fontSize: 27, fontWeight: '700', letterSpacing: -0.6 },
  emptyChat: { flex: 1, alignItems: 'center', justifyContent: 'center', gap: 15, paddingHorizontal: 24, paddingBottom: 24 },
  emptyLogo: { width: 172, height: 152 },
  createError: { alignItems: 'center', gap: 7, maxWidth: 360 },
  composerShell: { gap: 7, paddingHorizontal: 14, alignSelf: 'center', width: '100%', maxWidth: 760 },
  composer: { backgroundColor: glass, borderRadius: 26, padding: 8, gap: 3, borderWidth: StyleSheet.hairlineWidth, borderColor: '#d1d4d8', boxShadow: '0 4px 18px rgba(23,25,28,0.045)' },
  input: { minHeight: 42, maxHeight: 124, fontSize: 16, lineHeight: 22, color: ink, paddingHorizontal: 10, paddingVertical: 8 },
  toolbar: { flexDirection: 'row', alignItems: 'center', gap: 6 },
  toolButton: { backgroundColor: 'rgba(255,255,255,0.69)' },
  model: { flexShrink: 1, minHeight: 44, alignItems: 'center', flexDirection: 'row', maxWidth: '52%', paddingHorizontal: 12, gap: 5, borderRadius: 22, backgroundColor: 'rgba(255,255,255,0.76)' },
  modelName: { flexShrink: 1, color: accent, fontSize: 13, fontWeight: '600' },
  send: { backgroundColor: accent },
  draftRail: { flexDirection: 'row', alignItems: 'center', gap: 8, paddingHorizontal: 10, borderRadius: 17, borderWidth: 1, borderColor: '#fff', backgroundColor: glass },
  chip: { flexDirection: 'row', alignItems: 'center', maxWidth: 166, borderRadius: 13, paddingLeft: 8, gap: 5, backgroundColor: '#f1f2f3' },
  chipName: { flexShrink: 1, color: ink, fontSize: 12 },
  pending: { backgroundColor: userBubble, borderRadius: 16, padding: 9, gap: 4, alignSelf: 'flex-end', maxWidth: '86%' },
  queue: { marginHorizontal: 20, paddingHorizontal: 10, borderRadius: 13, marginBottom: 6, backgroundColor: glass, borderWidth: 1, borderColor: edge },
  steer: { minHeight: 44, justifyContent: 'center', paddingHorizontal: 6 },
})
